package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// fakeSoakDriver is the deterministic in-memory mechanism proof named by
// SoakDriver's own doc comment: useful for exercising RunAcceleratedSoakWithDriver's
// cycle/fault/assertion wiring, but never claimed as production soak evidence
// because its DriverKind says so plainly.
type fakeSoakDriver struct {
	failIngestOnCycle     int
	failQueryOnCycle      int
	failBackupOnCycle     int
	failFaultOn           SoakFault
	failRecover           bool
	failDurability        bool
	failSnapshot          bool
	duplicateEveryNCycles int

	faultsSeen []SoakFault
	closed     bool
}

func (f *fakeSoakDriver) DriverKind() string { return "fake_in_memory_deterministic" }

func (f *fakeSoakDriver) Ingest(_ context.Context, cycle int) (string, error) {
	if cycle == f.failIngestOnCycle {
		return "", errors.New("fake_ingest_failed")
	}
	if f.duplicateEveryNCycles > 0 && cycle%f.duplicateEveryNCycles == 0 {
		return "evt_0001", nil
	}
	return "evt_" + itoa4(cycle), nil
}

func (f *fakeSoakDriver) QueryRollup(_ context.Context, cycle int) error {
	if cycle == f.failQueryOnCycle {
		return errors.New("fake_query_failed")
	}
	return nil
}

func (f *fakeSoakDriver) BackupCountSnapshot(_ context.Context, cycle int) error {
	if cycle == f.failBackupOnCycle {
		return errors.New("fake_backup_failed")
	}
	return nil
}

func (f *fakeSoakDriver) ExecuteFault(_ context.Context, fault SoakFault) error {
	if fault == f.failFaultOn {
		return errors.New("fake_fault_failed")
	}
	f.faultsSeen = append(f.faultsSeen, fault)
	return nil
}

func (f *fakeSoakDriver) Recover(context.Context) error {
	if f.failRecover {
		return errors.New("fake_recover_failed")
	}
	return nil
}

func (f *fakeSoakDriver) IsDurable(context.Context, string) (bool, error) {
	if f.failDurability {
		return false, errors.New("fake_durability_probe_failed")
	}
	return true, nil
}

func (f *fakeSoakDriver) Snapshot(context.Context) (SoakSnapshot, error) {
	if f.failSnapshot {
		return SoakSnapshot{}, errors.New("fake_snapshot_failed")
	}
	uniqueCount := SoakLogicalDays * SoakCyclesPerDay
	replayCount := 0
	if f.duplicateEveryNCycles > 0 {
		duplicates := uniqueCount / f.duplicateEveryNCycles
		uniqueCount -= duplicates
		replayCount = duplicates
	}
	return SoakSnapshot{
		FactCount: int64(uniqueCount), EvidenceReplayCount: int64(replayCount),
		SpoolDepth: 0, NonTerminalJobs: 0, BackupCountsMatch: true, DiagnosticsSafe: true,
	}, nil
}

func (f *fakeSoakDriver) Close(context.Context) error {
	f.closed = true
	return nil
}

func itoa4(value int) string {
	digits := [4]byte{}
	for i := 3; i >= 0; i-- {
		digits[i] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[:])
}

func fixedSoakClock(times ...time.Time) func() time.Time {
	calls := 0
	return func() time.Time {
		if calls >= len(times) {
			return times[len(times)-1]
		}
		value := times[calls]
		calls++
		return value
	}
}

func goldenSoakEvidenceBytes(t *testing.T, evidencePath string) []byte {
	t.Helper()
	driver := &fakeSoakDriver{}
	clock := fixedSoakClock(
		time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
	)
	if _, err := RunAcceleratedSoakWithDriver(context.Background(), driver, evidencePath, clock); err != nil {
		t.Fatalf("RunAcceleratedSoakWithDriver() error = %v, want nil", err)
	}
	raw, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", evidencePath, err)
	}
	return raw
}

