package privacy

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const sanitizerVersion = "kansoku.ingress-sanitizer/1"

var (
	outcomes       = stringSet("succeeded", "failed", "cancelled", "interrupted", "timed_out", "abandoned", "unknown")
	valueStates    = stringSet(string(ValueUnsupported), string(ValueNotObserved), string(ValueRedacted), string(ValueUnknown), string(ValueNumericZero))
	rawFieldGroups = map[string]string{
		"prompt": "prompt", "response": "response", "source_code": "source", "tool_input": "tool_io",
		"tool_output": "tool_io", "command": "command", "path": "path", "environment": "environment",
		"credentials": "credential", "exception": "exception", "attachments": "attachment",
	}
)

type IngressSanitizer struct {
	key    []byte
	limits Limits
	now    func() time.Time
}

func NewIngressSanitizer(key []byte, limits Limits) (*IngressSanitizer, error) {
	if len(key) < 32 {
		return nil, errors.New("identity key must be at least 32 bytes")
	}
	if limits.MaxTotalBytes <= 0 || limits.MaxDepth <= 0 || limits.MaxArrayItems <= 0 ||
		limits.MaxObjectFields <= 0 || limits.MaxStringBytes <= 0 || limits.MaxNumberBytes <= 0 || limits.MaxRecords <= 0 || limits.MaxProtobufFrame <= 0 {
		return nil, errors.New("all ingress limits must be positive")
	}
	return &IngressSanitizer{
		key: append([]byte(nil), key...), limits: limits, now: time.Now,
	}, nil
}

func (s *IngressSanitizer) SetClockForTest(now func() time.Time) {
	s.now = now
}

func (s *IngressSanitizer) InspectMetadata(reader io.Reader, limit Limits) (Fingerprint, error) {
	raw, category := readBounded(reader, limit.MaxTotalBytes)
	if category != "" {
		return Fingerprint{}, errors.New(category)
	}
	if !utf8.Valid(raw) {
		return Fingerprint{}, errors.New("invalid_utf8")
	}
	value, category := decodeStrictJSON(raw, limit)
	if category != "" {
		return Fingerprint{}, errors.New(category)
	}
	if category, _ := validateBounds(value, limit, 1, "$", false); category != "" {
		return Fingerprint{}, errors.New(category)
	}
	return Fingerprint{
		SchemaFingerprint: s.keyedFingerprint("structural-shape/1", safeShape(value, 1)),
		TotalBytes:        int64(len(raw)), RecordCount: rootRecordCount(value),
	}, nil
}

func (s *IngressSanitizer) DecodeAndExtract(reader io.Reader, schema SourceSchema) ([]SafeRecord, *SafeError) {
	now := s.now().UTC()
	raw, category := readBounded(reader, s.limits.MaxTotalBytes)
	trustedSchemaID := trustedSchemaID(schema)
	schemaFingerprint := s.schemaFingerprint(schema, nil)
	if category != "" {
		return nil, s.safeError(trustedSchemaID, schemaFingerprint, "$", category, int64(len(raw)), 0, time.Time{}, now)
	}
	if isCompressed(raw) {
		return nil, s.safeError(trustedSchemaID, schemaFingerprint, "$", "compressed_input_rejected", int64(len(raw)), 0, time.Time{}, now)
	}
	if !utf8.Valid(raw) {
		return nil, s.safeError(trustedSchemaID, schemaFingerprint, "$", "invalid_utf8", int64(len(raw)), 0, time.Time{}, now)
	}
	value, category := decodeStrictJSON(raw, s.limits)
	schemaFingerprint = s.schemaFingerprint(schema, value)
	if category != "" {
		return nil, s.safeError(trustedSchemaID, schemaFingerprint, "$", category, int64(len(raw)), 0, time.Time{}, now)
	}
	if category, path := validateBounds(value, s.limits, 1, "$", true); category != "" {
		return nil, s.safeError(trustedSchemaID, schemaFingerprint, path, category, int64(len(raw)), rootRecordCount(value), time.Time{}, now)
	}

	objects, ok := rootObjects(value)
	if !ok || len(objects) == 0 {
		return nil, s.safeError(trustedSchemaID, schemaFingerprint, "$", "invalid_record_container", int64(len(raw)), rootRecordCount(value), time.Time{}, now)
	}
	if len(objects) > s.limits.MaxRecords {
		return nil, s.safeError(trustedSchemaID, schemaFingerprint, "$", "record_limit", int64(len(raw)), rootRecordCount(value), time.Time{}, now)
	}
	canonicalSchema := FixtureSourceSchema()
	if schema.ID != canonicalSchema.ID || schema.AdapterID != canonicalSchema.AdapterID || schema.AdapterVersion != canonicalSchema.AdapterVersion {
		return nil, s.safeError(trustedSchemaID, schemaFingerprint, "$", "unknown_schema", int64(len(raw)), len(objects), time.Time{}, now)
	}
	if !reflect.DeepEqual(schema, canonicalSchema) {
		return nil, s.safeError(trustedSchemaID, schemaFingerprint, "$", "schema_contract_mismatch", int64(len(raw)), len(objects), time.Time{}, now)
	}
	schema = canonicalSchema
	records := make([]SafeRecord, 0, len(objects))
	for index, object := range objects {
		record, safeErr := s.extractObject(object, schema, schemaFingerprint, int64(len(raw)), index, now)
		if safeErr != nil {
			return nil, safeErr
		}
		records = append(records, record)
	}
	return records, nil
}

