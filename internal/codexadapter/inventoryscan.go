package codexadapter

import (
	"os"
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

// Plugin cache traversal is deliberately bounded across every mounted
// CODEX_HOME profile. The host directory probe already caps each individual
// directory at 512 entries; these aggregate limits prevent a Cartesian
// profiles × marketplaces × plugins × versions × skills expansion from
// producing an unbounded inventory graph.
const (
	maxCodexHomeProfiles              = 64
	maxPluginCacheMarketplaces        = 64
	maxPluginCacheVersions            = 512
	maxPluginCacheVersionsPerPlugin   = 8
	maxPluginCacheSkillManifestsTotal = 2048
)

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
	input := InventoryInput{
		InstallationID: target.InstallationID,
		CoverageGaps:   adaptersdk.CoverageGaps{},
	}
	if host == nil || target.StateRoot == "" || !filepath.IsAbs(target.StateRoot) {
		return input, false
	}
	homes, homesComplete := codexHomeRoots(host, target.StateRoot)
	if !homesComplete {
		return input, false
	}
	scanned := false
	cacheBudget := pluginCacheScanBudget{
		versionsRemaining: maxPluginCacheVersions,
		skillsRemaining:   maxPluginCacheSkillManifestsTotal,
	}
	for _, home := range homes {
		configPath := filepath.Join(home, configFileName)
		configuredPlugins := map[string]*PluginDescriptor{}
		result, err := host.ReadConfigProbe(configPath)
		if err != nil || result.Truncated {
			return InventoryInput{InstallationID: target.InstallationID}, false
		}
		if result.Exists {
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
				if _, duplicate := configuredPlugins[plugin.name]; duplicate {
					// Duplicate TOML tables are invalid and must never be
					// resolved by last-write-wins guessing.
					return InventoryInput{InstallationID: target.InstallationID}, false
				}
				enabledFor := ""
				if plugin.enabled {
					enabledFor = target.InstallationID
				}
				descriptor := PluginDescriptor{
					Name: plugin.name, Scope: adaptersdk.ScopeUser,
					ActiveEnabledFor: enabledFor, PathPseudonym: pathPseudonym,
					Fingerprint: stableHex("plugin-config", plugin.name, boolString(plugin.enabled)),
				}
				configuredPlugins[plugin.name] = &descriptor
			}
		}

		cacheCandidates, cacheObserved, cacheComplete := scanCodexPluginCache(host, home, &cacheBudget)
		if !cacheComplete {
			// Returning no partial graph is intentional: a bounded source
			// coverage failure must remain not_observed, not look like a
			// complete inventory with whichever entries happened to fit.
			return InventoryInput{InstallationID: target.InstallationID}, false
		}
		if cacheObserved {
			scanned = true
		}
		input.Plugins = append(
			input.Plugins,
			mergeCodexPluginCacheCandidates(configuredPlugins, cacheCandidates)...,
		)
	}
	for _, root := range documentedSkillRoots(target.StateRoot) {
		skills, gaps, observed := scanSkillRoot(host, root.path, root.scope)
		if observed {
			scanned = true
		}
		input.CoverageGaps.Merge(gaps)
		input.Skills = append(input.Skills, skills...)
	}
	input.Plugins = deduplicateExactPluginDescriptors(input.Plugins)
	sort.Slice(input.Plugins, func(i, j int) bool {
		if input.Plugins[i].Name != input.Plugins[j].Name {
			return input.Plugins[i].Name < input.Plugins[j].Name
		}
		if input.Plugins[i].Version != input.Plugins[j].Version {
			return input.Plugins[i].Version < input.Plugins[j].Version
		}
		return input.Plugins[i].PathPseudonym < input.Plugins[j].PathPseudonym
	})
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

type pluginCacheScanBudget struct {
	versionsRemaining int
	skillsRemaining   int
}

// codexHomeRoots supports the direct documented CODEX_HOME layout and
// Kansoku's read-only multi-profile mirror <state-root>/state/<profile>.
// Profile enumeration is one level only and fails closed when the explicit
// aggregate bound is exceeded.
func codexHomeRoots(host *adaptersdk.HostView, stateRoot string) ([]string, bool) {
	homes := []string{stateRoot}
	profilesRoot := filepath.Join(stateRoot, "state")
	entries, err := host.ListDirectoryProbe(profilesRoot)
	if err != nil {
		return nil, false
	}
	profileCount := 0
	for _, entry := range entries {
		if entry.IsDir && !entry.IsSymlink {
			profileCount++
			if profileCount > maxCodexHomeProfiles {
				return nil, false
			}
			homes = append(homes, filepath.Join(profilesRoot, entry.Name))
		}
	}
	sort.Strings(homes)
	return homes, true
}

