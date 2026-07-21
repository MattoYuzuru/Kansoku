package privacy

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type canaryFixture struct {
	Canaries map[string]string `json:"canaries"`
	Payload  json.RawMessage   `json:"payload"`
	Expected struct {
		RecordCount        int      `json:"record_count"`
		RequiredSinkCount  int      `json:"required_sink_count"`
		ComponentMentions  []string `json:"component_mentions"`
		PromptState        string   `json:"prompt_state"`
		AttachmentCount    int      `json:"attachment_count"`
		URLReferenceCount  int      `json:"url_reference_count"`
		FileReferenceCount int      `json:"file_reference_count"`
	} `json:"expected"`
}

func loadCanaryFixture(t testing.TB) canaryFixture {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "tests", "fixtures", "session-02", "raw-canary-input.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture canaryFixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func testSanitizer(t *testing.T) *IngressSanitizer {
	t.Helper()
	sanitizer, err := NewIngressSanitizer(bytes.Repeat([]byte{0x42}, 32), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	sanitizer.SetClockForTest(func() time.Time { return time.Date(2026, 7, 21, 12, 35, 0, 0, time.UTC) })
	return sanitizer
}

func TestRawCanaryCannotReachAnySinkAndSafeMetadataSurvives(t *testing.T) {
	fixture := loadCanaryFixture(t)
	sanitizer := testSanitizer(t)
	records, safeErr := sanitizer.DecodeAndExtract(bytes.NewReader(fixture.Payload), FixtureSourceSchema())
	if safeErr != nil {
		t.Fatalf("unexpected safe error: %#v", safeErr)
	}
	if len(records) != fixture.Expected.RecordCount {
		t.Fatalf("records=%d", len(records))
	}
	record := records[0]
	if record.IdempotencyKey == "" || record.Lineage.SchemaFingerprint == "" || record.Confidence != 1 {
		t.Fatal("lineage/idempotency/confidence missing")
	}
	if !reflect.DeepEqual(record.ComponentMentions, fixture.Expected.ComponentMentions) {
		t.Fatalf("mentions=%v", record.ComponentMentions)
	}
	if string(record.PromptFeatures.State) != fixture.Expected.PromptState ||
		record.PromptFeatures.AttachmentCount != fixture.Expected.AttachmentCount ||
		record.PromptFeatures.URLReferenceCount != fixture.Expected.URLReferenceCount ||
		record.PromptFeatures.FileReferenceCount != fixture.Expected.FileReferenceCount {
		t.Fatalf("prompt features=%#v", record.PromptFeatures)
	}
	replay, safeErr := sanitizer.DecodeAndExtract(bytes.NewReader(fixture.Payload), FixtureSourceSchema())
	if safeErr != nil || replay[0].IdempotencyKey != record.IdempotencyKey || replay[0].RecordID != record.RecordID {
		t.Fatal("replay is not idempotent")
	}
	sinks, err := SerializeAllSinks(records, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(sinks) != fixture.Expected.RequiredSinkCount {
		t.Fatalf("sink count=%d", len(sinks))
	}
	for _, sinkID := range RequiredSinkIDs() {
		if _, ok := sinks[sinkID]; !ok {
			t.Errorf("missing sink %s", sinkID)
		}
	}
	if matches := ScanCanaries(sinks, fixture.Canaries); len(matches) != 0 {
		t.Fatalf("raw canary leak: %v", matches)
	}
	if matches := ScanSecretFormats(sinks); len(matches) != 0 {
		t.Fatalf("secret-format leak: %v", matches)
	}
	for sinkID, encoded := range sinks {
		if !json.Valid(encoded) {
			t.Errorf("sink %s is not JSON", sinkID)
		}
	}
}

func TestDurableRecordHasExactAllowlistAndCanonicalStates(t *testing.T) {
	fixture := loadCanaryFixture(t)
	records, safeErr := testSanitizer(t).DecodeAndExtract(bytes.NewReader(fixture.Payload), FixtureSourceSchema())
	if safeErr != nil {
		t.Fatal(safeErr)
	}
	encoded, _ := json.Marshal(records[0])
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	expected := stringSet(
		"record_id", "idempotency_key", "adapter_id", "adapter_version", "source_schema_id",
		"schema_fingerprint", "observed_at", "received_at", "confidence", "event_type", "outcome",
		"value_state", "model", "tool", "component_mentions", "prompt_features",
		"redaction_counts", "lineage",
	)
	if !reflect.DeepEqual(fieldsToSet(fields), expected) {
		t.Fatalf("safe fields=%v", fieldsToSet(fields))
	}
	if records[0].ValueState != "redacted" {
		t.Fatalf("value state=%s", records[0].ValueState)
	}
	if records[0].Model.State != ObservationObserved || records[0].Model.ID == nil || records[0].Tool.State != ObservationObserved || records[0].Tool.ID == nil {
		t.Fatalf("catalog observations=%#v %#v", records[0].Model, records[0].Tool)
	}
	if records[0].RedactionCounts.AttachmentFields != 1 {
		t.Fatalf("attachment redaction count=%d", records[0].RedactionCounts.AttachmentFields)
	}
}

func TestUnknownSchemaAndFieldBecomeMetadataOnlyQuarantine(t *testing.T) {
	fixture := loadCanaryFixture(t)
	sanitizer := testSanitizer(t)
	unknown := FixtureSourceSchema()
	unknown.ID = "private-content-as-schema-name"
	records, safeErr := sanitizer.DecodeAndExtract(bytes.NewReader(fixture.Payload), unknown)
	if records != nil || safeErr == nil || safeErr.Category != "unknown_schema" || safeErr.SourceSchemaID != "unknown" {
		t.Fatalf("unexpected unknown-schema result: records=%v err=%#v", records, safeErr)
	}
	sinks, err := SerializeAllSinks(nil, safeErr)
	if err != nil {
		t.Fatal(err)
	}
	if matches := ScanCanaries(sinks, fixture.Canaries); len(matches) != 0 {
		t.Fatalf("quarantine leak: %v", matches)
	}
	if bytes.Contains(sinks["quarantine"], []byte("private-content-as-schema-name")) {
		t.Fatal("untrusted schema ID leaked")
	}

	var payload map[string]any
	if err := json.Unmarshal(fixture.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	unknownFieldCanary := fixture.Canaries["prompt"]
	payload[unknownFieldCanary] = "private"
	mutated, _ := json.Marshal(payload)
	_, safeErr = sanitizer.DecodeAndExtract(bytes.NewReader(mutated), FixtureSourceSchema())
	if safeErr == nil || safeErr.Category != "unknown_field" || safeErr.FieldPath != "$.[unknown]" {
		t.Fatalf("unexpected unknown-field error: %#v", safeErr)
	}
	encoded, _ := json.Marshal(safeErr)
	if bytes.Contains(encoded, []byte(unknownFieldCanary)) {
		t.Fatal("unknown field name leaked")
	}
}

func TestCallerCannotWidenTrustedSchemaCatalogOrFieldAllowlist(t *testing.T) {
	fixture := loadCanaryFixture(t)
	sanitizer := testSanitizer(t)
	mutations := []func(*SourceSchema){
		func(schema *SourceSchema) { schema.InputFields["private_raw_alias"] = struct{}{} },
		func(schema *SourceSchema) { schema.Models[fixture.Canaries["prompt"]] = struct{}{} },
		func(schema *SourceSchema) { schema.Tools[fixture.Canaries["tool_input"]] = struct{}{} },
		func(schema *SourceSchema) { schema.Components[fixture.Canaries["prompt"]] = struct{}{} },
		func(schema *SourceSchema) { schema.EventTypes[fixture.Canaries["prompt"]] = struct{}{} },
	}
	for index, mutate := range mutations {
		schema := FixtureSourceSchema()
		mutate(&schema)
		records, safeErr := sanitizer.DecodeAndExtract(bytes.NewReader(fixture.Payload), schema)
		if records != nil || safeErr == nil || safeErr.Category != "schema_contract_mismatch" {
			t.Fatalf("mutation %d escaped: records=%v err=%#v", index, records, safeErr)
		}
		encoded, _ := json.Marshal(safeErr)
		for _, canary := range fixture.Canaries {
			if bytes.Contains(encoded, []byte(canary)) {
				t.Fatalf("mutation %d leaked canary", index)
			}
		}
	}
}

func TestDecoderRejectsMalformedCompressedInvalidUTF8AndAllBounds(t *testing.T) {
	sanitizer := testSanitizer(t)
	cases := []struct {
		name     string
		raw      []byte
		category string
	}{
		{"malformed", []byte(`{"event_id":`), "malformed_json"},
		{"trailing", []byte(`{} {}`), "trailing_json"},
		{"duplicate", []byte(`{"event_id":"one","event_id":"two"}`), "duplicate_field"},
		{"escaped_duplicate", []byte(`{"event_id":"one","event_\u0069d":"two"}`), "duplicate_field"},
		{"unpaired_high_surrogate", []byte(`{"event_id":"\ud800"}`), "invalid_unicode_scalar"},
		{"unpaired_low_surrogate", []byte(`{"event_id":"\udc00"}`), "invalid_unicode_scalar"},
		{"numeric_extreme", []byte(`{"event_id":"x","value":1e9999}`), "numeric_range"},
		{"null", []byte(`null`), "invalid_record_container"},
		{"primitive", []byte(`1`), "invalid_record_container"},
		{"gzip", []byte{0x1f, 0x8b, 0x08}, "compressed_input_rejected"},
		{"zip", []byte{'P', 'K', 0x03, 0x04}, "compressed_input_rejected"},
		{"utf8", []byte{0xff, 0xfe}, "invalid_utf8"},
	}
	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			_, safeErr := sanitizer.DecodeAndExtract(bytes.NewReader(item.raw), FixtureSourceSchema())
			if safeErr == nil || safeErr.Category != item.category {
				t.Fatalf("err=%#v", safeErr)
			}
			if strings.Contains(safeErr.Error(), string(item.raw)) {
				t.Fatal("raw decoder value in error")
			}
		})
	}
	oversized := bytes.Repeat([]byte{'x'}, int(DefaultLimits().MaxTotalBytes)+1)
	_, safeErr := sanitizer.DecodeAndExtract(bytes.NewReader(oversized), FixtureSourceSchema())
	if safeErr == nil || safeErr.Category != "oversized_input" {
		t.Fatalf("err=%#v", safeErr)
	}

	limits := DefaultLimits()
	limits.MaxDepth = 2
	depthSanitizer, _ := NewIngressSanitizer(bytes.Repeat([]byte{1}, 32), limits)
	_, safeErr = depthSanitizer.DecodeAndExtract(strings.NewReader(`{"event_id":{"nested":true}}`), FixtureSourceSchema())
	if safeErr == nil || safeErr.Category != "depth_limit" {
		t.Fatalf("depth err=%#v", safeErr)
	}

	limits = DefaultLimits()
	limits.MaxStringBytes = 4
	stringSanitizer, _ := NewIngressSanitizer(bytes.Repeat([]byte{1}, 32), limits)
	_, safeErr = stringSanitizer.DecodeAndExtract(strings.NewReader(`{"event_id":"private"}`), FixtureSourceSchema())
	if safeErr == nil || safeErr.Category != "string_limit" {
		t.Fatalf("string err=%#v", safeErr)
	}

	limits = DefaultLimits()
	limits.MaxArrayItems = 1
	arraySanitizer, _ := NewIngressSanitizer(bytes.Repeat([]byte{1}, 32), limits)
	_, safeErr = arraySanitizer.DecodeAndExtract(strings.NewReader(`[{},{}]`), FixtureSourceSchema())
	if safeErr == nil || safeErr.Category != "array_limit" {
		t.Fatalf("array err=%#v", safeErr)
	}
	if err := ValidateProtobufFrameLength(DefaultLimits().MaxProtobufFrame+1, DefaultLimits()); err == nil || err.Error() != "oversized_protobuf_frame" {
		t.Fatalf("protobuf frame err=%v", err)
	}
	if err := ValidateProtobufFrameLength(-1, DefaultLimits()); err == nil || err.Error() != "invalid_protobuf_frame_length" {
		t.Fatalf("negative protobuf frame err=%v", err)
	}
	items := make([]string, DefaultLimits().MaxRecords+1)
	for index := range items {
		items[index] = `{}`
	}
	_, safeErr = sanitizer.DecodeAndExtract(strings.NewReader("["+strings.Join(items, ",")+"]"), FixtureSourceSchema())
	if safeErr == nil || safeErr.Category != "record_limit" {
		t.Fatalf("record limit err=%#v", safeErr)
	}
}

