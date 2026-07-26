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
		}
	}

	for _, root := range documentedSkillRoots(target.StateRoot) {
		skills, observed := scanSkillRoot(host, root.path, root.scope)
		if observed {
			scanned = true
		}
		input.StandaloneSkills = append(input.StandaloneSkills, skills...)
	}

	return input, scanned
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