func TestRunAcceleratedSoakWithDriverPassesAndWritesEvidence(t *testing.T) {
	driver := &fakeSoakDriver{}
	evidencePath := filepath.Join(t.TempDir(), "accelerated-soak.json")
	clock := fixedSoakClock(
		time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 31, 0, 0, 0, 0, time.UTC),
	)
	evidence, err := RunAcceleratedSoakWithDriver(context.Background(), driver, evidencePath, clock)
	if err != nil {
		t.Fatalf("RunAcceleratedSoakWithDriver() error = %v, want nil", err)
	}
	if evidence.Status != "pass" {
		t.Fatalf("Status = %q, want pass", evidence.Status)
	}
	if evidence.CyclesCompleted != SoakLogicalDays*SoakCyclesPerDay {
		t.Fatalf("CyclesCompleted = %d, want %d", evidence.CyclesCompleted, SoakLogicalDays*SoakCyclesPerDay)
	}
	wantFaults := []SoakFault{SoakProcessRestart, SoakDatabaseRestart, SoakUpgradeBoundary}
	sort.Slice(wantFaults, func(i, j int) bool { return wantFaults[i] < wantFaults[j] })
	if len(evidence.FaultsExecuted) != len(wantFaults) {
		t.Fatalf("FaultsExecuted = %v, want %v", evidence.FaultsExecuted, wantFaults)
	}
	for i, fault := range wantFaults {
		if evidence.FaultsExecuted[i] != fault {
			t.Fatalf("FaultsExecuted[%d] = %q, want %q", i, evidence.FaultsExecuted[i], fault)
		}
	}
	if evidence.AcknowledgedCount != SoakLogicalDays*SoakCyclesPerDay || evidence.UniqueEventCount != SoakLogicalDays*SoakCyclesPerDay {
		t.Fatalf("AcknowledgedCount/UniqueEventCount = %d/%d, want %d/%d",
			evidence.AcknowledgedCount, evidence.UniqueEventCount, SoakLogicalDays*SoakCyclesPerDay, SoakLogicalDays*SoakCyclesPerDay)
	}
	for name, passed := range evidence.Assertions {
		if !passed {
			t.Fatalf("assertion %q did not pass", name)
		}
	}
	if evidence.WallClockSevenDays {
		t.Fatal("WallClockSevenDays must stay false: this is a logical-cycle harness, not a literal seven-day wall-clock claim")
	}
	if !driver.closed {
		t.Fatal("driver was not closed")
	}
	raw, err := os.ReadFile(evidencePath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", evidencePath, err)
	}
	var persisted SoakEvidence
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("Unmarshal persisted evidence: %v", err)
	}
	if persisted.Status != "pass" || persisted.DriverKind != driver.DriverKind() {
		t.Fatalf("persisted evidence mismatch: %+v", persisted)
	}
}

func TestRunAcceleratedSoakWithDriverRejectsInvalidConfiguration(t *testing.T) {
	validPath := filepath.Join(t.TempDir(), "accelerated-soak.json")
	cases := map[string]struct {
		driver SoakDriver
		path   string
	}{
		"nil driver":        {driver: nil, path: validPath},
		"empty driver kind": {driver: &unnamedFakeDriver{fakeSoakDriver: &fakeSoakDriver{}}, path: validPath},
		"relative path":     {driver: &namedFakeDriver{fakeSoakDriver: &fakeSoakDriver{}}, path: "accelerated-soak.json"},
		"root path":         {driver: &namedFakeDriver{fakeSoakDriver: &fakeSoakDriver{}}, path: "/"},
	}
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := RunAcceleratedSoakWithDriver(context.Background(), testCase.driver, testCase.path, nil)
			if err == nil || err.Error() != "invalid_soak_configuration" {
				t.Fatalf("error = %v, want invalid_soak_configuration", err)
			}
		})
	}
}

// unnamedFakeDriver overrides DriverKind to genuinely return "", isolating the
// driver-kind validation branch of RunAcceleratedSoakWithDriver from path
// validation.
type unnamedFakeDriver struct{ *fakeSoakDriver }

func (u *unnamedFakeDriver) DriverKind() string { return "" }

// namedFakeDriver gives fakeSoakDriver a non-empty DriverKind so the path
// validation branch of RunAcceleratedSoakWithDriver is exercised in
// isolation from the driver-kind check.
type namedFakeDriver struct{ *fakeSoakDriver }

func (n *namedFakeDriver) DriverKind() string { return "fake_named" }

func TestRunAcceleratedSoakWithDriverPropagatesConcurrentCycleFailure(t *testing.T) {
	cases := map[string]*fakeSoakDriver{
		"ingest fails": {failIngestOnCycle: 10},
		"query fails":  {failQueryOnCycle: 20},
		"backup fails": {failBackupOnCycle: 30},
	}
	for name, driver := range cases {
		t.Run(name, func(t *testing.T) {
			evidencePath := filepath.Join(t.TempDir(), "accelerated-soak.json")
			_, err := RunAcceleratedSoakWithDriver(context.Background(), driver, evidencePath, nil)
			if err == nil || err.Error() != "soak_concurrent_cycle_failed" {
				t.Fatalf("error = %v, want soak_concurrent_cycle_failed", err)
			}
		})
	}
}

