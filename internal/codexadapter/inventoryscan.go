package codexadapter

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"kansoku.local/kansoku/internal/adaptersdk"
)

// configFileName is Codex's documented single configuration file. MCP
// servers are declared there under one [mcp_servers.<name>] TOML table per
// server -- contracts/codex/rollout-and-inventory.yaml's inventory_source
// names mcp_servers as a components_inventoried entry, and this file is the
// only on-disk location that section is ever read from.
const configFileName = "config.toml"

// maxMCPServerTables bounds how many [mcp_servers.<name>] tables one scan
// reads, mirroring InventoryInput's own maxRepositoryTargets ceiling: a
// config file may not be used to force an unbounded parse even if a caller
// somehow gets ReadConfigProbe to hand back a very large (still
// maxConfigProbeBytes-bounded) file.
const maxMCPServerTables = 256
const maxSkillManifests = 512

// mcpServerTableHeader matches a bounded, closed shape: [mcp_servers.<name>]
// on its own line, where name is a bare TOML key (letters, digits,
// underscore, hyphen) or a double-quoted string. It never matches a nested
// key beneath the table (e.g. [mcp_servers.foo.env]), which this scan
// intentionally does not descend into -- only presence/enabled/name are
// read, never env values, command arguments or any other field that could
// carry a credential.
var mcpServerTableHeader = regexp.MustCompile(`^\[mcp_servers\.(?:"([^"\\]{1,256})"|([A-Za-z0-9_-]{1,256}))\]\s*$`)

// mcpServerEnabledKey matches a bounded `enabled = <bool>` key=value line
// directly inside an [mcp_servers.<name>] table (never a nested subtable).
// Codex documents enabled=false as the explicit way to declare a configured
// but currently-disabled server; absence of the key means "enabled" is the
// documented default, matching Codex's own config precedence.
var mcpServerEnabledKey = regexp.MustCompile(`^enabled\s*=\s*(true|false)\s*$`)
var pluginTableHeader = regexp.MustCompile(`^\[plugins\.(?:"([^"\\]{1,256})"|([A-Za-z0-9_@./-]{1,256}))\]\s*$`)
var skillNameLine = regexp.MustCompile(`^name\s*:\s*["']?([A-Za-z0-9][A-Za-z0-9._-]{0,127})["']?\s*$`)

// ScanHostInventory performs the bounded, read-only host scan TDD 11
// section C requires: it reads Codex's config.toml MCP section through
// host.ReadConfigProbe (never a raw unbounded filesystem read) and returns
// an InventoryInput ready for BuildInventorySnapshot. host may be nil (no
// scan possible, e.g. a caller with no permission-checked HostView yet);
// scanned reports whether any bounded scan was actually attempted and
// completed, so the caller can distinguish "we looked and found genuinely
// zero MCP servers" from "we never looked" -- both cases still populate
// only what was actually observed, never a fabricated entry.
func ScanHostInventory(host *adaptersdk.HostView, target adaptersdk.Installation) (InventoryInput, bool) {
	input := InventoryInput{InstallationID: target.InstallationID}
	if host == nil || target.StateRoot == "" || !filepath.IsAbs(target.StateRoot) {
		return input, false
	}
	scanned := false
	for _, configPath := range codexConfigPaths(host, target.StateRoot) {
		result, err := host.ReadConfigProbe(configPath)
		if err != nil || !result.Exists || result.Truncated {
			continue
		}
		scanned = true
		pathPseudonym := host.PseudonymizePath(configPath)
		for _, server := range parseMCPServers(string(result.Content)) {
			input.MCPServers = append(input.MCPServers, MCPServerDescriptor{
				Name: server.name, Scope: adaptersdk.ScopeUser, Enabled: server.enabled,
				PathPseudonym: pathPseudonym,
				Fingerprint:   stableHex("mcp-server-config", server.name, boolString(server.enabled)),
			})
		}
		for _, plugin := range parsePlugins(string(result.Content)) {
			enabledFor := ""
			if plugin.enabled {
				enabledFor = target.InstallationID
			}
			input.Plugins = append(input.Plugins, PluginDescriptor{
				Name: plugin.name, Scope: adaptersdk.ScopeUser,
				ActiveEnabledFor: enabledFor, PathPseudonym: pathPseudonym,
				Fingerprint: stableHex("plugin-config", plugin.name, boolString(plugin.enabled)),
			})
		}
	}
	for _, root := range documentedSkillRoots(target.StateRoot) {
		skills, observed := scanSkillRoot(host, root.path, root.scope)
		if observed {
			scanned = true
		}
		input.Skills = append(input.Skills, skills...)
	}
	return input, scanned
}

type mcpServerTable struct {
	name    string
	enabled bool
}

type pluginTable struct {
	name    string
	enabled bool
}

type skillRoot struct {
	path  string
	scope adaptersdk.SourceScope
}

// codexConfigPaths supports the direct documented CODEX_HOME/config.toml
// layout and Kansoku's read-only multi-profile mirror
// <state-root>/state/<profile>/config.toml. The latter lets one logical
// installation reconcile several explicitly mounted CODEX_HOME profiles
// without mounting a user's whole home directory.
func codexConfigPaths(host *adaptersdk.HostView, stateRoot string) []string {
	paths := []string{filepath.Join(stateRoot, configFileName)}
	profilesRoot := filepath.Join(stateRoot, "state")
	entries, err := host.ListDirectoryProbe(profilesRoot)
	if err != nil {
		return paths
	}
	for _, entry := range entries {
		if entry.IsDir && !entry.IsSymlink {
			paths = append(paths, filepath.Join(profilesRoot, entry.Name, configFileName))
		}
	}
	sort.Strings(paths)
	return paths
}