func (s *IngressSanitizer) extractObject(object map[string]any, schema SourceSchema, fingerprint string, totalBytes int64, index int, receivedAt time.Time) (SafeRecord, *SafeError) {
	fields := make([]string, 0, len(object))
	for field := range object {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		if _, ok := schema.InputFields[field]; !ok {
			return SafeRecord{}, s.safeError(schema.ID, fingerprint, "$.[unknown]", "unknown_field", totalBytes, 1, time.Time{}, receivedAt)
		}
	}
	eventID, ok := object["event_id"].(string)
	if !ok || eventID == "" {
		return SafeRecord{}, s.safeError(schema.ID, fingerprint, "$.event_id", "invalid_required_field", totalBytes, 1, time.Time{}, receivedAt)
	}
	sessionID, ok := object["session_id"].(string)
	if !ok || sessionID == "" {
		return SafeRecord{}, s.safeError(schema.ID, fingerprint, "$.session_id", "invalid_required_field", totalBytes, 1, time.Time{}, receivedAt)
	}
	observedText, ok := object["observed_at"].(string)
	if !ok {
		return SafeRecord{}, s.safeError(schema.ID, fingerprint, "$.observed_at", "invalid_required_field", totalBytes, 1, time.Time{}, receivedAt)
	}
	observedAt, err := time.Parse(time.RFC3339Nano, observedText)
	if err != nil {
		return SafeRecord{}, s.safeError(schema.ID, fingerprint, "$.observed_at", "invalid_timestamp", totalBytes, 1, time.Time{}, receivedAt)
	}
	eventType, ok := object["event_type"].(string)
	if _, known := schema.EventTypes[eventType]; !ok || !known {
		return SafeRecord{}, s.safeError(schema.ID, fingerprint, "$.event_type", "unknown_enum", totalBytes, 1, observedAt, receivedAt)
	}
	outcome := optionalEnum(object, "outcome", outcomes, "unknown")
	if outcome == "" {
		return SafeRecord{}, s.safeError(schema.ID, fingerprint, "$.outcome", "unknown_enum", totalBytes, 1, observedAt, receivedAt)
	}
	valueState := optionalEnum(object, "value_state", valueStates, string(ValueUnknown))
	if valueState == "" {
		return SafeRecord{}, s.safeError(schema.ID, fingerprint, "$.value_state", "unknown_enum", totalBytes, 1, observedAt, receivedAt)
	}
	model, safeErr := allowedCatalogValue(object, "model", schema.Models)
	if safeErr != "" {
		return SafeRecord{}, s.safeError(schema.ID, fingerprint, "$.model", safeErr, totalBytes, 1, observedAt, receivedAt)
	}
	tool, safeErr := allowedCatalogValue(object, "tool_name", schema.Tools)
	if safeErr != "" {
		return SafeRecord{}, s.safeError(schema.ID, fingerprint, "$.tool_name", safeErr, totalBytes, 1, observedAt, receivedAt)
	}

	promptFeatures := PromptFeatures{State: CompletenessUnknown, CoarseScript: "unknown"}
	attachmentCount := 0
	if attachments, present := object["attachments"]; present {
		array, valid := attachments.([]any)
		if !valid {
			return SafeRecord{}, s.safeError(schema.ID, fingerprint, "$.attachments", "invalid_field_type", totalBytes, 1, observedAt, receivedAt)
		}
		attachmentCount = len(array)
	}
	if promptValue, present := object["prompt"]; present {
		prompt, valid := promptValue.(string)
		if !valid {
			return SafeRecord{}, s.safeError(schema.ID, fingerprint, "$.prompt", "invalid_field_type", totalBytes, 1, observedAt, receivedAt)
		}
		promptFeatures = extractPromptFeatures(prompt, attachmentCount)
	}

	mentions := make([]string, 0, len(schema.Components))
	if prompt, present := object["prompt"].(string); present {
		for component := range schema.Components {
			if strings.Contains(prompt, component) {
				mentions = append(mentions, component)
			}
		}
		sort.Strings(mentions)
	}
	redactions := countRedactions(object)
	sourcePseudonym := s.pseudonym("source-record/1", schema.AdapterID+"\x00"+eventID)
	sessionPseudonym := s.pseudonym("session/1", schema.AdapterID+"\x00"+sessionID)
	idempotency := s.pseudonym("idempotency/1", schema.AdapterID+"\x00"+schema.ID+"\x00"+eventID+"\x00"+observedAt.UTC().Format(time.RFC3339Nano))
	recordID := s.pseudonym("record/1", schema.AdapterID+"\x00"+sessionID+"\x00"+eventID+"\x00"+strconv.Itoa(index))
	return SafeRecord{
		RecordID: recordID, IdempotencyKey: idempotency,
		AdapterID: schema.AdapterID, AdapterVersion: schema.AdapterVersion,
		SourceSchemaID: schema.ID, SchemaFingerprint: fingerprint,
		ObservedAt: observedAt.UTC(), ReceivedAt: receivedAt,
		Confidence: 1, EventType: eventType, Outcome: outcome, ValueState: ValueState(valueState),
		Model: model, Tool: tool, ComponentMentions: mentions,
		PromptFeatures: promptFeatures, RedactionCounts: redactions,
		Lineage: Lineage{
			SourceRecordPseudonym: sourcePseudonym, SessionPseudonym: sessionPseudonym, AdapterID: schema.AdapterID,
			AdapterVersion: schema.AdapterVersion, SourceSchemaID: schema.ID,
			SchemaFingerprint: fingerprint, SanitizerVersion: sanitizerVersion,
			ContractSHA256: PrivacyContractSemanticSHA256,
		},
	}, nil
}

