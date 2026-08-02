// Package adaptersdk defines the Session 05 adapter contract: a typed
// manifest, a permission-checked HostView, an immutable inventory snapshot
// graph, reversible configuration change plans and a capability-routed
// adapter registry. No type or function in this package branches on an
// agent name; every agent-specific behavior lives inside a concrete
// Adapter implementation registered under its own adapter ID.
package adaptersdk

import "time"

const (
	AdapterAPIVersion                = "kansoku.adapter/v1"
	AdapterSDKContractSemanticSHA256 = "TO_BE_REPLACED"

	MaxManifestConfigEntries = 256
	MaxManifestConfigDepth   = 8
	MaxManifestConfigString  = 4096
)

// ExecutionForm is the adapter hosting model. Only builtin is fully wired
// through the in-process registry today; the rest are declared so a
// manifest can state its intended form ahead of the runtime supporting it.
type ExecutionForm string

const (
	ExecutionBuiltin         ExecutionForm = "builtin"
	ExecutionExternalProcess ExecutionForm = "external_process"
	ExecutionWasm            ExecutionForm = "wasm"
	ExecutionContainer       ExecutionForm = "container"
)

// NetworkGrade is the only network permission an adapter manifest may
// declare; there is no "unrestricted" grade.
type NetworkGrade string

const (
	NetworkNone         NetworkGrade = "none"
	NetworkLoopbackOnly NetworkGrade = "loopback_only"
)

// CapabilityID is a stable, agent-independent capability identifier. UI
// features and health routing bind to a CapabilityID, never to an adapter
// or agent brand string.
type CapabilityID string

const (
	CapabilityDiscoveryAgentAndSurface      CapabilityID = "discovery.agent_and_surface"
	CapabilityInventoryComponents           CapabilityID = "inventory.components"
	CapabilityActivitySessions              CapabilityID = "activity.sessions"
	CapabilityActivityPromptMetadata        CapabilityID = "activity.prompt_metadata"
	CapabilityActivityTokenModelCost        CapabilityID = "activity.token_model_cost"
	CapabilityComponentsSkillInvocation     CapabilityID = "components.skill_invocation"
	CapabilityComponentsPluginAndCustomCmd  CapabilityID = "components.plugin_and_custom_command"
	CapabilityComponentsMCPLifecycle        CapabilityID = "components.mcp_lifecycle"
	CapabilityComponentsBuiltinToolCalls    CapabilityID = "components.builtin_tool_calls_and_approvals"
	CapabilityComponentsSubagentsCompaction CapabilityID = "components.subagents_and_compaction"
	CapabilityIngestionHistoricalImport     CapabilityID = "ingestion.historical_import"
	CapabilityIngestionLiveStream           CapabilityID = "ingestion.live_stream"
	// CapabilityIngestionEvidenceBridge identifies an optional, adapter-owned
	// rich-evidence source. Its health is intentionally independent from the
	// adapter's OTel/hook lanes: losing a bridge may degrade bridge-only
	// attribution without making ingestion.live_stream unavailable.
	CapabilityIngestionEvidenceBridge CapabilityID = "ingestion.evidence_bridge"
	CapabilityConfigurationInstall    CapabilityID = "configuration.install"
	CapabilityConfigurationLiveCanary CapabilityID = "configuration.live_canary"
	// CapabilityConfigurationHookInstall is a Session 11 addition (ADR 0014):
	// codex.user_hook/claude.user_hook write into the *same physical file*
	// configuration.install's codex.user_otel/claude.user_otel targets
	// already own (config.toml / settings.json, different keys/tables). A
	// distinct capability id -- rather than overloading
	// CapabilityConfigurationInstall to somehow mean two different
	// installer.Plan/ChangePlan pairs at once -- keeps PlanConfiguration's
	// one-capability-to-one-ChangePlan shape intact and keeps the two
	// targets' apply/rollback lifecycles independently observable.
	CapabilityConfigurationHookInstall CapabilityID = "configuration.hook_install"
)