func TestInspectMetadataUsesKeyedStructuralFingerprint(t *testing.T) {
	sanitizer := testSanitizer(t)
	one, err := sanitizer.InspectMetadata(strings.NewReader(`{"private-one":"secret-one"}`), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	two, err := sanitizer.InspectMetadata(strings.NewReader(`{"private-two":"secret-two"}`), DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if one.SchemaFingerprint == two.SchemaFingerprint || !strings.HasPrefix(one.SchemaFingerprint, "hmac-sha256:") {
		t.Fatal("distinct structures did not receive keyed fingerprints")
	}
	three, _ := sanitizer.InspectMetadata(strings.NewReader(`{"private-one":"different"}`), DefaultLimits())
	if one.SchemaFingerprint != three.SchemaFingerprint {
		t.Fatal("structural fingerprint depends on raw value")
	}
}

func TestUnknownSchemaFingerprintsAreDistinctAndKnownDriftChangesIdentity(t *testing.T) {
	sanitizer := testSanitizer(t)
	one := FixtureSourceSchema()
	one.ID = "unknown-one"
	two := FixtureSourceSchema()
	two.ID = "unknown-two"
	_, first := sanitizer.DecodeAndExtract(strings.NewReader(`{"event_id":"x"}`), one)
	_, second := sanitizer.DecodeAndExtract(strings.NewReader(`{"event_id":"x","prompt":"secret"}`), two)
	if first == nil || second == nil || first.SchemaFingerprint == second.SchemaFingerprint || !strings.HasPrefix(first.SchemaFingerprint, "hmac-sha256:") {
		t.Fatalf("unknown fingerprints=%#v %#v", first, second)
	}
	known := FixtureSourceSchema()
	_, knownErr := sanitizer.DecodeAndExtract(strings.NewReader(`{"event_id":"x"}`), known)
	if knownErr == nil || !strings.HasPrefix(knownErr.SchemaFingerprint, "sha256:") {
		t.Fatalf("known fingerprint=%#v", knownErr)
	}
}

func TestMissingStatesAreTypedAndNeverCatalogSentinels(t *testing.T) {
	raw := `{"event_id":"e","session_id":"s","observed_at":"2026-07-21T00:00:00Z","event_type":"session_started"}`
	records, safeErr := testSanitizer(t).DecodeAndExtract(strings.NewReader(raw), FixtureSourceSchema())
	if safeErr != nil {
		t.Fatal(safeErr)
	}
	record := records[0]
	if record.ValueState != ValueUnknown || record.Model.State != ObservationNotObserved || record.Model.ID != nil || record.Tool.State != ObservationNotObserved || record.Tool.ID != nil {
		t.Fatalf("typed absence=%#v", record)
	}
}

func TestKeyFileIsCreateOnceNoFollowAndMode0600(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "identity.key")
	if err := CreateHMACKeyFile(path, bytes.NewReader(bytes.Repeat([]byte{0x33}, HMACKeyBytes))); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
	key, err := LoadHMACKeyFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(key, bytes.Repeat([]byte{0x33}, HMACKeyBytes)) {
		t.Fatal("key mismatch")
	}
	if err := CreateHMACKeyFile(path, bytes.NewReader(bytes.Repeat([]byte{0x44}, HMACKeyBytes))); err == nil {
		t.Fatal("overwrote key")
	}
	link := filepath.Join(directory, "link.key")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHMACKeyFile(link); err == nil {
		t.Fatal("followed symlink")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHMACKeyFile(path); err == nil {
		t.Fatal("accepted broad key permissions")
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	hardlink := filepath.Join(directory, "hardlink.key")
	if err := os.Link(path, hardlink); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHMACKeyFile(path); err == nil {
		t.Fatal("accepted multiply-linked key")
	}
	if err := os.Remove(hardlink); err != nil {
		t.Fatal(err)
	}
	ancestor := filepath.Join(directory, "ancestor-link")
	if err := os.Symlink(directory, ancestor); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadHMACKeyFile(filepath.Join(ancestor, "identity.key")); err == nil {
		t.Fatal("followed ancestor symlink")
	}
	unsafeDirectory := filepath.Join(directory, "unsafe")
	if err := os.Mkdir(unsafeDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CreateHMACKeyFile(filepath.Join(unsafeDirectory, "key"), bytes.NewReader(bytes.Repeat([]byte{0x55}, HMACKeyBytes))); err == nil {
		t.Fatal("accepted group/world-accessible secret directory")
	}
}

func fieldsToSet(fields map[string]any) map[string]struct{} {
	result := make(map[string]struct{}, len(fields))
	for key := range fields {
		result[key] = struct{}{}
	}
	return result
}

func FuzzDecodeAndExtractIsBoundedAndTyped(f *testing.F) {
	fixture := loadCanaryFixture(f)
	f.Add([]byte(fixture.Payload))
	f.Add([]byte(`{"event_id":`))
	f.Add([]byte{0x1f, 0x8b, 0x08})
	f.Fuzz(func(t *testing.T, raw []byte) {
		sanitizer := testSanitizer(t)
		records, safeErr := sanitizer.DecodeAndExtract(bytes.NewReader(raw), FixtureSourceSchema())
		sinks, err := SerializeAllSinks(records, safeErr)
		if err != nil {
			t.Fatal(err)
		}
		for sinkID, encoded := range sinks {
			if !json.Valid(encoded) {
				t.Fatalf("sink %s is not valid JSON", sinkID)
			}
		}
		if matches := ScanSecretFormats(sinks); len(matches) != 0 {
			t.Fatalf("secret leaked: %v", matches)
		}
		recordsAgain, safeErrAgain := sanitizer.DecodeAndExtract(bytes.NewReader(raw), FixtureSourceSchema())
		sinksAgain, err := SerializeAllSinks(recordsAgain, safeErrAgain)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(sinks, sinksAgain) {
			t.Fatal("nondeterministic sanitizer output")
		}
	})
}

func BenchmarkDecodeAndSerializeCanary(b *testing.B) {
	fixture := loadCanaryFixture(b)
	sanitizer, err := NewIngressSanitizer(bytes.Repeat([]byte{0x42}, 32), DefaultLimits())
	if err != nil {
		b.Fatal(err)
	}
	sanitizer.SetClockForTest(func() time.Time { return time.Date(2026, 7, 21, 12, 35, 0, 0, time.UTC) })
	b.ReportAllocs()
	b.SetBytes(int64(len(fixture.Payload)))
	for index := 0; index < b.N; index++ {
		records, safeErr := sanitizer.DecodeAndExtract(bytes.NewReader(fixture.Payload), FixtureSourceSchema())
		if safeErr != nil {
			b.Fatal(safeErr)
		}
		if _, err := SerializeAllSinks(records, nil); err != nil {
			b.Fatal(err)
		}
	}
}
