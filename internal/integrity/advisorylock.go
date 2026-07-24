package integrity

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AdvisoryLockKeyName is the fixed literal audit-run-and-schedule.yaml's
// `lock_key_derivation` names: "a fixed 64-bit hash of the literal string
// kansoku.daily-integrity, one key for the whole workflow so at most one
// audit_run is running anywhere at a time".
const AdvisoryLockKeyName = "kansoku.daily-integrity"

// AdvisoryLockKey is the deterministic 64-bit signed integer PostgreSQL's
// pg_try_advisory_lock(bigint) family expects, derived once from
// AdvisoryLockKeyName. FNV-1a is used because it is a stable, dependency-
// free stdlib hash (hash/fnv) rather than pulling in a new package for a
// single deterministic integer derivation, matching ADR 0011's "no new
// external job-scheduling dependency" decision.
func AdvisoryLockKey() int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(AdvisoryLockKeyName))
	// pg_try_advisory_lock(bigint) takes a signed 64-bit key; converting the
	// unsigned FNV sum to int64 by reinterpreting its bits is safe and
	// deterministic (PostgreSQL treats the key as an opaque 64-bit lock
	// space, not a magnitude-ordered value).
	return int64(h.Sum64())
}

// ErrLockNotAcquired is returned by AcquireLock when another session
// already holds the advisory lock, matching the contract's
// non_overlap_guarantee: "a second scheduler instance or a manual trigger
// that fails to acquire the lock must record a skipped attempt and never
// start a concurrent audit_run row".
var ErrLockNotAcquired = errors.New("audit_run_advisory_lock_not_acquired")

// HeldLock represents a session-scoped PostgreSQL advisory lock held on one
// pinned pool connection. The lock is released either explicitly via
// Release or automatically when the underlying connection closes (e.g. the
// process crashes), which is exactly the crash-safety property ADR 0011
// requires without a manual unlock step. Callers must call Release exactly
// once when done (idempotent double-Release is safe).
type HeldLock struct {
	key      int64
	conn     *pgxpool.Conn
	released bool
}

// AcquireLock attempts to take the single daily-integrity session-scoped
// advisory lock on a connection pinned out of pool (internal/dataplatform's
// existing *pgxpool.Pool -- this package opens no second connection pool).
// It returns ErrLockNotAcquired, never blocking, if another session already
// holds the lock, so a caller can record a clean "already running" skip
// rather than racing for it.
func AcquireLock(ctx context.Context, pool *pgxpool.Pool) (*HeldLock, error) {
	return acquireLockWithKey(ctx, pool, AdvisoryLockKey())
}

// acquireLockWithKey is the key-parameterized implementation AcquireLock
// wraps; it exists separately so tests can exercise mutual exclusion with a
// dedicated key that cannot collide with a concurrently running real
// scheduler or another test using the package-default key.
func acquireLockWithKey(ctx context.Context, pool *pgxpool.Pool, key int64) (*HeldLock, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire pool connection: %w", err)
	}
	var acquired bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, key).Scan(&acquired); err != nil {
		conn.Release()
		return nil, fmt.Errorf("pg_try_advisory_lock: %w", err)
	}
	if !acquired {
		conn.Release()
		return nil, ErrLockNotAcquired
	}
	return &HeldLock{key: key, conn: conn}, nil
}

// Release unlocks the advisory lock and returns the pinned connection to
// the pool. It is safe to call more than once; only the first call has any
// effect.
func (l *HeldLock) Release(ctx context.Context) error {
	if l == nil || l.released {
		return nil
	}
	l.released = true
	defer l.conn.Release()
	_, err := l.conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, l.key)
	return err
}

// IsHeldByAnySession reports whether the daily-integrity advisory lock is
// currently held by any session (including this process's own held lock,
// if any). This is used by crash-recovery to decide whether a durable
// audit_run row still marked "running" corresponds to a live in-progress
// run or a stale one left behind by a crashed process (see
// MarkStaleRunsInterrupted): "any audit_run row still in state running
// whose advisory lock is not currently held by a live session is marked
// interrupted."
//
// Rather than parse pg_locks' classid/objid bit-packed encoding of a 64-bit
// advisory lock key (fragile, PostgreSQL-internal-representation-coupled),
// this uses the standard non-blocking-probe idiom: attempt to acquire the
// lock on a freshly pinned connection; if that attempt succeeds, no one
// else holds it, so immediately release and report "not held"; if the
// attempt fails, some other live session holds it, so report "held"
// without ever blocking the caller.
func IsHeldByAnySession(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	return isKeyHeldByAnySession(ctx, pool, AdvisoryLockKey())
}

func isKeyHeldByAnySession(ctx context.Context, pool *pgxpool.Pool, key int64) (bool, error) {
	held, err := acquireLockWithKey(ctx, pool, key)
	if err != nil {
		if errors.Is(err, ErrLockNotAcquired) {
			return true, nil
		}
		return false, err
	}
	if err := held.Release(ctx); err != nil {
		return false, err
	}
	return false, nil
}