func readBounded(reader io.Reader, maximum int64) ([]byte, string) {
	if maximum <= 0 {
		return nil, "invalid_limit"
	}
	raw, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil {
		return raw, "read_failure"
	}
	if int64(len(raw)) > maximum {
		return raw[:maximum], "oversized_input"
	}
	return raw, ""
}

// ValidateProtobufFrameLength is the pre-allocation gate shared by future OTLP
// protobuf routes. Session 02 rejects compressed frames entirely and proves
// that a declared frame cannot exceed the same one-megabyte boundary.
func ValidateProtobufFrameLength(length int64, limits Limits) error {
	if length < 0 {
		return errors.New("invalid_protobuf_frame_length")
	}
	if length > limits.MaxProtobufFrame {
		return errors.New("oversized_protobuf_frame")
	}
	return nil
}

func validateBounds(value any, limits Limits, depth int, path string, revealKnownPath bool) (string, string) {
	if depth > limits.MaxDepth {
		return "depth_limit", path
	}
	switch typed := value.(type) {
	case string:
		if len([]byte(typed)) > limits.MaxStringBytes {
			return "string_limit", path
		}
	case []any:
		if len(typed) > limits.MaxArrayItems {
			return "array_limit", path
		}
		for _, item := range typed {
			if category, nestedPath := validateBounds(item, limits, depth+1, path+"[]", revealKnownPath); category != "" {
				return category, nestedPath
			}
		}
	case map[string]any:
		if len(typed) > limits.MaxObjectFields {
			return "object_field_limit", path
		}
		fields := make([]string, 0, len(typed))
		for field := range typed {
			fields = append(fields, field)
		}
		sort.Strings(fields)
		for _, field := range fields {
			item := typed[field]
			fieldPath := path + ".[field]"
			if revealKnownPath {
				if _, known := FixtureSourceSchema().InputFields[field]; known {
					fieldPath = path + "." + field
				}
			}
			if len([]byte(field)) > limits.MaxStringBytes {
				return "string_limit", path + ".[field]"
			}
			if category, nestedPath := validateBounds(item, limits, depth+1, fieldPath, revealKnownPath); category != "" {
				return category, nestedPath
			}
		}
	}
	return "", ""
}

func rootObjects(value any) ([]map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return []map[string]any{typed}, true
	case []any:
		result := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			object, ok := item.(map[string]any)
			if !ok {
				return nil, false
			}
			result = append(result, object)
		}
		return result, true
	default:
		return nil, false
	}
}

func rootRecordCount(value any) int {
	switch typed := value.(type) {
	case map[string]any:
		return 1
	case []any:
		return len(typed)
	default:
		return 0
	}
}

func trustedSchemaID(schema SourceSchema) string {
	if schema.ID == FixtureSourceSchema().ID {
		return schema.ID
	}
	return "unknown"
}

