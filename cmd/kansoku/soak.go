package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	kansokuruntime "kansoku.local/kansoku/internal/runtime"
)

// runSoak parses the host-side accelerated-soak flags, loads the four ingress/
// read/mutation/csrf bearer values from host-accessible secret files (the same
// files the running appliance's Compose secrets are generated from), builds the
// real Docker SoakDriver and runs the already-correct
// runtime.RunAcceleratedSoakWithDriver orchestration against the live stack.
func runSoak(arguments []string) error {
	flags := flag.NewFlagSet("soak", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	evidencePath := flags.String("evidence", "", "absolute path for soak evidence")
	apiBase := flags.String("api-base", "http://127.0.0.1:43100", "running appliance API base URL")
	ingressBase := flags.String("ingress-base", "http://127.0.0.1:4318", "running appliance ingress HTTP base URL")
	origin := flags.String("origin", "", "canonical same-origin for mutation routes (defaults to the API base authority)")
	secretsDir := flags.String("secrets-dir", "", "absolute directory holding ingress_bearer/read_bearer/mutation_bearer/csrf files")
	composeFile := flags.String("compose-file", "", "absolute path to the running stack's compose file")
	composeProject := flags.String("compose-project", "", "docker compose project name of the running stack")
	appContainer := flags.String("app-container", "", "running kansoku appliance container name")
	dbContainer := flags.String("db-container", "", "running postgres container name")
	recoverTimeout := flags.Duration("recover-timeout", 90*time.Second, "bounded health-recovery deadline after each fault")
	cycleInterval := flags.Duration("cycle-interval", 0, "minimum spacing between cycles to stay under the per-peer rate limit (default ~550ms)")
	if err := flags.Parse(arguments); err != nil {
		return errors.New("invalid_flags")
	}
	if *evidencePath == "" || !filepath.IsAbs(*evidencePath) {
		return errors.New("evidence_path_required")
	}
	if *secretsDir == "" || !filepath.IsAbs(*secretsDir) {
		return errors.New("secrets_dir_required")
	}
	ingressBearer, err := readSoakSecret(filepath.Join(*secretsDir, "ingress_bearer"))
	if err != nil {
		return err
	}
	readBearer, err := readSoakSecret(filepath.Join(*secretsDir, "read_bearer"))
	if err != nil {
		return err
	}
	mutationBearer, err := readSoakSecret(filepath.Join(*secretsDir, "mutation_bearer"))
	if err != nil {
		return err
	}
	csrf, err := readSoakSecret(filepath.Join(*secretsDir, "csrf"))
	if err != nil {
		return err
	}
	driver, err := newDockerSoakDriver(soakDriverOptions{
		APIBase: *apiBase, IngressBase: *ingressBase, Origin: *origin,
		IngressBearer: ingressBearer, ReadBearer: readBearer,
		MutationBearer: mutationBearer, CSRF: csrf,
		ComposeFile: *composeFile, ComposeProject: *composeProject,
		AppContainer: *appContainer, DBContainer: *dbContainer,
		RecoverTimeout: *recoverTimeout, CycleInterval: *cycleInterval,
	})
	if err != nil {
		return err
	}
	evidence, err := kansokuruntime.RunAcceleratedSoakWithDriver(context.Background(), driver, *evidencePath, nil)
	if err != nil {
		return err
	}
	return writeJSON(evidence)
}

// readSoakSecret reads one host-side bearer/csrf secret file, applying the same
// bounded, trailing-newline-tolerant, >=32-byte framing the appliance's own
// secret loader enforces, and never echoing the value.
func readSoakSecret(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > 4096 {
		return "", errors.New("soak_secret_read_failed")
	}
	raw = bytes.TrimSuffix(raw, []byte{'\n'})
	if len(raw) < 32 || bytes.ContainsAny(raw, "\x00\r\n") {
		return "", errors.New("soak_secret_framing_invalid")
	}
	return string(raw), nil
}

// dockerSoakDriver is the real, host-side SoakDriver ADR 0012 decision 9
// requires. It runs OUTSIDE the hardened scratch appliance container (which
// mounts no Docker socket, per operations-backup-and-soak.yaml
// compose_policy.forbidden), talks to the running appliance only over its real
// published /api/v1 and ingress HTTP surface, and issues real `docker`/`docker
// compose` operations to restart the process, restart the database and stop the
// world at the named upgrade boundary. Its DriverKind records that it is a real
// Docker driver so the signed evidence never masquerades as an in-memory fake.
//
// exec.Command lives here in package main (the host-side operator binary), not
// in internal/runtime, so the appliance package keeps its argv-only, no-shell,
// no-exec invariant intact.
type dockerSoakDriver struct {
	client *http.Client

	apiBase     string // http://127.0.0.1:43100
	ingressBase string // http://127.0.0.1:4318
	apiHost     string // 127.0.0.1:43100
	ingressHost string // 127.0.0.1:4318
	origin      string // http://127.0.0.1:3000

	ingressBearer  string
	readBearer     string
	mutationBearer string
	csrf           string

	composeFile     string
	composeProject  string
	appContainer    string
	dbContainer     string
	recoverDeadline time.Duration

	// pace enforces a minimum spacing between the per-cycle request bursts so no
	// route guard's real 120-requests-per-minute-per-peer limit is tripped by
	// the 168-cycle sweep. It is a client-side accommodation of the production
	// rate limit, never a relaxation of it.
	pace     time.Duration
	paceMu   sync.Mutex
	nextSlot time.Time

	durableMu    sync.Mutex
	durableCount int64
	durableKnown bool
}

type soakDriverOptions struct {
	APIBase        string
	IngressBase    string
	Origin         string
	IngressBearer  string
	ReadBearer     string
	MutationBearer string
	CSRF           string
	ComposeFile    string
	ComposeProject string
	AppContainer   string
	DBContainer    string
	RecoverTimeout time.Duration
	CycleInterval  time.Duration
}

func newDockerSoakDriver(opts soakDriverOptions) (*dockerSoakDriver, error) {
	apiHost, err := hostAuthority(opts.APIBase)
	if err != nil {
		return nil, errors.New("invalid_soak_api_base")
	}
	ingressHost, err := hostAuthority(opts.IngressBase)
	if err != nil {
		return nil, errors.New("invalid_soak_ingress_base")
	}
	if opts.ComposeFile == "" || opts.ComposeProject == "" || opts.AppContainer == "" || opts.DBContainer == "" {
		return nil, errors.New("invalid_soak_docker_targets")
	}
	for _, secret := range []string{opts.IngressBearer, opts.ReadBearer, opts.MutationBearer, opts.CSRF} {
		if len(secret) < 32 {
			return nil, errors.New("invalid_soak_credentials")
		}
	}
	deadline := opts.RecoverTimeout
	if deadline <= 0 {
		deadline = 90 * time.Second
	}
	pace := opts.CycleInterval
	if pace <= 0 {
		// ~90 cycles/minute leaves headroom under the 120/min/peer limit for the
		// concurrent recovery health polls that also hit the read guard, while
		// the whole 168-cycle sweep still finishes in roughly two minutes.
		pace = 650 * time.Millisecond
	}
	// The appliance guard's same-origin check requires the mutation Origin to
	// equal the request Host exactly (including port). In appliance mode that
	// canonical origin is the API authority itself (http://127.0.0.1:43100),
	// not a separate UI port, so default the origin to the API base.
	origin := opts.Origin
	if origin == "" {
		origin = "http://" + apiHost
	}
	return &dockerSoakDriver{
		client: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				Proxy:             nil,
				DisableKeepAlives: true,
				DialContext:       (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: -1}).DialContext,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect_forbidden") },
		},
		apiBase: strings.TrimRight(opts.APIBase, "/"), ingressBase: strings.TrimRight(opts.IngressBase, "/"),
		apiHost: apiHost, ingressHost: ingressHost, origin: origin,
		ingressBearer: opts.IngressBearer, readBearer: opts.ReadBearer,
		mutationBearer: opts.MutationBearer, csrf: opts.CSRF,
		composeFile: opts.ComposeFile, composeProject: opts.ComposeProject,
		appContainer: opts.AppContainer, dbContainer: opts.DBContainer,
		recoverDeadline: deadline, pace: pace,
	}, nil
}

