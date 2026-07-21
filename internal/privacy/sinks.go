package privacy

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
)

var requiredSinkIDs = []string{
	"database", "application_logs", "internal_traces", "durable_queue", "retry_queue",
	"quarantine", "error_response", "dashboard_network", "export", "backup",
}

type SafeLogEvent struct {
	EventName         string     `json:"event_name"`
	Category          string     `json:"category"`
	AdapterID         string     `json:"adapter_id"`
	SourceSchemaID    string     `json:"source_schema_id"`
	SchemaFingerprint string     `json:"schema_fingerprint"`
	FieldPath         string     `json:"field_path"`
	ByteCount         int64      `json:"byte_count"`
	RecordCount       int        `json:"record_count"`
	Outcome           string     `json:"outcome"`
	ValueState        ValueState `json:"value_state"`
	DurationMS        int64      `json:"duration_ms"`
}

type SinkSnapshot map[string][]byte

func SerializeAllSinks(records []SafeRecord, safeErr *SafeError) (SinkSnapshot, error) {
	snapshot := make(SinkSnapshot, len(requiredSinkIDs))
	var quarantine any = []SafeError{}
	var errorResponse any = map[string]any{"status": "accepted", "record_count": len(records)}
	if safeErr != nil {
		quarantine = []SafeError{*safeErr}
		errorResponse = map[string]any{
			"status": "rejected", "incident_id": safeErr.IncidentID,
			"category": safeErr.Category, "field_path": safeErr.FieldPath,
		}
	}
	logEvent := SafeLogEvent{EventName: "ingress.completed", Category: "accepted", RecordCount: len(records)}
	if len(records) > 0 {
		logEvent.AdapterID = records[0].AdapterID
		logEvent.SourceSchemaID = records[0].SourceSchemaID
		logEvent.SchemaFingerprint = records[0].SchemaFingerprint
		logEvent.Outcome = records[0].Outcome
		logEvent.ValueState = records[0].ValueState
	}
	if safeErr != nil {
		logEvent.EventName = "ingress.rejected"
		logEvent.Category = safeErr.Category
		logEvent.SourceSchemaID = safeErr.SourceSchemaID
		logEvent.SchemaFingerprint = safeErr.SchemaFingerprint
		logEvent.FieldPath = safeErr.FieldPath
		logEvent.ByteCount = safeErr.TotalBytes
		logEvent.RecordCount = safeErr.RecordCount
	}
	objects := map[string]any{
		"database":         map[string]any{"schema": "safe_records/1", "records": records},
		"application_logs": []SafeLogEvent{logEvent},
		"internal_traces":  map[string]any{"span_name": "kansoku.ingress", "safe_attributes": logEvent},
		"durable_queue":    map[string]any{"schema": "safe_records/1", "records": records},
		"retry_queue":      map[string]any{"schema": "safe_errors/1", "incidents": quarantine},
		"quarantine":       quarantine,
		"error_response":   errorResponse,
		"dashboard_network": map[string]any{
			"schema": "privacy-safe-ui/1", "records": records, "numerator": len(records),
			"denominator": len(records), "exclusions": []string{}, "completeness": "complete",
		},
		"export": map[string]any{"schema": "privacy-safe-ndjson/1", "records": records},
	}
	for _, sinkID := range requiredSinkIDs[:9] {
		encoded, err := canonicalJSON(objects[sinkID])
		if err != nil {
			return nil, err
		}
		snapshot[sinkID] = encoded
	}
	exportHash := sha256.Sum256(snapshot["export"])
	backup, err := canonicalJSON(map[string]any{
		"schema": "privacy-safe-backup/1", "export_sha256": hex.EncodeToString(exportHash[:]),
		"privacy_manifest":    map[string]any{"raw_content": false, "records": len(records)},
		"export_bytes_base64": base64.StdEncoding.EncodeToString(snapshot["export"]),
	})
	if err != nil {
		return nil, err
	}
	snapshot["backup"] = backup
	return snapshot, nil
}

func canonicalJSON(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func RequiredSinkIDs() []string {
	return append([]string(nil), requiredSinkIDs...)
}

func ScanCanaries(snapshot SinkSnapshot, canaries map[string]string) map[string][]string {
	matches := map[string][]string{}
	for sinkID, encoded := range snapshot {
		for canaryID, canary := range canaries {
			if canary != "" && bytes.Contains(encoded, []byte(canary)) {
				matches[sinkID] = append(matches[sinkID], canaryID)
			}
		}
		sort.Strings(matches[sinkID])
	}
	return matches
}

var secretPatterns = map[string]*regexp.Regexp{
	"openai_key":     regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}`),
	"github_token":   regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),
	"aws_access_key": regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	"private_key":    regexp.MustCompile(`-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----`),
	"bearer":         regexp.MustCompile(`(?i)bearer[[:space:]]+[A-Za-z0-9._~+/-]{16,}`),
}

func ScanSecretFormats(snapshot SinkSnapshot) map[string][]string {
	matches := map[string][]string{}
	for sinkID, encoded := range snapshot {
		for patternID, pattern := range secretPatterns {
			if pattern.Find(encoded) != nil {
				matches[sinkID] = append(matches[sinkID], patternID)
			}
		}
		sort.Strings(matches[sinkID])
	}
	return matches
}
