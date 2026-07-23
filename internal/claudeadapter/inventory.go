package claudeadapter

import (
	"errors"
	"path/filepath"
	"sort"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
)

// claude.inventory builds the closed inventory entity graph
// contracts/claude/transcript-and-inventory.yaml's inventory_source declares:
// standalone skills, commands, subagent definitions, hooks, plugins,
// marketplaces/cache, MCP servers/tools and managed project/user settings
// layers. Every node/edge kind and source scope used here is reused verbatim
// from contracts/adapter-sdk/inventory-graph.yaml; this file invents no new
// vocabulary, matching contracts/claude/manifest.yaml's
// zero-agent-name-branch-in-core invariant by staying entirely inside this
// adapter's own package -- internal/adaptersdk itself never branches on
// "claude".
const (
	sourceIDInventory = "claude.inventory"

	// maxRepositoryTargets bounds how many explicit repository roots one
	// InventoryInput may declare; contracts/claude/transcript-and-inventory.yaml's
	// repository_scan_bound requires repository roots be scanned only for
	// known active projects or an explicit user target, never a speculative
	// recursive filesystem walk, so this ceiling is small and closed.
	maxRepositoryTargets = 64
)

// InventoryScope is the closed set of scopes a documented Claude Code
// component may be declared in, reused verbatim from
// contracts/adapter-sdk/inventory-graph.yaml's source_scopes (restricted to
// the scopes contracts/claude/transcript-and-inventory.yaml's
// inventory_source.scopes_inventoried names: system, user, repository,
// admin, marketplace, plugin_cache).
type InventoryScope = adaptersdk.SourceScope

// SkillDescriptor is one documented standalone (non-plugin-bundled) skill
// declaration as read from a Claude Code skill manifest, bounded and
// data-only -- no code execution ever parses this shape, matching
// contracts/claude/manifest.yaml's reused_parse_limits.code_execution.
type SkillDescriptor struct {
	Name             string
	Version          string
	Scope            InventoryScope
	Enabled          bool
	Disabled         bool
	DescriptionBytes int
	DescriptionChars int
	// PathPseudonym is the only durable representation of where this skill
	// was declared; the raw path is never itself recorded.
	PathPseudonym string
	Fingerprint   string
}

// CommandDescriptor is one documented custom slash-command definition.
type CommandDescriptor struct {
	Name          string
	Scope         InventoryScope
	Enabled       bool
	PathPseudonym string
	Fingerprint   string
}

// SubagentDescriptor is one documented subagent definition (a named,
// separately-configured agent persona Claude Code can delegate a turn to).
// It is distinct from a live SubagentStart/SubagentStop hook event: this
// descriptor is the static declared definition an inventory scan observes,
// never a runtime activation record.
type SubagentDescriptor struct {
	Name          string
	Scope         InventoryScope
	Enabled       bool
	PathPseudonym string
	Fingerprint   string
}

// HookDescriptor is one documented Claude Code hook definition (not
// necessarily claude.hook's own observer hook -- this covers any
// user-declared hook entry the inventory discovers in settings).
type HookDescriptor struct {
	Name          string
	Scope         InventoryScope
	Enabled       bool
	Trusted       bool
	PathPseudonym string
	Fingerprint   string
}

// PluginDescriptor is one documented plugin package declaration, which may
// be either an active, configured plugin or a marketplace/plugin-cache
// artifact. CachedOnly must never be reported as enabled per
// contracts/claude/transcript-and-inventory.yaml's cache_rule;
// ActiveEnabledFor names the surface/installation this plugin is actually
// wired into (empty when CachedOnly is true or when the plugin is merely
// present but not enabled anywhere). BundledSkills/BundledCommands/
// BundledSubagents/BundledHooks/BundledMCPServers name the components this
// plugin package bundles, each of which receives an EdgeBundles edge from
// this plugin node rather than ever being reported as a standalone,
// unowned component when bundling is observable.
type PluginDescriptor struct {
	Name             string
	Version          string
	Scope            InventoryScope
	CachedOnly       bool
	FromMarketplace  string
	ActiveEnabledFor string
	PathPseudonym    string
	Fingerprint      string

	BundledSkills     []SkillDescriptor
	BundledCommands   []CommandDescriptor
	BundledSubagents  []SubagentDescriptor
	BundledHooks      []HookDescriptor
	BundledMCPServers []MCPServerDescriptor
}

