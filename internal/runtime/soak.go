package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	SoakEvidenceVersion = "kansoku.accelerated-soak-evidence/1"
	SoakLogicalDays     = 7
	SoakCyclesPerDay    = 24
)

type SoakFault string

const (
	SoakProcessRestart  SoakFault = "process_restart"
	SoakDatabaseRestart SoakFault = "database_restart"
	SoakUpgradeBoundary SoakFault = "stop_the_world_upgrade_boundary"
)

type SoakSnapshot struct {
	FactCount           int64
	EvidenceReplayCount int64
	SpoolDepth          int64
	NonTerminalJobs     int64
	BackupCountsMatch   bool
	DiagnosticsSafe     bool
}

// SoakDriver is deliberately evidence-oriented: fault methods must return
// only after the named real fault and recovery have completed. A fake driver
// is useful for deterministic unit tests but cannot produce production soak
// evidence because DriverKind is recorded in the signed-off artifact.
type SoakDriver interface {
	DriverKind() string
	Ingest(context.Context, int) (string, error)
	QueryRollup(context.Context, int) error
	BackupCountSnapshot(context.Context, int) error
	ExecuteFault(context.Context, SoakFault) error
	Recover(context.Context) error
	IsDurable(context.Context, string) (bool, error)
	Snapshot(context.Context) (SoakSnapshot, error)
	Close(context.Context) error
}

type SoakEvidence struct {
	SchemaVersion      string          `json:"schema_version"`
	Status             string          `json:"status"`
	DriverKind         string          `json:"driver_kind"`
	LogicalDays        int             `json:"logical_days"`
	CyclesPerDay       int             `json:"cycles_per_day"`
	CyclesCompleted    int             `json:"cycles_completed"`
	WallClockSevenDays bool            `json:"wall_clock_seven_day_claim"`
	FaultsExecuted     []SoakFault     `json:"faults_executed"`
	AcknowledgedCount  int             `json:"acknowledged_count"`
	UniqueEventCount   int             `json:"unique_event_count"`
	FinalSnapshot      SoakSnapshot    `json:"final_snapshot"`
	Assertions         map[string]bool `json:"assertions"`
	StartedAt          time.Time       `json:"started_at"`
	FinishedAt         time.Time       `json:"finished_at"`
}

// RunAcceleratedSoak refuses to invent a database-restart result from inside
// the appliance. The Docker integration harness supplies a real SoakDriver
// through RunAcceleratedSoakWithDriver.
func RunAcceleratedSoak(context.Context, Config, Secrets, string) error {
	return errors.New("real_docker_soak_driver_required")
}

