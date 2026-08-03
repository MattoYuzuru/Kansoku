package claudeadapter

import (
	"encoding/json"
	"path/filepath"
	"sort"

	"kansoku.local/kansoku/internal/adaptersdk"
)

// A Claude Code plugin package ships more than skills. The on-disk layout
// under <cache>/<marketplace>/<plugin>/<version>/ carries agents/*.md,
// commands/*.md, hooks/hooks.json and an MCP server declaration alongside
// skills/, and contracts/plugins/inventory-and-identity.yaml has always
// declared skill, hook, mcp, command and app as child kinds. The scanner read
// only skills/, so four of the five child kinds were structurally invisible:
// PluginDescriptor already had BundledCommands/BundledSubagents/BundledHooks/
// BundledMCPServers, the graph already consumed them, and nothing ever filled
// them.
//
// Every bound below mirrors the skill scanner's: the same per-root manifest
// cap, the same coverage-gap classification, and the same rule that a refused
// read is recorded rather than swallowed.

const (
	// maxBundledManifests bounds one directory inside one plugin version, the
	// same way maxSkillManifests bounds a skill root.
	maxBundledManifests = 256
	// maxAdvertisedTools bounds the tool list read from an MCP declaration.
	maxAdvertisedTools = 128
)

// mcpServerConfigShape is the documented .mcp.json shape: a map of server name
// to its declaration. Only the names and any declared tool list are read --
// never a command line, an argument vector or an environment block, all of
// which routinely carry paths and secrets.
type mcpServerConfigShape struct {
	MCPServers map[string]struct {
		Tools    []string `json:"tools"`
		Disabled bool     `json:"disabled"`
	} `json:"mcpServers"`
}

// hooksConfigShape is the documented hooks/hooks.json shape. Matchers are
// deliberately not read: a matcher is user-authored and can embed a path or a
// project name, exactly as hook_matcher is excluded on the OTLP lane.
type hooksConfigShape struct {
	Hooks map[string][]struct {
		Matcher string `json:"matcher"`
		Hooks   []struct {
			Type string `json:"type"`
		} `json:"hooks"`
	} `json:"hooks"`
}

// scanBundledMarkdownRoot lists one directory of markdown-defined components
// (agents/ or commands/) inside a plugin version. The component name is the
// file's base name without its extension, which is how Claude Code addresses
// both subagents and slash commands.
func scanBundledMarkdownRoot(
	host *adaptersdk.HostView,
	root string,
	scope adaptersdk.SourceScope,
) (names []string, pseudonyms []string, fingerprints []string, gaps adaptersdk.CoverageGaps) {
	gaps = adaptersdk.CoverageGaps{}
	probe, err := host.ReadProbe(root)
	if err != nil {
		gaps.Add(rootGapClass(root))
		return nil, nil, nil, gaps
	}
	if !probe.Exists {
		return nil, nil, nil, nil
	}
	entries, err := host.ListDirectoryProbe(root)
	if err != nil {
		gaps.Add(rootGapClass(root))
		return nil, nil, nil, gaps
	}
	type bundled struct{ name, pseudonym, fingerprint string }
	found := make([]bundled, 0, len(entries))
	for _, entry := range entries {
		if len(found) >= maxBundledManifests {
			break
		}
		if entry.IsDir || filepath.Ext(entry.Name) != ".md" {
			continue
		}
		manifestPath := filepath.Join(root, entry.Name)
		result, err := host.ReadConfigProbe(manifestPath)
		if err != nil {
			if entry.IsSymlink {
				gaps.Add(adaptersdk.CoverageGapUnresolvableSymlink)
			} else {
				gaps.Add(adaptersdk.CoverageGapUnreadableManifest)
			}
			continue
		}
		if !result.Exists {
			if entry.IsSymlink {
				gaps.Add(adaptersdk.CoverageGapUnresolvableSymlink)
			}
			continue
		}
		if result.Truncated {
			gaps.Add(adaptersdk.CoverageGapTruncatedManifest)
			continue
		}
		// A markdown component may declare a name in frontmatter exactly as a
		// skill does. When it does not, the file name is the address, which is
		// an observation rather than a guess.
		name, _, _, ok := parseSkillFrontmatter(result.Content)
		if !ok || name == "" {
			name = entry.Name[:len(entry.Name)-len(".md")]
		}
		found = append(found, bundled{
			name:        name,
			pseudonym:   host.PseudonymizePath(manifestPath),
			fingerprint: stableHex("bundled-manifest", string(scope), name, string(result.Content)),
		})
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].name == found[j].name {
			return found[i].pseudonym < found[j].pseudonym
		}
		return found[i].name < found[j].name
	})
	for _, item := range found {
		names = append(names, item.name)
		pseudonyms = append(pseudonyms, item.pseudonym)
		fingerprints = append(fingerprints, item.fingerprint)
	}
	return names, pseudonyms, fingerprints, gaps
}

