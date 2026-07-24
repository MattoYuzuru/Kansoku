package integrity

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"kansoku.local/kansoku/internal/adaptersdk"
)

const LiveCanaryCheckID = "stage_10_optional_live_canary"

var DefaultExpectedCanaryDAG = []string{
	"session.started",
	"prompt.submitted",
	"component.invoked(canary)",
	"tool.called(mcp.echo)",
	"tool.succeeded",
	"component.succeeded",
	"session.stopped",
}

// LiveCanaryRecipe is the closed, data-only recipe shape. Command is argv,
// never a shell string. Session 08 validates and simulates this structure;
// it never invokes an external process.
type LiveCanaryRecipe struct {
	RecipeID           string
	AdapterID          string
	Command            []string
	FixtureWorkspace   string
	CanarySkillName    string
	LocalMCPEchoTool   string
	ExpectedEventDAG   []string
	AlternativeNote    string
	MaxTurns           int
	MaxTokens          int
	MaxCostUSD         float64
	MaxDuration        time.Duration
	Cooldown           time.Duration
	Cleanup            string
	NamespaceExclusion string
	Enabled            bool
}

type LiveCanaryGate struct {
	ExplicitCredentialsPresent  bool
	ExplicitUserConsentRecorded bool
	ConsentRecordedAt           time.Time
	LastRunAt                   time.Time
}

type LiveCanaryObservation struct {
	EventDAG         []string
	ProviderTimedOut bool
	Turns            int
	Tokens           int
	CostUSD          float64
	Duration         time.Duration
	ObservedAt       time.Time
}

type LiveCanaryObserver func(ctx context.Context, recipe LiveCanaryRecipe) (LiveCanaryObservation, error)
type LiveCanaryCleanup func(ctx context.Context, recipe LiveCanaryRecipe) error

type LiveCanaryRunState struct {
	LastStartedAt  time.Time
	LastFinishedAt time.Time
	LastStatus     CheckStatus
}

type LiveCanaryStateStore interface {
	Load(ctx context.Context, recipeID string) (LiveCanaryRunState, error)
	MarkStarted(ctx context.Context, recipeID string, startedAt time.Time) error
	MarkFinished(ctx context.Context, recipeID string, status CheckStatus, finishedAt time.Time) error
}

type LiveCanaryAuthorizationStore interface {
	LoadAuthorization(ctx context.Context, recipeID, adapterID string) (LiveCanaryGate, error)
}

// PostgresLiveCanaryStateStore makes cooldown survive process restarts.
// It stores only recipe identity, timestamps, and result state.
type PostgresLiveCanaryStateStore struct {
	Pool *pgxpool.Pool
}

