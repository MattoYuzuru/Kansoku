package claudeadapter_test

// Regression tests added by the 2026-08-01 component audit (lane 01 -- skills).
// Every test in this file documents a defect or pins an invariant that the
// audit found unprotected. They are deliberately kept in one file so a fix
// agent can find and re-run them as a group:
//
//	go test ./internal/claudeadapter/ -run Audit20260801 -v
//
// The env-gated test at the bottom scans the operator's REAL Claude Code state
// root read-only; it is skipped unless KANSOKU_AUDIT_REAL_CLAUDE_ROOT is set.

import (
	"os"
	"path/filepath"
	"testing"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/claudeadapter"
)

func auditPseudonymKey() []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 7)
	}
	return key
}

func auditHostView(t *testing.T, roots ...string) *adaptersdk.HostView {
	t.Helper()
	host, err := adaptersdk.NewHostView(roots, nil, auditPseudonymKey())
	if err != nil {
		t.Fatalf("NewHostView: %v", err)
	}
	return host
}

func auditWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

const auditSkillManifest = "---\nname: %s\ndescription: audit fixture skill\n---\n\nbody\n"

func auditSkillMD(name string) string {
	return "---\nname: " + name + "\ndescription: audit fixture skill\n---\n\nbody\n"
}

// buildRealShapedClaudeRoot reproduces, byte-shape for byte-shape, the real
// on-disk layout of a Claude Code 2.1.x user state root as observed on the
// audit host (2026-08-01):
//
//	<root>/settings.json                                              (enabledPlugins/mcpServers)
//	<root>/skills/<skill>/SKILL.md                                    (personal skills -- Claude Code's documented layout)
//	<root>/plugins/installed_plugins.json
//	<root>/plugins/cache/<marketplace>/<plugin>/<version>/.claude-plugin/plugin.json
//	<root>/plugins/cache/<marketplace>/<plugin>/<version>/skills/<skill>/SKILL.md
func buildRealShapedClaudeRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	auditWrite(t, filepath.Join(root, "settings.json"), `{
	  "enabledPlugins": {"t-skills-quality@t-skills-marketplace": true},
	  "mcpServers": {}
	}`)

	// Personal skills, exactly where Claude Code documents them:
	// ~/.claude/skills/<name>/SKILL.md -- no scope subdirectory.
	auditWrite(t, filepath.Join(root, "skills", "personal-audit-skill", "SKILL.md"),
		auditSkillMD("personal-audit-skill"))

	auditWrite(t, filepath.Join(root, "plugins", "installed_plugins.json"), `{
	  "version": 1,
	  "plugins": {
	    "t-skills-quality@t-skills-marketplace": [
	      {"scope": "user", "version": "152c60709767"}
	    ]
	  }
	}`)

	versionDir := filepath.Join(root, "plugins", "cache", "t-skills-marketplace", "t-skills-quality", "152c60709767")
	auditWrite(t, filepath.Join(versionDir, ".claude-plugin", "plugin.json"),
		`{"name": "t-skills-quality", "description": "quality skills"}`)
	auditWrite(t, filepath.Join(versionDir, "skills", "systematic-debugging", "SKILL.md"),
		auditSkillMD("systematic-debugging"))
	auditWrite(t, filepath.Join(versionDir, "skills", "test-driven-development", "SKILL.md"),
		auditSkillMD("test-driven-development"))

	return root
}

func countBundledSkills(input claudeadapter.InventoryInput) int {
	total := 0
	for _, plugin := range input.Plugins {
		total += len(plugin.BundledSkills)
	}
	return total
}

// TestAudit20260801PluginCacheSkillsAreFoundInRealLayout pins the part of the
// scan that IS correct: the plugin-cache path template
// plugins/cache/<marketplace>/<plugin>/<version>/skills/<skill>/SKILL.md and
// the SKILL.md frontmatter parser both match the real on-disk shape. If this
// test ever fails, the path template or the frontmatter parser regressed --
// not the symlink boundary.
func TestAudit20260801PluginCacheSkillsAreFoundInRealLayout(t *testing.T) {
	root := buildRealShapedClaudeRoot(t)
	host := auditHostView(t, root)

	input, scanned := claudeadapter.ScanHostInventory(host, adaptersdk.Installation{
		InstallationID: "ain_00000000000000000000000000000001",
		AdapterID:      claudeadapter.AdapterID,
		SurfaceID:      "cli",
		StateRoot:      root,
	})
	if !scanned {
		t.Fatalf("a real-shaped Claude state root must report scanned=true")
	}
	if len(input.Plugins) != 1 {
		t.Fatalf("expected exactly 1 plugin, got %d (%+v)", len(input.Plugins), input.Plugins)
	}
	if got := countBundledSkills(input); got != 2 {
		t.Fatalf("expected 2 plugin-bundled skills from the real cache layout, got %d", got)
	}
	if len(input.Marketplaces) != 1 {
		t.Fatalf("expected 1 marketplace, got %d", len(input.Marketplaces))
	}
}

