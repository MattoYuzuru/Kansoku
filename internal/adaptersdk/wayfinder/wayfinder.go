// Package wayfinder implements a SECOND conformance-only adapter for a
// fictional agent, independent of internal/adaptersdk/fakeadapter's
// "loomwright" and independent of the reserved "fixture-agent" adapter ID
// that remains load-bearing for Session 03's OTLP/hook conformance spike in
// internal/observability. Nothing in this package is a real agent
// integration; it exists only to prove, a second time and with a
// deliberately different shape, that internal/adaptersdk's Registry/
// HostView/inventory/reconciliation machinery hosts a differently-shaped
// fictional agent with zero new agent-name branch inside adaptersdk's own
// core files.
//
// Wayfinder is shaped unlike every other adapter in this repository on
// purpose:
//
//  1. It declares zero OTLP source. Its only source is a versioned local
//     event file ("wayfinder.eventfile"), the opposite extreme from
//     Codex/Claude, which both have an OTel source.
//  2. Its skill-equivalent component kind is called a "recipe" -- never
//     "skill" (Codex/Claude) or "thread" (loomwright).
//  3. Its session identifiers are non-UUID, short, monotonic sequence
//     tokens of the form "wf-session-<n>", proving the canonical model
//     assumes no particular session-id shape.
//  4. It genuinely lacks token/cost tracking: activity.token_model_cost is
//     reported as StateUnsupported/"unsupported" in its manifest, never
//     faked as a populated zero.
//  5. Its fixture event stream contains exactly one deliberately unknown
//     event type ("recipe.mystery") that matches no entry in its own
//     declared SourceSchema.EventTypes; Normalize must quarantine that one
//     record (a scoped, non-fatal error) rather than crash the whole batch
//     or silently drop every other record alongside it.
package wayfinder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"sort"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/privacy"
)

const (
	// AdapterID shares no substring with codex, claude, gemini, cursor,
	// loomwright or the reserved fixture-agent literal.
	AdapterID      = "wayfinder"
	AdapterVersion = "0.3.0"

	// StateRootEnv is the documented environment variable Discover checks
	// before considering a documented default -- never a speculative scan
	// of an entire home directory.
	StateRootEnv = "WAYFINDER_HOME"

	sourceID       = "wayfinder.eventfile"
	sourceSchemaID = "wayfinder.eventfile/1"

	// unknownSchemaEventType is the one deliberately unrecognized event
	// type this fixture agent's own fixture data emits. It is intentionally
	// absent from SourceSchemas()'s EventTypes set, so Normalize must
	// quarantine it rather than guess it into a known canonical type.
	unknownSchemaEventType = "recipe.mystery"
)

// Adapter implements adaptersdk.Adapter for the fictional Wayfinder agent.
type Adapter struct{}

// New returns a ready-to-register Wayfinder fake adapter.
func New() *Adapter { return &Adapter{} }

var _ adaptersdk.Adapter = (*Adapter)(nil)

// Manifest declares Wayfinder's closed capability set. Unlike loomwright
// (whose manifest declares every capability it lists as at least
// "supported"), Wayfinder's activity.token_model_cost is explicitly
// "unsupported": this fixture agent genuinely has no token/cost tracking,
// and that absence is declared data, never a populated zero metric computed
// downstream.
func (a *Adapter) Manifest() adaptersdk.Manifest {
	return adaptersdk.Manifest{
		APIVersion: adaptersdk.AdapterAPIVersion,
		ID:         AdapterID,
		Version:    AdapterVersion,
		Execution:  adaptersdk.ExecutionBuiltin,
		AgentDetection: adaptersdk.AgentDetection{
			Executables: []string{"wayctl"},
			StateRoots:  []string{"$" + StateRootEnv, "~/.wayfinder"},
		},
		Capabilities: map[adaptersdk.CapabilityID]string{
			adaptersdk.CapabilityDiscoveryAgentAndSurface:  "supported",
			adaptersdk.CapabilityInventoryComponents:       "supported",
			adaptersdk.CapabilityActivitySessions:          "supported",
			adaptersdk.CapabilityActivityPromptMetadata:    "unsupported",
			adaptersdk.CapabilityActivityTokenModelCost:    "unsupported",
			adaptersdk.CapabilityComponentsSkillInvocation: "supported",
			adaptersdk.CapabilityComponentsMCPLifecycle:    "unsupported",
			adaptersdk.CapabilityIngestionHistoricalImport: "supported",
			adaptersdk.CapabilityIngestionLiveStream:       "unsupported",
			adaptersdk.CapabilityConfigurationLiveCanary:   "unsupported",
		},
		Sources: []adaptersdk.SourceDescriptor{
			{ID: sourceID, Kind: "transcript_jsonl", Schemas: []string{sourceSchemaID}},
		},
		Permissions: adaptersdk.Permissions{
			FilesystemRead: []string{"$" + StateRootEnv + "/paths"},
			Network:        adaptersdk.NetworkNone,
			ProcessExec:    []string{"wayctl"},
		},
		HealthChecks: []string{"config", "fixture_replay"},
	}
}

