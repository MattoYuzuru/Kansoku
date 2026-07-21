package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"kansoku.local/kansoku/internal/privacy"
)

type fixture struct {
	Canaries            map[string]string `json:"canaries"`
	TransformedCanaries map[string]string `json:"transformed_canaries"`
	Payload             json.RawMessage   `json:"payload"`
	RejectionCases      []json.RawMessage `json:"rejection_cases"`
}

type sinkEvidence struct {
	ID     string `json:"id"`
	Bytes  int    `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type report struct {
	SchemaVersion           string            `json:"schema_version"`
	Status                  string            `json:"status"`
	RecordCount             int               `json:"record_count"`
	SinkCount               int               `json:"sink_count"`
	CanaryMatchCount        int               `json:"canary_match_count"`
	SecretFormatMatchCount  int               `json:"secret_format_match_count"`
	Preserved               []string          `json:"preserved"`
	Sinks                   []sinkEvidence    `json:"sinks"`
	RejectionSinks          []sinkEvidence    `json:"rejection_sinks"`
	SinkPayloadsBase64      map[string]string `json:"sink_payloads_base64,omitempty"`
	RejectionPayloadsBase64 map[string]string `json:"rejection_sink_payloads_base64,omitempty"`
}

func main() {
	fixturePath := flag.String("fixture", "tests/fixtures/session-02/raw-canary-input.json", "raw canary fixture")
	outputDirectory := flag.String("output-dir", "", "optional directory for safe sink snapshots")
	emitSinks := flag.Bool("emit-sinks-base64", false, "include exact safe sink bytes for an independent scanner")
	flag.Parse()
	raw, err := os.ReadFile(*fixturePath)
	if err != nil {
		fail(err)
	}
	var input fixture
	if err := json.Unmarshal(raw, &input); err != nil {
		fail(err)
	}
	sanitizer, err := privacy.NewIngressSanitizer(bytes.Repeat([]byte{0x42}, 32), privacy.DefaultLimits())
	if err != nil {
		fail(err)
	}
	sanitizer.SetClockForTest(func() time.Time { return time.Date(2026, 7, 21, 12, 35, 0, 0, time.UTC) })
	records, safeErr := sanitizer.DecodeAndExtract(bytes.NewReader(input.Payload), privacy.FixtureSourceSchema())
	if safeErr != nil {
		fail(safeErr)
	}
	snapshot, err := privacy.SerializeAllSinks(records, nil)
	if err != nil {
		fail(err)
	}
	allCanaries := map[string]string{}
	for key, value := range input.Canaries {
		allCanaries[key] = value
	}
	for key, value := range input.TransformedCanaries {
		allCanaries[key] = value
	}
	canaryMatches := privacy.ScanCanaries(snapshot, allCanaries)
	secretMatches := privacy.ScanSecretFormats(snapshot)
	rejectionSnapshots := make([]privacy.SinkSnapshot, 0, len(input.RejectionCases))
	for _, rejected := range input.RejectionCases {
		badRecords, badError := sanitizer.DecodeAndExtract(bytes.NewReader(rejected), privacy.FixtureSourceSchema())
		if badError == nil {
			fail(fmt.Errorf("rejection case unexpectedly accepted"))
		}
		badSnapshot, err := privacy.SerializeAllSinks(badRecords, badError)
		if err != nil {
			fail(err)
		}
		rejectionSnapshots = append(rejectionSnapshots, badSnapshot)
	}
	rejectionCombined := privacy.SinkSnapshot{}
	for _, sinkID := range privacy.RequiredSinkIDs() {
		items := make([]json.RawMessage, 0, len(rejectionSnapshots))
		for _, item := range rejectionSnapshots {
			items = append(items, json.RawMessage(item[sinkID]))
		}
		encoded, err := json.Marshal(items)
		if err != nil {
			fail(err)
		}
		rejectionCombined[sinkID] = append(encoded, '\n')
	}
	rejectionCanaryMatches := privacy.ScanCanaries(rejectionCombined, allCanaries)
	rejectionSecretMatches := privacy.ScanSecretFormats(rejectionCombined)
	result := report{
		SchemaVersion: "kansoku.privacy-canary-result/1", Status: "pass", RecordCount: len(records),
		SinkCount: len(snapshot), Preserved: []string{"approved prompt aggregates", "adapter/schema versions", "confidence", "idempotency key", "source lineage", "redaction counts"},
	}
	if *emitSinks {
		result.SinkPayloadsBase64 = map[string]string{}
		result.RejectionPayloadsBase64 = map[string]string{}
	}
	for _, values := range canaryMatches {
		result.CanaryMatchCount += len(values)
	}
	for _, values := range secretMatches {
		result.SecretFormatMatchCount += len(values)
	}
	for _, values := range rejectionCanaryMatches {
		result.CanaryMatchCount += len(values)
	}
	for _, values := range rejectionSecretMatches {
		result.SecretFormatMatchCount += len(values)
	}
	if result.CanaryMatchCount != 0 || result.SecretFormatMatchCount != 0 {
		result.Status = "fail"
	}
	ids := privacy.RequiredSinkIDs()
	sort.Strings(ids)
	for _, sinkID := range ids {
		encoded := snapshot[sinkID]
		hash := sha256.Sum256(encoded)
		result.Sinks = append(result.Sinks, sinkEvidence{ID: sinkID, Bytes: len(encoded), SHA256: hex.EncodeToString(hash[:])})
		if *emitSinks {
			result.SinkPayloadsBase64[sinkID] = base64.StdEncoding.EncodeToString(encoded)
			result.RejectionPayloadsBase64[sinkID] = base64.StdEncoding.EncodeToString(rejectionCombined[sinkID])
		}
		rejected := rejectionCombined[sinkID]
		rejectedHash := sha256.Sum256(rejected)
		result.RejectionSinks = append(result.RejectionSinks, sinkEvidence{ID: sinkID, Bytes: len(rejected), SHA256: hex.EncodeToString(rejectedHash[:])})
		if *outputDirectory != "" {
			if err := os.MkdirAll(*outputDirectory, 0o700); err != nil {
				fail(err)
			}
			if err := os.WriteFile(filepath.Join(*outputDirectory, sinkID+".json"), encoded, 0o600); err != nil {
				fail(err)
			}
		}
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fail(err)
	}
	fmt.Println(string(encoded))
	if result.Status != "pass" {
		os.Exit(1)
	}
}

func fail(err error) { fmt.Fprintln(os.Stderr, err.Error()); os.Exit(1) }
