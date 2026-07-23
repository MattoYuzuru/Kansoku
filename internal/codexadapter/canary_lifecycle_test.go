package codexadapter_test

import (
	"os"
	"path/filepath"
)

// This file implements the canary fixture project's "separately controlled
// temp lifecycle" requirement from
// tests/fixtures/session-06/canary/kansoku-canary-scenario.json's
// execution_constraints.generated_workspace_lifecycle: the canary workspace
// directory is created and destroyed by a lifecycle distinct from the
// canary run itself, never implicitly by the run being measured (and,
// deliberately, not solely by Go's t.TempDir() automatic per-test cleanup --
// canaryWorkspace.Destroy is an explicit, separately callable step so a test
// can assert the workspace still exists immediately after a canary "run"
// completes, and only vanishes once the distinct lifecycle step runs it).

// canaryFixtureRoot is the checked-in, read-only canary fixture project this
// package's tests copy from. It is never itself mutated by a test.
const canaryFixtureRoot = "../../tests/fixtures/session-06/canary/kansoku-canary-fixture-project"

// canaryWorkspace is one materialized, disposable copy of the canary fixture
// project. Its lifecycle (newCanaryWorkspace / Destroy) is intentionally
// distinct from any canary "run" helper operating on it: a run only ever
// reads/writes inside Root, and never deletes Root itself.
type canaryWorkspace struct {
	Root string
}

// newCanaryWorkspace creates a fresh, isolated copy of the checked-in canary
// fixture project beneath a new OS temp directory (never inside the
// repository, and never a real user repository). The caller owns calling
// Destroy separately; this constructor does not register any automatic
// cleanup itself, matching the "generated workspace is deleted through a
// separately controlled temp lifecycle" constraint precisely: cleanup is an
// explicit, distinct action, not an implicit side effect of workspace
// creation or of running the canary task.
//
// The materialized workspace nests the copy under the same
// "kansoku-canary-fixture-project" directory name the checked-in fixture
// uses (Root/kansoku-canary-fixture-project/...), matching every consumer's
// expectation (canary_chain_test.go) that Root is a lifecycle-controlled
// container one level above the actual fixture project, not the project
// directory itself.
func newCanaryWorkspace() (*canaryWorkspace, error) {
	root, err := os.MkdirTemp("", "kansoku-codex-canary-*")
	if err != nil {
		return nil, err
	}
	projectDst := filepath.Join(root, filepath.Base(canaryFixtureRoot))
	if err := os.MkdirAll(projectDst, 0o700); err != nil {
		os.RemoveAll(root)
		return nil, err
	}
	if err := copyFixtureTree(canaryFixtureRoot, projectDst); err != nil {
		os.RemoveAll(root)
		return nil, err
	}
	return &canaryWorkspace{Root: root}, nil
}

// Destroy removes the entire materialized workspace. It is the only
// deletion path for a canaryWorkspace's Root: no canary run helper in this
// package ever calls this itself, so a workspace always outlives the run
// that used it until this distinct step executes.
func (w *canaryWorkspace) Destroy() error {
	if w == nil || w.Root == "" {
		return nil
	}
	return os.RemoveAll(w.Root)
}

func copyFixtureTree(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, 0o700); err != nil {
				return err
			}
			if err := copyFixtureTree(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		data, err := os.ReadFile(srcPath)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dstPath, data, 0o600); err != nil {
			return err
		}
	}
	return nil
}