// scanBundledSubagents reads agents/*.md inside one plugin version.
func scanBundledSubagents(host *adaptersdk.HostView, versionDir string) ([]SubagentDescriptor, adaptersdk.CoverageGaps) {
	names, pseudonyms, fingerprints, gaps := scanBundledMarkdownRoot(
		host, filepath.Join(versionDir, "agents"), adaptersdk.ScopePluginCache)
	subagents := make([]SubagentDescriptor, 0, len(names))
	for index, name := range names {
		subagents = append(subagents, SubagentDescriptor{
			Name: name, Scope: adaptersdk.ScopePluginCache, Enabled: true,
			PathPseudonym: pseudonyms[index], Fingerprint: fingerprints[index],
		})
	}
	return subagents, gaps
}

// scanBundledCommands reads commands/*.md inside one plugin version.
func scanBundledCommands(host *adaptersdk.HostView, versionDir string) ([]CommandDescriptor, adaptersdk.CoverageGaps) {
	names, pseudonyms, fingerprints, gaps := scanBundledMarkdownRoot(
		host, filepath.Join(versionDir, "commands"), adaptersdk.ScopePluginCache)
	commands := make([]CommandDescriptor, 0, len(names))
	for index, name := range names {
		commands = append(commands, CommandDescriptor{
			Name: name, Scope: adaptersdk.ScopePluginCache, Enabled: true,
			PathPseudonym: pseudonyms[index], Fingerprint: fingerprints[index],
		})
	}
	return commands, gaps
}

// scanBundledHooks reads hooks/hooks.json inside one plugin version. One
// descriptor is produced per declared hook event, which is the granularity the
// OTLP hook_registered lane also reports.
//
// Trusted is false for a plugin-bundled hook: trust is a property of the
// user's own settings, and the scanner has no evidence of it here. Claiming
// trust the host never granted would be exactly the kind of fabrication the
// inventory contract forbids.
func scanBundledHooks(host *adaptersdk.HostView, versionDir string) ([]HookDescriptor, adaptersdk.CoverageGaps) {
	gaps := adaptersdk.CoverageGaps{}
	manifestPath := filepath.Join(versionDir, "hooks", "hooks.json")
	result, err := host.ReadConfigProbe(manifestPath)
	if err != nil {
		gaps.Add(adaptersdk.CoverageGapUnreadableManifest)
		return nil, gaps
	}
	if !result.Exists {
		return nil, nil
	}
	if result.Truncated {
		gaps.Add(adaptersdk.CoverageGapTruncatedManifest)
		return nil, gaps
	}
	var shape hooksConfigShape
	if json.Unmarshal(result.Content, &shape) != nil {
		gaps.Add(adaptersdk.CoverageGapUnparseableManifest)
		return nil, gaps
	}
	events := make([]string, 0, len(shape.Hooks))
	for event := range shape.Hooks {
		events = append(events, event)
	}
	sort.Strings(events)
	if len(events) > maxBundledManifests {
		events = events[:maxBundledManifests]
	}
	hooks := make([]HookDescriptor, 0, len(events))
	for _, event := range events {
		hooks = append(hooks, HookDescriptor{
			Name: event, Scope: adaptersdk.ScopePluginCache, Enabled: true, Trusted: false,
			PathPseudonym: host.PseudonymizePath(manifestPath),
			Fingerprint:   stableHex("bundled-hook", event, string(result.Content)),
		})
	}
	return hooks, gaps
}

// scanBundledMCPServers reads .mcp.json inside one plugin version. Only server
// names and any declared tool list are read; the command, arguments and
// environment of a server declaration are never touched.
func scanBundledMCPServers(host *adaptersdk.HostView, versionDir string) ([]MCPServerDescriptor, adaptersdk.CoverageGaps) {
	gaps := adaptersdk.CoverageGaps{}
	manifestPath := filepath.Join(versionDir, ".mcp.json")
	result, err := host.ReadConfigProbe(manifestPath)
	if err != nil {
		gaps.Add(adaptersdk.CoverageGapUnreadableManifest)
		return nil, gaps
	}
	if !result.Exists {
		return nil, nil
	}
	if result.Truncated {
		gaps.Add(adaptersdk.CoverageGapTruncatedManifest)
		return nil, gaps
	}
	var shape mcpServerConfigShape
	if json.Unmarshal(result.Content, &shape) != nil {
		gaps.Add(adaptersdk.CoverageGapUnparseableManifest)
		return nil, gaps
	}
	names := make([]string, 0, len(shape.MCPServers))
	for name := range shape.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > maxBundledManifests {
		names = names[:maxBundledManifests]
	}
	servers := make([]MCPServerDescriptor, 0, len(names))
	for _, name := range names {
		declaration := shape.MCPServers[name]
		tools := append([]string(nil), declaration.Tools...)
		sort.Strings(tools)
		if len(tools) > maxAdvertisedTools {
			tools = tools[:maxAdvertisedTools]
		}
		servers = append(servers, MCPServerDescriptor{
			Name: name, Scope: adaptersdk.ScopePluginCache, Enabled: !declaration.Disabled,
			AdvertisedTools: tools,
			PathPseudonym:   host.PseudonymizePath(manifestPath),
			Fingerprint:     stableHex("bundled-mcp", name, string(result.Content)),
		})
	}
	return servers, gaps
}