func (s *IngressSanitizer) schemaFingerprint(schema SourceSchema, value any) string {
	canonical := FixtureSourceSchema()
	if reflect.DeepEqual(schema, canonical) {
		digest := sha256.Sum256([]byte(canonicalSchemaMaterial(canonical) + "\x00" + PrivacyContractSemanticSHA256 + "\x00" + sanitizerVersion))
		return "sha256:" + hex.EncodeToString(digest[:])
	}
	return s.keyedFingerprint("unknown-schema-structure/1", canonicalSchemaMaterial(schema)+"\x00"+safeShape(value, 1))
}

func canonicalSchemaMaterial(schema SourceSchema) string {
	return strings.Join([]string{
		schema.ID, schema.AdapterID, schema.AdapterVersion,
		canonicalSet(schema.EventTypes), canonicalSet(schema.Models), canonicalSet(schema.Tools),
		canonicalSet(schema.Components), canonicalSet(schema.InputFields),
	}, "\x00")
}

func canonicalSet(values map[string]struct{}) string {
	items := make([]string, 0, len(values))
	for value := range values {
		items = append(items, value)
	}
	sort.Strings(items)
	return strings.Join(items, "\x1f")
}

func safeShape(value any, depth int) string {
	if depth > 16 {
		return "depth"
	}
	switch typed := value.(type) {
	case map[string]any:
		shapes := make([]string, 0, len(typed))
		for key, item := range typed {
			shapes = append(shapes, key+":"+safeShape(item, depth+1))
		}
		sort.Strings(shapes)
		return "object(" + strings.Join(shapes, ",") + ")"
	case []any:
		shapes := make([]string, 0, len(typed))
		for _, item := range typed {
			shapes = append(shapes, safeShape(item, depth+1))
		}
		sort.Strings(shapes)
		return "array(" + strings.Join(shapes, ",") + ")"
	case string:
		return "string"
	case json.Number:
		return "number"
	case bool:
		return "boolean"
	case nil:
		return "null"
	default:
		return "unknown"
	}
}

func isCompressed(raw []byte) bool {
	return (len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b) ||
		(len(raw) >= 4 && bytes.Equal(raw[:4], []byte{'P', 'K', 0x03, 0x04})) ||
		(len(raw) >= 3 && bytes.Equal(raw[:3], []byte{'B', 'Z', 'h'}))
}

func optionalEnum(object map[string]any, field string, allowed map[string]struct{}, missing string) string {
	value, present := object[field]
	if !present {
		return missing
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	if _, ok := allowed[text]; !ok {
		return ""
	}
	return text
}

func allowedCatalogValue(object map[string]any, field string, allowed map[string]struct{}) (CatalogObservation, string) {
	value, present := object[field]
	if !present {
		return CatalogObservation{State: ObservationNotObserved, ID: nil}, ""
	}
	text, ok := value.(string)
	if !ok {
		return CatalogObservation{}, "invalid_field_type"
	}
	if _, ok := allowed[text]; !ok {
		return CatalogObservation{}, "unknown_catalog_value"
	}
	identifier := text
	return CatalogObservation{State: ObservationObserved, ID: &identifier}, ""
}

func countRedactions(object map[string]any) RedactionCounts {
	counts := RedactionCounts{}
	for field, group := range rawFieldGroups {
		if _, present := object[field]; !present {
			continue
		}
		switch group {
		case "prompt":
			counts.PromptFields++
		case "attachment":
			counts.AttachmentFields++
		case "response":
			counts.ResponseFields++
		case "source":
			counts.SourceFields++
		case "tool_io":
			counts.ToolIOFields++
		case "command":
			counts.CommandFields++
		case "path":
			counts.PathFields++
			counts.SensitiveIdentifierFields++
		case "environment":
			counts.EnvironmentFields++
		case "credential":
			counts.CredentialFields++
		case "exception":
			counts.ExceptionFields++
		}
	}
	return counts
}

func (s *IngressSanitizer) pseudonym(domain, value string) string {
	mac := hmac.New(sha256.New, s.key)
	_, _ = mac.Write([]byte(domain))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(value))
	return "hmac-sha256:" + hex.EncodeToString(mac.Sum(nil))
}

func (s *IngressSanitizer) keyedFingerprint(domain, value string) string {
	return s.pseudonym(domain, value)
}

func (s *IngressSanitizer) safeError(schemaID, fingerprint, fieldPath, category string, totalBytes int64, recordCount int, observedAt, receivedAt time.Time) *SafeError {
	incident := s.pseudonym("incident/1", schemaID+"\x00"+fingerprint+"\x00"+fieldPath+"\x00"+category+"\x00"+strconv.FormatInt(totalBytes, 10))
	return &SafeError{
		IncidentID: incident, SourceSchemaID: schemaID, SchemaFingerprint: fingerprint,
		FieldPath: fieldPath, Category: category, TotalBytes: totalBytes, RecordCount: recordCount,
		ObservedAt: observedAt.UTC(), ReceivedAt: receivedAt,
	}
}