// scanCodexPluginCache walks only the documented
// <CODEX_HOME>/plugins/cache/<marketplace>/<plugin>/<version>/skills tree.
// It reads SKILL.md frontmatter through HostView and returns identity
// metadata/fingerprints only; descriptions, bodies and raw paths never leave
// this function.
func scanCodexPluginCache(
	host *adaptersdk.HostView,
	home string,
	budget *pluginCacheScanBudget,
) ([]PluginDescriptor, bool, bool) {
	root := filepath.Join(home, "plugins", "cache")
	probe, err := host.ReadProbe(root)
	if err != nil {
		return nil, false, false
	}
	if !probe.Exists {
		return nil, false, true
	}
	marketEntries, err := host.ListDirectoryProbe(root)
	if err != nil {
		return nil, true, false
	}
	markets := directoryNames(marketEntries)
	if len(markets) > maxPluginCacheMarketplaces {
		return nil, true, false
	}

	var candidates []PluginDescriptor
	for _, market := range markets {
		marketPath := filepath.Join(root, market)
		pluginEntries, err := host.ListDirectoryProbe(marketPath)
		if err != nil {
			return nil, true, false
		}
		for _, pluginName := range directoryNames(pluginEntries) {
			pluginPath := filepath.Join(marketPath, pluginName)
			versionEntries, err := host.ListDirectoryProbe(pluginPath)
			if err != nil {
				return nil, true, false
			}
			versions := directoryNames(versionEntries)
			if len(versions) > maxPluginCacheVersionsPerPlugin ||
				len(versions) > budget.versionsRemaining {
				return nil, true, false
			}
			for _, version := range versions {
				versionPath := filepath.Join(pluginPath, version)
				skills, complete := scanPluginSkillRoot(
					host,
					filepath.Join(versionPath, "skills"),
					budget,
				)
				if !complete {
					return nil, true, false
				}
				compositeName := pluginName + "@" + market
				candidates = append(candidates, PluginDescriptor{
					Name:          compositeName,
					Version:       version,
					Scope:         adaptersdk.ScopePluginCache,
					CachedOnly:    true,
					PathPseudonym: host.PseudonymizePath(versionPath),
					Fingerprint:   stableHex("plugin-cache", compositeName, version),
					BundledSkills: skills,
				})
				budget.versionsRemaining--
			}
		}
	}
	return candidates, true, true
}

func scanPluginSkillRoot(
	host *adaptersdk.HostView,
	root string,
	budget *pluginCacheScanBudget,
) ([]SkillDescriptor, bool) {
	probe, err := host.ReadProbe(root)
	if err != nil {
		return nil, false
	}
	if !probe.Exists {
		return nil, true
	}
	entries, err := host.ListDirectoryProbe(root)
	if err != nil {
		return nil, false
	}
	names := directoryNames(entries)
	if len(names) > maxSkillManifests || len(names) > budget.skillsRemaining {
		return nil, false
	}
	skills := make([]SkillDescriptor, 0, len(names))
	for _, directoryName := range names {
		manifestPath := filepath.Join(root, directoryName, "SKILL.md")
		result, err := host.ReadConfigProbe(manifestPath)
		if err != nil || result.Truncated {
			return nil, false
		}
		if !result.Exists {
			continue
		}
		name, descriptionBytes, descriptionChars, ok := parseSkillFrontmatter(result.Content)
		if !ok {
			continue
		}
		skills = append(skills, SkillDescriptor{
			Name: name, Scope: adaptersdk.ScopePluginCache,
			DescriptionBytes: descriptionBytes, DescriptionChars: descriptionChars,
			PathPseudonym: host.PseudonymizePath(manifestPath),
			Fingerprint:   stableHex("skill-manifest", name, string(result.Content)),
		})
	}
	budget.skillsRemaining -= len(names)
	sort.Slice(skills, func(i, j int) bool {
		if skills[i].Name != skills[j].Name {
			return skills[i].Name < skills[j].Name
		}
		return skills[i].PathPseudonym < skills[j].PathPseudonym
	})
	return skills, true
}