// Discover resolves the Wayfinder state root strictly from the documented
// WAYFINDER_HOME env var (already populated into HostView.AllowedRoots by
// the caller) before considering the documented default, never scanning
// beyond that one resolved root.
func (a *Adapter) Discover(ctx context.Context, host *adaptersdk.HostView) ([]adaptersdk.InstallationCandidate, error) {
	var candidates []adaptersdk.InstallationCandidate
	for _, root := range host.AllowedRoots() {
		probe, err := host.ReadProbe(filepath.Join(root, "paths"))
		if err != nil {
			if errors.Is(err, adaptersdk.ErrOutsideAllowedRoots) {
				continue
			}
			return nil, err
		}
		if !probe.Exists {
			continue
		}
		candidates = append(candidates, adaptersdk.InstallationCandidate{
			CandidateID:     "waycand_" + stableHex(root),
			AdapterID:       AdapterID,
			SurfaceID:       "wayctl-cli",
			StateRoot:       root,
			DetectedVersion: "unknown",
			DetectionMethod: adaptersdk.DetectionDocumentedEnvVar,
			Confidence:      0.9,
		})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].CandidateID < candidates[j].CandidateID })
	return candidates, nil
}

// Inventory produces a deterministic InventorySnapshot using Wayfinder's own
// vocabulary: "recipe" (skill-equivalent component), "atlas" (plugin-
// equivalent package), "wayctl-cli" (installation surface). None of these
// names appear in any other adapter's vocabulary.
func (a *Adapter) Inventory(ctx context.Context, target adaptersdk.Installation, host *adaptersdk.HostView) (adaptersdk.InventorySnapshot, error) {
	now := time.Now().UTC()
	installationNode := adaptersdk.Node{
		NodeID: "node_" + stableHex("installation", target.InstallationID), Kind: adaptersdk.NodeAgentInstallation,
		DeclaredName: "wayfinder", Version: AdapterVersion, SourceScope: adaptersdk.ScopeUser,
		Fingerprint: stableHex("installation-fp", target.InstallationID),
	}
	surfaceNode := adaptersdk.Node{
		NodeID: "node_" + stableHex("surface", target.InstallationID), Kind: adaptersdk.NodeAgentSurface,
		DeclaredName: "wayctl-cli", SourceScope: adaptersdk.ScopeUser,
		Fingerprint: stableHex("surface-fp", target.InstallationID),
	}
	atlasNode := adaptersdk.Node{
		NodeID: "node_" + stableHex("atlas", target.InstallationID), Kind: adaptersdk.NodePluginPackage,
		DeclaredName: "trail-atlas", Version: "1.4.0", SourceScope: adaptersdk.ScopeMarketplace,
		Fingerprint: stableHex("atlas-fp", target.InstallationID),
	}
	recipeNode := adaptersdk.Node{
		NodeID: "node_" + stableHex("recipe", target.InstallationID), Kind: adaptersdk.NodeSkillIdentity,
		DeclaredName: "brew-tea", Version: "1.0.0", SourceScope: adaptersdk.ScopeMarketplace,
		Fingerprint: stableHex("recipe-fp", target.InstallationID),
	}
	nodes := []adaptersdk.Node{installationNode, surfaceNode, atlasNode, recipeNode}
	edges := []adaptersdk.Edge{
		{EdgeID: "edge_" + stableHex("bundles", target.InstallationID), Kind: adaptersdk.EdgeBundles, FromNode: installationNode.NodeID, ToNode: surfaceNode.NodeID},
		{EdgeID: "edge_" + stableHex("enabled", target.InstallationID), Kind: adaptersdk.EdgeEnabledFor, FromNode: atlasNode.NodeID, ToNode: surfaceNode.NodeID},
		{EdgeID: "edge_" + stableHex("provides", target.InstallationID), Kind: adaptersdk.EdgeProvides, FromNode: atlasNode.NodeID, ToNode: recipeNode.NodeID},
	}
	fingerprint := stableHex("snapshot-fp", target.InstallationID, now.Format(time.RFC3339))
	return adaptersdk.InventorySnapshot{
		SnapshotID: "waysnap_" + stableHex("snapshot", target.InstallationID, fingerprint), AdapterID: AdapterID,
		AdapterVersion: AdapterVersion, InstallationID: target.InstallationID, ObservedAt: now,
		Fingerprint: fingerprint, Nodes: nodes, Edges: edges,
	}, nil
}

