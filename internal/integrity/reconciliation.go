package integrity

import (
	"context"
	"fmt"
	"time"

	"kansoku.local/kansoku/internal/adaptersdk"
)

// CrossSourceReconciliationCheckID is the check_id every
// CrossSourceReconciliationCheck outcome reports, matching
// audit-run-and-schedule.yaml's stage_6_cross_source_reconciliation stage and
// incident-and-health.yaml's "reconciliation_coverage" health dimension.
const CrossSourceReconciliationCheckID = "stage_6_cross_source_reconciliation"

// ReconciliationMismatchClass is the closed, versioned-tolerance-aware
// classification of one session's cross-source reconciliation outcome,
// mirroring the LaneCompleteness/Mismatched shape internal/codexadapter and
// internal/claudeadapter's own ReconcileLane already produce (this package
// never re-derives that per-lane comparison itself: it reads its OUTPUT,
// exactly as SourceFreshnessCheck reads Watermark's already-computed
// classification rather than re-deriving gap/stall math).
type ReconciliationMismatchClass string

const (
	// MismatchClassNone means every compared source agreed within tolerance
	// for the terminal-delay window; nothing degraded or disagreed.
	MismatchClassNone ReconciliationMismatchClass = "none"
	// MismatchClassDegradedSource means at least one expected source was
	// missing/absent for the window (a coverage gap), but present sources
	// did not disagree -- matching reconciliation.missing_source_rule:
	// "missing one expected source ... never silently reports zero usage
	// for the whole session."
	MismatchClassDegradedSource ReconciliationMismatchClass = "degraded_source"
	// MismatchClassCountMismatch means two or more present sources disagreed
	// on counts/identities beyond the versioned tolerance.
	MismatchClassCountMismatch ReconciliationMismatchClass = "count_mismatch"
	// MismatchClassUnknownTolerance means the compatibility/tolerance
	// version this window's sessions declare is not recognized by the
	// adapter's own tolerance registry, so ratio/mismatch cannot be judged
	// against a real bound -- reported distinctly rather than silently
	// assumed "none".
	MismatchClassUnknownTolerance ReconciliationMismatchClass = "unknown_tolerance"
)

// SessionReconciliationSummary is the generic, adapter-agnostic input this
// check compares: one already-reconciled session's ratio/mismatch-class
// outcome for one installation, for the terminal-delay window under audit.
// A real caller closes ReconciliationSummaryLookup over whatever
// adapter-specific reconciler already produced this (codexadapter's
// ReconcileSession/ReconcileLane, claudeadapter's own package-private
// equivalent, or any future adapter's own reconciler) -- this package never
// re-derives per-lane hook/otel/rollout comparison logic itself, and it
// never type-switches on adapter_id to decide how to call into one.
type SessionReconciliationSummary struct {
	SessionID            string
	CompatibilityVersion string
	// ToleranceKnown reports whether the adapter's own tolerance registry
	// recognized CompatibilityVersion. false routes straight to
	// MismatchClassUnknownTolerance regardless of the other fields.
	ToleranceKnown bool
	// TotalLanes/AgreeingLanes describe this session's own lane coverage:
	// AgreeingLanes/TotalLanes is the match ratio this check reports.
	// TotalLanes is always > 0 for a summary actually returned by the
	// lookup (a session with zero declared lanes is not itself
	// reconcilable and must not be returned as a target).
	TotalLanes     int
	AgreeingLanes  int
	DegradedLanes  int
	MismatchLanes  int
	AdapterVersion string
}

// matchRatio returns AgreeingLanes/TotalLanes, or 0 when TotalLanes is 0
// (never divides by zero).
func (s SessionReconciliationSummary) matchRatio() float64 {
	if s.TotalLanes <= 0 {
		return 0
	}
	return float64(s.AgreeingLanes) / float64(s.TotalLanes)
}

// classify derives this summary's ReconciliationMismatchClass, matching the
// precedence: unknown tolerance first (nothing else can be judged), then
// any count mismatch (a disagreement among sources that were actually
// present), then any degraded/missing source, else none.
func (s SessionReconciliationSummary) classify() ReconciliationMismatchClass {
	if !s.ToleranceKnown {
		return MismatchClassUnknownTolerance
	}
	if s.MismatchLanes > 0 {
		return MismatchClassCountMismatch
	}
	if s.DegradedLanes > 0 {
		return MismatchClassDegradedSource
	}
	return MismatchClassNone
}