// CapabilityState is the lifecycle of one capability for one installation.
type CapabilityState string

const (
	StateUnsupported CapabilityState = "unsupported"
	StateAvailable   CapabilityState = "available"
	StateConfigured  CapabilityState = "configured"
	StateHealthy     CapabilityState = "healthy"
	StateDegraded    CapabilityState = "degraded"
)

// EvidenceTier mirrors internal/observability's tiers so capability health
// and event evidence use one shared vocabulary.
type EvidenceTier string

const (
	TierCorroborated  EvidenceTier = "corroborated"
	TierNative        EvidenceTier = "native"
	TierReconstructed EvidenceTier = "reconstructed"
	TierInferred      EvidenceTier = "inferred"
)

// CapabilityRecord is the closed, durable shape a Reconcile/Audit call
// reports for one capability of one installation.
type CapabilityRecord struct {
	CapabilityID  CapabilityID    `json:"capability_id"`
	State         CapabilityState `json:"state"`
	EvidenceTier  EvidenceTier    `json:"evidence_tier"`
	LastCheckedAt time.Time       `json:"last_checked_at"`
	DetailRef     string          `json:"detail_ref"`
}

// AgentDetection is the closed, data-only detection hint set inside a
// Manifest. It is never executed; Discover only reads these hints to decide
// where to look before falling back to documented defaults.
type AgentDetection struct {
	Executables []string `json:"executables"`
	StateRoots  []string `json:"state_roots"`
}

// SourceDescriptor names one ingestion source an adapter can produce and
// the schema fingerprints it may emit under that source.
type SourceDescriptor struct {
	ID      string   `json:"id"`
	Kind    string   `json:"kind"`
	Schemas []string `json:"schemas"`
}

// Permissions is the exact, closed permission grant a manifest declares.
// A manifest requesting anything outside this shape fails validation.
type Permissions struct {
	FilesystemRead []string     `json:"filesystem_read"`
	Network        NetworkGrade `json:"network"`
	ProcessExec    []string     `json:"process_exec"`
}

// EvidencePlane names one component evidence surface whose support an adapter
// may declare. It is not the plane vocabulary of
// contracts/component-evidence.yaml (availability/runtime/optimization): those
// group assertion kinds, while this names the one surface whose *existence*
// varies between agents and therefore has to be stated rather than inferred.
type EvidencePlane string

// PlaneExposed is the model-visible component set: what the agent actually
// offered the model for this turn, as distinct from what is installed or
// enabled. Codex reports it through the App Server skills/list response.
// Claude Code documents no equivalent event or snapshot, so its absence there
// is a property of the agent, not an observation gap.
const PlaneExposed EvidencePlane = "exposed"

// PlaneSupportState is how an adapter obtains one evidence plane.
type PlaneSupportState string

const (
	// PlaneNative: the agent reports the plane directly.
	PlaneNative PlaneSupportState = "native"
	// PlaneReconstructed: the adapter derives the plane from another
	// observed surface, with the evidence tier that implies.
	PlaneReconstructed PlaneSupportState = "reconstructed"
	// PlaneUnsupported: the agent exposes no surface to read at all. This is
	// never rendered as zero and never as "not enough evidence yet" -- there
	// is nothing to look at, which is a different claim from having looked.
	PlaneUnsupported PlaneSupportState = "unsupported"
)

// ComponentPlaneSupport is one adapter's declaration, for one component kind,
// of how (or whether) it can observe one evidence plane. It is data: the data
// platform reads it to decide eligibility instead of branching on an agent
// name, and a future adapter with the same missing surface inherits the
// behaviour by declaring it.
//
// Reason is a bounded, machine-stable token explaining the state -- not a
// user-facing sentence and never a path, credential or host value.
type ComponentPlaneSupport struct {
	ComponentKind string            `json:"component_kind"`
	Plane         EvidencePlane     `json:"plane"`
	State         PlaneSupportState `json:"state"`
	Reason        string            `json:"reason"`
}

