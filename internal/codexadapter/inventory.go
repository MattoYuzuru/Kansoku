package codexadapter

import (
	"errors"
	"path/filepath"
	"sort"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
)

// codex.inventory builds the closed inventory entity graph
// contracts/codex/rollout-and-inventory.yaml's inventory_source declares:
// documented system/admin/user/repository skills, enabled/disabled skill
// config, plugins, marketplaces/cache, hooks and MCP servers. Every
// node/edge kind and source scope used here is reused verbatim from
// contracts/adapter-sdk/inventory-graph.yaml; this file invents no new
// vocabulary of its own, matching contracts/codex/manifest.yaml's
// zero-agent-name-branch-in-core invariant by staying entirely inside this
// adapter's own package.
const (
	sourceIDInventory = "codex.inventory"

	// maxRepositoryTargets bounds how many explicit repository roots one
	// InventoryInput may declare; repository roots are scanned only for
	// known active projects or an explicit user target, never a
	// speculative recursive filesystem walk, so this ceiling is small and
	// closed.
	maxRepositoryTargets = 64
)

// SkillScope is the closed set of scopes a documented Codex skill may be
// declared in, reused verbatim from contracts/adapter-sdk/inventory-graph.yaml's
// source_scopes (restricted to the scopes contracts/codex/rollout-and-inventory.yaml's
// inventory_source.scopes_inventoried actually names for skills).
type SkillScope = adaptersdk.SourceScope

// SkillDescriptor is one documented skill declaration as read from a Codex
// config/skill manifest, bounded and data-only (no code execution ever
// parses this shape; see contracts/codex/manifest.yaml's
// reused_parse_limits.code_execution).
type SkillDescriptor struct {
	Name             string
	Version          string
	Scope            SkillScope
	Enabled          bool
	Disabled         bool
	DescriptionBytes int
	DescriptionChars int
	// PathPseudonym is the only durable representation of where this skill
	// was declared; the raw path is never itself recorded.
	PathPseudonym string
	Fingerprint   string
}

// PluginDescriptor is one documented plugin/package declaration, which may
// be either an active, configured plugin or a plugin-cache artifact.
// CachedOnly must never be reported as enabled per
// contracts/codex/rollout-and-inventory.yaml's cache_rule; ActiveEnabledFor
// names the surface/installation this plugin is actually wired into (empty
// when CachedOnly is true or when the plugin is merely present but not
// enabled anywhere).
type PluginDescriptor struct {
	Name             string
	Version          string
	Scope            SkillScope
	CachedOnly       bool
	ActiveEnabledFor string
	PathPseudonym    string
	Fingerprint      string
}

// HookDescriptor is one documented Codex hook definition (not necessarily
// codex.hook's own observer hook -- this covers any user-declared hook
// entry the inventory discovers).
type HookDescriptor struct {
	Name          string
	Scope         SkillScope
	Enabled       bool
	Trusted       bool
	PathPseudonym string
	Fingerprint   string
}

// MCPServerDescriptor is one documented/configured MCP server instance and
// the tool names it advertises. Advertised tools are the configured/
// documented set; actually-observed MCP tool calls are compared against
// this set only in reconciliation (see reconcile.go), never merged here.
type MCPServerDescriptor struct {
	Name            string
	Scope           SkillScope
	Enabled         bool
	AdvertisedTools []string
	PathPseudonym   string
	Fingerprint     string
}

// InventoryInput is the closed, bounded set of documented facts one
// Inventory call assembles a snapshot from. It is deliberately not a
// generic map: every field here was itself produced by a bounded,
// non-executing read (fingerprints, presence/size-class, enabled flags)
// via adaptersdk.HostView, exactly like FingerprintInstallation.
type InventoryInput struct {
	InstallationID string
	Skills         []SkillDescriptor
	Plugins        []PluginDescriptor
	Hooks          []HookDescriptor
	MCPServers     []MCPServerDescriptor
	// RepositoryTargets is the closed, explicit list of repository roots to
	// inventory: either a known active project (already recorded elsewhere
	// as active) or a target the user explicitly named. Inventory never
	// discovers a repository root itself via speculative recursive walk.
	RepositoryTargets []string
}

// ErrTooManyRepositoryTargets is returned when InventoryInput declares more
// repository roots than maxRepositoryTargets, closing off an unbounded scan
// even if every individual target were otherwise legitimate.
var ErrTooManyRepositoryTargets = errors.New("codex_inventory_too_many_repository_targets")

