package codexadapter

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"kansoku.local/kansoku/internal/privacy"
)

// codex.rollout is the checkpointed, streaming JSONL importer over
// CODEX_HOME session rollout files. It mirrors the checkpoint/offset/
// rotation/truncation shape internal/observability.ImportTranscript already
// established for Session 03's generic transcript_jsonl lane (see
// internal/observability/importer.go) so this source reuses that protocol
// verbatim rather than inventing a second checkpointing scheme; the only
// difference is that this package never touches internal/observability's
// durable store directly -- it only ever produces already-sanitized,
// metadata-only RolloutRecord values a later ingestion stage feeds onward,
// exactly like codex.hook/codex.otel already do for their own sources.
const (
	sourceIDRollout       = "codex.rollout"
	rolloutSourceSchemaID = "codex.rollout/1"

	// maxRolloutLineBytes bounds one JSONL record read, matching the same
	// 1MiB ceiling contracts/observability/ingress.yaml's limits.max_frame_bytes
	// and internal/observability.Ingestor.sanitizerLimits already use, so a
	// Codex rollout record is never treated more permissively than any other
	// bounded record in this repository.
	maxRolloutLineBytes = 1 << 20
)

// RolloutFileIdentity is the checkpoint-scoped identity of one rollout file:
// a stable content-free identity derived from its canonical path plus,
// where the host filesystem exposes one, an inode/file-id value. Two
// distinct files must never resolve to the same identity, and the same
// file must always resolve to the same identity across process restarts.
type RolloutFileIdentity struct {
	// PathPseudonym is an HMAC-derived pseudonym of the canonical file path
	// (see adaptersdk.HostView.PseudonymizePath for the identical
	// construction); the raw path is never itself part of a checkpoint.
	PathPseudonym string `json:"path_pseudonym"`
	// InodeOrFileID is the platform inode/file-id string where the host
	// filesystem exposes one (empty otherwise -- "where available" per
	// contracts/codex/rollout-and-inventory.yaml, never fabricated).
	InodeOrFileID string `json:"inode_or_file_id"`
}

// RolloutCheckpoint is the durable-shaped, checkpointed importer state for
// one rollout file: file identity, byte offset, first/last record
// fingerprint and rotation generation. Committing a new RolloutCheckpoint
// after each accepted record is the caller's responsibility (mirroring
// internal/observability.ImportTranscript's per-record checkpoint commit);
// this package only ever computes the next checkpoint value.
type RolloutCheckpoint struct {
	ImporterID             string              `json:"importer_id"`
	FileIdentity           RolloutFileIdentity `json:"file_identity"`
	ByteOffset             int64               `json:"byte_offset"`
	Sequence               uint64              `json:"sequence"`
	FirstRecordFingerprint string              `json:"first_record_fingerprint"`
	LastRecordFingerprint  string              `json:"last_record_fingerprint"`
	RotationGeneration     uint64              `json:"rotation_generation"`
	TruncationDetected     bool                `json:"truncation_detected"`
}

// RolloutRecordKind is the closed classification RolloutRecord reports for
// one parsed JSONL line. A corrupt or unrecognized schema is never dropped
// silently: it is reported as RolloutRecordUnknownSchema, which the caller
// must open as a metadata-only incident scoped to codex.rollout alone.
type RolloutRecordKind string

const (
	RolloutRecordSessionMeta RolloutRecordKind = "session_meta"
	RolloutRecordUserMessage RolloutRecordKind = "user_message"
	RolloutRecordToolCall    RolloutRecordKind = "tool_call"
	RolloutRecordToolResult  RolloutRecordKind = "tool_result"
	RolloutRecordSubagent    RolloutRecordKind = "subagent_event"
	RolloutRecordUnknown     RolloutRecordKind = "unknown_schema"
)

// rolloutEnvelope is the closed, bounded shape one Codex rollout JSONL line
// is decoded into. It intentionally has no generic payload map: unmodeled
// keys are rejected by DisallowUnknownFields in decodeRolloutLine, exactly
// like HookHelperInput. content is decoded only when
// RolloutImportOptions.AllowTransientContentParsing is true; otherwise the
// decoder never even attempts to hold it beyond the single decode call, and
// it is discarded before this function returns.
type rolloutEnvelope struct {
	Type      string `json:"type"`
	SessionID string `json:"session_id"`
	TurnID    string `json:"turn_id,omitempty"`
	ToolID    string `json:"tool_id,omitempty"`
	ModelID   string `json:"model_id,omitempty"`
	Role      string `json:"role,omitempty"`
	Timestamp string `json:"timestamp,omitempty"`
	// Content is read only when opted in, only to compute in-memory
	// PromptFeatures; it is never copied into RolloutRecord or any other
	// durable-bound value returned by this package.
	Content string `json:"content,omitempty"`
}