// Manifest is the closed, versioned, data-only description of one adapter.
// Manifests are parsed with ParseManifest, never evaluated as code.
//
// ComponentPlaneSupport is optional: a manifest omitting it parses, and an
// installation with no declaration is treated as supported, which preserves
// today's behaviour exactly for fakeadapter, wayfinder and every future
// adapter that has not yet been audited for this.
type Manifest struct {
	APIVersion            string                  `json:"api_version"`
	ID                    string                  `json:"id"`
	Version               string                  `json:"version"`
	Execution             ExecutionForm           `json:"execution"`
	AgentDetection        AgentDetection          `json:"agent_detection"`
	Capabilities          map[CapabilityID]string `json:"capabilities"`
	Sources               []SourceDescriptor      `json:"sources"`
	Permissions           Permissions             `json:"permissions"`
	HealthChecks          []string                `json:"health_checks"`
	ComponentPlaneSupport []ComponentPlaneSupport `json:"component_plane_support,omitempty"`
}

// DetectionMethod records how an InstallationCandidate was found, so a
// discovery result never overstates its own confidence.
type DetectionMethod string

const (
	DetectionExecutableOnPath     DetectionMethod = "executable_on_path"
	DetectionDocumentedEnvVar     DetectionMethod = "documented_env_var"
	DetectionDocumentedConfigFile DetectionMethod = "documented_config_file"
	DetectionDocumentedStateRoot  DetectionMethod = "documented_state_root_present"
)

// InstallationCandidate is one thing Discover found: a possible agent
// installation, with the evidence for why it was found expressed as a
// DetectionMethod and a confidence in [0,1], not a boolean certainty.
type InstallationCandidate struct {
	CandidateID     string          `json:"candidate_id"`
	AdapterID       string          `json:"adapter_id"`
	SurfaceID       string          `json:"surface_id"`
	StateRoot       string          `json:"state_root"`
	DetectedVersion string          `json:"detected_version"`
	DetectionMethod DetectionMethod `json:"detection_method"`
	Confidence      float64         `json:"confidence"`
}

// Installation is a confirmed, addressable target for Inventory/PlanConfiguration/Reconcile/Audit.
type Installation struct {
	InstallationID string `json:"installation_id"`
	AdapterID      string `json:"adapter_id"`
	SurfaceID      string `json:"surface_id"`
	StateRoot      string `json:"state_root"`
}

// NodeKind enumerates the inventory entity graph vertex types from TDD 05.
type NodeKind string

const (
	NodeDevice                  NodeKind = "device"
	NodeAgentInstallation       NodeKind = "agent_installation"
	NodeAgentSurface            NodeKind = "agent_surface"
	NodeAgentVersion            NodeKind = "agent_version"
	NodePluginPackage           NodeKind = "plugin_package"
	NodePluginVersion           NodeKind = "plugin_version"
	NodeSkillIdentity           NodeKind = "skill_identity"
	NodeMCPServerInstance       NodeKind = "mcp_server_instance"
	NodeMCPTool                 NodeKind = "mcp_tool"
	NodeHookDefinition          NodeKind = "hook_definition"
	NodeCustomCommandDefinition NodeKind = "custom_command_definition"
	NodeAppDefinition           NodeKind = "app_definition"
	NodeSubagentDefinition      NodeKind = "subagent_definition"
	NodeCacheArtifact           NodeKind = "cache_artifact"
)

// SourceScope is where a node was declared/discovered from.
type SourceScope string

const (
	ScopeSystem           SourceScope = "system"
	ScopeUser             SourceScope = "user"
	ScopeRepository       SourceScope = "repository"
	ScopeAdmin            SourceScope = "admin"
	ScopeMarketplace      SourceScope = "marketplace"
	ScopePluginCache      SourceScope = "plugin_cache"
	ScopeTransientSession SourceScope = "transient_session"
)

