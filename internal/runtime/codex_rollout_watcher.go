package runtime

import (
	"bufio"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"kansoku.local/kansoku/internal/codexadapter"
	"kansoku.local/kansoku/internal/observability"
	"kansoku.local/kansoku/internal/privacy"
)

const (
	codexRolloutSchemaVersion = "codex.rollout/2"
	maxRolloutWatchFiles      = 1024
	maxRolloutWatchLineBytes  = 1 << 20
)

var rolloutSkillMarker = regexp.MustCompile(`\$([A-Za-z0-9][A-Za-z0-9._:-]{0,127})`)

var errCodexRolloutHealthPersistence = errors.New("codex_rollout_health_persistence_failed")

type rolloutFileMemory struct {
	currentTurn  string
	pendingSkill map[string]string
	pendingCall  map[string]string
}

type codexRolloutRoot struct {
	path           string
	installationID string
}

// CodexRolloutWatcher is a supervised, read-only observer for ordinary Codex
// CLI sessions. It never opens an agent file for writing. Raw JSONL content
// exists only inside scanFile and has no durable representation.
type CodexRolloutWatcher struct {
	pool      *pgxpool.Pool
	ingestor  *observability.Ingestor
	store     *observability.CompactStore
	targets   []InventoryTarget
	key       []byte
	interval  time.Duration
	startedAt time.Time
	mu        sync.Mutex
	memory    map[string]*rolloutFileMemory
}

func NewCodexRolloutWatcher(
	pool *pgxpool.Pool,
	ingestor *observability.Ingestor,
	store *observability.CompactStore,
	targets []InventoryTarget,
	key []byte,
	interval time.Duration,
) (*CodexRolloutWatcher, error) {
	if pool == nil || ingestor == nil || store == nil || len(key) < 32 ||
		interval < time.Second || interval > time.Minute {
		return nil, errors.New("invalid_codex_rollout_watcher_configuration")
	}
	watcher := &CodexRolloutWatcher{
		pool: pool, ingestor: ingestor, store: store,
		targets: append([]InventoryTarget(nil), targets...),
		key:     append([]byte(nil), key...), interval: interval,
		startedAt: time.Now().UTC(), memory: map[string]*rolloutFileMemory{},
	}
	return watcher, nil
}

func (w *CodexRolloutWatcher) Run(ctx context.Context) {
	_ = w.ScanOnce(ctx)
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = w.ScanOnce(ctx)
		}
	}
}

func (w *CodexRolloutWatcher) ScanOnce(ctx context.Context) error {
	var roots []codexRolloutRoot
	for _, target := range w.targets {
		if target.AdapterID == codexadapter.AdapterID {
			installationID := target.InstallationID
			if installationID == "" {
				installationID = normalizedInstallationID(codexadapter.AdapterID)
			}
			roots = append(roots, codexRolloutRoot{
				path: target.StateRoot, installationID: installationID,
			})
		}
	}
	if len(roots) == 0 {
		if err := w.persistSourceHealth(
			ctx, "codex.rollout", "not_configured", "not_observed", "",
		); err != nil {
			return err
		}
		if err := w.persistSourceHealth(
			ctx, "codex.remote_orchestration", "unsupported", "unsupported", "",
		); err != nil {
			return err
		}
		return nil
	}
	if err := w.persistSourceHealth(
		ctx, "codex.remote_orchestration", "unsupported", "unsupported", "",
	); err != nil {
		return err
	}
	installationByPath := map[string]string{}
	for _, root := range roots {
		err := filepath.WalkDir(root.path, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".jsonl") &&
				strings.Contains(filepath.ToSlash(path), "/sessions/") {
				if existing, duplicate := installationByPath[path]; duplicate &&
					existing != root.installationID {
					return errors.New("codex_rollout_installation_binding_conflict")
				}
				installationByPath[path] = root.installationID
				if len(installationByPath) > maxRolloutWatchFiles {
					return errors.New("codex_rollout_file_limit_exceeded")
				}
			}
			return nil
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			if healthErr := w.persistSourceHealth(
				ctx, "codex.rollout", "degraded", "unknown", "rollout_discovery_failed",
			); healthErr != nil {
				return healthErr
			}
			return err
		}
	}
	paths := make([]string, 0, len(installationByPath))
	for path := range installationByPath {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := w.scanFile(ctx, path, installationByPath[path]); err != nil {
			if healthErr := w.persistSourceHealth(
				ctx, "codex.rollout", "degraded", "unknown", "rollout_scan_failed",
			); healthErr != nil {
				return healthErr
			}
			_ = w.ingestor.SetSourceLifecycle(observability.SourceCodexRollout, observability.SourceDegraded, false)
			return err
		}
	}
	state := "configured"
	if len(paths) > 0 {
		state = "producing"
		_ = w.ingestor.SetSourceLifecycle(observability.SourceCodexRollout, observability.SourceProducing, true)
	}
	return w.persistSourceHealth(ctx, "codex.rollout", state, "observed", "")
}

