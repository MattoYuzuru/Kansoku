package integrity

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kansoku.local/kansoku/internal/adaptersdk"
)

// DailyScheduleConfig configures the scheduled_daily trigger's local
// time-of-day plus bounded jitter, matching audit-run-and-schedule.yaml's
// triggers[0].description: "configurable local time of day plus bounded
// random jitter, full 11-stage run".
type DailyScheduleConfig struct {
	// LocalHour/LocalMinute is the target local time of day (0-23, 0-59).
	LocalHour   int
	LocalMinute int
	// MaxJitter bounds the random delay added after LocalHour:LocalMinute is
	// reached, so many installations do not all fire at exactly the same
	// instant.
	MaxJitter time.Duration
	// Location is the local timezone the schedule is evaluated in; a nil
	// Location defaults to time.Local.
	Location *time.Location
}

// NextScheduledDailyRun returns the next scheduled_daily fire time strictly
// after after, applying MaxJitter deterministically-once (the jitter for a
// given calendar day is stable across repeated calls with the same after
// falling on the same day, since callers are expected to call this once
// per day when deciding the next tick, not on every poll).
func (c DailyScheduleConfig) NextScheduledDailyRun(after time.Time) time.Time {
	loc := c.Location
	if loc == nil {
		loc = time.Local
	}
	after = after.In(loc)
	candidate := time.Date(after.Year(), after.Month(), after.Day(), c.LocalHour, c.LocalMinute, 0, 0, loc)
	if !candidate.After(after) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	return candidate.Add(randomJitter(c.MaxJitter))
}

// Coordinator ties a Scheduler to the three automatic triggers this
// session's contract declares (scheduled_daily, startup,
// version_change_detected); manual_operator_request is expected to be
// invoked directly via Scheduler.StartRun by whatever operator-facing
// surface a later session builds (e.g. a CLI or dashboard action), so it
// has no Coordinator method of its own here.
type Coordinator struct {
	scheduler           *Scheduler
	registry            *adaptersdk.Registry
	dailyConfig         DailyScheduleConfig
	lastVersions        AdapterVersionSnapshot
	fingerprintPool     *pgxpool.Pool
	fingerprintProvider FingerprintProvider
}

// FingerprintProvider observes the complete metadata-only drift identity set
// for executable, config recipe, adapter, fixture, formula registry and event
// schema inputs. Production wiring supplies one provider; tests may supply a
// deterministic fixture provider.
type FingerprintProvider func(ctx context.Context, now time.Time) ([]DriftFingerprint, error)

// NewCoordinator constructs a Coordinator over an already-built Scheduler
// and the same adaptersdk.Registry the rest of the process uses for
// adapter dispatch (never a second registry instance).
func NewCoordinator(scheduler *Scheduler, registry *adaptersdk.Registry, dailyConfig DailyScheduleConfig) *Coordinator {
	return &Coordinator{scheduler: scheduler, registry: registry, dailyConfig: dailyConfig}
}

func (c *Coordinator) ConfigureFingerprintWatch(pool *pgxpool.Pool, provider FingerprintProvider) error {
	if pool == nil || provider == nil {
		return errors.New("fingerprint watch requires PostgreSQL pool and provider")
	}
	c.fingerprintPool = pool
	c.fingerprintProvider = provider
	return nil
}

// RunOnStartup fires the startup trigger's reduced-mode run once, matching
// triggers[1].description: "runs once shortly after process start in
// reduced mode, never full mode, to avoid duplicating a scheduled run that
// starts moments later". It also takes the first AdapterVersionSnapshot so
// a later RunOnVersionCheck call has something to diff against; per
// ChangedAdapterIDs, this first snapshot alone never fires a spurious
// version-change run.
func (c *Coordinator) RunOnStartup(ctx context.Context, now time.Time) (RunResult, error) {
	if c.scheduler == nil {
		return RunResult{}, errors.New("startup audit scheduler not configured")
	}
	interrupted, err := MarkStaleRunsInterrupted(ctx, c.scheduler.pool, now)
	if err != nil {
		return RunResult{}, err
	}
	prior, err := loadReusablePasses(ctx, c.scheduler.pool, interrupted, now)
	if err != nil {
		return RunResult{}, err
	}
	if c.fingerprintProvider != nil {
		current, err := c.fingerprintProvider(ctx, now)
		if err != nil {
			return RunResult{}, err
		}
		result, err := c.scheduler.startStartupRecoveryRun(ctx, fingerprintInputs(current), prior, now)
		if err != nil {
			return result, err
		}
		if result.Run.State == RunPassed {
			if err := StoreFingerprints(ctx, c.fingerprintPool, current); err != nil {
				return result, err
			}
		}
		return result, nil
	}
	if c.registry != nil {
		snapshot, err := SnapshotAdapterVersions(c.registry)
		if err == nil {
			c.lastVersions = snapshot
		}
	}
	return c.scheduler.startStartupRecoveryRun(ctx, adapterVersionInputs(c.lastVersions), prior, now)
}