func documentedSkillRoots(stateRoot string) []skillRoot {
	return []skillRoot{
		{path: filepath.Join(stateRoot, "skills", "user"), scope: adaptersdk.ScopeUser},
		{path: filepath.Join(stateRoot, "skills", "repository"), scope: adaptersdk.ScopeRepository},
		{path: filepath.Join(stateRoot, "skills", "admin"), scope: adaptersdk.ScopeAdmin},
		{path: filepath.Join(stateRoot, "skills", "system"), scope: adaptersdk.ScopeSystem},
		// Backward-compatible direct CODEX_HOME bundled system skills.
		{path: filepath.Join(stateRoot, "skills", ".system"), scope: adaptersdk.ScopeSystem},
	}
}

func scanSkillRoot(host *adaptersdk.HostView, root string, scope adaptersdk.SourceScope) ([]SkillDescriptor, bool) {
	probe, err := host.ReadProbe(root)
	if err != nil || !probe.Exists {
		return nil, false
	}
	entries, err := host.ListDirectoryProbe(root)
	if err != nil {
		return nil, false
	}
	skills := make([]SkillDescriptor, 0, len(entries))
	for _, entry := range entries {
		if len(skills) >= maxSkillManifests || (!entry.IsDir && !entry.IsSymlink) {
			continue
		}
		manifestPath := filepath.Join(root, entry.Name, "SKILL.md")
		result, err := host.ReadConfigProbe(manifestPath)
		if err != nil || !result.Exists || result.Truncated {
			continue
		}
		name, descriptionBytes, descriptionChars, ok := parseSkillFrontmatter(result.Content)
		if !ok {
			continue
		}
		skills = append(skills, SkillDescriptor{
			Name: name, Scope: scope, Enabled: true,
			DescriptionBytes: descriptionBytes, DescriptionChars: descriptionChars,
			PathPseudonym: host.PseudonymizePath(manifestPath),
			Fingerprint:   stableHex("skill-manifest", name, string(result.Content)),
		})
	}
	sort.Slice(skills, func(i, j int) bool {
		if skills[i].Name == skills[j].Name {
			return skills[i].PathPseudonym < skills[j].PathPseudonym
		}
		return skills[i].Name < skills[j].Name
	})
	return skills, true
}

func parseSkillFrontmatter(content []byte) (name string, descriptionBytes, descriptionChars int, ok bool) {
	lines := strings.Split(string(content), "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return "", 0, 0, false
	}
	for index := 1; index < len(lines) && index <= 64; index++ {
		line := strings.TrimSpace(lines[index])
		if line == "---" {
			return name, descriptionBytes, descriptionChars, name != ""
		}
		if match := skillNameLine.FindStringSubmatch(line); match != nil {
			name = match[1]
			continue
		}
		if strings.HasPrefix(line, "description:") {
			description := strings.TrimSpace(strings.TrimPrefix(line, "description:"))
			description = strings.Trim(description, `"'`)
			descriptionBytes = len([]byte(description))
			descriptionChars = utf8.RuneCountInString(description)
		}
	}
	return "", 0, 0, false
}

// parseMCPServers performs a bounded, non-executing, line-oriented scan of
// config.toml for [mcp_servers.<name>] table headers and their directly-
// nested enabled key. This is deliberately not a general TOML parser (no
// external dependency is vendored for one): it recognizes exactly the
// documented mcp_servers table shape and nothing else, matching
// contracts/adapter-sdk/discovery-and-plans.yaml's discovery_safety_rules
// ("parse manifests and config with size and depth limits and no code
// execution"). Any table other than [mcp_servers.<name>] (including a
// nested [mcp_servers.<name>.env] subtable) ends the current server's
// section without being read.
func parseMCPServers(content string) []mcpServerTable {
	var servers []mcpServerTable
	var current *mcpServerTable
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if match := mcpServerTableHeader.FindStringSubmatch(line); match != nil {
				if len(servers) >= maxMCPServerTables {
					return servers
				}
				name := match[1]
				if name == "" {
					name = match[2]
				}
				servers = append(servers, mcpServerTable{name: name, enabled: true})
				current = &servers[len(servers)-1]
				continue
			}
			// Any other table header (including a nested subtable of the
			// current server) closes the current server's own key scope.
			current = nil
			continue
		}
		if current == nil {
			continue
		}
		if match := mcpServerEnabledKey.FindStringSubmatch(line); match != nil {
			current.enabled = match[1] == "true"
		}
	}
	sort.Slice(servers, func(i, j int) bool { return servers[i].name < servers[j].name })
	return servers
}

func parsePlugins(content string) []pluginTable {
	var plugins []pluginTable
	var current *pluginTable
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			if match := pluginTableHeader.FindStringSubmatch(line); match != nil {
				if len(plugins) >= maxMCPServerTables {
					return plugins
				}
				name := match[1]
				if name == "" {
					name = match[2]
				}
				plugins = append(plugins, pluginTable{name: name, enabled: true})
				current = &plugins[len(plugins)-1]
				continue
			}
			current = nil
			continue
		}
		if current != nil {
			if match := mcpServerEnabledKey.FindStringSubmatch(line); match != nil {
				current.enabled = match[1] == "true"
			}
		}
	}
	sort.Slice(plugins, func(i, j int) bool { return plugins[i].name < plugins[j].name })
	return plugins
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