func (w *CodexRolloutWatcher) scanFile(
	ctx context.Context,
	path, installationID string,
) error {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("codex_rollout_not_regular")
	}
	identity, err := codexadapter.ComputeRolloutFileIdentity(path, w.key)
	if err != nil {
		return err
	}
	importerID := "codex-rollout-" + w.hmac("importer/1", identity.PathPseudonym)[:32]
	fileID := w.hmac("file/1", identity.PathPseudonym+"\x00"+identity.InodeOrFileID)
	checkpoint := w.store.Snapshot().Checkpoints[importerID]
	if checkpoint.FileID == "" {
		// Existing historical files are baselined at startup. Files created
		// after supervision began are consumed from byte zero.
		offset := int64(0)
		if !info.ModTime().UTC().After(w.startedAt) {
			offset = info.Size()
		}
		checkpoint = observability.Checkpoint{
			ImporterID: importerID, Offset: offset, FileID: fileID,
		}
		if _, err := w.store.Commit(observability.CommitRequest{Checkpoint: &checkpoint}); err != nil {
			return err
		}
		if offset == info.Size() {
			return nil
		}
	}
	if checkpoint.FileID != fileID || checkpoint.Offset > info.Size() {
		// Rotation/truncation gets a new generation through the stable file
		// identity and safely restarts at zero; event idempotency prevents
		// duplicate facts.
		checkpoint.Offset = 0
		checkpoint.FileID = fileID
		_ = w.ingestor.SetSourceLifecycle(observability.SourceCodexRollout, observability.SourceDegraded, false)
	}
	sessionID, adapterVersion := readRolloutSessionMetadata(path)
	if sessionID == "" {
		return errors.New("codex_rollout_session_identity_unavailable")
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Seek(checkpoint.Offset, 0); err != nil {
		return err
	}
	w.mu.Lock()
	memory := w.memory[importerID]
	if memory == nil {
		memory = &rolloutFileMemory{
			pendingSkill: map[string]string{}, pendingCall: map[string]string{},
		}
		w.memory[importerID] = memory
	}
	w.mu.Unlock()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), maxRolloutWatchLineBytes)
	offset := checkpoint.Offset
	for scanner.Scan() {
		line := scanner.Bytes()
		nextOffset := offset + int64(len(line)) + 1
		if nextOffset > info.Size() {
			break
		}
		checkpoint.Sequence++
		records, schemaFingerprint := w.projectRolloutLine(
			line, sessionID, adapterVersion, checkpoint.Sequence, memory,
		)
		if schemaFingerprint != "" {
			if err := w.ingestor.IngestUnknown(
				observability.SourceCodexRollout, schemaFingerprint,
				int64(len(line)), 1,
			); err != nil {
				return err
			}
		}
		for _, record := range records {
			if _, err := w.ingestor.IngestSanitizedRolloutRecordForInstallation(
				record, checkpoint.Sequence, installationID,
			); err != nil {
				return err
			}
		}
		offset = nextOffset
		checkpoint.Offset = offset
		if _, err := w.store.Commit(observability.CommitRequest{Checkpoint: &checkpoint}); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return errors.New("codex_rollout_line_oversized_or_unreadable")
	}
	return nil
}

type nativeRolloutEnvelope struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

