package claudeadapter

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

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

// maxSkillManifests bounds how many SKILL.md manifests one scan of a single
// skill root reads, mirroring codexadapter's identical ceiling: a skill
// directory may not be used to force an unbounded parse even though
// ReadConfigProbe already bounds each individual file read.
const maxSkillManifests = 512

// maxPluginCacheMarketplaces bounds how many top-level marketplace
// directories one scan of the plugin cache root reads.
const maxPluginCacheMarketplaces = 64

// maxPluginCachePlugins bounds how many version-hash directories, summed
// across every marketplace/plugin folder, one plugin-cache scan reads --
// the per-directory-level ListDirectoryProbe calls are themselves bounded to
// 512 entries each, but a host with many marketplaces times many plugins
// times many stale versions could still add up to an unbounded parse
// without this running total.
const maxPluginCachePlugins = 1024

// maxPluginCacheVersionsPerPlugin bounds how many version-hash directories
// one plugin folder contributes, so a single plugin with an unusually deep
// version history cannot starve the shared maxPluginCachePlugins budget
// away from every other plugin folder.
const maxPluginCacheVersionsPerPlugin = 8

// skillNameLine matches a bounded `name: <value>` frontmatter key, identical
// to codexadapter's pattern since Claude Code's documented SKILL.md
// frontmatter uses the same shape.
var skillNameLine = regexp.MustCompile(`^name\s*:\s*["']?([A-Za-z0-9][A-Za-z0-9._-]{0,127})["']?\s*$`)

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

// pluginCacheRoot is Claude Code's documented plugin-cache tree:
// <marketplace>/<plugin-folder>/<version-hash>/, discovered dynamically at
// scan time -- never hardcode a marketplace or plugin name here, since a
// real host may have many such structures, not just one.
func pluginCacheRoot(stateRoot string) string {
	return filepath.Join(stateRoot, "plugins", "cache")
}

// installedPluginsPath is Claude Code's pointer file naming which
// version-hash directory is the currently active install for each
// "<plugin>@<marketplace>" composite name.
func installedPluginsPath(stateRoot string) string {
	return filepath.Join(stateRoot, "plugins", "installed_plugins.json")
}

// pluginManifestShape is the closed, bounded subset of a version-hash
// directory's .claude-plugin/plugin.json this scan ever decodes -- only the
// declared name, matching the "no content beyond identity" discipline
// already applied to settings.json's mcpServers entries.
type pluginManifestShape struct {
	Name string `json:"name"`
}

// installedPluginsShape is the closed, bounded subset of
// installed_plugins.json this scan ever decodes.
type installedPluginsShape struct {
	Plugins map[string][]installedPluginEntry `json:"plugins"`
}