// PlanConfiguration returns an unimplemented-write error: this fixture
// agent proves discovery/inventory/normalization/reconciliation
// conformance only. It never fabricates a second, parallel apply mechanism.
func (a *Adapter) PlanConfiguration(ctx context.Context, target adaptersdk.Installation, capability adaptersdk.CapabilityID) (adaptersdk.ChangePlan, error) {
	return adaptersdk.ChangePlan{}, errors.New("wayfinder_configuration_write_not_implemented_conformance_only")
}

// SourceSchemas declares the closed event-type vocabulary this fixture
// agent's real, recognized events may carry. unknownSchemaEventType
// ("recipe.mystery") is deliberately absent from EventTypes: it is the one
// event this agent's own fixture data emits that matches no declared
// schema entry, and Normalize must quarantine it rather than silently
// widen this set to match.
func (a *Adapter) SourceSchemas() []privacy.SourceSchema {
	return []privacy.SourceSchema{{
		ID: sourceSchemaID, AdapterID: AdapterID, AdapterVersion: AdapterVersion,
		EventTypes: stringSet("path.opened", "recipe.consulted", "path.closed"),
		Models:     stringSet(),
		Tools:      stringSet(),
		Components: stringSet("inventory/recipe-safe"),
		InputFields: stringSet(
			"event_id", "session_id", "sequence", "observed_at", "event_type", "outcome", "value_state",
			"recipe_name", "prompt", "attachments", "response", "source_code",
			"tool_input", "tool_output", "command", "path", "environment", "credentials", "exception",
		),
	}}
}

// eventTypeMap translates Wayfinder's own vocabulary into the shared
// canonical event_type used elsewhere. recipe.mystery is intentionally
// absent from this map (as well as from SourceSchemas' EventTypes): both
// omissions are the same one deliberately-unknown-schema fact, checked in
// two independent places so the omission cannot silently drift.
var eventTypeMap = map[string]string{
	"path.opened":      "session.started",
	"recipe.consulted": "component.invoked",
	"path.closed":      "session.stopped",
}

// ErrUnknownEventSchema is returned by Normalize for
// unknownSchemaEventType (or any other event_type outside eventTypeMap).
// The caller must treat this as a quarantine signal scoped to the one
// offending record -- it must never crash the batch this record was part
// of, and it must never cause every other record in the same batch to be
// silently dropped alongside it.
var ErrUnknownEventSchema = errors.New("wayfinder_unknown_event_schema_quarantined")

// Normalize maps one already-sanitized Wayfinder SafeRecord onto the
// shared canonical vocabulary. It receives only privacy.SafeRecord -- never
// a raw payload -- and never invents a second sanitizer. A record whose
// EventType is unknownSchemaEventType (or any other unrecognized type)
// returns ErrUnknownEventSchema so the caller can quarantine that one
// record and continue processing the rest of the batch; it is never
// silently coerced into a plausible-looking canonical type.
func (a *Adapter) Normalize(ctx context.Context, source adaptersdk.SafeSourceRecord) ([]adaptersdk.CanonicalEvent, error) {
	canonicalType, ok := eventTypeMap[source.EventType]
	if !ok {
		return nil, ErrUnknownEventSchema
	}
	normalized := source
	normalized.EventType = canonicalType
	return []adaptersdk.CanonicalEvent{normalized}, nil
}

// NormalizeBatch normalizes every record in records, degrading gracefully
// on the one deliberately unknown schema event: a record that fails
// Normalize is recorded in QuarantinedRecordIDs (never crashing the call
// and never causing sibling records to be dropped), while every other
// record in the same batch is still normalized and returned.
type NormalizeBatchResult struct {
	Events               []adaptersdk.CanonicalEvent
	QuarantinedRecordIDs []string
}

// NormalizeBatch is Wayfinder's own batch-degradation helper: it proves
// property 5 (graceful degradation on exactly one unknown schema) at the
// batch level, on top of the single-record Normalize this type also
// implements to satisfy adaptersdk.Adapter. A batch containing the one
// deliberately unknown "recipe.mystery" record alongside three recognized
// records yields three normalized events and exactly one quarantined
// record id -- never a total failure, and never a silently shrunk result
// that drops a *recognized* sibling record too.
func (a *Adapter) NormalizeBatch(ctx context.Context, records []adaptersdk.SafeSourceRecord) NormalizeBatchResult {
	result := NormalizeBatchResult{}
	for _, record := range records {
		events, err := a.Normalize(ctx, record)
		if err != nil {
			result.QuarantinedRecordIDs = append(result.QuarantinedRecordIDs, record.RecordID)
			continue
		}
		result.Events = append(result.Events, events...)
	}
	sort.Strings(result.QuarantinedRecordIDs)
	return result
}

