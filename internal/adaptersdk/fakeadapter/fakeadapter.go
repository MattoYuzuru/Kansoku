// Package fakeadapter implements a conformance-only adapter for a fictional
// agent, "Loomwright". Its executable name, state root, event vocabulary and
// component kind are deliberately unlike Codex, Claude Code, Gemini CLI or
// Cursor: this is the proof that internal/adaptersdk's core has no
// agent-name branch anywhere outside an adapter's own registration. Nothing
// in this package is a real agent integration; it exists only to be run
// through the exact same inventory/health/reconciliation APIs a real
// built-in adapter would use.
package fakeadapter

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
	// AdapterID intentionally shares no substring with codex/claude/gemini/cursor.
	AdapterID      = "loomwright"
	AdapterVersion = "0.9.0"

	// StateRootEnv is the documented environment variable Discover checks
	// before falling back to a documented default -- never a home-directory
	// speculative scan.
	StateRootEnv = "LOOMWRIGHT_HOME"

	sourceSchemaID = "loomwright.spindle/v3"
)

// Adapter implements adaptersdk.Adapter for the fictional Loomwright agent.
type Adapter struct{}

// New returns a ready-to-register Loomwright fake adapter.
func New() *Adapter { return &Adapter{} }

var _ adaptersdk.Adapter = (*Adapter)(nil)

func (a *Adapter) Manifest() adaptersdk.Manifest {
	return adaptersdk.Manifest{
		APIVersion: adaptersdk.AdapterAPIVersion,
		ID:         AdapterID,
		Version:    AdapterVersion,
		Execution:  adaptersdk.ExecutionBuiltin,
		AgentDetection: adaptersdk.AgentDetection{
			Executables: []string{"loomctl"},
			StateRoots:  []string{"$" + StateRootEnv, "~/.loomwright"},
		},
		Capabilities: map[adaptersdk.CapabilityID]string{
			adaptersdk.CapabilityDiscoveryAgentAndSurface:  "supported",
			adaptersdk.CapabilityInventoryComponents:       "supported",
			adaptersdk.CapabilityActivitySessions:          "supported",
			adaptersdk.CapabilityActivityPromptMetadata:    "partial",
			adaptersdk.CapabilityComponentsSkillInvocation: "supported",
			adaptersdk.CapabilityComponentsMCPLifecycle:    "unsupported",
			adaptersdk.CapabilityIngestionLiveStream:       "supported",
			adaptersdk.CapabilityConfigurationLiveCanary:   "supported",
		},
		Sources: []adaptersdk.SourceDescriptor{
			{ID: "spindle", Kind: "hook", Schemas: []string{sourceSchemaID}},
		},
		Permissions: adaptersdk.Permissions{
			FilesystemRead: []string{"$" + StateRootEnv + "/looms"},
			Network:        adaptersdk.NetworkLoopbackOnly,
			ProcessExec:    []string{"loomctl"},
		},
		HealthChecks: []string{"config", "fixture_replay", "watermark"},
	}
}

// Discover resolves the Loomwright state root strictly from the documented
// LOOMWRIGHT_HOME env var (passed in via host to keep this deterministic
// and test-friendly) before considering the documented default, and it
// never scans beyond that one resolved root.
func (a *Adapter) Discover(ctx context.Context, host *adaptersdk.HostView) ([]adaptersdk.InstallationCandidate, error) {
	var candidates []adaptersdk.InstallationCandidate
	for _, root := range host.AllowedRoots() {
		probe, err := host.ReadProbe(filepath.Join(root, "looms"))
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
			CandidateID:     "loomcand_" + stableHex(root),
			AdapterID:       AdapterID,
			SurfaceID:       "loomctl-cli",
			StateRoot:       root,
			DetectedVersion: "unknown",
			DetectionMethod: adaptersdk.DetectionDocumentedEnvVar,
			Confidence:      0.9,
		})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].CandidateID < candidates[j].CandidateID })
	return candidates, nil
}