// Node is one vertex of the inventory entity graph. PathPseudonym is the
// only durable representation of a filesystem path: it is an HMAC output,
// never a raw path. DisplayAlias is a user-assigned local label and is
// never derived from raw path bytes either.
type Node struct {
	NodeID        string      `json:"node_id"`
	Kind          NodeKind    `json:"kind"`
	DeclaredName  string      `json:"declared_name"`
	Version       string      `json:"version"`
	SourceScope   SourceScope `json:"source_scope"`
	PathPseudonym string      `json:"path_pseudonym"`
	DisplayAlias  string      `json:"display_alias"`
	CachedOnly    bool        `json:"cached_only"`
	Fingerprint   string      `json:"fingerprint"`
}

// EdgeKind enumerates the relationship types between two inventory nodes.
type EdgeKind string

const (
	EdgeBundles       EdgeKind = "bundles"
	EdgeProvides      EdgeKind = "provides"
	EdgeConfiguredIn  EdgeKind = "configured_in"
	EdgeEnabledFor    EdgeKind = "enabled_for"
	EdgeShadows       EdgeKind = "shadows"
	EdgeCollidesWith  EdgeKind = "collides_with"
	EdgeDependsOn     EdgeKind = "depends_on"
	EdgeObservedUsing EdgeKind = "observed_using"
)

// Edge connects two Node values by NodeID. The same declared name across
// two different SourceScope/Fingerprint values never forces a merge into
// one node; instead a Shadows or CollidesWith edge links the two nodes.
type Edge struct {
	EdgeID   string   `json:"edge_id"`
	Kind     EdgeKind `json:"kind"`
	FromNode string   `json:"from_node_id"`
	ToNode   string   `json:"to_node_id"`
}

// CoverageGapClass classifies one entry an inventory scan found but could not
// turn into a component node. The vocabulary is closed: a scanner that meets
// something outside it must extend this list rather than skip the entry, which
// is the whole point — an unrecorded skip is a silently truncated inventory
// that still reports itself complete.
type CoverageGapClass string

const (
	// CoverageGapUnresolvableSymlink: the entry is a symlink whose target is
	// not reachable from the permitted roots. On a containerised host this is
	// the ordinary consequence of binding a link directory without binding the
	// library the links point into.
	CoverageGapUnresolvableSymlink CoverageGapClass = "unresolvable_symlink"
	// CoverageGapUnreadableManifest: the manifest exists but the probe was
	// refused or failed.
	CoverageGapUnreadableManifest CoverageGapClass = "unreadable_component_manifest"
	// CoverageGapTruncatedManifest: the manifest exceeded the bounded read
	// ceiling, so any parse would be of a partial file.
	CoverageGapTruncatedManifest CoverageGapClass = "truncated_component_manifest"
	// CoverageGapUnparseableManifest: the manifest was read in full but its
	// frontmatter declares no usable identity.
	CoverageGapUnparseableManifest CoverageGapClass = "unparseable_component_manifest"
)

// ValidCoverageGapClass reports whether class is a member of the closed
// vocabulary.
func ValidCoverageGapClass(class CoverageGapClass) bool {
	switch class {
	case CoverageGapUnresolvableSymlink, CoverageGapUnreadableManifest,
		CoverageGapTruncatedManifest, CoverageGapUnparseableManifest:
		return true
	default:
		return false
	}
}

// CoverageGaps is a per-class tally of entries one scan could not inventory.
type CoverageGaps map[CoverageGapClass]int

// Add records one gap of the given class. An out-of-vocabulary class is
// recorded as unparseable rather than dropped.
func (g CoverageGaps) Add(class CoverageGapClass) {
	if !ValidCoverageGapClass(class) {
		class = CoverageGapUnparseableManifest
	}
	g[class]++
}

// Merge folds another tally into this one.
func (g CoverageGaps) Merge(other CoverageGaps) {
	for class, count := range other {
		g[class] += count
	}
}

// Total is the number of entries the scan could not inventory.
func (g CoverageGaps) Total() int {
	total := 0
	for _, count := range g {
		total += count
	}
	return total
}