// Reconcile diffs two InventorySnapshot values by NodeID membership,
// deterministically and idempotently.
func (a *Adapter) Reconcile(ctx context.Context, scope adaptersdk.ReconcileScope, previous, current adaptersdk.InventorySnapshot) adaptersdk.ReconcileResult {
	previousNodes := map[string]adaptersdk.Node{}
	for _, node := range previous.Nodes {
		previousNodes[node.NodeID] = node
	}
	currentNodes := map[string]adaptersdk.Node{}
	for _, node := range current.Nodes {
		currentNodes[node.NodeID] = node
	}
	var added, removed, changed []string
	for id, node := range currentNodes {
		if priorNode, existed := previousNodes[id]; !existed {
			added = append(added, id)
		} else if priorNode.Fingerprint != node.Fingerprint {
			changed = append(changed, id)
		}
	}
	for id := range previousNodes {
		if _, stillPresent := currentNodes[id]; !stillPresent {
			removed = append(removed, id)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	sort.Strings(changed)
	completeness := "complete"
	if len(current.Nodes) == 0 {
		completeness = "unknown"
	}
	return adaptersdk.ReconcileResult{
		SnapshotID: current.SnapshotID, AddedNodeIDs: added, RemovedNodeIDs: removed,
		ChangedNodeIDs: changed, NewCollisions: []string{}, Completeness: completeness,
	}
}

// Audit runs the closed set of health checks this manifest declares. It
// never contacts the network (Wayfinder's manifest declares
// NetworkNone -- an even stricter grade than loomwright's loopback-only)
// and never returns a database credential.
func (a *Adapter) Audit(ctx context.Context, target adaptersdk.Installation, mode adaptersdk.AuditMode) []adaptersdk.CheckResult {
	now := time.Now().UTC()
	statuses := []adaptersdk.CapabilityID{
		adaptersdk.CapabilityDiscoveryAgentAndSurface,
		adaptersdk.CapabilityInventoryComponents,
		adaptersdk.CapabilityActivitySessions,
	}
	results := make([]adaptersdk.CheckResult, 0, len(statuses)+1)
	for _, capability := range statuses {
		results = append(results, adaptersdk.CheckResult{
			CheckID:      "check_" + stableHex(string(capability), target.InstallationID, string(mode)),
			CapabilityID: capability, Mode: mode, Status: adaptersdk.CheckPass,
			DetailRef: "wayfinder_conformance_fixture", ObservedAt: now,
		})
	}
	// activity.token_model_cost is unsupported: audit reports this
	// explicitly as skipped_unsupported, never a fabricated pass/fail.
	results = append(results, adaptersdk.CheckResult{
		CheckID:      "check_" + stableHex(string(adaptersdk.CapabilityActivityTokenModelCost), target.InstallationID, string(mode)),
		CapabilityID: adaptersdk.CapabilityActivityTokenModelCost, Mode: mode, Status: adaptersdk.CheckSkippedUnsupported,
		DetailRef: "wayfinder_conformance_fixture", ObservedAt: now,
	})
	return results
}

func stableHex(values ...string) string {
	hash := sha256.New()
	for _, value := range values {
		hash.Write([]byte{0})
		hash.Write([]byte(value))
	}
	return hex.EncodeToString(hash.Sum(nil))[:32]
}

func stringSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

// NextSessionID derives the next non-UUID, monotonic short sequence
// session identifier of the form "wf-session-<n>" from a previous one (or
// "wf-session-0" as the seed). This is the concrete proof that Wayfinder's
// session identifier shape is not a UUID and that no core adaptersdk code
// assumes one: SessionPseudonym/Lineage fields elsewhere in the pipeline
// are plain strings, and this scheme is never rejected by any core
// validation path.
func NextSessionID(previous string) string {
	if previous == "" {
		return "wf-session-1"
	}
	n := 0
	for i := len(previous) - 1; i >= 0; i-- {
		if previous[i] < '0' || previous[i] > '9' {
			break
		}
		n++
	}
	if n == 0 {
		return "wf-session-1"
	}
	numeric := previous[len(previous)-n:]
	prefix := previous[:len(previous)-n]
	value := 0
	for _, r := range numeric {
		value = value*10 + int(r-'0')
	}
	value++
	return prefix + itoa(value)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