type installedPluginEntry struct {
	Version string `json:"version"`
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
	scanned := false
	pluginsByName := map[string]*PluginDescriptor{}

	settingsPath := filepath.Join(target.StateRoot, settingsFileName)
	result, err := host.ReadConfigProbe(settingsPath)
	if err == nil && result.Exists {
		var shape claudeSettingsShape
		decoder := json.NewDecoder(strings.NewReader(string(result.Content)))
		// A malformed or truncated settings.json is not a scan the caller can
		// trust for plugins/MCP servers; skip it rather than guessing at a
		// partial parse, but still let skill-root scanning below proceed
		// independently -- one config file's failure must not hide skills
		// that live entirely outside of it.
		if decoder.Decode(&shape) == nil {
			scanned = true
			pathPseudonym := host.PseudonymizePath(settingsPath)

			pluginNames := sortedKeysBool(shape.EnabledPlugins, maxScannedEntries)
			for _, name := range pluginNames {
				enabled := shape.EnabledPlugins[name]
				activeEnabledFor := ""
				if enabled {
					activeEnabledFor = target.InstallationID
				}
				descriptor := PluginDescriptor{
					Name:             name,
					Scope:            adaptersdk.ScopeUser,
					ActiveEnabledFor: activeEnabledFor,
					PathPseudonym:    pathPseudonym,
					Fingerprint:      stableHex("plugin-config", name, boolString(enabled)),
				}
				pluginsByName[name] = &descriptor
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
		}
	}

	for _, root := range documentedSkillRoots(target.StateRoot) {
		skills, observed := scanSkillRoot(host, root.path, root.scope)
		if observed {
			scanned = true
		}
		input.StandaloneSkills = append(input.StandaloneSkills, skills...)
	}

	marketplaces, cacheCandidates, cacheObserved := scanPluginCache(host, target.StateRoot)
	if cacheObserved {
		scanned = true
	}
	input.Marketplaces = marketplaces
	activeVersions := readActivePluginVersions(host, target.StateRoot)
	extraCacheOnly := mergePluginCacheCandidates(pluginsByName, cacheCandidates, activeVersions)

	input.Plugins = make([]PluginDescriptor, 0, len(pluginsByName)+len(extraCacheOnly))
	for _, descriptor := range pluginsByName {
		input.Plugins = append(input.Plugins, *descriptor)
	}
	input.Plugins = append(input.Plugins, extraCacheOnly...)
	sort.Slice(input.Plugins, func(i, j int) bool {
		if input.Plugins[i].Name == input.Plugins[j].Name {
			return input.Plugins[i].Version < input.Plugins[j].Version
		}
		return input.Plugins[i].Name < input.Plugins[j].Name
	})
	sort.Slice(input.Marketplaces, func(i, j int) bool {
		return input.Marketplaces[i].Name < input.Marketplaces[j].Name
	})

	return input, scanned
}

// scanPluginCache performs the bounded, read-only walk of Claude Code's
// plugin-cache tree: <stateRoot>/plugins/cache/<marketplace>/<plugin-folder>/
// <version-hash>/. Every level -- marketplace name, plugin folder, version
// hash -- is discovered from the directory listing itself; none is ever
// hardcoded, since a real host may have many such structures, each with its
// own marketplace/plugin names. One PluginDescriptor candidate is returned
// per version-hash directory found; ScanHostInventory decides which
// candidate(s) merge into an already-configured settings.json entry and
// which remain distinct cache-only artifacts.
func scanPluginCache(host *adaptersdk.HostView, stateRoot string) (marketplaces []MarketplaceDescriptor, candidates []PluginDescriptor, observed bool) {
	root := pluginCacheRoot(stateRoot)
	probe, err := host.ReadProbe(root)
	if err != nil || !probe.Exists {
		return nil, nil, false
	}
	marketEntries, err := host.ListDirectoryProbe(root)
	if err != nil {
		return nil, nil, false
	}

	marketNames := make([]string, 0, len(marketEntries))
	for _, entry := range marketEntries {
		if !entry.IsDir && !entry.IsSymlink {
			continue
		}
		marketNames = append(marketNames, entry.Name)
	}
	sort.Strings(marketNames)
	if len(marketNames) > maxPluginCacheMarketplaces {
		marketNames = marketNames[:maxPluginCacheMarketplaces]
	}

	pluginBudget := maxPluginCachePlugins
	for _, marketName := range marketNames {
		marketDir := filepath.Join(root, marketName)
		candidatesBefore := len(candidates)

		pluginEntries, err := host.ListDirectoryProbe(marketDir)
		if err != nil {
			continue
		}
		pluginFolders := make([]string, 0, len(pluginEntries))
		for _, entry := range pluginEntries {
			if !entry.IsDir && !entry.IsSymlink {
				continue
			}
			pluginFolders = append(pluginFolders, entry.Name)
		}
		sort.Strings(pluginFolders)

		for _, pluginFolder := range pluginFolders {
			if pluginBudget <= 0 {
				break
			}
			pluginDir := filepath.Join(marketDir, pluginFolder)
			versionEntries, err := host.ListDirectoryProbe(pluginDir)
			if err != nil {
				continue
			}
			versionNames := make([]string, 0, len(versionEntries))
			for _, entry := range versionEntries {
				if !entry.IsDir && !entry.IsSymlink {
					continue
				}
				versionNames = append(versionNames, entry.Name)
			}
			sort.Strings(versionNames)
			if len(versionNames) > maxPluginCacheVersionsPerPlugin {
				versionNames = versionNames[:maxPluginCacheVersionsPerPlugin]
			}

			for _, versionHash := range versionNames {
				if pluginBudget <= 0 {
					break
				}
				versionDir := filepath.Join(pluginDir, versionHash)
				declaredName := readPluginManifestName(host, versionDir, pluginFolder)
				composite := declaredName + "@" + marketName
				skills, _ := scanSkillRoot(host, filepath.Join(versionDir, "skills"), adaptersdk.ScopePluginCache)
				candidates = append(candidates, PluginDescriptor{
					Name:            composite,
					Version:         versionHash,
					Scope:           adaptersdk.ScopePluginCache,
					FromMarketplace: marketName,
					PathPseudonym:   host.PseudonymizePath(versionDir),
					Fingerprint:     stableHex("plugin-cache", composite, versionHash),
					BundledSkills:   skills,
				})
				pluginBudget--
			}
		}

		// Only a marketplace directory that actually contributed at least one
		// real plugin candidate becomes a MarketplaceDescriptor -- an empty or
		// otherwise-content-free top-level directory (e.g. a placeholder used
		// when no real plugin cache is mounted) must never fabricate a
		// marketplace node with nothing bundled underneath it.
		if len(candidates) > candidatesBefore {
			marketplaces = append(marketplaces, MarketplaceDescriptor{
				Name:          marketName,
				PathPseudonym: host.PseudonymizePath(marketDir),
				Fingerprint:   stableHex("marketplace-cache", marketName),
			})
		}
	}

	return marketplaces, candidates, true
}

// readPluginManifestName reads one version-hash directory's
// .claude-plugin/plugin.json for its declared name, falling back to the
// plugin's cache folder name when the manifest is absent, unreadable,
// truncated or declares an empty name -- the folder name is always a safe
// fallback here since it is the same identity Claude Code's own
// installed_plugins.json keys by.
func readPluginManifestName(host *adaptersdk.HostView, versionDir, fallback string) string {
	manifestPath := filepath.Join(versionDir, ".claude-plugin", "plugin.json")
	result, err := host.ReadConfigProbe(manifestPath)
	if err != nil || !result.Exists || result.Truncated {
		return fallback
	}
	var shape pluginManifestShape
	if json.Unmarshal(result.Content, &shape) != nil || shape.Name == "" {
		return fallback
	}
	return shape.Name
}

// readActivePluginVersions reads installed_plugins.json's composite-name ->
// active-version-hash pointer, used only to disambiguate which cache
// version-hash directory is the currently installed one when more than one
// exists for the same plugin folder. A missing, unreadable, truncated or
// malformed file returns nil -- callers then treat every candidate version
// for an ambiguous plugin as cache-only rather than guessing which is
// active.
func readActivePluginVersions(host *adaptersdk.HostView, stateRoot string) map[string]string {
	result, err := host.ReadConfigProbe(installedPluginsPath(stateRoot))
	if err != nil || !result.Exists || result.Truncated {
		return nil
	}
	var shape installedPluginsShape
	if json.Unmarshal(result.Content, &shape) != nil {
		return nil
	}
	versions := make(map[string]string, len(shape.Plugins))
	for name, entries := range shape.Plugins {
		if len(entries) == 0 || entries[0].Version == "" {
			continue
		}
		versions[name] = entries[0].Version
	}
	return versions
}

// mergePluginCacheCandidates applies the "merge, don't duplicate" rule: a
// cache candidate whose composite name already has a settings.json-seeded
// entry in pluginsByName is folded into that same descriptor (enriching it
// with Version/FromMarketplace/BundledSkills) rather than creating a second
// node that would collide with it. When more than one version-hash
// directory exists for the same composite name, activeVersions disambiguates
// which one is the real bundling source for a configured entry; every other
// version becomes its own separate CachedOnly entry, returned here for the
// caller to append directly. A composite name absent from pluginsByName has
// no configured entry to merge into at all, so every one of its candidates
// becomes a separate CachedOnly entry instead.
func mergePluginCacheCandidates(pluginsByName map[string]*PluginDescriptor, candidates []PluginDescriptor, activeVersions map[string]string) []PluginDescriptor {
	grouped := make(map[string][]PluginDescriptor, len(candidates))
	for _, candidate := range candidates {
		grouped[candidate.Name] = append(grouped[candidate.Name], candidate)
	}

	names := make([]string, 0, len(grouped))
	for name := range grouped {
		names = append(names, name)
	}
	sort.Strings(names)

	var extraCacheOnly []PluginDescriptor
	for _, name := range names {
		group := grouped[name]
		existing, configured := pluginsByName[name]
		if !configured {
			for _, candidate := range group {
				candidate.CachedOnly = true
				extraCacheOnly = append(extraCacheOnly, candidate)
			}
			continue
		}
		if len(group) == 1 {
			mergePluginCacheData(existing, group[0])
			continue
		}
		mergedIndex := -1
		if activeHash, ok := activeVersions[name]; ok {
			for i, candidate := range group {
				if candidate.Version == activeHash {
					mergedIndex = i
					break
				}
			}
		}
		for i, candidate := range group {
			if i == mergedIndex {
				mergePluginCacheData(existing, candidate)
				continue
			}
			candidate.CachedOnly = true
			extraCacheOnly = append(extraCacheOnly, candidate)
		}
	}
	return extraCacheOnly
}

// mergePluginCacheData enriches an already-configured plugin entry (seeded
// from settings.json's enabledPlugins) with the one cache version-hash
// directory that is its actual bundling source. CachedOnly and
// ActiveEnabledFor are left untouched: whether this plugin is configured/
// enabled is decided purely by settings.json, never by cache presence.
func mergePluginCacheData(target *PluginDescriptor, cache PluginDescriptor) {
	target.Version = cache.Version
	target.FromMarketplace = cache.FromMarketplace
	target.PathPseudonym = cache.PathPseudonym
	target.BundledSkills = cache.BundledSkills
	target.Fingerprint = stableHex("plugin-config-cache", target.Name, boolString(target.ActiveEnabledFor != ""), cache.Fingerprint)
}

// skillRoot pairs one documented Claude Code skill directory with the
// inventory scope its contents are declared under, mirroring codexadapter's
// identical type.
type skillRoot struct {
	path  string
	scope adaptersdk.SourceScope
}

// documentedSkillRoots lists Claude Code's documented standalone skill
// directories: personal (user) skills, project (repository) skills, and the
// admin/system scopes Kansoku's own multi-surface state-root layout mirrors
// alongside codexadapter's identical scope set. Marketplace/plugin-bundled
// skills live under a plugin's own directory and are out of scope here --
// they are scanned, if at all, as part of plugin inventory, never here.
func documentedSkillRoots(stateRoot string) []skillRoot {
	return []skillRoot{
		{path: filepath.Join(stateRoot, "skills", "user"), scope: adaptersdk.ScopeUser},
		{path: filepath.Join(stateRoot, "skills", "repository"), scope: adaptersdk.ScopeRepository},
		{path: filepath.Join(stateRoot, "skills", "admin"), scope: adaptersdk.ScopeAdmin},
		{path: filepath.Join(stateRoot, "skills", "system"), scope: adaptersdk.ScopeSystem},
	}
}

// scanSkillRoot performs the bounded, read-only directory scan of one skill
// root: each immediate subdirectory's SKILL.md is read through
// host.ReadConfigProbe (never a raw unbounded filesystem read) and parsed
// for exactly its name/description frontmatter, mirroring codexadapter's
// identical scan.
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

// parseSkillFrontmatter performs a bounded, non-executing, line-oriented
// scan of a SKILL.md's YAML frontmatter for exactly its name/description
// keys, identical to codexadapter's parser -- Claude Code documents the
// same frontmatter shape.
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
