// Package codexadapter implements the Codex Adapter (Session 06) against the
// Session 05 adaptersdk.Adapter contract. It is the first real, non-fictional
// agent adapter in the repository: internal/adaptersdk/fakeadapter proved the
// registry/inventory/reconciliation machinery has zero agent-name branch;
// this package proves the same machinery hosts a real agent by registering
// itself once, under its own manifest.ID ("codex"), the exact same way.
//
// This file (and discover.go) implement Stage 2 of the session's sequential
// checkpointed build: installation discovery only. Inventory,
// PlanConfiguration, Normalize/Reconcile/Audit for the full adaptersdk.Adapter
// interface are completed in a later stage; see hook.go and otel.go for the
// codex.hook / codex.otel source-side logic this stage also delivers.
package codexadapter

import (
	"kansoku.local/kansoku/internal/adaptersdk"
)

const (
	// AdapterID matches contracts/codex/manifest.yaml's adapter_id exactly.
	// adaptersdk.Registry keys purely on this string; nothing in
	// internal/adaptersdk itself ever branches on it.
	AdapterID = "codex"

	// AdapterVersion is this adapter recipe's own version, independent of any
	// Codex CLI/IDE/app release the recipe targets. It is recorded in every
	// InventorySnapshot/ChangePlan this adapter produces.
	AdapterVersion = "0.1.0"

	// StateRootEnv is the documented Codex environment variable Discover
	// checks before falling back to the documented default state root. This
	// mirrors contracts/codex/manifest.yaml's agent_detection.state_root_env_var
	// verbatim.
	StateRootEnv = "CODEX_HOME"

	// DocumentedDefaultStateRoot is the documented default Codex state root,
	// consulted only after StateRootEnv is resolved and found absent -- Discover
	// never scans the entire home directory speculatively.
	DocumentedDefaultStateRoot = "~/.codex"

	// executableName is the only executable agent_detection.executables names;
	// ParseManifest's HostView.ExecProbe allowlist must include this exact
	// name for the version probe to run at all.
	executableName = "codex"
)

// Surface enumerates the Codex surfaces contracts/codex/manifest.yaml
// declares. Detect where observable; two candidates sharing one state root
// but differing surface remain distinct installation candidates -- Discover
// never merges installations solely by shared state root.
type Surface string

const (
	SurfaceCLI          Surface = "cli"
	SurfaceIDEExtension Surface = "ide_extension"
	SurfaceApp          Surface = "app"
)

// Adapter implements adaptersdk.Adapter for Codex. Stage 2 supplies Manifest
// and Discover; the remaining interface methods are added by a later stage
// and, until then, return an explicit "not yet implemented" error rather than
// silently fabricating a plausible-looking empty result.
type Adapter struct{}

// New returns a ready-to-register Codex adapter.
func New() *Adapter { return &Adapter{} }

var _ adaptersdk.Adapter = (*Adapter)(nil)

// Manifest returns the closed, data-only Codex adapter manifest. Every field
// here must stay in lockstep with contracts/codex/manifest.yaml; a later
// validator stage checks the two never drift silently.
func (a *Adapter) Manifest() adaptersdk.Manifest {
	return adaptersdk.Manifest{
		APIVersion: adaptersdk.AdapterAPIVersion,
		ID:         AdapterID,
		Version:    AdapterVersion,
		Execution:  adaptersdk.ExecutionBuiltin,
		AgentDetection: adaptersdk.AgentDetection{
			Executables: []string{executableName},
			StateRoots:  []string{"$" + StateRootEnv, DocumentedDefaultStateRoot},
		},
		Capabilities: map[adaptersdk.CapabilityID]string{
			adaptersdk.CapabilityDiscoveryAgentAndSurface:      "supported",
			adaptersdk.CapabilityInventoryComponents:           "supported",
			adaptersdk.CapabilityActivitySessions:              "supported",
			adaptersdk.CapabilityActivityPromptMetadata:        "supported",
			adaptersdk.CapabilityActivityTokenModelCost:        "supported",
			adaptersdk.CapabilityComponentsSkillInvocation:     "partial",
			adaptersdk.CapabilityComponentsPluginAndCustomCmd:  "supported",
			adaptersdk.CapabilityComponentsMCPLifecycle:        "supported",
			adaptersdk.CapabilityComponentsBuiltinToolCalls:    "supported",
			adaptersdk.CapabilityComponentsSubagentsCompaction: "supported",
			adaptersdk.CapabilityIngestionHistoricalImport:     "supported",
			adaptersdk.CapabilityIngestionLiveStream:           "supported",
			adaptersdk.CapabilityIngestionEvidenceBridge:       "available",
			adaptersdk.CapabilityConfigurationInstall:          "supported",
			adaptersdk.CapabilityConfigurationLiveCanary:       "supported",
		},
		Sources: []adaptersdk.SourceDescriptor{
			{ID: sourceIDHook, Kind: "hook_http", Schemas: []string{hookSourceSchemaID}},
			{ID: sourceIDOTel, Kind: "otlp_log_span_metric", Schemas: []string{otelSourceSchemaID}},
			{ID: AppServerBridgeID, Kind: "evidence_bridge", Schemas: []string{AdapterID + ".bridge/" + AppServerSchemaVersion}},
		},
		Permissions: adaptersdk.Permissions{
			FilesystemRead: []string{
				"$" + StateRootEnv,
				"$" + StateRootEnv + "/config.toml",
				"$" + StateRootEnv + "/sessions",
				"$" + StateRootEnv + "/hooks",
				"$" + StateRootEnv + "/skills",
				"$" + StateRootEnv + "/plugins",
			},
			Network:     adaptersdk.NetworkLoopbackOnly,
			ProcessExec: []string{executableName},
		},
		HealthChecks: []string{"config", "hook_trust", "otel_config", "fixture_replay", "watermark", "live_canary"},
	}
}