func RunAcceleratedSoakWithDriver(ctx context.Context, driver SoakDriver, evidencePath string, now func() time.Time) (SoakEvidence, error) {
	if driver == nil || driver.DriverKind() == "" || !filepath.IsAbs(evidencePath) || evidencePath == "/" {
		return SoakEvidence{}, errors.New("invalid_soak_configuration")
	}
	if now == nil {
		now = time.Now
	}
	evidence := SoakEvidence{
		SchemaVersion: SoakEvidenceVersion, Status: "running",
		DriverKind: driver.DriverKind(), LogicalDays: SoakLogicalDays,
		CyclesPerDay: SoakCyclesPerDay, WallClockSevenDays: false,
		StartedAt: now().UTC(), Assertions: map[string]bool{},
	}
	defer func() { _ = driver.Close(context.WithoutCancel(ctx)) }()
	var ledger []string
	faultAt := map[int]SoakFault{
		48: SoakProcessRestart, 96: SoakDatabaseRestart, 144: SoakUpgradeBoundary,
	}
	totalCycles := SoakLogicalDays * SoakCyclesPerDay
	for cycle := 1; cycle <= totalCycles; cycle++ {
		var wg sync.WaitGroup
		var ingestID string
		var ingestErr, queryErr, backupErr error
		wg.Add(3)
		go func() {
			defer wg.Done()
			ingestID, ingestErr = driver.Ingest(ctx, cycle)
		}()
		go func() {
			defer wg.Done()
			queryErr = driver.QueryRollup(ctx, cycle)
		}()
		go func() {
			defer wg.Done()
			backupErr = driver.BackupCountSnapshot(ctx, cycle)
		}()
		wg.Wait()
		if ingestErr != nil || queryErr != nil || backupErr != nil || ingestID == "" {
			return SoakEvidence{}, errors.New("soak_concurrent_cycle_failed")
		}
		ledger = append(ledger, ingestID)
		evidence.CyclesCompleted = cycle
		if fault, ok := faultAt[cycle]; ok {
			if err := driver.ExecuteFault(ctx, fault); err != nil {
				return SoakEvidence{}, errors.New("soak_fault_execution_failed")
			}
			evidence.FaultsExecuted = append(evidence.FaultsExecuted, fault)
			if err := driver.Recover(ctx); err != nil {
				return SoakEvidence{}, errors.New("soak_recovery_failed")
			}
		}
	}
	unique := map[string]bool{}
	allDurable := true
	for _, acknowledged := range ledger {
		unique[acknowledged] = true
		durable, err := driver.IsDurable(ctx, acknowledged)
		if err != nil {
			return SoakEvidence{}, errors.New("soak_durability_probe_failed")
		}
		allDurable = allDurable && durable
	}
	snapshot, err := driver.Snapshot(ctx)
	if err != nil {
		return SoakEvidence{}, errors.New("soak_final_snapshot_failed")
	}
	faults := append([]SoakFault(nil), evidence.FaultsExecuted...)
	sort.Slice(faults, func(i, j int) bool { return faults[i] < faults[j] })
	evidence.FaultsExecuted = faults
	evidence.AcknowledgedCount = len(ledger)
	evidence.UniqueEventCount = len(unique)
	evidence.FinalSnapshot = snapshot
	evidence.Assertions = map[string]bool{
		"acknowledged_equals_durable_or_spooled": allDurable,
		"event_fact_count_no_inflation":          snapshot.FactCount == int64(len(unique)),
		"replay_count_tracks_duplicates":         snapshot.EvidenceReplayCount == int64(len(ledger)-len(unique)),
		"backup_counts_match_source_snapshot":    snapshot.BackupCountsMatch,
		"all_spools_empty_after_recovery":        snapshot.SpoolDepth == 0,
		"all_jobs_terminal":                      snapshot.NonTerminalJobs == 0,
		"no_prohibited_diagnostics_fields":       snapshot.DiagnosticsSafe,
	}
	for _, passed := range evidence.Assertions {
		if !passed {
			return SoakEvidence{}, errors.New("soak_assertion_failed")
		}
	}
	evidence.Status = "pass"
	evidence.FinishedAt = now().UTC()
	if err := writeSoakEvidence(evidencePath, evidence); err != nil {
		return SoakEvidence{}, err
	}
	return evidence, nil
}

func writeSoakEvidence(path string, evidence SoakEvidence) error {
	if err := ensurePrivateDirectory(filepath.Dir(path)); err != nil {
		return err
	}
	encoded, err := json.Marshal(evidence)
	if err != nil || len(encoded) > 1<<20 {
		return errors.New("soak_evidence_encode_failed")
	}
	temporary := path + ".tmp"
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.New("soak_evidence_create_failed")
	}
	writeErr := error(nil)
	if _, err := file.Write(append(encoded, '\n')); err != nil {
		writeErr = err
	}
	if err := file.Sync(); writeErr == nil && err != nil {
		writeErr = err
	}
	if err := file.Close(); writeErr == nil && err != nil {
		writeErr = err
	}
	if writeErr != nil {
		_ = os.Remove(temporary)
		return errors.New("soak_evidence_write_failed")
	}
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return errors.New("soak_evidence_publish_failed")
	}
	return nil
}
