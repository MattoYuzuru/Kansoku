package integrity_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
	"kansoku.local/kansoku/internal/adaptersdk/fakeadapter"
	"kansoku.local/kansoku/internal/integrity"
)

func newTestHostView(t *testing.T, roots []string) *adaptersdk.HostView {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	host, err := adaptersdk.NewHostView(roots, []string{"loomctl"}, key)
	if err != nil {
		t.Fatalf("NewHostView: %v", err)
	}
	host.SetExecCommandForTest(func(ctx context.Context, name string, args ...string) ([]byte, int, error) {
		return []byte("loomctl 0.9.0\n"), 0, nil
	})
	return host
}

func registryWithLoomwright(t *testing.T) *adaptersdk.Registry {
	t.Helper()
	registry := adaptersdk.NewRegistry()
	if err := registry.Register(fakeadapter.New()); err != nil {
		t.Fatalf("register fakeadapter: %v", err)
	}
	return registry
}

// TestDiscoveryConfigCheckDrivesAdaptersGenerically proves the Session 08
// discovery/config check iterates Registry.IDs()/Get() and calls Discover/
// Inventory through the standard Adapter interface -- never a hardcoded
// "codex"/"claude" branch -- by driving it entirely against the fictional
// Loomwright conformance adapter.
func TestDiscoveryConfigCheckDrivesAdaptersGenerically(t *testing.T) {
	root := t.TempDir()
	if err := writeLoomsMarker(root); err != nil {
		t.Fatalf("seed looms marker: %v", err)
	}
	host := newTestHostView(t, []string{root})
	registry := registryWithLoomwright(t)

	installation := adaptersdk.Installation{
		InstallationID: "install-1", AdapterID: fakeadapter.AdapterID,
		SurfaceID: "loomctl-cli", StateRoot: root,
	}
	installations := func(ctx context.Context, adapterID string) ([]adaptersdk.Installation, error) {
		if adapterID != fakeadapter.AdapterID {
			return nil, nil
		}
		return []adaptersdk.Installation{installation}, nil
	}

	check := integrity.NewDiscoveryConfigCheck(registry, host, installations, nil)
	if check.StageID() != integrity.Stage1DiscoveryAndConfiguration {
		t.Fatalf("StageID = %s, want stage_1_discovery_and_configuration", check.StageID())
	}

	now := time.Date(2026, 7, 23, 9, 0, 0, 0, time.UTC)
	in := integrity.CheckInput{AuditRunID: "run_1", Mode: integrity.RunModeFull, Now: now}
	targets, err := check.Targets(context.Background(), in)
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(targets) != 1 || targets[0].InstallationID != "install-1" {
		t.Fatalf("Targets = %+v, want exactly one target for install-1", targets)
	}

	outcome, err := check.Evaluate(context.Background(), in, targets[0])
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcome.Status != integrity.CheckStatusPass {
		t.Fatalf("outcome = %+v, want pass (executable resolvable, state root readable, inventory completes)", outcome)
	}
	if outcome.CheckID != integrity.DiscoveryConfigCheckID {
		t.Fatalf("CheckID = %s, want %s", outcome.CheckID, integrity.DiscoveryConfigCheckID)
	}
}

// TestDiscoveryConfigCheckDetectsUnreadableStateRoot proves a state root that
// has vanished (or fallen outside the allowed roots) fails the check with a
// permission_denied category rather than silently passing.
func TestDiscoveryConfigCheckDetectsUnreadableStateRoot(t *testing.T) {
	root := t.TempDir()
	if err := writeLoomsMarker(root); err != nil {
		t.Fatalf("seed looms marker: %v", err)
	}
	host := newTestHostView(t, []string{root})
	registry := registryWithLoomwright(t)

	// Installation claims a state root that was never allowed, simulating a
	// removed/relocated install.
	installation := adaptersdk.Installation{
		InstallationID: "install-missing", AdapterID: fakeadapter.AdapterID,
		SurfaceID: "loomctl-cli", StateRoot: root + "/does-not-exist",
	}
	installations := func(ctx context.Context, adapterID string) ([]adaptersdk.Installation, error) {
		return []adaptersdk.Installation{installation}, nil
	}
	check := integrity.NewDiscoveryConfigCheck(registry, host, installations, nil)
	in := integrity.CheckInput{AuditRunID: "run_1", Now: time.Now()}
	outcome, err := check.Evaluate(context.Background(), in, integrity.CheckTarget{InstallationID: "install-missing"})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if outcome.Status != integrity.CheckStatusFail {
		t.Fatalf("outcome = %+v, want fail for unreadable state root", outcome)
	}
	if outcome.Category != string(integrity.FailureClassPermissionDenied) {
		t.Fatalf("Category = %s, want %s", outcome.Category, integrity.FailureClassPermissionDenied)
	}
}