// TestAudit20260801PersonalSkillsAtDocumentedClaudeHomePathAreDiscovered is a
// FAILING regression test for defect F-01-2.
//
// Claude Code documents personal skills at ~/.claude/skills/<name>/SKILL.md.
// claudeadapter.documentedSkillRoots (internal/claudeadapter/inventoryscan.go)
// instead looks only under <stateRoot>/skills/{user,repository,admin,system}/,
// a Kansoku-invented scope subdirectory that does not exist on a real host, so
// every personal skill is invisible to inventory.
//
// Expected once fixed: <stateRoot>/skills/<name>/SKILL.md is scanned as a
// user-scope standalone skill (while keeping the existing scope
// subdirectories for Kansoku's own multi-surface state-root layout).
func TestAudit20260801PersonalSkillsAtDocumentedClaudeHomePathAreDiscovered(t *testing.T) {
	root := buildRealShapedClaudeRoot(t)
	host := auditHostView(t, root)

	input, _ := claudeadapter.ScanHostInventory(host, adaptersdk.Installation{
		InstallationID: "ain_00000000000000000000000000000001",
		AdapterID:      claudeadapter.AdapterID,
		SurfaceID:      "cli",
		StateRoot:      root,
	})

	found := false
	for _, skill := range input.StandaloneSkills {
		if skill.Name == "personal-audit-skill" {
			found = true
		}
	}
	if !found {
		t.Fatalf("personal skill at <stateRoot>/skills/<name>/SKILL.md (Claude Code's documented layout) "+
			"was not discovered; StandaloneSkills=%+v", input.StandaloneSkills)
	}
}