func directoryNames(entries []adaptersdk.DirectoryProbeEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir && !entry.IsSymlink {
			names = append(names, entry.Name)
		}
	}
	sort.Strings(names)
	return names
}

// mergeCodexPluginCacheCandidates only promotes cache metadata into a
// configured plugin when exactly one version exists for that composite
// plugin@marketplace identity. Multiple versions remain cache-only, so
// inventory coverage is visible but ownership is never guessed.
func mergeCodexPluginCacheCandidates(
	configured map[string]*PluginDescriptor,
	candidates []PluginDescriptor,
) []PluginDescriptor {
	grouped := make(map[string][]PluginDescriptor, len(candidates))
	for _, candidate := range candidates {
		grouped[candidate.Name] = append(grouped[candidate.Name], candidate)
	}
	for name, group := range grouped {
		target, exists := configured[name]
		if !exists || len(group) != 1 {
			continue
		}
		mergeCodexPluginCacheData(target, group[0])
		delete(grouped, name)
	}

	names := make([]string, 0, len(configured)+len(grouped))
	for name := range configured {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]PluginDescriptor, 0, len(configured)+len(candidates))
	for _, name := range names {
		result = append(result, *configured[name])
	}
	cacheNames := make([]string, 0, len(grouped))
	for name := range grouped {
		cacheNames = append(cacheNames, name)
	}
	sort.Strings(cacheNames)
	for _, name := range cacheNames {
		group := grouped[name]
		sort.Slice(group, func(i, j int) bool {
			if group[i].Version != group[j].Version {
				return group[i].Version < group[j].Version
			}
			return group[i].PathPseudonym < group[j].PathPseudonym
		})
		result = append(result, group...)
	}
	return result
}

func mergeCodexPluginCacheData(target *PluginDescriptor, cache PluginDescriptor) {
	target.Version = cache.Version
	target.Scope = adaptersdk.ScopeMarketplace
	target.PathPseudonym = cache.PathPseudonym
	target.BundledSkills = append([]SkillDescriptor(nil), cache.BundledSkills...)
	for index := range target.BundledSkills {
		target.BundledSkills[index].Scope = adaptersdk.ScopeMarketplace
		target.BundledSkills[index].Enabled = target.ActiveEnabledFor != ""
	}
	target.Fingerprint = stableHex(
		"plugin-config-cache",
		target.Name,
		boolString(target.ActiveEnabledFor != ""),
		cache.Fingerprint,
	)
}

// deduplicateExactPluginDescriptors collapses the same logical plugin
// declaration observed through multiple explicitly mounted profiles only
// when identity, version, enabled state and every bundled child fingerprint
// are identical. Profile disagreement remains as distinct colliding nodes;
// this function never chooses one divergent profile as authoritative.
func deduplicateExactPluginDescriptors(values []PluginDescriptor) []PluginDescriptor {
	byKey := make(map[string]PluginDescriptor, len(values))
	for _, value := range values {
		key := exactPluginDescriptorKey(value)
		current, exists := byKey[key]
		if !exists || value.PathPseudonym < current.PathPseudonym {
			byKey[key] = value
		}
	}
	keys := make([]string, 0, len(byKey))
	for key := range byKey {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]PluginDescriptor, 0, len(keys))
	for _, key := range keys {
		result = append(result, byKey[key])
	}
	return result
}