func TestRunAcceleratedSoakWithDriverPropagatesFaultAndRecoveryFailure(t *testing.T) {
	t.Run("fault execution fails", func(t *testing.T) {
		driver := &fakeSoakDriver{failFaultOn: SoakProcessRestart}
		evidencePath := filepath.Join(t.TempDir(), "accelerated-soak.json")
		_, err := RunAcceleratedSoakWithDriver(context.Background(), driver, evidencePath, nil)
		if err == nil || err.Error() != "soak_fault_execution_failed" {
			t.Fatalf("error = %v, want soak_fault_execution_failed", err)
		}
	})
	t.Run("recovery fails", func(t *testing.T) {
		driver := &fakeSoakDriver{failRecover: true}
		evidencePath := filepath.Join(t.TempDir(), "accelerated-soak.json")
		_, err := RunAcceleratedSoakWithDriver(context.Background(), driver, evidencePath, nil)
		if err == nil || err.Error() != "soak_recovery_failed" {
			t.Fatalf("error = %v, want soak_recovery_failed", err)
		}
	})
}

func TestRunAcceleratedSoakWithDriverDetectsInflationAndDuplicates(t *testing.T) {
	driver := &fakeSoakDriver{duplicateEveryNCycles: 7}
	evidencePath := filepath.Join(t.TempDir(), "accelerated-soak.json")
	evidence, err := RunAcceleratedSoakWithDriver(context.Background(), driver, evidencePath, nil)
	if err != nil {
		t.Fatalf("RunAcceleratedSoakWithDriver() error = %v, want nil", err)
	}
	if evidence.AcknowledgedCount == evidence.UniqueEventCount {
		t.Fatal("expected duplicate acknowledgements to be reflected as unique < acknowledged")
	}
	if !evidence.Assertions["replay_count_tracks_duplicates"] {
		t.Fatal("replay_count_tracks_duplicates assertion should still pass when the driver's snapshot agrees with the ledger")
	}
}

// TestAcceleratedSoakFixtureIsLiveNotStale re-derives the bundled
// tests/fixtures/session-09/accelerated-soak.json byte-for-byte from the same
// fake-driver mechanism this file tests elsewhere. This fixture is a
// deterministic in-memory proof of RunAcceleratedSoakWithDriver's cycle/
// fault/assertion/persistence wiring -- not a real Docker Compose seven-day
// soak -- and DriverKind records that honestly (see reports/session-09-
// reconciliation.md).
func TestAcceleratedSoakFixtureIsLiveNotStale(t *testing.T) {
	fixturePath := filepath.Join("..", "..", "tests", "fixtures", "session-09", "accelerated-soak.json")
	want, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", fixturePath, err)
	}
	got := goldenSoakEvidenceBytes(t, filepath.Join(t.TempDir(), "accelerated-soak.json"))
	if string(got) != string(want) {
		t.Fatalf("bundled fixture is stale relative to RunAcceleratedSoakWithDriver's current output:\ngot:  %s\nwant: %s", got, want)
	}
	var evidence SoakEvidence
	if err := json.Unmarshal(want, &evidence); err != nil {
		t.Fatalf("Unmarshal fixture: %v", err)
	}
	if evidence.SchemaVersion != SoakEvidenceVersion || evidence.LogicalDays != SoakLogicalDays ||
		evidence.CyclesPerDay != SoakCyclesPerDay || evidence.WallClockSevenDays {
		t.Fatalf("fixture schema invariants changed: %+v", evidence)
	}
	wantFaultSet := map[SoakFault]bool{SoakProcessRestart: true, SoakDatabaseRestart: true, SoakUpgradeBoundary: true}
	if len(evidence.FaultsExecuted) != len(wantFaultSet) {
		t.Fatalf("fixture fault set changed: %v", evidence.FaultsExecuted)
	}
	for _, fault := range evidence.FaultsExecuted {
		if !wantFaultSet[fault] {
			t.Fatalf("fixture contains unexpected fault: %v", fault)
		}
	}
}