// MCPServerDescriptor is one documented/configured MCP server instance and
// the tool names it advertises. Advertised tools are the configured/
// documented set; actually-observed MCP tool calls are compared against
// this set only in reconciliation (see reconcile.go), never merged here.
type MCPServerDescriptor struct {
	Name            string
	Scope           InventoryScope
	Enabled         bool
	AdvertisedTools []string
	PathPseudonym   string
	Fingerprint     string
}

// MarketplaceDescriptor is one documented plugin marketplace registration
// (a source a plugin was or could be installed from). It is always reported
// separately from the active-plugin-installation graph, matching
// active_vs_cached_distinction verbatim: a marketplace entry alone is never
// evidence of an active, enabled plugin.
type MarketplaceDescriptor struct {
	Name          string
	PathPseudonym string
	Fingerprint   string
}

// InventoryInput is the closed, bounded set of documented facts one
// Inventory call assembles a snapshot from. It is deliberately not a
// generic map: every field here was itself produced by a bounded,
// non-executing read (fingerprints, presence/size-class, enabled flags) via
// adaptersdk.HostView, exactly like FingerprintInstallation. StandaloneSkills/
// StandaloneCommands/StandaloneSubagents/StandaloneHooks/StandaloneMCPServers
// are components observed with no owning plugin; Plugins carries every
// plugin-bundled component instead, so the same component never appears in
// both a standalone list and a plugin's Bundled* list.
type InventoryInput struct {
	InstallationID       string
	StandaloneSkills     []SkillDescriptor
	StandaloneCommands   []CommandDescriptor
	StandaloneSubagents  []SubagentDescriptor
	StandaloneHooks      []HookDescriptor
	StandaloneMCPServers []MCPServerDescriptor
	Plugins              []PluginDescriptor
	Marketplaces         []MarketplaceDescriptor
	// RepositoryTargets is the closed, explicit list of repository roots to
	// inventory: either a known active project (already recorded elsewhere
	// as active) or a target the user explicitly named. Inventory never
	// discovers a repository root itself via speculative recursive walk.
	RepositoryTargets []string
}

// ErrTooManyRepositoryTargets is returned when InventoryInput declares more
// repository roots than maxRepositoryTargets, closing off an unbounded scan
// even if every individual target were otherwise legitimate.
var ErrTooManyRepositoryTargets = errors.New("claude_inventory_too_many_repository_targets")