// TestDiscoveryConfigCheckDistinguishesDriftFromReappliedPlan proves a
// config-fingerprint mismatch is distinguished from a legitimate re-applied
// plan: an installation with NO applied plan on record yet (never
// configured) is not treated as drift, while one whose AppliedPlanFingerprint
// lookup errors is a genuine failure.
func TestDiscoveryConfigCheckDistinguishesDriftFromReappliedPlan(t *testing.T) {
	root := t.TempDir()
	if err := writeLoomsMarker(root); err != nil {
		t.Fatalf("seed looms marker: %v", err)
	}
	host := newTestHostView(t, []string{root})
	registry := registryWithLoomwright(t)
	installation := adaptersdk.Installation{
		InstallationID: "install-cfg", AdapterID: fakeadapter.AdapterID,
		SurfaceID: "loomctl-cli", StateRoot: root,
	}
	installations := func(ctx context.Context, adapterID string) ([]adaptersdk.Installation, error) {
		return []adaptersdk.Installation{installation}, nil
	}

	t.Run("never_configured_is_not_drift", func(t *testing.T) {
		// known=false: the installation has never had a plan applied. This
		// must never be reported as configuration drift.
		appliedPlan := func(ctx context.Context, installationID string, capability adaptersdk.CapabilityID) (string, bool, error) {
			return "", false, nil
		}
		check := integrity.NewDiscoveryConfigCheck(registry, host, installations, appliedPlan)
		in := integrity.CheckInput{AuditRunID: "run_1", Now: time.Now()}
		outcome, err := check.Evaluate(context.Background(), in, integrity.CheckTarget{InstallationID: "install-cfg"})
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if outcome.Status != integrity.CheckStatusPass {
			t.Fatalf("outcome = %+v, want pass: unconfigured installation is not drift", outcome)
		}
	})

	t.Run("legitimately_reapplied_plan_is_not_drift", func(t *testing.T) {
		// known=true with a genuine, non-empty fingerprint on record: a
		// legitimately re-applied plan (this stage's own applied-plan
		// lookup reflects the latest approved plan, so there is nothing to
		// disagree with).
		appliedPlan := func(ctx context.Context, installationID string, capability adaptersdk.CapabilityID) (string, bool, error) {
			return "sha256:deadbeef", true, nil
		}
		check := integrity.NewDiscoveryConfigCheck(registry, host, installations, appliedPlan)
		check.SetObservedPlanFingerprint(appliedPlan)
		in := integrity.CheckInput{AuditRunID: "run_1", Now: time.Now()}
		outcome, err := check.Evaluate(context.Background(), in, integrity.CheckTarget{InstallationID: "install-cfg"})
		if err != nil {
			t.Fatalf("Evaluate: %v", err)
		}
		if outcome.Status != integrity.CheckStatusPass {
			t.Fatalf("outcome = %+v, want pass: legitimately re-applied plan is on record, not drift", outcome)
		}
	})

	t.Run("observed_fingerprint_mismatch_is_drift", func(t *testing.T) {
		appliedPlan := func(ctx context.Context, installationID string, capability adaptersdk.CapabilityID) (string, bool, error) {
			return "sha256:approved", true, nil
		}
		observedPlan := func(ctx context.Context, installationID string, capability adaptersdk.CapabilityID) (string, bool, error) {
			return "sha256:changed", true, nil
		}
		check := integrity.NewDiscoveryConfigCheck(registry, host, installations, appliedPlan)
		check.SetObservedPlanFingerprint(observedPlan)
		outcome, err := check.Evaluate(context.Background(), integrity.CheckInput{Now: time.Now()}, integrity.CheckTarget{InstallationID: "install-cfg"})
		if err != nil || outcome.Status != integrity.CheckStatusFail || outcome.DetailRef == "" {
			t.Fatalf("outcome=%+v err=%v, want visible config drift", outcome, err)
		}
	})

	t.Run("lookup_error_is_a_genuine_failure", func(t *testing.T) {
		// A broken applied-plan lookup is itself a real audit finding (the
		// check could not confirm no-drift), not an infrastructure crash: it
		// must surface as a failed CheckOutcome with a specific category,
		// never a fabricated pass and never an opaque Go error that would
		// get bucketed into the scheduler's generic stage_timeout category.
		appliedPlan := func(ctx context.Context, installationID string, capability adaptersdk.CapabilityID) (string, bool, error) {
			return "", false, errFixtureLookup
		}
		check := integrity.NewDiscoveryConfigCheck(registry, host, installations, appliedPlan)
		in := integrity.CheckInput{AuditRunID: "run_1", Now: time.Now()}
		outcome, err := check.Evaluate(context.Background(), in, integrity.CheckTarget{InstallationID: "install-cfg"})
		if err != nil {
			t.Fatalf("Evaluate: unexpected Go error %v, want a failed CheckOutcome instead", err)
		}
		if outcome.Status != integrity.CheckStatusFail {
			t.Fatalf("outcome = %+v, want fail: a broken applied-plan lookup must not be silently treated as no-drift", outcome)
		}
	})
}

var errFixtureLookup = errors.New("fixture_applied_plan_lookup_failure")

func writeLoomsMarker(root string) error {
	return os.WriteFile(root+"/looms", []byte("marker"), 0o600)
}

// TestDiscoveryConfigCheckEmptyRegistryProducesZeroTargets proves an empty
// registry (or nil) never fabricates a placeholder target.
func TestDiscoveryConfigCheckEmptyRegistryProducesZeroTargets(t *testing.T) {
	check := integrity.NewDiscoveryConfigCheck(adaptersdk.NewRegistry(), nil, nil, nil)
	targets, err := check.Targets(context.Background(), integrity.CheckInput{})
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("Targets = %+v, want empty for a registry with no adapters", targets)
	}
}