func (w *CodexRolloutWatcher) projectRolloutLine(
	line []byte,
	sessionID, adapterVersion string,
	sequence uint64,
	memory *rolloutFileMemory,
) ([]privacy.SafeRecord, string) {
	var envelope nativeRolloutEnvelope
	if json.Unmarshal(line, &envelope) != nil || envelope.Type == "" {
		return nil, w.hmac("schema/1", "invalid-envelope")
	}
	observedAt, err := time.Parse(time.RFC3339Nano, envelope.Timestamp)
	if err != nil {
		return nil, w.hmac("schema/1", "invalid-timestamp\x00"+envelope.Type)
	}
	var payload struct {
		Type      string          `json:"type"`
		TurnID    string          `json:"turn_id"`
		Message   string          `json:"message"`
		CallID    string          `json:"call_id"`
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Input     json.RawMessage `json:"input"`
	}
	_ = json.Unmarshal(envelope.Payload, &payload)
	switch envelope.Type {
	case "session_meta", "world_state", "compacted", "inter_agent_communication_metadata":
		return nil, ""
	case "turn_context":
		if payload.TurnID != "" {
			memory.currentTurn = payload.TurnID
		}
		return nil, ""
	case "event_msg":
		switch payload.Type {
		case "task_started":
			if payload.TurnID != "" {
				memory.currentTurn = payload.TurnID
			}
		case "user_message":
			var records []privacy.SafeRecord
			for _, match := range rolloutSkillMarker.FindAllStringSubmatch(payload.Message, 32) {
				// RE2 deliberately has no look-behind. Trim only punctuation
				// that the identity alphabet also permits at the end of a
				// prose sentence; internal dotted and plugin:skill names stay
				// untouched.
				identity := strings.TrimRight(match[1], ".:")
				if identity == "" {
					continue
				}
				memory.pendingSkill[identity] = memory.currentTurn
				records = append(records, w.rolloutRecord(
					"requested:"+identity+":"+envelope.Timestamp,
					sessionID, memory.currentTurn, identity,
					"component.requested", "requested", "rollout_marker",
					observedAt, adapterVersion, sequence,
					privacy.RedactionCounts{PromptFields: 1},
				))
			}
			return records, ""
		}
		return nil, ""
	case "response_item":
		switch payload.Type {
		case "function_call", "custom_tool_call":
			if payload.CallID == "" {
				return nil, ""
			}
			if identity := rolloutReadSkill(payload.Name, payload.Arguments, payload.Input, memory.pendingSkill); identity != "" {
				memory.pendingCall[payload.CallID] = identity
			}
		case "function_call_output", "custom_tool_call_output":
			identity := memory.pendingCall[payload.CallID]
			if identity == "" {
				return nil, ""
			}
			delete(memory.pendingCall, payload.CallID)
			turnID := memory.pendingSkill[identity]
			delete(memory.pendingSkill, identity)
			return []privacy.SafeRecord{
				w.rolloutRecord(
					"loaded:"+payload.CallID, sessionID, turnID, identity,
					"component.loaded", "not_observed", "rollout_skill_md_read",
					observedAt, adapterVersion, sequence,
					privacy.RedactionCounts{PathFields: 1, ToolIOFields: 1},
				),
				w.rolloutRecord(
					"invoked:"+payload.CallID, sessionID, turnID, identity,
					"component.invoked", "explicit", "rollout_corroborated",
					observedAt, adapterVersion, sequence,
					privacy.RedactionCounts{PathFields: 1, ToolIOFields: 1},
				),
			}, ""
		}
		return nil, ""
	default:
		return nil, w.hmac("schema/1", "unknown-top-level\x00"+envelope.Type)
	}
}

func (w *CodexRolloutWatcher) rolloutRecord(
	nativeID, sessionID, turnID, identity,
	eventType, invocationMode, identitySource string,
	observedAt time.Time,
	adapterVersion string,
	sequence uint64,
	redactions privacy.RedactionCounts,
) privacy.SafeRecord {
	qualified := identity
	plain := identity
	owner := ""
	if index := strings.LastIndex(identity, ":"); index > 0 {
		owner, plain = identity[:index], identity[index+1:]
	}
	sourceRecord := "hmac-sha256:" + w.hmac("source-record/1", nativeID)
	sessionPseudo := "hmac-sha256:" + w.hmac("session/1", sessionID)
	turnPseudo := ""
	if turnID != "" {
		turnPseudo = "hmac-sha256:" + w.hmac("turn/1", sessionID+"\x00"+turnID)
	}
	tool := privacy.CatalogObservation{State: privacy.ObservationObserved, ID: &plain}
	schemaFingerprint := "hmac-sha256:" + w.hmac("schema-fingerprint/1", codexRolloutSchemaVersion)
	return privacy.SafeRecord{
		RecordID: "hmac-sha256:" + w.hmac("record/1", nativeID),
		IdempotencyKey: "hmac-sha256:" + w.hmac(
			"idempotency/1", nativeID+"\x00"+eventType,
		),
		AdapterID: codexadapter.AdapterID, AdapterVersion: adapterVersion,
		SourceSchemaID: codexRolloutSchemaVersion, SchemaFingerprint: schemaFingerprint,
		ObservedAt: observedAt.UTC(), ReceivedAt: time.Now().UTC(),
		Confidence: .85, EventType: eventType, Outcome: "unknown",
		ValueState: privacy.ValueObserved,
		Model:      privacy.CatalogObservation{State: privacy.ObservationNotObserved},
		Tool:       tool, ComponentKind: "skill",
		ComponentEvidence: privacy.ComponentEvidenceMetadata{
			QualifiedIdentity: qualified, IdentitySource: identitySource,
			OwnerPluginIdentity: owner, InvocationMode: invocationMode,
		},
		RedactionCounts: redactions,
		Lineage: privacy.Lineage{
			SourceRecordPseudonym: sourceRecord,
			SessionPseudonym:      sessionPseudo, TurnPseudonym: turnPseudo,
			AdapterID: codexadapter.AdapterID, AdapterVersion: adapterVersion,
			SourceSchemaID:    codexRolloutSchemaVersion,
			SchemaFingerprint: schemaFingerprint,
			SanitizerVersion:  "kansoku.ingress-sanitizer/1",
			ContractSHA256:    privacy.PrivacyContractSemanticSHA256,
		},
	}
}