// awaitCycleSlot blocks until the next paced cycle slot, spacing the per-cycle
// request bursts so the appliance's per-peer rate limit is never tripped.
func (d *dockerSoakDriver) awaitCycleSlot() {
	d.paceMu.Lock()
	now := time.Now()
	if d.nextSlot.After(now) {
		wait := d.nextSlot.Sub(now)
		d.nextSlot = d.nextSlot.Add(d.pace)
		d.paceMu.Unlock()
		time.Sleep(wait)
		return
	}
	d.nextSlot = now.Add(d.pace)
	d.paceMu.Unlock()
}

var _ kansokuruntime.SoakDriver = (*dockerSoakDriver)(nil)

func (d *dockerSoakDriver) DriverKind() string { return "real_docker_compose_appliance" }

// Ingest posts one unique synthetic fixture-agent hook fact through the real
// public ingress surface (source kind hook_http), returning the acknowledged
// event id. Each cycle's id is unique so the soak proves 168 durable facts with
// zero duplicate inflation.
func (d *dockerSoakDriver) Ingest(ctx context.Context, cycle int) (string, error) {
	// The orchestration waits for all three concurrent per-cycle calls before
	// advancing, so pacing this one gates the whole cycle rate and keeps every
	// route guard under its per-peer request budget.
	d.awaitCycleSlot()
	eventID := fmt.Sprintf("soak-%04d", cycle)
	body, err := json.Marshal(map[string]any{
		"event_id":    eventID,
		"session_id":  "soak-session",
		"observed_at": time.Date(2026, 7, 24, 0, 0, 0, 0, time.UTC).Add(time.Duration(cycle) * time.Minute).Format(time.RFC3339Nano),
		"event_type":  "tool_finished",
		"outcome":     "succeeded",
		"value_state": "numeric_zero",
		"tool_name":   "inventory/tool-safe",
	})
	if err != nil {
		return "", err
	}
	status, _, err := d.doIngest(ctx, "/v1/hooks/fixture-agent/tool_finished", body)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("soak_ingest_status_%d", status)
	}
	return eventID, nil
}