// BuildInventorySnapshot assembles one adaptersdk.InventorySnapshot from
// input, deterministically: the same input always yields byte-identical
// nodes/edges/fingerprint (modulo the observed_at timestamp, which the
// caller injects so tests can pin it). Cache-only plugins never receive an
// EdgeEnabledFor edge; two skill/command/subagent/hook/MCP-server nodes
// sharing a declared name across scopes (including duplicate names between
// a standalone component and a plugin-bundled component of the same kind)
// are never merged into one node -- they remain distinct nodes joined by an
// EdgeCollidesWith edge, matching contracts/adapter-sdk/inventory-graph.yaml's
// identity_rule verbatim. Every plugin-bundled component receives an
// EdgeBundles edge from its owning plugin node and is never also reported
// through the standalone lists.
func BuildInventorySnapshot(input InventoryInput, now time.Time) (adaptersdk.InventorySnapshot, error) {
	if len(input.RepositoryTargets) > maxRepositoryTargets {
		return adaptersdk.InventorySnapshot{}, ErrTooManyRepositoryTargets
	}
	for _, target := range input.RepositoryTargets {
		if !filepath.IsAbs(target) {
			return adaptersdk.InventorySnapshot{}, errors.New("claude_inventory_repository_target_must_be_absolute")
		}
	}

	var nodes []adaptersdk.Node
	var edges []adaptersdk.Edge

	installationNode := adaptersdk.Node{
		NodeID:       "node_" + stableHex("installation", input.InstallationID),
		Kind:         adaptersdk.NodeAgentInstallation,
		DeclaredName: "claude",
		Version:      AdapterVersion,
		SourceScope:  adaptersdk.ScopeUser,
		Fingerprint:  stableHex("installation-fp", input.InstallationID),
	}
	nodes = append(nodes, installationNode)

	// byName collects every node of a given component kind (skill, command,
	// subagent, hook, mcp-server) keyed by declared name, standalone and
	// plugin-bundled alike, so collision detection considers the *whole*
	// inventory rather than only the standalone slice -- a plugin-bundled
	// skill sharing a name with a standalone skill (or another plugin's
	// bundled skill) is exactly the "duplicate-component-name" case this
	// stage must disambiguate via EdgeCollidesWith, never a silent merge.
	skillByName := map[string][]adaptersdk.Node{}
	commandByName := map[string][]adaptersdk.Node{}
	subagentByName := map[string][]adaptersdk.Node{}
	hookByName := map[string][]adaptersdk.Node{}
	mcpByName := map[string][]adaptersdk.Node{}
	pluginByName := map[string][]adaptersdk.Node{}
	marketplaceByName := map[string][]adaptersdk.Node{}

	addSkill := func(skill SkillDescriptor, owner *adaptersdk.Node) adaptersdk.Node {
		node := adaptersdk.Node{
			NodeID:        "node_" + stableHex("skill", input.InstallationID, skill.Name, string(skill.Scope), skill.PathPseudonym, ownerKey(owner)),
			Kind:          adaptersdk.NodeSkillIdentity,
			DeclaredName:  skill.Name,
			Version:       skill.Version,
			SourceScope:   skill.Scope,
			PathPseudonym: skill.PathPseudonym,
			Fingerprint:   skill.Fingerprint,
		}
		nodes = append(nodes, node)
		skillByName[skill.Name] = append(skillByName[skill.Name], node)
		if owner == nil && skill.Enabled && !skill.Disabled {
			edges = append(edges, edgeEnabledFor(node, installationNode))
		}
		return node
	}
	addCommand := func(command CommandDescriptor, owner *adaptersdk.Node) adaptersdk.Node {
		node := adaptersdk.Node{
			NodeID:        "node_" + stableHex("command", input.InstallationID, command.Name, string(command.Scope), command.PathPseudonym, ownerKey(owner)),
			Kind:          adaptersdk.NodeCustomCommandDefinition,
			DeclaredName:  command.Name,
			SourceScope:   command.Scope,
			PathPseudonym: command.PathPseudonym,
			Fingerprint:   command.Fingerprint,
		}
		nodes = append(nodes, node)
		commandByName[command.Name] = append(commandByName[command.Name], node)
		if owner == nil && command.Enabled {
			edges = append(edges, edgeEnabledFor(node, installationNode))
		}
		return node
	}
	addSubagent := func(subagent SubagentDescriptor, owner *adaptersdk.Node) adaptersdk.Node {
		node := adaptersdk.Node{
			NodeID:        "node_" + stableHex("subagent", input.InstallationID, subagent.Name, string(subagent.Scope), subagent.PathPseudonym, ownerKey(owner)),
			Kind:          adaptersdk.NodeSubagentDefinition,
			DeclaredName:  subagent.Name,
			SourceScope:   subagent.Scope,
			PathPseudonym: subagent.PathPseudonym,
			Fingerprint:   subagent.Fingerprint,
		}
		nodes = append(nodes, node)
		subagentByName[subagent.Name] = append(subagentByName[subagent.Name], node)
		if owner == nil && subagent.Enabled {
			edges = append(edges, edgeEnabledFor(node, installationNode))
		}
		return node
	}
	addHook := func(hook HookDescriptor, owner *adaptersdk.Node) adaptersdk.Node {
		node := adaptersdk.Node{
			NodeID:        "node_" + stableHex("hook", input.InstallationID, hook.Name, string(hook.Scope), hook.PathPseudonym, ownerKey(owner)),
			Kind:          adaptersdk.NodeHookDefinition,
			DeclaredName:  hook.Name,
			SourceScope:   hook.Scope,
			PathPseudonym: hook.PathPseudonym,
			Fingerprint:   hook.Fingerprint,
		}
		nodes = append(nodes, node)
		hookByName[hook.Name] = append(hookByName[hook.Name], node)
		if owner == nil && hook.Enabled && hook.Trusted {
			edges = append(edges, edgeEnabledFor(node, installationNode))
		}
		return node
	}
	addMCPServer := func(mcp MCPServerDescriptor, owner *adaptersdk.Node) adaptersdk.Node {
		serverNode := adaptersdk.Node{
			NodeID:        "node_" + stableHex("mcp-server", input.InstallationID, mcp.Name, string(mcp.Scope), mcp.PathPseudonym, ownerKey(owner)),
			Kind:          adaptersdk.NodeMCPServerInstance,
			DeclaredName:  mcp.Name,
			SourceScope:   mcp.Scope,
			PathPseudonym: mcp.PathPseudonym,
			Fingerprint:   mcp.Fingerprint,
		}
		nodes = append(nodes, serverNode)
		mcpByName[mcp.Name] = append(mcpByName[mcp.Name], serverNode)
		if owner == nil && mcp.Enabled {
			edges = append(edges, edgeEnabledFor(serverNode, installationNode))
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
		return serverNode
	}

	for _, skill := range input.StandaloneSkills {
		addSkill(skill, nil)
	}
	for _, command := range input.StandaloneCommands {
		addCommand(command, nil)
	}
	for _, subagent := range input.StandaloneSubagents {
		addSubagent(subagent, nil)
	}
	for _, hook := range input.StandaloneHooks {
		addHook(hook, nil)
	}
	for _, mcp := range input.StandaloneMCPServers {
		addMCPServer(mcp, nil)
	}

	for _, plugin := range input.Plugins {
		kind := adaptersdk.NodePluginPackage
		scope := plugin.Scope
		if plugin.CachedOnly {
			kind = adaptersdk.NodeCacheArtifact
			scope = adaptersdk.ScopePluginCache
		}
		pluginNode := adaptersdk.Node{
			NodeID:        "node_" + stableHex("plugin", input.InstallationID, plugin.Name, string(scope), plugin.PathPseudonym),
			Kind:          kind,
			DeclaredName:  plugin.Name,
			Version:       plugin.Version,
			SourceScope:   scope,
			PathPseudonym: plugin.PathPseudonym,
			CachedOnly:    plugin.CachedOnly,
			Fingerprint:   plugin.Fingerprint,
		}
		nodes = append(nodes, pluginNode)
		pluginByName[plugin.Name] = append(pluginByName[plugin.Name], pluginNode)

		// cache_rule: a marketplace/plugin-cache artifact is never
		// considered enabled. It only ever gets an EdgeEnabledFor edge when
		// ActiveEnabledFor names a real target *and* the plugin is not
		// cache-only.
		if !plugin.CachedOnly && plugin.ActiveEnabledFor != "" {
			edges = append(edges, adaptersdk.Edge{
				EdgeID: "edge_" + stableHex("plugin-enabled", pluginNode.NodeID, plugin.ActiveEnabledFor),
				Kind:   adaptersdk.EdgeEnabledFor, FromNode: pluginNode.NodeID, ToNode: installationNode.NodeID,
			})
		}
		if plugin.FromMarketplace != "" {
			marketNode := adaptersdk.Node{
				NodeID:       "node_" + stableHex("marketplace", plugin.FromMarketplace),
				Kind:         adaptersdk.NodeCacheArtifact,
				DeclaredName: plugin.FromMarketplace,
				SourceScope:  adaptersdk.ScopeMarketplace,
				Fingerprint:  stableHex("marketplace-fp", plugin.FromMarketplace),
			}
			nodes = append(nodes, marketNode)
			marketplaceByName[plugin.FromMarketplace] = append(marketplaceByName[plugin.FromMarketplace], marketNode)
			edges = append(edges, adaptersdk.Edge{
				EdgeID: "edge_" + stableHex("plugin-configured-in-marketplace", pluginNode.NodeID, marketNode.NodeID),
				Kind:   adaptersdk.EdgeConfiguredIn, FromNode: pluginNode.NodeID, ToNode: marketNode.NodeID,
			})
		}

		// Every component this plugin bundles is linked to the plugin
		// package node by an EdgeBundles edge and is never also reported as
		// a standalone, unowned component -- addSkill/addCommand/etc. are
		// called with owner=&pluginNode here, which both skips the direct
		// installation EdgeEnabledFor edge (bundled components are enabled
		// transitively through the plugin, not independently) and folds the
		// bundled component into the same by-name collision-detection map
		// standalone components use.
		for _, skill := range plugin.BundledSkills {
			node := addSkill(skill, &pluginNode)
			edges = append(edges, edgeBundles(pluginNode, node))
		}
		for _, command := range plugin.BundledCommands {
			node := addCommand(command, &pluginNode)
			edges = append(edges, edgeBundles(pluginNode, node))
		}
		for _, subagent := range plugin.BundledSubagents {
			node := addSubagent(subagent, &pluginNode)
			edges = append(edges, edgeBundles(pluginNode, node))
		}
		for _, hook := range plugin.BundledHooks {
			node := addHook(hook, &pluginNode)
			edges = append(edges, edgeBundles(pluginNode, node))
		}
		for _, mcp := range plugin.BundledMCPServers {
			node := addMCPServer(mcp, &pluginNode)
			edges = append(edges, edgeBundles(pluginNode, node))
		}
	}

	for _, marketplace := range input.Marketplaces {
		marketNode := adaptersdk.Node{
			NodeID:        "node_" + stableHex("marketplace", marketplace.Name, marketplace.PathPseudonym),
			Kind:          adaptersdk.NodeCacheArtifact,
			DeclaredName:  marketplace.Name,
			SourceScope:   adaptersdk.ScopeMarketplace,
			PathPseudonym: marketplace.PathPseudonym,
			Fingerprint:   marketplace.Fingerprint,
		}
		nodes = append(nodes, marketNode)
		marketplaceByName[marketplace.Name] = append(marketplaceByName[marketplace.Name], marketNode)
	}

	edges = append(edges, collisionEdges(skillByName, "skill-collision")...)
	edges = append(edges, collisionEdges(commandByName, "command-collision")...)
	edges = append(edges, collisionEdges(subagentByName, "subagent-collision")...)
	edges = append(edges, collisionEdges(hookByName, "hook-collision")...)
	edges = append(edges, collisionEdges(mcpByName, "mcp-collision")...)
	edges = append(edges, collisionEdges(pluginByName, "plugin-collision")...)
	edges = append(edges, collisionEdges(marketplaceByName, "marketplace-collision")...)

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
		SnapshotID:     "claudesnap_" + stableHex("claude-snapshot", input.InstallationID, fingerprint),
		AdapterID:      AdapterID,
		AdapterVersion: AdapterVersion,
		InstallationID: input.InstallationID,
		ObservedAt:     now.UTC(),
		Fingerprint:    fingerprint,
		Nodes:          nodes,
		Edges:          edges,
	}, nil
}