func rolloutReadSkill(
	name string,
	arguments, input json.RawMessage,
	pending map[string]string,
) string {
	lowerName := strings.ToLower(name)
	if !strings.Contains(lowerName, "read") && !strings.Contains(lowerName, "view") &&
		!strings.Contains(lowerName, "exec") && !strings.Contains(lowerName, "shell") {
		return ""
	}
	material := append(append([]byte(nil), arguments...), input...)
	if !bytes.Contains(bytes.ToLower(material), []byte("skill.md")) {
		return ""
	}
	for identity := range pending {
		plain := identity
		if index := strings.LastIndex(identity, ":"); index >= 0 {
			plain = identity[index+1:]
		}
		if bytes.Contains(material, []byte(plain)) {
			return identity
		}
	}
	return ""
}

func readRolloutSessionMetadata(path string) (sessionID, adapterVersion string) {
	file, err := os.Open(path)
	if err != nil {
		return "", ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), maxRolloutWatchLineBytes)
	for count := 0; count < 8 && scanner.Scan(); count++ {
		var envelope nativeRolloutEnvelope
		if json.Unmarshal(scanner.Bytes(), &envelope) != nil || envelope.Type != "session_meta" {
			continue
		}
		var payload struct {
			SessionID  string `json:"session_id"`
			ID         string `json:"id"`
			CLIVersion string `json:"cli_version"`
		}
		if json.Unmarshal(envelope.Payload, &payload) == nil {
			sessionID = payload.SessionID
			if sessionID == "" {
				sessionID = payload.ID
			}
			adapterVersion = payload.CLIVersion
			if adapterVersion == "" {
				adapterVersion = codexadapter.AdapterVersion
			}
			return sessionID, adapterVersion
		}
	}
	return "", ""
}

func (w *CodexRolloutWatcher) persistSourceHealth(
	ctx context.Context,
	sourceID, state, valueState, errorClass string,
) error {
	var successfulAt any
	if state == "producing" {
		successfulAt = time.Now().UTC()
	}
	if _, err := w.pool.Exec(ctx, `
		INSERT INTO runtime_source_health (
			source_id,state,value_state,last_attempted_at,last_successful_at,
			last_error_class,updated_at
		) VALUES ($1,$2,$3,now(),$4,NULLIF($5,''),now())
		ON CONFLICT (source_id) DO UPDATE SET
			state=EXCLUDED.state, value_state=EXCLUDED.value_state,
			last_attempted_at=EXCLUDED.last_attempted_at,
			last_successful_at=COALESCE(
				EXCLUDED.last_successful_at,
				runtime_source_health.last_successful_at
			),
			last_error_class=EXCLUDED.last_error_class,
			updated_at=now()
	`, sourceID, state, valueState, successfulAt, errorClass); err != nil {
		return errCodexRolloutHealthPersistence
	}
	return nil
}

func (w *CodexRolloutWatcher) hmac(namespace, value string) string {
	mac := hmac.New(sha256.New, w.key)
	mac.Write([]byte(namespace))
	mac.Write([]byte{0})
	mac.Write([]byte(value))
	return hex.EncodeToString(mac.Sum(nil))
}