// InventorySnapshot is an immutable, point-in-time observation. Reconcile
// derives current state by diffing two snapshots; a snapshot itself is
// never mutated in place.
//
// CoverageGapCount/CoverageGapClasses carry what the scan could not read. They
// are not cosmetic: cold eligibility for an agent with no exposure surface
// rests on this snapshot being complete, so a mis-mounted host has to fail the
// completeness check rather than yield a confident count over a truncated scan.
type InventorySnapshot struct {
	SnapshotID         string       `json:"snapshot_id"`
	AdapterID          string       `json:"adapter_id"`
	AdapterVersion     string       `json:"adapter_version"`
	InstallationID     string       `json:"installation_id"`
	ObservedAt         time.Time    `json:"observed_at"`
	Fingerprint        string       `json:"fingerprint"`
	Nodes              []Node       `json:"nodes"`
	Edges              []Edge       `json:"edges"`
	CoverageGapCount   int          `json:"coverage_gap_count"`
	CoverageGapClasses CoverageGaps `json:"coverage_gap_classes,omitempty"`
}

// ChangePlan is the reversible configuration change contract for one
// capability of one installation control point. Session 05 does not invent
// a parallel apply/rollback mechanism: an adapter's PlanConfiguration
// builds an internal/installer Plan (see BuildChangePlan) and Apply/
// Rollback/Remove reuse installer.SimulateApply/SimulateRollback/
// SimulateRemove verbatim, scoped by CapabilityID.
type ChangePlan struct {
	PlanID                  string       `json:"plan_id"`
	InstallationCandidateID string       `json:"installation_candidate_id"`
	CapabilityID            CapabilityID `json:"capability_id"`
	PreconditionHash        string       `json:"precondition_hash"`
	BeforeSanitizedDiff     []string     `json:"before_sanitized_diff"`
	AfterSanitizedDiff      []string     `json:"after_sanitized_diff"`
	BackupLocator           string       `json:"backup_locator"`
	Commands                []string     `json:"commands"`
	PrivacyDisclosure       []string     `json:"privacy_disclosure"`
	RollbackCommand         string       `json:"rollback_command"`
}

// ReconcileScope bounds one Reconcile call to an installation and the
// capabilities it should refresh, optionally incremental since a prior
// snapshot.
type ReconcileScope struct {
	InstallationID  string         `json:"installation_id"`
	SinceSnapshotID string         `json:"since_snapshot_id"`
	CapabilityIDs   []CapabilityID `json:"capability_ids"`
}

// ReconcileResult is the closed, deterministic outcome of diffing two
// InventorySnapshot values. Replaying the same two snapshots twice must
// yield a byte-identical ReconcileResult.
type ReconcileResult struct {
	SnapshotID     string   `json:"snapshot_id"`
	AddedNodeIDs   []string `json:"added_node_ids"`
	RemovedNodeIDs []string `json:"removed_node_ids"`
	ChangedNodeIDs []string `json:"changed_node_ids"`
	NewCollisions  []string `json:"new_collisions"`
	Completeness   string   `json:"completeness"`
}

// AuditMode selects how a Check is performed.
type AuditMode string

const (
	AuditPassive       AuditMode = "passive"
	AuditFixtureReplay AuditMode = "fixture_replay"
	AuditLiveCanary    AuditMode = "live_canary"
)

// CheckStatus is the closed outcome of one CheckResult.
type CheckStatus string

const (
	CheckPass               CheckStatus = "pass"
	CheckFail               CheckStatus = "fail"
	CheckSkippedUnsupported CheckStatus = "skipped_unsupported"
)

// CheckResult is one audit outcome for one capability of one installation.
type CheckResult struct {
	CheckID      string       `json:"check_id"`
	CapabilityID CapabilityID `json:"capability_id"`
	Mode         AuditMode    `json:"mode"`
	Status       CheckStatus  `json:"status"`
	DetailRef    string       `json:"detail_ref"`
	ObservedAt   time.Time    `json:"observed_at"`
}