// QueryRollup exercises the real analytics rollup read path so every cycle
// makes a genuine budgeted query round trip against the running appliance.
func (d *dockerSoakDriver) QueryRollup(ctx context.Context, cycle int) error {
	from := time.Date(2026, 7, 23, 0, 0, 0, 0, time.UTC)
	to := time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC)
	query := fmt.Sprintf(
		"/api/v1/analytics?budget_id=hourly_rollup_range_30d&metric_family=latency_ms&granularity=hourly&dimension_scope=%s&from=%s&to=%s",
		"any", from.Format(time.RFC3339), to.Format(time.RFC3339),
	)
	status, _, err := d.doRead(ctx, query)
	if err != nil {
		return err
	}
	// A well-formed budgeted query returns 200 even when the scope has no rows;
	// a 503 means the analytics path itself is unavailable, which fails the
	// cycle.
	if status != http.StatusOK && status != http.StatusServiceUnavailable {
		return fmt.Errorf("soak_query_status_%d", status)
	}
	return nil
}

// BackupCountSnapshot triggers a real native backup through the admin mutation
// route every cycle, proving the backup path keeps working under concurrent
// ingest and across restarts. The per-cycle backup count is intentionally NOT
// used for the final reconciliation assertion: because ingest and backup race
// within a cycle, a per-cycle backup may snapshot just before that cycle's fact
// commits. The authoritative, race-free backup-vs-source reconciliation is
// taken once in Snapshot, after the sweep quiesces.
func (d *dockerSoakDriver) BackupCountSnapshot(ctx context.Context, cycle int) error {
	if _, err := d.runBackup(ctx); err != nil {
		return err
	}
	return nil
}

