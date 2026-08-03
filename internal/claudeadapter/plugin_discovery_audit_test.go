package claudeadapter_test

import (
	"os"
	"path/filepath"
	"testing"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/claudeadapter"
)

// This file holds the 2026-08-01 component audit (lane 02 -- plugins)
// regression tests. Each test documents one defect found by auditing plugin
// discovery against a real Claude Code host layout. They are expected to FAIL
// against the current implementation; each failure message names the exact
// broken hop.
//
// Audit artifact: reports/artifacts/2026-08-01-component-audit/02-plugins.md

func lane02PseudonymKey() []byte {
	return []byte("claudeadapter-plugin-discovery-audit-key-000001")
}

// lane02BuildRealPluginTree writes one plugin package in Claude Code's real
// on-disk shape underneath root:
//
//	<root>/plugins/cache/<marketplace>/<plugin>/<version>/.claude-plugin/plugin.json
//	<root>/plugins/cache/<marketplace>/<plugin>/<version>/skills/<skill>/SKILL.md
//	<root>/plugins/cache/<marketplace>/<plugin>/<version>/agents/<agent>.md
//	<root>/plugins/cache/<marketplace>/<plugin>/<version>/hooks/hooks.json
//	<root>/plugins/cache/<marketplace>/<plugin>/<version>/commands/<command>.md
//	<root>/plugins/installed_plugins.json
//	<root>/settings.json
//
// This is byte-for-byte the layout observed on the audited host
// (~/.claude/plugins/cache/yuzuru-engineering/sre-agent/0.1.0/).
func lane02BuildRealPluginTree(t *testing.T, root string) {
	t.Helper()
	mkdir := func(path string) {
		t.Helper()
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string) {
		t.Helper()
		mkdir(filepath.Dir(path))
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	versionDir := filepath.Join(root, "plugins", "cache", "yuzuru-engineering", "sre-agent", "0.1.0")
	write(filepath.Join(versionDir, ".claude-plugin", "plugin.json"),
		`{"name":"sre-agent","version":"0.1.0","skills":"./skills/"}`)
	write(filepath.Join(versionDir, "skills", "sre-agent", "SKILL.md"),
		"---\nname: sre-agent\ndescription: routing\n---\nbody\n")
	write(filepath.Join(versionDir, "skills", "verification-strategy", "SKILL.md"),
		"---\nname: verification-strategy\ndescription: scope\n---\nbody\n")
	write(filepath.Join(versionDir, "agents", "test-actor.md"),
		"---\nname: test-actor\ndescription: isolated scenario runner\n---\nbody\n")
	write(filepath.Join(versionDir, "hooks", "hooks.json"),
		`{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"./load_target_guard.py"}]}]}}`)
	write(filepath.Join(versionDir, "commands", "verify.md"),
		"---\nname: verify\ndescription: run verification\n---\nbody\n")

	write(filepath.Join(root, "plugins", "installed_plugins.json"), `{
	  "version": 2,
	  "plugins": {
	    "sre-agent@yuzuru-engineering": [
	      {"scope":"user","installPath":"`+versionDir+`","version":"0.1.0",
	       "gitCommitSha":"5af7fec5630a3dd9adff3211c903d34b7c483b93"}
	    ]
	  }
	}`)
	write(filepath.Join(root, "settings.json"),
		`{"enabledPlugins":{"sre-agent@yuzuru-engineering":true}}`)
}

func lane02ScanRoot(t *testing.T, allowedRoot, stateRoot string) (claudeadapter.InventoryInput, bool) {
	t.Helper()
	host, err := adaptersdk.NewHostView([]string{allowedRoot}, nil, lane02PseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	return claudeadapter.ScanHostInventory(host, adaptersdk.Installation{
		InstallationID: "ain_0102030405060708090a0b0c0d0e0f10",
		AdapterID:      claudeadapter.AdapterID,
		SurfaceID:      "cli",
		StateRoot:      stateRoot,
	})
}

// TestAuditLane02DirectStateRootDiscoversRealPluginLayout is the control: with the
// state root pointing straight at the real tree (no symlinks), discovery must
// find the plugin, its version and its bundled skills. This test is expected
// to PASS -- it exists so the symlink test below cannot be dismissed as "the
// fixture layout is wrong".
func TestAuditLane02DirectStateRootDiscoversRealPluginLayout(t *testing.T) {
	root := t.TempDir()
	lane02BuildRealPluginTree(t, root)

	input, scanned := lane02ScanRoot(t, root, root)
	if !scanned {
		t.Fatalf("scanned=false for a direct, readable state root")
	}
	if len(input.Plugins) != 1 {
		t.Fatalf("plugins=%d, want 1: %+v", len(input.Plugins), input.Plugins)
	}
	plugin := input.Plugins[0]
	if plugin.Name != "sre-agent@yuzuru-engineering" {
		t.Fatalf("plugin name=%q, want sre-agent@yuzuru-engineering", plugin.Name)
	}
	if plugin.Version != "0.1.0" {
		t.Fatalf("plugin version=%q, want 0.1.0", plugin.Version)
	}
	if len(plugin.BundledSkills) != 2 {
		t.Fatalf("bundled skills=%d, want 2", len(plugin.BundledSkills))
	}
}

// TestAuditLane02SymlinkedStateRootDiscoversPlugins documents defect F-02-1.
//
// The documented non-container deployment assembles a read-only state root by
// symlinking the agent's real files into it (this is exactly what the audit
// test bed does, and what any host-native install that does not want to expose
// the whole home directory must do). Every adaptersdk.HostView probe resolves
// one symlink level and then requires the *target* to be inside the allowed
// root, so every such link is rejected with ErrOutsideAllowedRoots. The
// rejection is swallowed by inventoryscan.go (scanPluginCache returns
// observed=false on any probe error, ScanHostInventory ignores the settings
// read error), so a fully-populated host is reported as a host with no plugins
// at all.
//
// Resolution (2026-08-03). Of the two acceptable outcomes the paragraph above
// names, only the second is available: following the link would mean reading a
// target the operator did not put inside an allowed root, which is the one
// guarantee HostView exists to make. Widening it to make a scan succeed would
// trade a visible gap for an invisible privilege.
//
// So the refusal is now reported distinguishably -- the settings read, the
// marketplace listing and the plugin-folder listing each tally a coverage gap
// instead of returning silently -- and this test asserts that, not discovery.
// The deployment answer to a symlinked state root is to mount the link target
// read-only as an allowed root (KANSOKU_AGENT_LINK_ROOT_*, added in
// c49437d); TestAuditLane02LinkedLibraryInsideAllowedRootsIsFullyDiscovered
// below pins that supported layout end to end.
func TestAuditLane02SymlinkedStateRootDiscoversPlugins(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	stateRoot := filepath.Join(base, "state")
	lane02BuildRealPluginTree(t, real)

	if err := os.MkdirAll(filepath.Join(stateRoot, "plugins"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, link := range []struct{ from, to string }{
		{filepath.Join(real, "settings.json"), filepath.Join(stateRoot, "settings.json")},
		{filepath.Join(real, "plugins", "cache"), filepath.Join(stateRoot, "plugins", "cache")},
		{filepath.Join(real, "plugins", "installed_plugins.json"), filepath.Join(stateRoot, "plugins", "installed_plugins.json")},
	} {
		if err := os.Symlink(link.from, link.to); err != nil {
			t.Fatal(err)
		}
	}

	input, _ := lane02ScanRoot(t, stateRoot, stateRoot)
	if len(input.Plugins) != 0 {
		t.Fatalf("a link target outside the allowed roots was read anyway: %d plugins", len(input.Plugins))
	}
	if input.CoverageGaps.Total() == 0 {
		t.Fatal(
			"F-02-1: a refused symlinked state root reported no coverage gap at all, so a " +
				"populated host stays indistinguishable from an empty one",
		)
	}
	if input.CoverageGaps[adaptersdk.CoverageGapUnresolvableSymlink] == 0 {
		t.Fatalf(
			"the refusal was tallied but not classified as an unresolvable symlink: %v",
			input.CoverageGaps,
		)
	}
}

// TestAuditLane02LinkedLibraryInsideAllowedRootsIsFullyDiscovered is the other
// half of F-02-1: the supported deployment, where the link target is itself
// mounted read-only as an allowed root. Discovery must then be complete --
// plugin, version, and all five bundled child kinds -- with no coverage gap.
func TestAuditLane02LinkedLibraryInsideAllowedRootsIsFullyDiscovered(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	stateRoot := filepath.Join(base, "state")
	lane02BuildRealPluginTree(t, real)

	if err := os.MkdirAll(filepath.Join(stateRoot, "plugins"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, link := range []struct{ from, to string }{
		{filepath.Join(real, "settings.json"), filepath.Join(stateRoot, "settings.json")},
		{filepath.Join(real, "plugins", "cache"), filepath.Join(stateRoot, "plugins", "cache")},
		{filepath.Join(real, "plugins", "installed_plugins.json"), filepath.Join(stateRoot, "plugins", "installed_plugins.json")},
	} {
		if err := os.Symlink(link.from, link.to); err != nil {
			t.Fatal(err)
		}
	}

	// base covers both the state root and the linked library, which is exactly
	// what mounting the link target read-only achieves in the compose stack.
	host, err := adaptersdk.NewHostView([]string{base}, nil, lane02PseudonymKey())
	if err != nil {
		t.Fatal(err)
	}
	input, scanned := claudeadapter.ScanHostInventory(host, adaptersdk.Installation{
		InstallationID: "ain_0102030405060708090a0b0c0d0e0f10",
		AdapterID:      claudeadapter.AdapterID,
		SurfaceID:      "cli",
		StateRoot:      stateRoot,
	})
	if !scanned || len(input.Plugins) == 0 {
		t.Fatalf("linked library inside the allowed roots was not discovered: scanned=%v plugins=%d",
			scanned, len(input.Plugins))
	}
	if input.CoverageGaps.Total() != 0 {
		t.Errorf("a fully readable layout still reported coverage gaps: %v", input.CoverageGaps)
	}
	var found *claudeadapter.PluginDescriptor
	for index := range input.Plugins {
		if input.Plugins[index].Name == "sre-agent@yuzuru-engineering" {
			found = &input.Plugins[index]
		}
	}
	if found == nil {
		t.Fatalf("expected plugin not discovered: %+v", input.Plugins)
	}
	if len(found.BundledSkills) == 0 || len(found.BundledSubagents) == 0 ||
		len(found.BundledCommands) == 0 || len(found.BundledHooks) == 0 {
		t.Errorf("bundled children incomplete through a link: skills=%d subagents=%d commands=%d hooks=%d",
			len(found.BundledSkills), len(found.BundledSubagents),
			len(found.BundledCommands), len(found.BundledHooks))
	}
}

// TestAuditLane02PluginBundledAgentsHooksCommandsAreAttributed documents defect
// F-02-3: claudeadapter's plugin-cache scan only ever populates BundledSkills.
// A real plugin package also ships agents/, hooks/ and commands/ (the audited
// sre-agent@yuzuru-engineering plugin ships all three), and
// contracts/plugins/inventory-and-identity.yaml's child_kinds declares
// skill/hook/mcp/command/app. Those children are therefore never bundled to
// their owning plugin, never receive an EdgeBundles edge, and can never be
// attributed by dataplatform.persistPluginChildActivity.
func TestAuditLane02PluginBundledAgentsHooksCommandsAreAttributed(t *testing.T) {
	root := t.TempDir()
	lane02BuildRealPluginTree(t, root)

	input, scanned := lane02ScanRoot(t, root, root)
	if !scanned || len(input.Plugins) != 1 {
		t.Fatalf("precondition failed: scanned=%v plugins=%d", scanned, len(input.Plugins))
	}
	plugin := input.Plugins[0]
	if len(plugin.BundledSubagents) == 0 || len(plugin.BundledHooks) == 0 ||
		len(plugin.BundledCommands) == 0 {
		t.Fatalf(
			"F-02-3: plugin %q bundled skills=%d subagents=%d hooks=%d commands=%d mcp=%d; "+
				"the on-disk package ships agents/test-actor.md, hooks/hooks.json and "+
				"commands/verify.md but claudeadapter/inventoryscan.go:281 only scans skills/",
			plugin.Name, len(plugin.BundledSkills), len(plugin.BundledSubagents),
			len(plugin.BundledHooks), len(plugin.BundledCommands), len(plugin.BundledMCPServers),
		)
	}
}

// TestAuditLane02EnabledPluginWithoutCacheKeepsVersionStateDistinguishable documents
// the "configured but not installed" case observed on the audited host:
// settings.json enables t-skills-sql@t-skills-marketplace but no cache
// directory and no installed_plugins.json entry exist for it. The descriptor
// must not present an empty Version as if the version had been observed.
func TestAuditLane02EnabledPluginWithoutCacheKeepsVersionStateDistinguishable(t *testing.T) {
	root := t.TempDir()
	lane02BuildRealPluginTree(t, root)
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(
		`{"enabledPlugins":{"sre-agent@yuzuru-engineering":true,"t-skills-sql@t-skills-marketplace":true}}`,
	), 0o600); err != nil {
		t.Fatal(err)
	}

	input, _ := lane02ScanRoot(t, root, root)
	var ghost *claudeadapter.PluginDescriptor
	for i := range input.Plugins {
		if input.Plugins[i].Name == "t-skills-sql@t-skills-marketplace" {
			ghost = &input.Plugins[i]
		}
	}
	if ghost == nil {
		t.Fatalf("configured-but-uninstalled plugin was dropped entirely: %+v", input.Plugins)
	}
	if ghost.Version != "" {
		t.Fatalf("unexpected version %q for an uninstalled plugin", ghost.Version)
	}
	// The descriptor carries no state field at all, so the empty Version is
	// the only signal and it is indistinguishable from "observed as empty".
	t.Logf("F-02-5 context: uninstalled-but-enabled plugin descriptor = %+v", *ghost)
}