// ReconciliationWindowSummary aggregates every SessionReconciliationSummary
// for one installation's terminal-delay window into the single ratio/
// mismatch-class outcome this check's CheckOutcome reports, so one audit
// check row always reflects the whole window rather than one arbitrary
// session.
type ReconciliationWindowSummary struct {
	InstallationID         string
	SourceID               string
	Sessions               []SessionReconciliationSummary
	MinimumRatio           float64
	PreviousAdapterVersion string
}

// ReconciliationWindowLookup returns every session reconciliation summary
// due for the terminal-delay window for one (adapter_id, installation_id)
// pair, plus the minimum acceptable match ratio (the "versioned tolerance"
// the incident-and-health.yaml reconciliation_coverage dimension names) and
// the previous adapter version this window's outcome is compared against
// for a regression. A real caller closes this over each adapter's own
// terminal-delay-windowed session store; this package supplies no default
// session source of its own.
type ReconciliationWindowLookup func(ctx context.Context, adapterID, installationID string) (ReconciliationWindowSummary, error)

// CrossSourceReconciliationCheck implements stage_6_cross_source_reconciliation:
// for every installation of every adapter registered in an
// adaptersdk.Registry, it asks ReconciliationWindowLookup for that
// installation's already-reconciled terminal-delay window, computes the
// aggregate match ratio and worst mismatch class across that window's
// sessions, and compares the current adapter version's outcome against the
// previous adapter version's last-known outcome to detect a regression --
// matching incident-and-health.yaml's "no regression vs the previous
// adapter version" and fault-injection-and-live-canary.yaml's
// cross_source_reconciliation_regression detection claim.
//
// This check never re-derives the per-lane hook/otel/rollout comparison
// itself (that remains internal/codexadapter's and internal/claudeadapter's
// own ReconcileLane/ReconcileSession machinery); it only aggregates and
// compares already-computed SessionReconciliationSummary values, generically
// across every registered adapter via Registry.IDs()/Get(), with zero
// agent-name branching.
type CrossSourceReconciliationCheck struct {
	Registry      *adaptersdk.Registry
	Installations InstallationLister
	Window        ReconciliationWindowLookup
	PreviousRatio PreviousRatioLookup
	Now           func() time.Time
}

var _ Check = (*CrossSourceReconciliationCheck)(nil)

// PreviousRatioLookup returns the last-recorded match ratio and mismatch
// class this (installation_id, source_id) pair achieved under a different
// (adapter_version, source_id) pair than the current one, so
// CrossSourceReconciliationCheck can detect "ratio/mismatch class regressed
// vs the previous adapter version" without this package inventing its own
// second durable history mechanism; a real caller closes this over the same
// audit_checks history table (ListChecksForRun/GetCheck across prior runs)
// this package already persists. ok=false means no prior-version baseline is
// on record yet, which is not itself a regression (nothing to regress from).
type PreviousRatioLookup func(ctx context.Context, installationID, sourceID, currentAdapterVersion string) (ratio float64, class ReconciliationMismatchClass, ok bool, err error)

// NewCrossSourceReconciliationCheck constructs a
// CrossSourceReconciliationCheck. installations/window/previousRatio may be
// nil, producing zero targets / an empty window / "no baseline known"
// respectively, so a caller without a wired session store yet still gets a
// Check that runs safely rather than panicking.
func NewCrossSourceReconciliationCheck(registry *adaptersdk.Registry, installations InstallationLister, window ReconciliationWindowLookup, previousRatio PreviousRatioLookup) *CrossSourceReconciliationCheck {
	if installations == nil {
		installations = func(context.Context, string) ([]adaptersdk.Installation, error) { return nil, nil }
	}
	if window == nil {
		window = func(context.Context, string, string) (ReconciliationWindowSummary, error) {
			return ReconciliationWindowSummary{}, nil
		}
	}
	if previousRatio == nil {
		previousRatio = func(context.Context, string, string, string) (float64, ReconciliationMismatchClass, bool, error) {
			return 0, MismatchClassNone, false, nil
		}
	}
	return &CrossSourceReconciliationCheck{Registry: registry, Installations: installations, Window: window, PreviousRatio: previousRatio, Now: time.Now}
}