// TestAudit20260801SymlinkedStateRootReportsScannedTrueWithFabricatedZero is a
// FAILING regression test for defect F-01-1.
//
// HostView.ReadProbe/ReadConfigProbe/ListDirectoryProbe resolve a symlink one
// level and then require the TARGET to also live inside an allowed root
// (internal/adaptersdk/hostview.go:162-183, 204-225, 266-286). A state root
// assembled out of symlinks into the operator's real ~/.claude therefore
// yields ErrOutsideAllowedRoots for every probe. scanSkillRoot and
// scanPluginCache swallow that error and return observed=false
// (inventoryscan.go:214-221, 457-465), while an unrelated real-but-empty
// scope directory still flips scanned=true -- so ScanHostInventory returns
// (empty input, scanned=true) and the whole tree is silently reported as a
// plausible-looking zero.
//
// AGENTS.md: "A parser may quarantine unknown data; it may not silently drop
// or coerce it." contracts/claude/skill-evidence-and-reconciliation.yaml
// exit_gate.independent_visible_degradation: every source must "fail visibly
// ... never silently reporting plausible looking zero usage".
//
// Expected once fixed: an unreadable/refused skill or plugin root must be
// distinguishable from an empty one -- either scanned=false, or a durable
// degraded/quarantine signal naming the refused root.
func TestAudit20260801SymlinkedStateRootReportsScannedTrueWithFabricatedZero(t *testing.T) {
	outside := buildRealShapedClaudeRoot(t) // stands in for the real ~/.claude
	root := t.TempDir()                     // stands in for agent-state/claude

	if err := os.Symlink(filepath.Join(outside, "settings.json"), filepath.Join(root, "settings.json")); err != nil {
		t.Fatalf("symlink settings.json: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "plugins"), 0o755); err != nil {
		t.Fatalf("mkdir plugins: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "plugins", "cache"), filepath.Join(root, "plugins", "cache")); err != nil {
		t.Fatalf("symlink plugins/cache: %v", err)
	}
	if err := os.Symlink(
		filepath.Join(outside, "plugins", "installed_plugins.json"),
		filepath.Join(root, "plugins", "installed_plugins.json"),
	); err != nil {
		t.Fatalf("symlink installed_plugins.json: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "skills"), 0o755); err != nil {
		t.Fatalf("mkdir skills: %v", err)
	}
	if err := os.Symlink(filepath.Join(outside, "skills"), filepath.Join(root, "skills", "user")); err != nil {
		t.Fatalf("symlink skills/user: %v", err)
	}
	// A real, empty, in-root scope directory -- exactly what the test bed has.
	if err := os.MkdirAll(filepath.Join(root, "skills", "system"), 0o755); err != nil {
		t.Fatalf("mkdir skills/system: %v", err)
	}

	host := auditHostView(t, root)
	input, scanned := claudeadapter.ScanHostInventory(host, adaptersdk.Installation{
		InstallationID: "ain_00000000000000000000000000000001",
		AdapterID:      claudeadapter.AdapterID,
		SurfaceID:      "cli",
		StateRoot:      root,
	})

	empty := len(input.Plugins) == 0 && len(input.Marketplaces) == 0 &&
		len(input.StandaloneSkills) == 0 && len(input.StandaloneMCPServers) == 0
	// Closed 2026-08-01 by the second of the two remedies this test's own
	// docstring names: the refusal is now a durable coverage-gap signal, which
	// travels onto the snapshot and downgrades its completeness to partial
	// (ADR 0023 decision 5). The `scanned` boolean stays as it was -- it is too
	// coarse to carry "one root was empty and three were refused", which is why
	// the tally exists. The invariant being asserted is unchanged: an empty
	// inventory must never be reported without saying what could not be read.
	if scanned && empty && input.CoverageGaps.Total() == 0 {
		t.Fatalf("refused-by-allowlist symlink roots produced scanned=true with a completely empty inventory " +
			"and no coverage gap: a refused root is indistinguishable from an empty one, which is a fabricated zero")
	}
	if empty && input.CoverageGaps[adaptersdk.CoverageGapUnresolvableSymlink] == 0 {
		t.Fatalf("refused symlink roots must be classified unresolvable_symlink; gaps=%v", input.CoverageGaps)
	}
}

// TestAudit20260801RealClaudeStateRootYieldsSkills scans the operator's real
// Claude Code state root read-only and asserts that a host with plugins
// installed yields a non-empty inventory. It is env-gated so the normal
// `go test ./...` sweep never touches a real home directory.
//
//	KANSOKU_AUDIT_REAL_CLAUDE_ROOT=$HOME/.claude \
//	  go test ./internal/claudeadapter/ -run TestAudit20260801RealClaudeStateRootYieldsSkills -v
func TestAudit20260801RealClaudeStateRootYieldsSkills(t *testing.T) {
	root := os.Getenv("KANSOKU_AUDIT_REAL_CLAUDE_ROOT")
	if root == "" {
		t.Skip("set KANSOKU_AUDIT_REAL_CLAUDE_ROOT to a real Claude Code state root to run this audit probe")
	}
	host := auditHostView(t, root)
	input, scanned := claudeadapter.ScanHostInventory(host, adaptersdk.Installation{
		InstallationID: "ain_00000000000000000000000000000001",
		AdapterID:      claudeadapter.AdapterID,
		SurfaceID:      "cli",
		StateRoot:      root,
	})
	t.Logf("scanned=%v plugins=%d marketplaces=%d standalone_skills=%d bundled_skills=%d mcp_servers=%d",
		scanned, len(input.Plugins), len(input.Marketplaces),
		len(input.StandaloneSkills), countBundledSkills(input), len(input.StandaloneMCPServers))
	for _, plugin := range input.Plugins {
		t.Logf("  plugin %-44s version=%-14s cached_only=%v bundled_skills=%d",
			plugin.Name, plugin.Version, plugin.CachedOnly, len(plugin.BundledSkills))
	}
	for _, skill := range input.StandaloneSkills {
		t.Logf("  standalone skill %s scope=%s", skill.Name, skill.Scope)
	}
	if countBundledSkills(input)+len(input.StandaloneSkills) == 0 {
		t.Fatalf("a real Claude Code state root with installed plugins yielded zero skills")
	}
}
