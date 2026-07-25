package claudeadapter

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"kansoku.local/kansoku/internal/adaptersdk"
)

// settingsFileName is the Claude Code surface config file this scan reads,
// matching surfaceProbes' own "settings.json" entry in discover.go.
const settingsFileName = "settings.json"

// maxScannedEntries bounds how many enabledPlugins/mcpServers entries one
// scan reads out of a single settings.json, mirroring InventoryInput's own
// maxRepositoryTargets ceiling: a config file may not be used to force an
// unbounded parse even though ReadConfigProbe already bounds the raw byte
// read.
const maxScannedEntries = 256

// claudeSettingsShape is the closed, bounded subset of settings.json this
// scan ever decodes. contracts/claude/transcript-and-inventory.yaml names
// "plugins" and "mcp_servers" as inventoried components; this scan reads
// exactly the two documented keys TDD 11 section C names
// (enabledPlugins, mcpServers) and nothing else in the file -- no hook
// command, env value or other settings.json key is ever parsed here.
type claudeSettingsShape struct {
	EnabledPlugins map[string]bool                     `json:"enabledPlugins"`
	MCPServers     map[string]claudeMCPServerConfigJSON `json:"mcpServers"`
}

// claudeMCPServerConfigJSON is the closed set of fields this scan reads
// from one mcpServers entry. Only presence/name is ever used to build a
// Node; command/args/env are decoded (so json.Unmarshal does not choke on
// them) but never read into any Descriptor or Node field, matching the
// "never a raw command/credential value leaves this scan" guarantee.
type claudeMCPServerConfigJSON struct {
	Disabled bool `json:"disabled"`
}

// ScanHostInventory performs the bounded, read-only host scan TDD 11
// section C requires: it reads Claude Code's settings.json enabledPlugins/
// mcpServers keys through host.ReadConfigProbe (never a raw unbounded
// filesystem read) and returns an InventoryInput ready for
// BuildInventorySnapshot. host may be nil (no scan possible); scanned
// reports whether a bounded scan was actually attempted and completed, so
// the caller can distinguish "we looked and found genuinely zero plugins/
// MCP servers" from "we never looked."
func ScanHostInventory(host *adaptersdk.HostView, target adaptersdk.Installation) (InventoryInput, bool) {
	input := InventoryInput{InstallationID: target.InstallationID}
	if host == nil || target.StateRoot == "" || !filepath.IsAbs(target.StateRoot) {
		return input, false
	}
	settingsPath := filepath.Join(target.StateRoot, settingsFileName)
	result, err := host.ReadConfigProbe(settingsPath)
	if err != nil || !result.Exists {
		return input, false
	}
	var shape claudeSettingsShape
	decoder := json.NewDecoder(strings.NewReader(string(result.Content)))
	if err := decoder.Decode(&shape); err != nil {
		// A malformed or truncated settings.json is not a scan the caller can
		// trust; report "attempted but nothing usable observed" rather than
		// guessing at a partial parse.
		return input, false
	}

	pathPseudonym := host.PseudonymizePath(settingsPath)

	pluginNames := sortedKeysBool(shape.EnabledPlugins, maxScannedEntries)
	for _, name := range pluginNames {
		enabled := shape.EnabledPlugins[name]
		activeEnabledFor := ""
		if enabled {
			activeEnabledFor = target.InstallationID
		}
		input.Plugins = append(input.Plugins, PluginDescriptor{
			Name:             name,
			Scope:            adaptersdk.ScopeUser,
			ActiveEnabledFor: activeEnabledFor,
			PathPseudonym:    pathPseudonym,
			Fingerprint:      stableHex("plugin-config", name, boolString(enabled)),
		})
	}

	mcpNames := sortedKeysMCP(shape.MCPServers, maxScannedEntries)
	for _, name := range mcpNames {
		server := shape.MCPServers[name]
		input.StandaloneMCPServers = append(input.StandaloneMCPServers, MCPServerDescriptor{
			Name:            name,
			Scope:           adaptersdk.ScopeUser,
			Enabled:         !server.Disabled,
			AdvertisedTools: nil,
			PathPseudonym:   pathPseudonym,
			Fingerprint:     stableHex("mcp-server-config", name, boolString(!server.Disabled)),
		})
	}

	return input, true
}

func sortedKeysBool(values map[string]bool, limit int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	return keys
}

func sortedKeysMCP(values map[string]claudeMCPServerConfigJSON, limit int) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) > limit {
		keys = keys[:limit]
	}
	return keys
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