// rolloutTypeKind is the closed, documented mapping from a rollout record's
// declared "type" field to RolloutRecordKind. An undeclared type never
// silently defaults to a known kind; it resolves to RolloutRecordUnknown.
var rolloutTypeKind = map[string]RolloutRecordKind{
	"session_meta":   RolloutRecordSessionMeta,
	"user_message":   RolloutRecordUserMessage,
	"tool_call":      RolloutRecordToolCall,
	"tool_result":    RolloutRecordToolResult,
	"subagent_event": RolloutRecordSubagent,
}

// RolloutRecord is the already-sanitized, metadata-only shape one accepted
// rollout JSONL line normalizes to. Raw content has no field here by
// default; PromptFeatures is populated only for RolloutRecordUserMessage
// records and only when the caller opted into transient content parsing.
type RolloutRecord struct {
	Kind           RolloutRecordKind       `json:"kind"`
	SessionID      string                  `json:"session_id"`
	TurnID         string                  `json:"turn_id,omitempty"`
	ToolID         string                  `json:"tool_id,omitempty"`
	ModelID        string                  `json:"model_id,omitempty"`
	Role           string                  `json:"role,omitempty"`
	Fingerprint    string                  `json:"fingerprint"`
	PromptFeatures *privacy.PromptFeatures `json:"prompt_features,omitempty"`
}

// RolloutImportOptions controls one ImportRolloutFile call. AllowTransientContentParsing
// defaults to false (the zero value): historical content-bearing records are
// only ever examined transiently, in memory, when a user has explicitly
// opted in; the default safe mode uses only the structured, non-content
// fields above and may therefore report lower skill coverage, exactly as
// contracts/codex/rollout-and-inventory.yaml's historical_content_mode
// documents.
type RolloutImportOptions struct {
	AllowTransientContentParsing bool
}

// ErrRolloutSourceChanged is returned when a checkpoint's recorded file
// identity no longer matches the file at the given path: this is either
// rotation or an operator having pointed the importer at a different file
// under the same name, and it must degrade only codex.rollout, never be
// silently accepted as a continuation of the same stream.
var ErrRolloutSourceChanged = errors.New("codex_rollout_source_identity_changed")

// ErrRolloutInvalidCheckpoint is returned when a checkpoint's byte offset is
// out of range for the file's current size.
var ErrRolloutInvalidCheckpoint = errors.New("codex_rollout_invalid_checkpoint")

// ErrRolloutLineOversized is returned when one JSONL line exceeds
// maxRolloutLineBytes; the importer never buffers an unbounded line.
var ErrRolloutLineOversized = errors.New("codex_rollout_line_oversized")

// RolloutImportResult is the closed outcome of one ImportRolloutFile call.
type RolloutImportResult struct {
	Checkpoint         RolloutCheckpoint
	Accepted           []RolloutRecord
	QuarantinedCount   int
	TruncationDetected bool
	RotationDetected   bool
}

// ComputeRolloutFileIdentity derives a RolloutFileIdentity for path using
// pseudonymKey for the path pseudonym and, where the platform-specific
// inode/file-id accessor returns one, that value. It never itself performs
// a speculative directory scan; the caller must already have resolved path
// from CODEX_HOME or an explicit rollout root.
func ComputeRolloutFileIdentity(path string, pseudonymKey []byte) (RolloutFileIdentity, error) {
	canonical, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return RolloutFileIdentity{}, errors.New("codex_rollout_identity_failure")
	}
	pseudonym := pseudonymizePath(canonical, pseudonymKey)
	inodeOrFileID := platformFileID(path)
	return RolloutFileIdentity{PathPseudonym: pseudonym, InodeOrFileID: inodeOrFileID}, nil
}

// pseudonymizePath mirrors adaptersdk.HostView.PseudonymizePath's exact HMAC
// construction (same hash, same domain-separated namespace shape) so a
// rollout file's identity pseudonym is derived the identical, non-reversible
// way every other durable path pseudonym in this repository already is; the
// raw canonical path itself is never a durable field.
func pseudonymizePath(canonicalPath string, pseudonymKey []byte) string {
	mac := hmac.New(sha256.New, pseudonymKey)
	_, _ = mac.Write([]byte("codexadapter-rollout-path-pseudonym/1\x00" + canonicalPath))
	return "hmac-sha256:" + hex.EncodeToString(mac.Sum(nil))
}