func (s PostgresLiveCanaryStateStore) RecordAuthorization(ctx context.Context, recipeID, adapterID string, credentialsConfirmedAt, consentRecordedAt time.Time) error {
	if s.Pool == nil || recipeID == "" || adapterID == "" ||
		credentialsConfirmedAt.IsZero() || consentRecordedAt.IsZero() {
		return errors.New("durable live canary authorization requires recipe, adapter, credentials, and consent timestamps")
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO integrity_live_canary_state
		    (recipe_id, adapter_id, credentials_confirmed_at, consent_recorded_at, updated_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (recipe_id) DO UPDATE SET
		    adapter_id = EXCLUDED.adapter_id,
		    credentials_confirmed_at = EXCLUDED.credentials_confirmed_at,
		    consent_recorded_at = EXCLUDED.consent_recorded_at,
		    updated_at = now()
	`, recipeID, adapterID, credentialsConfirmedAt.UTC(), consentRecordedAt.UTC())
	return err
}

func (s PostgresLiveCanaryStateStore) LoadAuthorization(ctx context.Context, recipeID, adapterID string) (LiveCanaryGate, error) {
	if s.Pool == nil {
		return LiveCanaryGate{}, errors.New("live canary state pool is required")
	}
	var credentialsAt, consentAt, lastStartedAt *time.Time
	err := s.Pool.QueryRow(ctx, `
		SELECT credentials_confirmed_at, consent_recorded_at, last_started_at
		FROM integrity_live_canary_state
		WHERE recipe_id = $1 AND adapter_id = $2
	`, recipeID, adapterID).Scan(&credentialsAt, &consentAt, &lastStartedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return LiveCanaryGate{}, nil
	}
	if err != nil {
		return LiveCanaryGate{}, err
	}
	gate := LiveCanaryGate{}
	if credentialsAt != nil {
		gate.ExplicitCredentialsPresent = true
	}
	if consentAt != nil {
		gate.ExplicitUserConsentRecorded = true
		gate.ConsentRecordedAt = consentAt.UTC()
	}
	if lastStartedAt != nil {
		gate.LastRunAt = lastStartedAt.UTC()
	}
	return gate, nil
}

func (s PostgresLiveCanaryStateStore) Load(ctx context.Context, recipeID string) (LiveCanaryRunState, error) {
	if s.Pool == nil {
		return LiveCanaryRunState{}, errors.New("live canary state pool is required")
	}
	var (
		state                 LiveCanaryRunState
		startedAt, finishedAt *time.Time
		status                *string
	)
	err := s.Pool.QueryRow(ctx, `
		SELECT last_started_at, last_finished_at, last_status
		FROM integrity_live_canary_state
		WHERE recipe_id = $1
	`, recipeID).Scan(&startedAt, &finishedAt, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return LiveCanaryRunState{}, nil
	}
	if err != nil {
		return LiveCanaryRunState{}, err
	}
	if startedAt != nil {
		state.LastStartedAt = startedAt.UTC()
	}
	if finishedAt != nil {
		state.LastFinishedAt = finishedAt.UTC()
	}
	if status != nil {
		state.LastStatus = CheckStatus(*status)
	}
	return state, nil
}

func (s PostgresLiveCanaryStateStore) MarkStarted(ctx context.Context, recipeID string, startedAt time.Time) error {
	if s.Pool == nil {
		return errors.New("live canary state pool is required")
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO integrity_live_canary_state (recipe_id, last_started_at, updated_at)
		VALUES ($1, $2, now())
		ON CONFLICT (recipe_id) DO UPDATE SET
		    last_started_at = EXCLUDED.last_started_at,
		    updated_at = now()
	`, recipeID, startedAt.UTC())
	return err
}

func (s PostgresLiveCanaryStateStore) MarkFinished(ctx context.Context, recipeID string, status CheckStatus, finishedAt time.Time) error {
	if s.Pool == nil {
		return errors.New("live canary state pool is required")
	}
	if status != CheckStatusPass && status != CheckStatusFail && status != CheckStatusSkippedUnsupported {
		return errors.New("invalid terminal live canary status")
	}
	_, err := s.Pool.Exec(ctx, `
		INSERT INTO integrity_live_canary_state
		    (recipe_id, last_finished_at, last_status, updated_at)
		VALUES ($1, $2, $3, now())
		ON CONFLICT (recipe_id) DO UPDATE SET
		    last_finished_at = EXCLUDED.last_finished_at,
		    last_status = EXCLUDED.last_status,
		    updated_at = now()
	`, recipeID, finishedAt.UTC(), string(status))
	return err
}

func (r LiveCanaryRecipe) Validate() error {
	if r.RecipeID == "" || r.AdapterID == "" || len(r.Command) == 0 {
		return errors.New("live canary recipe identity and argv are required")
	}
	for _, arg := range r.Command {
		if strings.TrimSpace(arg) == "" || strings.ContainsRune(arg, '\x00') {
			return errors.New("live canary argv contains an invalid element")
		}
	}
	if r.FixtureWorkspace == "" || !strings.Contains(filepath.Base(r.FixtureWorkspace), "kansoku-canary-") {
		return errors.New("fixture workspace must use the reserved kansoku-canary namespace")
	}
	if !strings.HasPrefix(r.CanarySkillName, "kansoku-canary-") {
		return errors.New("canary skill must use the reserved kansoku-canary namespace")
	}
	if r.LocalMCPEchoTool == "" || len(r.ExpectedEventDAG) == 0 {
		return errors.New("local MCP echo tool and expected DAG are required")
	}
	if r.MaxTurns <= 0 || r.MaxTokens <= 0 || r.MaxCostUSD <= 0 ||
		r.MaxDuration <= 0 || r.MaxDuration > 300*time.Second || r.Cooldown <= 0 {
		return errors.New("live canary budgets must be positive and duration must not exceed stage timeout")
	}
	if r.Cleanup == "" || r.NamespaceExclusion == "" {
		return errors.New("cleanup and namespace exclusion declarations are required")
	}
	return nil
}

func (g LiveCanaryGate) Authorize(recipe LiveCanaryRecipe, now time.Time) (bool, string) {
	if !recipe.Enabled {
		return false, "disabled_by_default"
	}
	if !g.ExplicitCredentialsPresent {
		return false, "explicit_credentials_absent"
	}
	if !g.ExplicitUserConsentRecorded || g.ConsentRecordedAt.IsZero() {
		return false, "explicit_user_consent_absent"
	}
	if !g.LastRunAt.IsZero() && now.Sub(g.LastRunAt) < recipe.Cooldown {
		return false, "cooldown_active"
	}
	return true, ""
}

// LiveCanaryCheck validates gate/budget/cleanup semantics and consumes only
// caller-provided simulated/fixture evidence. It contains no os/exec or
// network client and therefore cannot run a real provider canary.
type LiveCanaryCheck struct {
	Recipes  []LiveCanaryRecipe
	Gates    map[string]LiveCanaryGate
	Observe  LiveCanaryObserver
	Cleanup  LiveCanaryCleanup
	State    LiveCanaryStateStore
	Registry *adaptersdk.Registry
	Now      func() time.Time
	byTarget map[string]LiveCanaryRecipe
}

var _ Check = (*LiveCanaryCheck)(nil)

func NewLiveCanaryCheck(recipes []LiveCanaryRecipe, gates map[string]LiveCanaryGate, observe LiveCanaryObserver, cleanup LiveCanaryCleanup) *LiveCanaryCheck {
	return &LiveCanaryCheck{Recipes: append([]LiveCanaryRecipe(nil), recipes...), Gates: gates, Observe: observe, Cleanup: cleanup, Now: time.Now}
}

func (c *LiveCanaryCheck) StageID() StageID { return Stage10OptionalLiveCanary }
func (c *LiveCanaryCheck) CheckID() string  { return LiveCanaryCheckID }

func (c *LiveCanaryCheck) validateProductionReady(sharedPool *pgxpool.Pool) error {
	if c == nil || c.Now == nil {
		return errors.New("live canary clock is required")
	}
	for _, recipe := range c.Recipes {
		if err := recipe.Validate(); err != nil {
			return err
		}
		if recipe.Enabled && (c.Observe == nil || c.Cleanup == nil || c.State == nil) {
			return errors.New("enabled live canary requires observer, deterministic cleanup, and durable cooldown state")
		}
		if recipe.Enabled {
			var statePool *pgxpool.Pool
			switch state := c.State.(type) {
			case PostgresLiveCanaryStateStore:
				statePool = state.Pool
			case *PostgresLiveCanaryStateStore:
				if state != nil {
					statePool = state.Pool
				}
			}
			if statePool == nil || statePool != sharedPool {
				return errors.New("enabled live canary requires durable authorization/cooldown state on assembly PostgreSQL pool")
			}
			if c.Registry == nil {
				return errors.New("enabled live canary requires adapter registry validation")
			}
			adapter, err := c.Registry.Get(recipe.AdapterID)
			if err != nil {
				return fmt.Errorf("live canary adapter %s is not registered", recipe.AdapterID)
			}
			if _, declared := adapter.Manifest().Capabilities[adaptersdk.CapabilityConfigurationLiveCanary]; !declared {
				return fmt.Errorf("live canary adapter %s does not declare live-canary capability", recipe.AdapterID)
			}
		}
	}
	return nil
}

func (c *LiveCanaryCheck) Targets(_ context.Context, _ CheckInput) ([]CheckTarget, error) {
	c.byTarget = make(map[string]LiveCanaryRecipe, len(c.Recipes))
	out := make([]CheckTarget, 0, len(c.Recipes))
	for _, recipe := range c.Recipes {
		if err := recipe.Validate(); err != nil {
			return nil, fmt.Errorf("recipe %s: %w", recipe.RecipeID, err)
		}
		target := CheckTarget{
			InstallationID: recipe.AdapterID,
			CapabilityID:   string(adaptersdk.CapabilityConfigurationLiveCanary),
			SourceID:       recipe.RecipeID,
		}
		key := endpointTargetKey(target.InstallationID, target.CapabilityID, target.SourceID)
		if _, duplicate := c.byTarget[key]; duplicate {
			return nil, fmt.Errorf("duplicate live canary recipe target %s", recipe.RecipeID)
		}
		c.byTarget[key] = recipe
		out = append(out, target)
	}
	return out, nil
}

func (c *LiveCanaryCheck) Evaluate(ctx context.Context, in CheckInput, target CheckTarget) (outcome CheckOutcome, err error) {
	now := c.Now()
	if !in.Now.IsZero() {
		now = in.Now
	}
	recipe, ok := c.byTarget[endpointTargetKey(target.InstallationID, target.CapabilityID, target.SourceID)]
	if !ok {
		return liveCanaryFailure(now, FailureClassLiveCanaryPartialDAG, "recipe_target_not_enumerated"), nil
	}
	gate := c.Gates[recipe.RecipeID]
	if authStore, ok := c.State.(LiveCanaryAuthorizationStore); ok {
		durableGate, authErr := authStore.LoadAuthorization(ctx, recipe.RecipeID, recipe.AdapterID)
		if authErr != nil {
			return liveCanaryFailure(now, FailureClassLiveCanaryPartialDAG, "durable_authorization_lookup_failed"), nil
		}
		gate = durableGate
	}
	if c.State != nil {
		state, stateErr := c.State.Load(ctx, recipe.RecipeID)
		if stateErr != nil {
			return liveCanaryFailure(now, FailureClassLiveCanaryPartialDAG, "durable_cooldown_state_lookup_failed"), nil
		}
		gate.LastRunAt = state.LastStartedAt
	}
	if authorized, reason := gate.Authorize(recipe, now); !authorized {
		return CheckOutcome{
			CheckID: LiveCanaryCheckID, Status: CheckStatusSkippedUnsupported,
			DetailRef: reason, ObservedAt: now,
		}, nil
	}
	if c.Observe == nil {
		return CheckOutcome{
			CheckID: LiveCanaryCheckID, Status: CheckStatusSkippedUnsupported,
			DetailRef: "simulation_observer_not_wired", ObservedAt: now,
		}, nil
	}
	if c.Cleanup == nil {
		return liveCanaryFailure(now, FailureClassLiveCanaryPartialDAG, "cleanup_not_wired"), nil
	}
	if c.State != nil {
		if stateErr := c.State.MarkStarted(ctx, recipe.RecipeID, now); stateErr != nil {
			return liveCanaryFailure(now, FailureClassLiveCanaryPartialDAG, "durable_cooldown_start_failed"), nil
		}
	}
	defer func() {
		cleanupTimeout := recipe.MaxDuration
		if cleanupTimeout <= 0 || cleanupTimeout > 30*time.Second {
			cleanupTimeout = 30 * time.Second
		}
		cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), cleanupTimeout)
		cleanupErr := func() (cleanupErr error) {
			defer func() {
				if recovered := recover(); recovered != nil {
					cleanupErr = fmt.Errorf("cleanup panic: %v", recovered)
				}
			}()
			return c.Cleanup(cleanupCtx, recipe)
		}()
		cancelCleanup()
		if cleanupErr != nil {
			outcome = liveCanaryFailure(now, FailureClassLiveCanaryPartialDAG, "deterministic_cleanup_failed")
		}
		if c.State != nil {
			status := outcome.Status
			if status != CheckStatusPass {
				status = CheckStatusFail
			}
			stateCtx, cancelState := context.WithTimeout(context.Background(), 5*time.Second)
			stateErr := c.State.MarkFinished(stateCtx, recipe.RecipeID, status, time.Now().UTC())
			cancelState()
			if stateErr != nil {
				outcome = liveCanaryFailure(now, FailureClassLiveCanaryPartialDAG, "durable_cooldown_finish_failed")
				err = nil
			}
		}
	}()
	observeCtx, cancelObserve := context.WithTimeout(ctx, recipe.MaxDuration)
	started := time.Now()
	type observeResult struct {
		observation LiveCanaryObservation
		err         error
	}
	observed := make(chan observeResult, 1)
	go func() {
		result := observeResult{}
		defer func() {
			if recover() != nil {
				result.err = errors.New("live_canary_observer_panicked")
			}
			observed <- result
		}()
		result.observation, result.err = c.Observe(observeCtx, recipe)
	}()
	var observation LiveCanaryObservation
	var observeErr error
	select {
	case result := <-observed:
		observation, observeErr = result.observation, result.err
	case <-observeCtx.Done():
		observeErr = observeCtx.Err()
	}
	elapsed := time.Since(started)
	observeContextErr := observeCtx.Err()
	cancelObserve()
	if observeErr != nil || observation.ProviderTimedOut || errors.Is(observeContextErr, context.DeadlineExceeded) {
		return liveCanaryFailure(now, FailureClassLiveCanaryProviderTimeout, "provider_timeout"), nil
	}
	duration := observation.Duration
	if duration <= 0 {
		duration = elapsed
	}
	if observation.Turns > recipe.MaxTurns || observation.Tokens > recipe.MaxTokens ||
		observation.CostUSD > recipe.MaxCostUSD || duration > recipe.MaxDuration {
		return liveCanaryFailure(now, FailureClassLiveCanaryPartialDAG, "canary_budget_exceeded"), nil
	}
	if detail := eventDAGMismatch(recipe.ExpectedEventDAG, observation.EventDAG); detail != "" {
		return liveCanaryFailure(now, FailureClassLiveCanaryPartialDAG, detail), nil
	}
	if observation.ObservedAt.IsZero() {
		observation.ObservedAt = now
	}
	return CheckOutcome{
		CheckID: LiveCanaryCheckID, Status: CheckStatusPass,
		DetailRef: "bounded_simulated_event_dag_matched", ObservedAt: observation.ObservedAt,
	}, nil
}