func loadReusablePasses(ctx context.Context, pool *pgxpool.Pool, interrupted []string, now time.Time) (reusablePasses, error) {
	out := reusablePasses{}
	for _, runID := range interrupted {
		checks, err := ListChecksForRun(ctx, pool, runID)
		if err != nil {
			return nil, err
		}
		for _, check := range checks {
			if check.Status != CheckStatusPass || check.ObservedAt == nil ||
				!IsFresh(*check.ObservedAt, now, DefaultFreshnessWindow) {
				continue
			}
			key := reusablePassKey(check.CheckID, check.CapabilityID, check.InstallationID, check.SourceID)
			if existing, ok := out[key]; !ok || existing.ObservedAt == nil || check.ObservedAt.After(*existing.ObservedAt) {
				out[key] = check
			}
		}
	}
	return out, nil
}

// RunOnVersionCheck compares the registry's current adapter versions
// against the last observed snapshot and, if any changed, fires a
// version_change_detected reduced-mode run scoped by the contract's
// reduced_mode_stage_scope to that trigger. It returns (RunResult{}, nil,
// nil) with a nil-valued "ran" flag semantics expressed via the returned
// bool when no version changed, so callers can distinguish "no drift, no
// run" from "ran and here is the result" without inspecting a zero-value
// RunResult ambiguously.
func (c *Coordinator) RunOnVersionCheck(ctx context.Context, now time.Time) (ran bool, result RunResult, err error) {
	if c.scheduler == nil {
		return false, RunResult{}, errors.New("version audit scheduler not configured")
	}
	if c.fingerprintProvider != nil {
		previous, err := LoadFingerprints(ctx, c.fingerprintPool)
		if err != nil {
			return false, RunResult{}, err
		}
		current, err := c.fingerprintProvider(ctx, now)
		if err != nil {
			return false, RunResult{}, err
		}
		if len(previous) == 0 {
			// A polling loop may not establish an unaudited baseline. The
			// startup run owns first-baseline creation and stores it only
			// after that run passes.
			return false, RunResult{}, nil
		}
		changes := ChangedFingerprints(previous, current)
		if len(changes) == 0 {
			return false, RunResult{}, nil
		}
		result, err = c.scheduler.StartTargetedVersionRun(ctx, changes, fingerprintInputs(current), now)
		if err != nil {
			// The durable baseline intentionally remains unchanged. The next
			// poll retries the exact same drift instead of losing it.
			return true, result, err
		}
		if result.Run.State == RunPassed {
			if err := StoreFingerprints(ctx, c.fingerprintPool, current); err != nil {
				return true, result, err
			}
		}
		return true, result, nil
	}
	if c.registry == nil {
		return false, RunResult{}, nil
	}
	current, err := SnapshotAdapterVersions(c.registry)
	if err != nil {
		return false, RunResult{}, err
	}
	changed := ChangedAdapterIDs(c.lastVersions, current)
	if len(changed) == 0 {
		return false, RunResult{}, nil
	}
	result, err = c.scheduler.StartRun(ctx, RunModeReduced, TriggerVersionChangeDetected, adapterVersionInputs(current), now)
	if err == nil {
		c.lastVersions = current
	}
	return true, result, err
}

// RunScheduledDaily fires a full-mode scheduled_daily run.
func (c *Coordinator) RunScheduledDaily(ctx context.Context, now time.Time) (RunResult, error) {
	if c.scheduler == nil {
		return RunResult{}, errors.New("daily audit scheduler not configured")
	}
	if c.fingerprintProvider != nil {
		current, err := c.fingerprintProvider(ctx, now)
		if err != nil {
			return RunResult{}, err
		}
		result, err := c.scheduler.StartRun(ctx, RunModeFull, TriggerScheduledDaily, fingerprintInputs(current), now)
		if err != nil {
			return result, err
		}
		if result.Run.State == RunPassed {
			if err := StoreFingerprints(ctx, c.fingerprintPool, current); err != nil {
				return result, err
			}
		}
		return result, nil
	}
	inputs := adapterVersionInputs(c.lastVersions)
	if c.registry != nil {
		if snapshot, err := SnapshotAdapterVersions(c.registry); err == nil {
			c.lastVersions = snapshot
			inputs = adapterVersionInputs(snapshot)
		}
	}
	return c.scheduler.StartRun(ctx, RunModeFull, TriggerScheduledDaily, inputs, now)
}

func fingerprintInputs(rows []DriftFingerprint) map[string]string {
	inputs := make(map[string]string, len(rows))
	for _, row := range rows {
		inputs[fingerprintIdentity(row)] = row.ValueRef
	}
	return inputs
}

func adapterVersionInputs(snapshot AdapterVersionSnapshot) map[string]string {
	inputs := make(map[string]string, len(snapshot))
	for id, version := range snapshot {
		inputs["adapter_version:"+id] = version
	}
	return inputs
}