// BuildInventorySnapshot assembles one adaptersdk.InventorySnapshot from
// input, deterministically: the same input always yields byte-identical
// nodes/edges/fingerprint (modulo the observed_at timestamp, which the
// caller injects so tests can pin it). Cache-only plugins never receive an
// EdgeEnabledFor edge; two skill/MCP nodes sharing a declared name across
// scopes are never merged into one node -- they remain distinct nodes
// joined by an EdgeShadows or EdgeCollidesWith edge, matching
// contracts/adapter-sdk/inventory-graph.yaml's identity_rule verbatim.
func BuildInventorySnapshot(input InventoryInput, now time.Time) (adaptersdk.InventorySnapshot, error) {
	if len(input.RepositoryTargets) > maxRepositoryTargets {
		return adaptersdk.InventorySnapshot{}, ErrTooManyRepositoryTargets
	}

	var nodes []adaptersdk.Node
	var edges []adaptersdk.Edge

	installationNode := adaptersdk.Node{
		NodeID:       "node_" + stableHex("installation", input.InstallationID),
		Kind:         adaptersdk.NodeAgentInstallation,
		DeclaredName: "codex",
		Version:      AdapterVersion,
		SourceScope:  adaptersdk.ScopeUser,
		Fingerprint:  stableHex("installation-fp", input.InstallationID),
	}
	nodes = append(nodes, installationNode)

	skillByNameScope := map[string][]adaptersdk.Node{}
	for _, skill := range input.Skills {
		node := adaptersdk.Node{
			NodeID:        "node_" + stableHex("skill", input.InstallationID, skill.Name, string(skill.Scope), skill.PathPseudonym),
			Kind:          adaptersdk.NodeSkillIdentity,
			DeclaredName:  skill.Name,
			Version:       skill.Version,
			SourceScope:   skill.Scope,
			PathPseudonym: skill.PathPseudonym,
			Fingerprint:   skill.Fingerprint,
		}
		nodes = append(nodes, node)
		skillByNameScope[skill.Name] = append(skillByNameScope[skill.Name], node)
		if skill.Enabled && !skill.Disabled {
			edges = append(edges, adaptersdk.Edge{
				EdgeID: "edge_" + stableHex("skill-enabled", node.NodeID, installationNode.NodeID),
				Kind:   adaptersdk.EdgeEnabledFor, FromNode: node.NodeID, ToNode: installationNode.NodeID,
			})
		}
	}
	edges = append(edges, collisionEdges(skillByNameScope, "skill-collision")...)

	pluginByName := map[string][]adaptersdk.Node{}
	for _, plugin := range input.Plugins {
		kind := adaptersdk.NodePluginPackage
		scope := plugin.Scope
		if plugin.CachedOnly {
			kind = adaptersdk.NodeCacheArtifact
			scope = adaptersdk.ScopePluginCache
		}
		node := adaptersdk.Node{
			NodeID:        "node_" + stableHex("plugin", input.InstallationID, plugin.Name, string(scope), plugin.PathPseudonym),
			Kind:          kind,
			DeclaredName:  plugin.Name,
			Version:       plugin.Version,
			SourceScope:   scope,
			PathPseudonym: plugin.PathPseudonym,
			CachedOnly:    plugin.CachedOnly,
			Fingerprint:   plugin.Fingerprint,
		}
		nodes = append(nodes, node)
		pluginByName[plugin.Name] = append(pluginByName[plugin.Name], node)
		// cache_rule: a plugin-cache artifact is never considered enabled.
		// It only ever gets an EdgeEnabledFor edge when ActiveEnabledFor
		// names a real target *and* the plugin is not cache-only.
		if !plugin.CachedOnly && plugin.ActiveEnabledFor != "" {
			edges = append(edges, adaptersdk.Edge{
				EdgeID: "edge_" + stableHex("plugin-enabled", node.NodeID, plugin.ActiveEnabledFor),
				Kind:   adaptersdk.EdgeEnabledFor, FromNode: node.NodeID, ToNode: installationNode.NodeID,
			})
		}
	}
	edges = append(edges, collisionEdges(pluginByName, "plugin-collision")...)

	for _, hook := range input.Hooks {
		node := adaptersdk.Node{
			NodeID:        "node_" + stableHex("hook", input.InstallationID, hook.Name, string(hook.Scope), hook.PathPseudonym),
			Kind:          adaptersdk.NodeHookDefinition,
			DeclaredName:  hook.Name,
			SourceScope:   hook.Scope,
			PathPseudonym: hook.PathPseudonym,
			Fingerprint:   hook.Fingerprint,
		}
		nodes = append(nodes, node)
		if hook.Enabled && hook.Trusted {
			edges = append(edges, adaptersdk.Edge{
				EdgeID: "edge_" + stableHex("hook-enabled", node.NodeID, installationNode.NodeID),
				Kind:   adaptersdk.EdgeEnabledFor, FromNode: node.NodeID, ToNode: installationNode.NodeID,
			})
		}
	}

	mcpByName := map[string][]adaptersdk.Node{}
	for _, mcp := range input.MCPServers {
		serverNode := adaptersdk.Node{
			NodeID:        "node_" + stableHex("mcp-server", input.InstallationID, mcp.Name, string(mcp.Scope), mcp.PathPseudonym),
			Kind:          adaptersdk.NodeMCPServerInstance,
			DeclaredName:  mcp.Name,
			SourceScope:   mcp.Scope,
			PathPseudonym: mcp.PathPseudonym,
			Fingerprint:   mcp.Fingerprint,
		}
		nodes = append(nodes, serverNode)
		mcpByName[mcp.Name] = append(mcpByName[mcp.Name], serverNode)
		if mcp.Enabled {
			edges = append(edges, adaptersdk.Edge{
				EdgeID: "edge_" + stableHex("mcp-enabled", serverNode.NodeID, installationNode.NodeID),
				Kind:   adaptersdk.EdgeEnabledFor, FromNode: serverNode.NodeID, ToNode: installationNode.NodeID,
			})
		}
		toolNames := append([]string(nil), mcp.AdvertisedTools...)
		sort.Strings(toolNames)
		for _, toolName := range toolNames {
			toolNode := adaptersdk.Node{
				NodeID:       "node_" + stableHex("mcp-tool", serverNode.NodeID, toolName),
				Kind:         adaptersdk.NodeMCPTool,
				DeclaredName: toolName,
				SourceScope:  mcp.Scope,
				Fingerprint:  stableHex("mcp-tool-fp", serverNode.NodeID, toolName),
			}
			nodes = append(nodes, toolNode)
			edges = append(edges, adaptersdk.Edge{
				EdgeID: "edge_" + stableHex("mcp-provides", serverNode.NodeID, toolNode.NodeID),
				Kind:   adaptersdk.EdgeProvides, FromNode: serverNode.NodeID, ToNode: toolNode.NodeID,
			})
		}
	}
	edges = append(edges, collisionEdges(mcpByName, "mcp-collision")...)

	for _, target := range input.RepositoryTargets {
		if !filepath.IsAbs(target) {
			return adaptersdk.InventorySnapshot{}, errors.New("codex_inventory_repository_target_must_be_absolute")
		}
	}

	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })
	sort.Slice(edges, func(i, j int) bool { return edges[i].EdgeID < edges[j].EdgeID })

	fingerprintParts := make([]string, 0, len(nodes)+len(edges)+1)
	fingerprintParts = append(fingerprintParts, input.InstallationID)
	for _, node := range nodes {
		fingerprintParts = append(fingerprintParts, node.NodeID, node.Fingerprint)
	}
	for _, edge := range edges {
		fingerprintParts = append(fingerprintParts, edge.EdgeID)
	}
	fingerprint := stableHex(fingerprintParts...)

	return adaptersdk.InventorySnapshot{
		SnapshotID:     "snap_" + stableHex("codex-snapshot", input.InstallationID, fingerprint),
		AdapterID:      AdapterID,
		AdapterVersion: AdapterVersion,
		InstallationID: input.InstallationID,
		ObservedAt:     now.UTC(),
		Fingerprint:    fingerprint,
		Nodes:          nodes,
		Edges:          edges,
	}, nil
}

// collisionEdges links every pair of distinct nodes that share one declared
// name (across different scopes/fingerprints) with an EdgeCollidesWith
// edge, matching contracts/adapter-sdk/inventory-graph.yaml's identity_rule:
// same declared name never forces identity merge. A single node under one
// name produces no collision edge at all.
func collisionEdges(byName map[string][]adaptersdk.Node, label string) []adaptersdk.Edge {
	var edges []adaptersdk.Edge
	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		group := byName[name]
		if len(group) < 2 {
			continue
		}
		sort.Slice(group, func(i, j int) bool { return group[i].NodeID < group[j].NodeID })
		for i := 0; i < len(group); i++ {
			for j := i + 1; j < len(group); j++ {
				edges = append(edges, adaptersdk.Edge{
					EdgeID:   "edge_" + stableHex(label, group[i].NodeID, group[j].NodeID),
					Kind:     adaptersdk.EdgeCollidesWith,
					FromNode: group[i].NodeID,
					ToNode:   group[j].NodeID,
				})
			}
		}
	}
	return edges
}