func (c *CrossSourceReconciliationCheck) StageID() StageID { return Stage6CrossSourceReconciliation }
func (c *CrossSourceReconciliationCheck) CheckID() string  { return CrossSourceReconciliationCheckID }

// Targets enumerates one CheckTarget per (adapter's installations), filed
// under CapabilityActivitySessions since cross-source session reconciliation
// is that capability's own evidence.
func (c *CrossSourceReconciliationCheck) Targets(ctx context.Context, in CheckInput) ([]CheckTarget, error) {
	if c.Registry == nil {
		return nil, nil
	}
	var targets []CheckTarget
	for _, adapterID := range c.Registry.IDs() {
		installations, err := c.Installations(ctx, adapterID)
		if err != nil {
			return nil, fmt.Errorf("list installations for %s: %w", adapterID, err)
		}
		for _, installation := range installations {
			targets = append(targets, CheckTarget{
				CapabilityID:   string(adaptersdk.CapabilityActivitySessions),
				InstallationID: installation.InstallationID,
				AdapterID:      adapterID,
			})
		}
	}
	return targets, nil
}

// Evaluate aggregates one installation's reconciliation window and compares
// it against the previous adapter version's last-known outcome.
func (c *CrossSourceReconciliationCheck) Evaluate(ctx context.Context, in CheckInput, target CheckTarget) (CheckOutcome, error) {
	now := c.Now()
	if !in.Now.IsZero() {
		now = in.Now
	}
	adapterID, installation, err := c.resolveInstallation(ctx, target.InstallationID)
	if err != nil {
		return CheckOutcome{
			CheckID: CrossSourceReconciliationCheckID, Status: CheckStatusFail,
			Category: string(FailureClassReconciliationMismatch), DetailRef: "installation_not_resolvable",
			ObservedAt: now,
		}, nil
	}
	_ = installation

	window, err := c.Window(ctx, adapterID, target.InstallationID)
	if err != nil {
		return CheckOutcome{}, fmt.Errorf("reconciliation window lookup for %s: %w", target.InstallationID, err)
	}
	if len(window.Sessions) == 0 {
		// No sessions completed the terminal-delay window yet this run: this
		// is not itself a failure (nothing to reconcile does not mean
		// something is broken), matching the "gray is honest default" spirit
		// -- reported as skipped_unsupported rather than a fabricated pass.
		return CheckOutcome{
			CheckID: CrossSourceReconciliationCheckID, Status: CheckStatusSkippedUnsupported,
			Category: "", DetailRef: "no_sessions_in_terminal_delay_window",
			ObservedAt: now,
		}, nil
	}

	ratio, class, sourceID, adapterVersion := aggregateWindow(window)

	minimumRatio := window.MinimumRatio
	if minimumRatio <= 0 {
		minimumRatio = 1.0
	}
	if class == MismatchClassUnknownTolerance {
		return CheckOutcome{
			CheckID: CrossSourceReconciliationCheckID, Status: CheckStatusFail,
			Category:   string(FailureClassReconciliationMismatch),
			DetailRef:  fmt.Sprintf("unknown_tolerance_version sessions=%d", len(window.Sessions)),
			ObservedAt: now,
		}, nil
	}

	regressed, detail := c.detectRegression(ctx, target.InstallationID, sourceID, adapterVersion, ratio, class)
	if regressed {
		return CheckOutcome{
			CheckID: CrossSourceReconciliationCheckID, Status: CheckStatusFail,
			Category: string(FailureClassReconciliationMismatch), DetailRef: detail,
			ObservedAt: now,
		}, nil
	}

	if ratio+1e-9 < minimumRatio || class == MismatchClassCountMismatch {
		return CheckOutcome{
			CheckID: CrossSourceReconciliationCheckID, Status: CheckStatusFail,
			Category:   string(FailureClassReconciliationMismatch),
			DetailRef:  fmt.Sprintf("ratio=%.4f minimum=%.4f class=%s sessions=%d", ratio, minimumRatio, class, len(window.Sessions)),
			ObservedAt: now,
		}, nil
	}

	return CheckOutcome{
		CheckID: CrossSourceReconciliationCheckID, Status: CheckStatusPass,
		Category:   "",
		DetailRef:  fmt.Sprintf("ratio=%.4f class=%s sessions=%d", ratio, class, len(window.Sessions)),
		ObservedAt: now,
	}, nil
}

