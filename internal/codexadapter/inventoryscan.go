package codexadapter

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"

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
	configPath := filepath.Join(target.StateRoot, configFileName)
	result, err := host.ReadConfigProbe(configPath)
	if err != nil || !result.Exists {
		return input, false
	}
	servers := parseMCPServers(string(result.Content))
	pathPseudonym := host.PseudonymizePath(configPath)
	for _, server := range servers {
		input.MCPServers = append(input.MCPServers, MCPServerDescriptor{
			Name:            server.name,
			Scope:           adaptersdk.ScopeUser,
			Enabled:         server.enabled,
			AdvertisedTools: nil,
			PathPseudonym:   pathPseudonym,
			Fingerprint:     stableHex("mcp-server-config", server.name, boolString(server.enabled)),
		})
	}
	return input, true
}

type mcpServerTable struct {
	name    string
	enabled bool
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

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