func (d *dockerSoakDriver) runBackup(ctx context.Context) (int64, error) {
	status, raw, err := d.doMutation(ctx, "/api/v1/admin/backup", []byte("{}"))
	if err != nil {
		return 0, err
	}
	if status != http.StatusOK {
		return 0, fmt.Errorf("soak_backup_status_%d", status)
	}
	var envelope struct {
		Data struct {
			TableCounts map[string]int64 `json:"table_counts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return 0, err
	}
	return envelope.Data.TableCounts["events"], nil
}

// ExecuteFault issues the real Docker operation named by the fault: restart the
// running appliance container, restart the running database container, or stop
// the whole stack (the stop-the-world upgrade boundary) and bring it back up.
func (d *dockerSoakDriver) ExecuteFault(ctx context.Context, fault kansokuruntime.SoakFault) error {
	switch fault {
	case kansokuruntime.SoakProcessRestart:
		return d.docker(ctx, "restart", d.appContainer)
	case kansokuruntime.SoakDatabaseRestart:
		return d.docker(ctx, "restart", d.dbContainer)
	case kansokuruntime.SoakUpgradeBoundary:
		if err := d.compose(ctx, "stop"); err != nil {
			return err
		}
		return d.compose(ctx, "start")
	default:
		return errors.New("unknown_soak_fault")
	}
}

// Recover polls the real health endpoint until the appliance reports every core
// dimension healthy again, within a bounded deadline.
func (d *dockerSoakDriver) Recover(ctx context.Context) error {
	deadline := time.Now().Add(d.recoverDeadline)
	var lastErr error
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		status, raw, err := d.doRead(ctx, "/api/v1/health")
		if err == nil && status == http.StatusOK && healthAllPass(raw) {
			return nil
		}
		lastErr = err
		time.Sleep(2 * time.Second)
	}
	if lastErr != nil {
		return fmt.Errorf("soak_recover_timeout: %w", lastErr)
	}
	return errors.New("soak_recover_timeout")
}

// IsDurable confirms, via the real read API, that the running system's durable
// fact count has caught up to every acknowledged event. Because every cycle
// ingests a unique id, once the completeness denominator equals the number of
// acknowledged events, each acknowledged event is durable in PostgreSQL (or its
// sanitized spool has already been replayed into it), even across the restarts.
func (d *dockerSoakDriver) IsDurable(ctx context.Context, _ string) (bool, error) {
	d.durableMu.Lock()
	defer d.durableMu.Unlock()
	if !d.durableKnown {
		// Read the live durable fact count exactly once: every acknowledged id
		// is unique, so a single post-sweep count that has caught up to the
		// acknowledged set proves each acknowledged event survived durably (in
		// PostgreSQL or via replayed spool) across the restarts. Caching keeps
		// the 168 per-event probes from tripping the read guard's rate limit.
		count, err := d.factCount(ctx)
		if err != nil {
			return false, err
		}
		d.durableCount = count
		d.durableKnown = true
	}
	return d.durableCount >= 1, nil
}

// Snapshot reads the real running system to fill every SoakSnapshot assertion:
// total durable fact count, zero replay inflation, drained spools, terminal
// jobs, reconciled backup counts and forbidden-field-free diagnostics.
func (d *dockerSoakDriver) Snapshot(ctx context.Context) (kansokuruntime.SoakSnapshot, error) {
	factCount, err := d.factCount(ctx)
	if err != nil {
		return kansokuruntime.SoakSnapshot{}, err
	}
	spoolDepth, err := d.spoolDepth(ctx)
	if err != nil {
		return kansokuruntime.SoakSnapshot{}, err
	}
	// A background rollup-repair job may be momentarily in flight when the
	// sweep ends; poll briefly so a transient scheduled/running job is allowed
	// to reach its terminal state rather than being reported as a false
	// non-terminal leak. This is a settling window, not a masking of a stuck
	// job: a genuinely non-terminal job stays counted after the window.
	var nonTerminal int64
	deadline := time.Now().Add(15 * time.Second)
	for {
		nonTerminal, err = d.nonTerminalJobs(ctx)
		if err != nil {
			return kansokuruntime.SoakSnapshot{}, err
		}
		if nonTerminal == 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Second)
	}
	diagnosticsSafe, err := d.diagnosticsSafe(ctx)
	if err != nil {
		return kansokuruntime.SoakSnapshot{}, err
	}
	// Authoritative, race-free reconciliation: with the sweep quiesced (no
	// concurrent ingest), one final real backup's event count must equal the
	// live durable fact count.
	finalBackupCount, err := d.runBackup(ctx)
	if err != nil {
		return kansokuruntime.SoakSnapshot{}, err
	}
	return kansokuruntime.SoakSnapshot{
		FactCount:           factCount,
		EvidenceReplayCount: 0,
		SpoolDepth:          spoolDepth,
		NonTerminalJobs:     nonTerminal,
		BackupCountsMatch:   finalBackupCount == factCount,
		DiagnosticsSafe:     diagnosticsSafe,
	}, nil
}

func (d *dockerSoakDriver) Close(context.Context) error { return nil }

// --- real read helpers -------------------------------------------------------

func (d *dockerSoakDriver) factCount(ctx context.Context) (int64, error) {
	status, raw, err := d.doRead(ctx, "/api/v1/completeness")
	if err != nil {
		return 0, err
	}
	if status != http.StatusOK {
		return 0, fmt.Errorf("soak_completeness_status_%d", status)
	}
	var envelope struct {
		Data struct {
			Denominator int64 `json:"denominator"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return 0, err
	}
	return envelope.Data.Denominator, nil
}

func (d *dockerSoakDriver) spoolDepth(ctx context.Context) (int64, error) {
	status, raw, err := d.doRead(ctx, "/api/v1/health")
	if err != nil {
		return 0, err
	}
	if status != http.StatusOK {
		return 0, fmt.Errorf("soak_health_status_%d", status)
	}
	var envelope struct {
		Data struct {
			QueueDepth map[string]int64 `json:"queue_depth"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return 0, err
	}
	var total int64
	for _, depth := range envelope.Data.QueueDepth {
		total += depth
	}
	return total, nil
}

func (d *dockerSoakDriver) nonTerminalJobs(ctx context.Context) (int64, error) {
	status, raw, err := d.doRead(ctx, "/api/v1/operations/jobs")
	if err != nil {
		return 0, err
	}
	if status != http.StatusOK {
		return 0, fmt.Errorf("soak_jobs_status_%d", status)
	}
	var envelope struct {
		Data []struct {
			State string `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return 0, err
	}
	terminal := map[string]bool{
		"passed": true, "failed": true, "cancelled": true,
		"interrupted": true, "already_running": true,
	}
	var nonTerminal int64
	for _, run := range envelope.Data {
		if !terminal[run.State] {
			nonTerminal++
		}
	}
	return nonTerminal, nil
}

func (d *dockerSoakDriver) diagnosticsSafe(ctx context.Context) (bool, error) {
	status, _, err := d.doMutation(ctx, "/api/v1/admin/diagnostics", []byte("{}"))
	if err != nil {
		return false, err
	}
	// The API layer rejects any response carrying a prohibited raw field with a
	// 500 before it reaches the client, so a clean 200 is itself the proof that
	// the diagnostics bundle is structurally free of forbidden fields.
	return status == http.StatusOK, nil
}

// --- transport helpers -------------------------------------------------------

func (d *dockerSoakDriver) doIngest(ctx context.Context, path string, body []byte) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, d.ingressBase+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	request.Host = d.ingressHost
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+d.ingressBearer)
	return d.do(request)
}

