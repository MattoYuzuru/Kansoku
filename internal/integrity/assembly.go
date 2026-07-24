package integrity

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kansoku.local/kansoku/internal/adaptersdk"
)

// ProductionAssemblyConfig is the explicit dependency boundary for the
// executable daily audit. Every stage implementation is constructed by the
// host process from its real registries/stores and supplied here; assembly
// rejects an incomplete graph before any run can be created.
type ProductionAssemblyConfig struct {
	Pool                *pgxpool.Pool
	AdapterRegistry     *adaptersdk.Registry
	Checks              []Check
	ReportSigningKeyID  string
	ReportSigningKey    []byte
	DailySchedule       DailyScheduleConfig
	VersionPollInterval time.Duration
	Fingerprints        FingerprintProvider
}

type ProductionAssembly struct {
	pool                *pgxpool.Pool
	scheduler           *Scheduler
	coordinator         *Coordinator
	versionPollInterval time.Duration
}

// productionReadyCheck is implemented by checks whose production behavior
// depends on stronger wiring than the generic Check interface can express.
// Conformance-only constructors remain available to unit tests, while the
// executable assembly rejects them before creating an audit run.
type productionReadyCheck interface {
	validateProductionReady(sharedPool *pgxpool.Pool) error
}

// NewProductionAssembly wires the one Scheduler/Coordinator instance used by
// startup, daily and version-change triggers. It does not open a second pool
// and does not silently substitute any missing stage dependency.
func NewProductionAssembly(config ProductionAssemblyConfig) (*ProductionAssembly, error) {
	if config.Pool == nil || config.AdapterRegistry == nil {
		return nil, errors.New("production integrity assembly requires shared PostgreSQL pool and adapter registry")
	}
	if config.Fingerprints == nil {
		return nil, errors.New("production integrity assembly requires complete fingerprint provider")
	}
	registry := NewCheckRegistry()
	seenIDs := map[string]bool{}
	for _, check := range config.Checks {
		if check == nil || check.CheckID() == "" {
			return nil, errors.New("production integrity assembly contains nil or unidentified check")
		}
		key := string(check.StageID()) + "\x00" + check.CheckID()
		if seenIDs[key] {
			return nil, fmt.Errorf("duplicate production integrity check %s", check.CheckID())
		}
		if productionCheck, ok := check.(productionReadyCheck); ok {
			if err := productionCheck.validateProductionReady(config.Pool); err != nil {
				return nil, fmt.Errorf("production integrity check %s: %w", check.CheckID(), err)
			}
		}
		seenIDs[key] = true
		registry.Register(check)
	}
	for _, descriptor := range StageRegistry {
		if descriptor.StageID == Stage11PersistReportAndIncidents {
			continue
		}
		if !registry.HasStage(descriptor.StageID) {
			return nil, fmt.Errorf("mandatory production integrity stage not wired: %s", descriptor.StageID)
		}
	}
	scheduler := NewScheduler(config.Pool, registry)
	if err := scheduler.ConfigureReportSigning(config.ReportSigningKeyID, config.ReportSigningKey); err != nil {
		return nil, err
	}
	coordinator := NewCoordinator(scheduler, config.AdapterRegistry, config.DailySchedule)
	if err := coordinator.ConfigureFingerprintWatch(config.Pool, config.Fingerprints); err != nil {
		return nil, err
	}
	poll := config.VersionPollInterval
	if poll <= 0 {
		poll = time.Minute
	}
	return &ProductionAssembly{
		pool: config.Pool, scheduler: scheduler, coordinator: coordinator,
		versionPollInterval: poll,
	}, nil
}

// Run applies the Session 08 migration, performs crash recovery plus the
// reduced startup audit, then services cancellable daily and version-change
// triggers until ctx is cancelled. Timers are stopped deterministically and
// the active Scheduler receives ctx so it can finalize a cancelled run as
// cancelled rather than leaving it running.
func (a *ProductionAssembly) Run(ctx context.Context) error {
	if a == nil || a.pool == nil || a.coordinator == nil {
		return errors.New("production integrity assembly is incomplete")
	}
	if err := Migrate(ctx, a.pool); err != nil {
		return err
	}
	if _, err := a.coordinator.RunOnStartup(ctx, time.Now().UTC()); err != nil &&
		!errors.Is(err, ErrAlreadyRunning) && !errors.Is(err, context.Canceled) {
		return err
	}
	if err := ctx.Err(); err != nil {
		return nil
	}

	nextDaily := a.coordinator.dailyConfig.NextScheduledDailyRun(time.Now())
	dailyTimer := time.NewTimer(time.Until(nextDaily))
	defer dailyTimer.Stop()
	versionTicker := time.NewTicker(a.versionPollInterval)
	defer versionTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case firedAt := <-dailyTimer.C:
			if _, err := a.coordinator.RunScheduledDaily(ctx, firedAt.UTC()); err != nil &&
				!errors.Is(err, ErrAlreadyRunning) && !errors.Is(err, context.Canceled) {
				return err
			}
			nextDaily = a.coordinator.dailyConfig.NextScheduledDailyRun(time.Now())
			dailyTimer.Reset(time.Until(nextDaily))
		case firedAt := <-versionTicker.C:
			if _, _, err := a.coordinator.RunOnVersionCheck(ctx, firedAt.UTC()); err != nil &&
				!errors.Is(err, ErrAlreadyRunning) && !errors.Is(err, context.Canceled) {
				return err
			}
		}
	}
}