func exactPluginDescriptorKey(value PluginDescriptor) string {
	parts := []string{
		value.Name,
		value.Version,
		string(value.Scope),
		boolString(value.CachedOnly),
		boolString(value.ActiveEnabledFor != ""),
		value.Fingerprint,
	}
	skills := append([]SkillDescriptor(nil), value.BundledSkills...)
	sort.Slice(skills, func(i, j int) bool {
		if skills[i].Name != skills[j].Name {
			return skills[i].Name < skills[j].Name
		}
		return skills[i].Fingerprint < skills[j].Fingerprint
	})
	for _, skill := range skills {
		parts = append(parts,
			"skill", skill.Name, skill.Version, string(skill.Scope),
			boolString(skill.Enabled), boolString(skill.Disabled),
			skill.Fingerprint,
		)
	}
	commands := append([]CommandDescriptor(nil), value.BundledCommands...)
	sort.Slice(commands, func(i, j int) bool {
		if commands[i].Name != commands[j].Name {
			return commands[i].Name < commands[j].Name
		}
		return commands[i].Fingerprint < commands[j].Fingerprint
	})
	for _, command := range commands {
		parts = append(parts, "command", command.Name, string(command.Scope), command.Fingerprint)
	}
	hooks := append([]HookDescriptor(nil), value.BundledHooks...)
	sort.Slice(hooks, func(i, j int) bool {
		if hooks[i].Name != hooks[j].Name {
			return hooks[i].Name < hooks[j].Name
		}
		return hooks[i].Fingerprint < hooks[j].Fingerprint
	})
	for _, hook := range hooks {
		parts = append(parts,
			"hook", hook.Name, string(hook.Scope),
			boolString(hook.Enabled), boolString(hook.Trusted), hook.Fingerprint,
		)
	}
	servers := append([]MCPServerDescriptor(nil), value.BundledMCPServers...)
	sort.Slice(servers, func(i, j int) bool {
		if servers[i].Name != servers[j].Name {
			return servers[i].Name < servers[j].Name
		}
		return servers[i].Fingerprint < servers[j].Fingerprint
	})
	for _, server := range servers {
		tools := append([]string(nil), server.AdvertisedTools...)
		sort.Strings(tools)
		parts = append(parts,
			"mcp", server.Name, string(server.Scope),
			boolString(server.Enabled), server.Fingerprint,
			strings.Join(tools, "\x1f"),
		)
	}
	apps := append([]AppDescriptor(nil), value.BundledApps...)
	sort.Slice(apps, func(i, j int) bool {
		if apps[i].Name != apps[j].Name {
			return apps[i].Name < apps[j].Name
		}
		return apps[i].Fingerprint < apps[j].Fingerprint
	})
	for _, app := range apps {
		parts = append(parts, "app", app.Name, string(app.Scope), app.Fingerprint)
	}
	return stableHex(parts...)
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

// scanSkillRoot mirrors claudeadapter's identical scan, including its coverage
// gap tally: an entry the scanner found but could not read is recorded in a
// closed class rather than silently skipped, so a truncated scan can never
// present itself as a complete one. Codex has a native exposure plane and so
// does not depend on inventory completeness for cold eligibility, but the
// symmetry matters anyway -- a silently truncated Codex inventory is just as
// wrong, and keeping one shape across both adapters is what stops the next
// scanner from reintroducing the bare `continue`.
func scanSkillRoot(host *adaptersdk.HostView, root string, scope adaptersdk.SourceScope) ([]SkillDescriptor, adaptersdk.CoverageGaps, bool) {
	gaps := adaptersdk.CoverageGaps{}
	probe, err := host.ReadProbe(root)
	if err != nil {
		gaps.Add(rootGapClass(root))
		return nil, gaps, false
	}
	if !probe.Exists {
		return nil, nil, false
	}
	entries, err := host.ListDirectoryProbe(root)
	if err != nil {
		gaps.Add(rootGapClass(root))
		return nil, gaps, false
	}
	skills := make([]SkillDescriptor, 0, len(entries))
	for _, entry := range entries {
		if len(skills) >= maxSkillManifests || (!entry.IsDir && !entry.IsSymlink) {
			continue
		}
		manifestPath := filepath.Join(root, entry.Name, "SKILL.md")
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
			// A plain directory without a SKILL.md is simply not a skill; a
			// symlink without one is a dangling link.
			if entry.IsSymlink {
				gaps.Add(adaptersdk.CoverageGapUnresolvableSymlink)
			}
			continue
		}
		if result.Truncated {
			gaps.Add(adaptersdk.CoverageGapTruncatedManifest)
			continue
		}
		name, descriptionBytes, descriptionChars, ok := parseSkillFrontmatter(result.Content)
		if !ok {
			gaps.Add(adaptersdk.CoverageGapUnparseableManifest)
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
	return skills, gaps, true
}

// rootGapClass classifies a refused or failing skill-root probe, mirroring
// claudeadapter's identical helper.
func rootGapClass(root string) adaptersdk.CoverageGapClass {
	if info, err := os.Lstat(root); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return adaptersdk.CoverageGapUnresolvableSymlink
	}
	return adaptersdk.CoverageGapUnreadableManifest
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