// ImportRolloutFile streams path from checkpoint.ByteOffset, parsing bounded
// JSONL records one at a time. It never writes anything back into the
// Codex session tree: the file is opened read-only and only ever Seek'd,
// never truncated, appended or rewritten. A corrupt or unrecognized-schema
// line is quarantined (counted, never fatal to the rest of the stream) and
// the raw bytes of that line are never retained beyond the single decode
// attempt.
func ImportRolloutFile(path string, checkpoint RolloutCheckpoint, pseudonymKey []byte, opts RolloutImportOptions) (RolloutImportResult, error) {
	identity, err := ComputeRolloutFileIdentity(path, pseudonymKey)
	if err != nil {
		return RolloutImportResult{}, err
	}
	if checkpoint.FileIdentity.PathPseudonym != "" && checkpoint.FileIdentity != identity {
		return RolloutImportResult{Checkpoint: checkpoint, RotationDetected: true}, ErrRolloutSourceChanged
	}

	file, err := os.Open(path)
	if err != nil {
		return RolloutImportResult{}, errors.New("codex_rollout_open_failure")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return RolloutImportResult{}, errors.New("codex_rollout_not_regular")
	}

	result := RolloutImportResult{Checkpoint: checkpoint}
	result.Checkpoint.FileIdentity = identity

	if checkpoint.ByteOffset > info.Size() {
		// The file is shorter than the checkpoint's recorded offset: this is
		// truncation. It degrades only codex.rollout's own capability/interval
		// and the caller must open an incident scoped to this source alone --
		// it never silently rewinds and reprocesses as if nothing happened.
		result.TruncationDetected = true
		return result, ErrRolloutInvalidCheckpoint
	}
	if checkpoint.ByteOffset < 0 {
		return RolloutImportResult{}, ErrRolloutInvalidCheckpoint
	}
	if _, err := file.Seek(checkpoint.ByteOffset, io.SeekStart); err != nil {
		return RolloutImportResult{}, errors.New("codex_rollout_seek_failure")
	}

	reader := bufio.NewReaderSize(file, 64*1024)
	offset := checkpoint.ByteOffset
	sequence := checkpoint.Sequence
	firstFingerprint := checkpoint.FirstRecordFingerprint
	lastFingerprint := checkpoint.LastRecordFingerprint
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) > 0 {
			if len(line) > maxRolloutLineBytes {
				return result, ErrRolloutLineOversized
			}
			offset += int64(len(line))
			sequence++
			trimmed := bytes.TrimRight(line, "\n")
			record, quarantined := decodeRolloutLine(trimmed, opts)
			fingerprint := rolloutRecordFingerprint(trimmed)
			if firstFingerprint == "" {
				firstFingerprint = fingerprint
			}
			lastFingerprint = fingerprint
			if quarantined {
				result.QuarantinedCount++
			} else {
				result.Accepted = append(result.Accepted, record)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return result, errors.New("codex_rollout_read_failure")
		}
	}
	result.Checkpoint.ByteOffset = offset
	result.Checkpoint.Sequence = sequence
	result.Checkpoint.FirstRecordFingerprint = firstFingerprint
	result.Checkpoint.LastRecordFingerprint = lastFingerprint
	return result, nil
}

// decodeRolloutLine bounds and strictly decodes one JSONL line into a
// RolloutRecord. Unknown top-level fields are rejected (never silently
// dropped) so an unmodeled Codex schema is quarantined rather than
// partially trusted; the second return value is true when the line must be
// quarantined as a metadata-only incident.
func decodeRolloutLine(line []byte, opts RolloutImportOptions) (RolloutRecord, bool) {
	if len(line) == 0 {
		return RolloutRecord{}, true
	}
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	var envelope rolloutEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		return RolloutRecord{}, true
	}
	if err := decoder.Decode(new(struct{})); !errors.Is(err, io.EOF) {
		return RolloutRecord{}, true
	}
	kind, ok := rolloutTypeKind[envelope.Type]
	if !ok || envelope.SessionID == "" {
		return RolloutRecord{}, true
	}
	record := RolloutRecord{
		Kind:        kind,
		SessionID:   envelope.SessionID,
		TurnID:      envelope.TurnID,
		ToolID:      envelope.ToolID,
		ModelID:     envelope.ModelID,
		Role:        envelope.Role,
		Fingerprint: rolloutRecordFingerprint(line),
	}
	// Content is read here only transiently: envelope.Content is a local,
	// stack-scoped value that goes out of scope the instant this function
	// returns, and it is never assigned to any field of RolloutRecord.
	if kind == RolloutRecordUserMessage && opts.AllowTransientContentParsing && envelope.Content != "" {
		features := privacy.ExtractPromptFeatures(envelope.Content, 0)
		record.PromptFeatures = &features
	}
	return record, false
}

func rolloutRecordFingerprint(line []byte) string {
	hash := sha256.Sum256(append([]byte("codex-rollout-record-fingerprint/1\x00"), line...))
	return hex.EncodeToString(hash[:])
}

// SourceSchemaIDRollout is exported so a later stage's SourceSchemas()
// implementation and Normalize dispatch can reuse this exact identifier.
func SourceSchemaIDRollout() string { return rolloutSourceSchemaID }