// ownerKey returns a stable disambiguator for a component's owning plugin
// node (or the empty string for a standalone component), so a
// plugin-bundled component's NodeID never collides with an otherwise
// identical standalone component's NodeID purely because they share every
// other identity field.
func ownerKey(owner *adaptersdk.Node) string {
	if owner == nil {
		return ""
	}
	return owner.NodeID
}

func edgeEnabledFor(component, installation adaptersdk.Node) adaptersdk.Edge {
	return adaptersdk.Edge{
		EdgeID: "edge_" + stableHex("enabled-for", component.NodeID, installation.NodeID),
		Kind:   adaptersdk.EdgeEnabledFor, FromNode: component.NodeID, ToNode: installation.NodeID,
	}
}

func edgeBundles(plugin, component adaptersdk.Node) adaptersdk.Edge {
	return adaptersdk.Edge{
		EdgeID: "edge_" + stableHex("bundles", plugin.NodeID, component.NodeID),
		Kind:   adaptersdk.EdgeBundles, FromNode: plugin.NodeID, ToNode: component.NodeID,
	}
}

// collisionEdges links every pair of distinct nodes that share one declared
// name (across different scopes/owners/fingerprints) with an
// EdgeCollidesWith edge, matching
// contracts/adapter-sdk/inventory-graph.yaml's identity_rule: same declared
// name never forces identity merge, whether the collision is between two
// standalone components, two different plugins' bundled components, or a
// standalone component and a plugin-bundled component of the same kind. A
// single node under one name produces no collision edge at all.
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