// aggregateWindow folds every session in window into one match ratio and the
// worst (most severe) mismatch class observed, plus the sourceID/adapterVersion
// this window's regression check keys against. Precedence for "worst" is
// unknown_tolerance > count_mismatch > degraded_source > none, matching
// classify()'s own precedence so a window with any unknown-tolerance session
// is never masked by other, less severe sessions' clean outcomes.
func aggregateWindow(window ReconciliationWindowSummary) (ratio float64, class ReconciliationMismatchClass, sourceID, adapterVersion string) {
	sourceID = window.SourceID
	var totalAgree, totalLanes int
	worst := MismatchClassNone
	rank := map[ReconciliationMismatchClass]int{
		MismatchClassNone: 0, MismatchClassDegradedSource: 1,
		MismatchClassCountMismatch: 2, MismatchClassUnknownTolerance: 3,
	}
	for _, session := range window.Sessions {
		totalAgree += session.AgreeingLanes
		totalLanes += session.TotalLanes
		sessionClass := session.classify()
		if rank[sessionClass] > rank[worst] {
			worst = sessionClass
		}
		if session.AdapterVersion != "" {
			adapterVersion = session.AdapterVersion
		}
	}
	if totalLanes > 0 {
		ratio = float64(totalAgree) / float64(totalLanes)
	}
	return ratio, worst, sourceID, adapterVersion
}

// detectRegression compares (ratio, class) against the last-recorded
// outcome for a DIFFERENT adapter version of the same installation/source,
// matching "no regression vs the previous adapter version". A regression is
// either the ratio dropping below the prior version's ratio, or the
// mismatch class getting strictly worse (never merely a coincidental
// single-run blip against itself, since PreviousRatioLookup is defined over
// a genuinely prior version).
func (c *CrossSourceReconciliationCheck) detectRegression(ctx context.Context, installationID, sourceID, currentAdapterVersion string, ratio float64, class ReconciliationMismatchClass) (bool, string) {
	if currentAdapterVersion == "" {
		return false, ""
	}
	previousRatio, previousClass, ok, err := c.PreviousRatio(ctx, installationID, sourceID, currentAdapterVersion)
	if err != nil || !ok {
		return false, ""
	}
	rank := map[ReconciliationMismatchClass]int{
		MismatchClassNone: 0, MismatchClassDegradedSource: 1,
		MismatchClassCountMismatch: 2, MismatchClassUnknownTolerance: 3,
	}
	if ratio+1e-9 < previousRatio || rank[class] > rank[previousClass] {
		return true, fmt.Sprintf("regression_vs_previous_adapter_version ratio=%.4f previous_ratio=%.4f class=%s previous_class=%s", ratio, previousRatio, class, previousClass)
	}
	return false, ""
}

// resolveInstallation looks up which registered adapter owns installationID,
// mirroring DiscoveryConfigCheck.resolveInstallation's own generic
// Registry.IDs()-driven search (never a hardcoded adapter_id).
func (c *CrossSourceReconciliationCheck) resolveInstallation(ctx context.Context, installationID string) (string, adaptersdk.Installation, error) {
	if c.Registry == nil {
		return "", adaptersdk.Installation{}, fmt.Errorf("no_adapter_registry_configured")
	}
	for _, adapterID := range c.Registry.IDs() {
		installations, err := c.Installations(ctx, adapterID)
		if err != nil {
			return "", adaptersdk.Installation{}, err
		}
		for _, installation := range installations {
			if installation.InstallationID == installationID {
				return adapterID, installation, nil
			}
		}
	}
	return "", adaptersdk.Installation{}, fmt.Errorf("installation %q not found among registered adapters", installationID)
}