// Inventory produces a deterministic InventorySnapshot using Loomwright's
// own vocabulary: "thread" (skill-equivalent), "spool" (plugin-equivalent),
// "loom" (installation-equivalent surface). None of these names, nor the
// event names in Normalize below, appear anywhere in a real adapter's
// vocabulary; that is the point of this package.
func (a *Adapter) Inventory(ctx context.Context, target adaptersdk.Installation, host *adaptersdk.HostView) (adaptersdk.InventorySnapshot, error) {
	now := time.Now().UTC()
	installationNode := adaptersdk.Node{
		NodeID: "node_" + stableHex("installation", target.InstallationID), Kind: adaptersdk.NodeAgentInstallation,
		DeclaredName: "loomwright", Version: AdapterVersion, SourceScope: adaptersdk.ScopeUser,
		Fingerprint: stableHex("installation-fp", target.InstallationID),
	}
	surfaceNode := adaptersdk.Node{
		NodeID: "node_" + stableHex("surface", target.InstallationID), Kind: adaptersdk.NodeAgentSurface,
		DeclaredName: "loomctl-cli", SourceScope: adaptersdk.ScopeUser,
		Fingerprint: stableHex("surface-fp", target.InstallationID),
	}
	spoolNode := adaptersdk.Node{
		NodeID: "node_" + stableHex("spool", target.InstallationID), Kind: adaptersdk.NodePluginPackage,
		DeclaredName: "warp-spool", Version: "2.1.0", SourceScope: adaptersdk.ScopeMarketplace,
		Fingerprint: stableHex("spool-fp", target.InstallationID),
	}
	threadNode := adaptersdk.Node{
		NodeID: "node_" + stableHex("thread", target.InstallationID), Kind: adaptersdk.NodeSkillIdentity,
		DeclaredName: "weft-thread", Version: "1.0.0", SourceScope: adaptersdk.ScopeMarketplace,
		Fingerprint: stableHex("thread-fp", target.InstallationID),
	}
	cacheNode := adaptersdk.Node{
		NodeID: "node_" + stableHex("cache", target.InstallationID), Kind: adaptersdk.NodeCacheArtifact,
		DeclaredName: "warp-spool", Version: "2.0.0", SourceScope: adaptersdk.ScopePluginCache,
		CachedOnly: true, Fingerprint: stableHex("cache-fp", target.InstallationID),
	}
	nodes := []adaptersdk.Node{installationNode, surfaceNode, spoolNode, threadNode, cacheNode}
	edges := []adaptersdk.Edge{
		{EdgeID: "edge_" + stableHex("bundles", target.InstallationID), Kind: adaptersdk.EdgeBundles, FromNode: installationNode.NodeID, ToNode: surfaceNode.NodeID},
		{EdgeID: "edge_" + stableHex("enabled", target.InstallationID), Kind: adaptersdk.EdgeEnabledFor, FromNode: spoolNode.NodeID, ToNode: surfaceNode.NodeID},
		{EdgeID: "edge_" + stableHex("provides", target.InstallationID), Kind: adaptersdk.EdgeProvides, FromNode: spoolNode.NodeID, ToNode: threadNode.NodeID},
	}
	fingerprint := stableHex("snapshot-fp", target.InstallationID, now.Format(time.RFC3339))
	return adaptersdk.InventorySnapshot{
		SnapshotID: "snap_" + stableHex("snapshot", target.InstallationID, fingerprint), AdapterID: AdapterID,
		AdapterVersion: AdapterVersion, InstallationID: target.InstallationID, ObservedAt: now,
		Fingerprint: fingerprint, Nodes: nodes, Edges: edges,
	}, nil
}

// PlanConfiguration returns an unimplemented-write error: Session 05's fake
// adapter proves discovery/inventory/normalization/reconciliation
// conformance. It deliberately does not fabricate a second, parallel
// apply mechanism -- installer.Plan/Approval/SimulateApply remain the only
// real write path, exercised via adaptersdk.BuildChangePlan by callers that
// have their own installer.Plan already built.
func (a *Adapter) PlanConfiguration(ctx context.Context, target adaptersdk.Installation, capability adaptersdk.CapabilityID) (adaptersdk.ChangePlan, error) {
	return adaptersdk.ChangePlan{}, errors.New("loomwright_configuration_write_not_implemented_conformance_only")
}

func (a *Adapter) SourceSchemas() []privacy.SourceSchema {
	return []privacy.SourceSchema{{
		ID: sourceSchemaID, AdapterID: AdapterID, AdapterVersion: AdapterVersion,
		EventTypes: stringSet("weave.begun", "shuttle.passed", "thread.completed", "weave.completed"),
		Models:     stringSet("catalog/loom-safe"),
		Tools:      stringSet("inventory/thread-safe"),
		Components: stringSet("inventory/spool-safe"),
		InputFields: stringSet(
			"event_id", "session_id", "observed_at", "event_type", "outcome", "value_state",
			"model", "tool_name", "prompt", "attachments", "response", "source_code",
			"tool_input", "tool_output", "command", "path", "environment", "credentials", "exception",
		),
	}}
}

// eventTypeMap translates Loomwright's own vocabulary into the shared
// canonical event_type/outcome/value_state used by privacy.SafeRecord --
// exactly the same shape internal/observability.NormalizedFromSafe expects,
// so this fake adapter proves the whole pipeline without a core edit.
var eventTypeMap = map[string]string{
	"weave.begun":      "session_started",
	"shuttle.passed":   "user_prompt",
	"thread.completed": "tool_finished",
	"weave.completed":  "session_finished",
}

// Normalize maps one already-sanitized Loomwright SafeRecord onto the
// shared canonical vocabulary. It receives only privacy.SafeRecord -- never
// a raw payload -- and never invents a second sanitizer.
func (a *Adapter) Normalize(ctx context.Context, source adaptersdk.SafeSourceRecord) ([]adaptersdk.CanonicalEvent, error) {
	canonicalType, ok := eventTypeMap[source.EventType]
	if !ok {
		return nil, errors.New("unsupported_loomwright_event_type")
	}
	normalized := source
	normalized.EventType = canonicalType
	return []adaptersdk.CanonicalEvent{normalized}, nil
}

// Reconcile diffs two InventorySnapshot values by NodeID membership,
// deterministically and idempotently: reconciling the same pair twice
// yields byte-identical sets every time, because it only ever compares the
// already-immutable snapshots and never mutates either one.
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
// never contacts the network beyond the loopback-only grant its own
// manifest declares (in fact this fake implementation performs no network
// calls at all) and never returns a database credential.
func (a *Adapter) Audit(ctx context.Context, target adaptersdk.Installation, mode adaptersdk.AuditMode) []adaptersdk.CheckResult {
	now := time.Now().UTC()
	statuses := []adaptersdk.CapabilityID{
		adaptersdk.CapabilityDiscoveryAgentAndSurface,
		adaptersdk.CapabilityInventoryComponents,
		adaptersdk.CapabilityActivitySessions,
	}
	results := make([]adaptersdk.CheckResult, 0, len(statuses))
	for _, capability := range statuses {
		results = append(results, adaptersdk.CheckResult{
			CheckID:      "check_" + stableHex(string(capability), target.InstallationID, string(mode)),
			CapabilityID: capability, Mode: mode, Status: adaptersdk.CheckPass,
			DetailRef: "loomwright_conformance_fixture", ObservedAt: now,
		})
	}
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
