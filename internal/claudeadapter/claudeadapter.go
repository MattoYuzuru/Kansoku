// Package claudeadapter implements the Claude Code Adapter (Session 07)
// against the Session 05 adaptersdk.Adapter contract. It is the second real,
// non-fictional agent adapter in the repository: Session 06's codexadapter
// already proved internal/adaptersdk hosts a real agent with zero agent-name
// branch inside adaptersdk's own core; this package proves the same registry/
// HostView/inventory/ChangePlan machinery hosts a second, differently-shaped
// real agent the identical way -- by registering itself once, under its own
// manifest.ID ("claude"), through adaptersdk.Registry.Register.
//
// This file (and discover.go, hook.go, otel.go) implement Stage 2 of the
// session's sequential checkpointed build: installation discovery, the
// claude.hook source and the claude.otel source only. Inventory,
// PlanConfiguration, Normalize/Reconcile/Audit for the full adaptersdk.Adapter
// interface are completed in a later stage; see stage2_stub.go for the
// minimal, honest (never-fabricated) implementations this stage supplies so
// the type still satisfies adaptersdk.Adapter end to end.
package claudeadapter

import (
	"kansoku.local/kansoku/internal/adaptersdk"
)

const (
	// AdapterID matches contracts/claude/manifest.yaml's adapter_id exactly.
	// adaptersdk.Registry keys purely on this string; nothing in
	// internal/adaptersdk itself ever branches on it.
	AdapterID = "claude"

	// AdapterVersion is this adapter recipe's own version, independent of any
	// Claude Code CLI/IDE-extension/app release the recipe targets. It is
	// recorded in every InventorySnapshot/ChangePlan this adapter produces.
	AdapterVersion = "0.1.0"

	// executableName is the only executable agent_detection.executables
	// names; ParseManifest's HostView.ExecProbe allowlist must include this
	// exact name for the version probe to run at all.
	executableName = "claude"
)

// DocumentedConfigRoot names one documented Claude Code settings location
// contracts/claude/manifest.yaml's agent_detection.documented_config_roots
// declares. Claude Code documents settings-file locations, not a dedicated
// CLAUDE_HOME-shaped state-root environment variable; Discover therefore
// resolves state strictly from these documented roots (already populated
// into HostView.AllowedRoots by the caller) and never falls back to a
// speculative home-directory scan.
type DocumentedConfigRoot string

const (
	ConfigRootUserSettings    DocumentedConfigRoot = "claude_user_settings"
	ConfigRootProjectSettings DocumentedConfigRoot = "claude_project_settings"
	ConfigRootManagedSettings DocumentedConfigRoot = "claude_managed_settings"
)

// DocumentedConfigRoots returns the closed, documented settings-location
// vocabulary this recipe knows about, mirroring
// contracts/claude/manifest.yaml's agent_detection.documented_config_roots
// verbatim.
func DocumentedConfigRoots() []DocumentedConfigRoot {
	return []DocumentedConfigRoot{ConfigRootUserSettings, ConfigRootProjectSettings, ConfigRootManagedSettings}
}

// Surface enumerates the Claude Code surfaces contracts/claude/manifest.yaml
// declares. Detect where observable; two candidates sharing one state root
// but differing surface remain distinct installation candidates -- Discover
// never merges installations solely by shared state root.
type Surface string

const (
	SurfaceCLI          Surface = "cli"
	SurfaceIDEExtension Surface = "ide_extension"
	SurfaceApp          Surface = "app"
)

// Adapter implements adaptersdk.Adapter for Claude Code. Stage 2 supplies
// Manifest, Discover, the claude.hook helper contract and the claude.otel
// schema-fingerprint mapping; the remaining interface methods are added by a
// later stage and, until then, return an explicit "not yet implemented"
// error (see stage2_stub.go) rather than silently fabricating a
// plausible-looking empty result.
type Adapter struct{}

// New returns a ready-to-register Claude Code adapter.
func New() *Adapter { return &Adapter{} }

var _ adaptersdk.Adapter = (*Adapter)(nil)

// Manifest returns the closed, data-only Claude Code adapter manifest. Every
// field here must stay in lockstep with contracts/claude/manifest.yaml; a
// later validator stage checks the two never drift silently.
func (a *Adapter) Manifest() adaptersdk.Manifest {
	return adaptersdk.Manifest{
		APIVersion: adaptersdk.AdapterAPIVersion,
		ID:         AdapterID,
		Version:    AdapterVersion,
		Execution:  adaptersdk.ExecutionBuiltin,
		AgentDetection: adaptersdk.AgentDetection{
			Executables: []string{executableName},
			// Claude Code documents settings file locations rather than a
			// dedicated state-root env var; these three tokens name the
			// documented config roots, never a speculative $HOME entry.
			StateRoots: []string{
				string(ConfigRootUserSettings),
				string(ConfigRootProjectSettings),
				string(ConfigRootManagedSettings),
			},
		},
		Capabilities: map[adaptersdk.CapabilityID]string{
			adaptersdk.CapabilityDiscoveryAgentAndSurface:      "supported",
			adaptersdk.CapabilityInventoryComponents:           "supported",
			adaptersdk.CapabilityActivitySessions:              "supported",
			adaptersdk.CapabilityActivityPromptMetadata:        "supported",
			adaptersdk.CapabilityActivityTokenModelCost:        "supported",
			adaptersdk.CapabilityComponentsSkillInvocation:     "supported",
			adaptersdk.CapabilityComponentsPluginAndCustomCmd:  "supported",
			adaptersdk.CapabilityComponentsMCPLifecycle:        "supported",
			adaptersdk.CapabilityComponentsBuiltinToolCalls:    "supported",
			adaptersdk.CapabilityComponentsSubagentsCompaction: "supported",
			adaptersdk.CapabilityIngestionHistoricalImport:     "supported",
			adaptersdk.CapabilityIngestionLiveStream:           "supported",
			adaptersdk.CapabilityConfigurationInstall:          "supported",
			adaptersdk.CapabilityConfigurationLiveCanary:       "supported",
		},
		Sources: []adaptersdk.SourceDescriptor{
			{ID: sourceIDHook, Kind: "hook_http", Schemas: []string{hookSourceSchemaID}},
			{ID: sourceIDOTel, Kind: "otlp_log_span_metric", Schemas: []string{otelSourceSchemaID}},
		},
		Permissions: adaptersdk.Permissions{
			FilesystemRead: []string{
				string(ConfigRootUserSettings),
				string(ConfigRootProjectSettings),
				string(ConfigRootManagedSettings),
				"claude_plugin_and_marketplace_cache",
				"claude_local_session_transcript_directory",
			},
			Network:     adaptersdk.NetworkLoopbackOnly,
			ProcessExec: []string{executableName},
		},
		HealthChecks: []string{"config", "hook_trust", "otel_config", "fixture_replay", "watermark", "live_canary"},
	}
}