func liveCanaryFailure(now time.Time, class FailureClass, detail string) CheckOutcome {
	return CheckOutcome{CheckID: LiveCanaryCheckID, Status: CheckStatusFail, Category: string(class), DetailRef: detail, ObservedAt: now}
}

func equalEventDAG(expected, observed []string) bool {
	if len(expected) != len(observed) {
		return false
	}
	for i := range expected {
		if expected[i] != observed[i] {
			return false
		}
	}
	return true
}

func eventDAGMismatch(expected, observed []string) string {
	if equalEventDAG(expected, observed) {
		return ""
	}
	expectedCounts := map[string]int{}
	observedCounts := map[string]int{}
	for _, event := range expected {
		expectedCounts[event]++
	}
	for _, event := range observed {
		observedCounts[event]++
	}
	missing, extra := 0, 0
	for event, count := range expectedCounts {
		if delta := count - observedCounts[event]; delta > 0 {
			missing += delta
		}
	}
	for event, count := range observedCounts {
		if delta := count - expectedCounts[event]; delta > 0 {
			extra += delta
		}
	}
	switch {
	case missing > 0 && extra > 0:
		return fmt.Sprintf("event_dag_missing_and_extra missing=%d extra=%d", missing, extra)
	case missing > 0:
		return fmt.Sprintf("event_dag_missing count=%d", missing)
	case extra > 0:
		return fmt.Sprintf("event_dag_extra count=%d", extra)
	default:
		return "event_dag_misordered"
	}
}