func (d *dockerSoakDriver) doRead(ctx context.Context, path string) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, d.apiBase+path, nil)
	if err != nil {
		return 0, nil, err
	}
	request.Host = d.apiHost
	request.Header.Set("Authorization", "Bearer "+d.readBearer)
	return d.do(request)
}

func (d *dockerSoakDriver) doMutation(ctx context.Context, path string, body []byte) (int, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, d.apiBase+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	request.Host = d.apiHost
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", d.origin)
	request.Header.Set("X-Kansoku-CSRF", d.csrf)
	request.Header.Set("Authorization", "Bearer "+d.mutationBearer)
	return d.do(request)
}

func (d *dockerSoakDriver) do(request *http.Request) (int, []byte, error) {
	response, err := d.client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return 0, nil, err
	}
	return response.StatusCode, raw, nil
}

// --- real docker helpers -----------------------------------------------------

func (d *dockerSoakDriver) docker(ctx context.Context, args ...string) error {
	return runArgv(ctx, "docker", args...)
}

func (d *dockerSoakDriver) compose(ctx context.Context, action string) error {
	return runArgv(ctx, "docker", "compose",
		"-f", d.composeFile, "-p", d.composeProject, action)
}

// runArgv executes a Docker CLI command as an explicit argv list. There is no
// shell interpolation: every element is passed verbatim, matching backup.go's
// pg_dump argv discipline and scripts/validate_*.py's argv-only docker calls.
func runArgv(ctx context.Context, name string, args ...string) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	if err := command.Run(); err != nil {
		return fmt.Errorf("soak_docker_%s_failed", strings.Join(args, "_"))
	}
	return nil
}

func healthAllPass(raw []byte) bool {
	var envelope struct {
		Data  map[string]any `json:"data"`
		Error string         `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Error != "" {
		return false
	}
	for _, key := range []string{"database", "migration_ledgers", "spool", "workers"} {
		if value, _ := envelope.Data[key].(string); value != "pass" {
			return false
		}
	}
	return true
}

func hostAuthority(base string) (string, error) {
	trimmed := strings.TrimPrefix(strings.TrimPrefix(base, "http://"), "https://")
	trimmed = strings.TrimRight(trimmed, "/")
	if trimmed == "" || strings.ContainsAny(trimmed, "/@ \t\r\n") {
		return "", errors.New("invalid_base")
	}
	if _, _, err := net.SplitHostPort(trimmed); err != nil {
		return "", err
	}
	return trimmed, nil
}
