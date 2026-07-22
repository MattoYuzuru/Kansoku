package adaptersdk

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrOutsideAllowedRoots is returned by HostView probes when a path or
// command falls outside the documented, already-allowed roots. Discovery
// never speculatively scans an entire home directory: every probe is
// checked against this exact allowlist before any syscall happens.
var ErrOutsideAllowedRoots = errors.New("outside_allowed_roots")

// ErrDisallowedExec is returned when an exec probe target is not on the
// closed, credential-free allowlist a HostView was constructed with.
var ErrDisallowedExec = errors.New("disallowed_exec_probe")

// ReadProbeResult is a bounded, best-effort filesystem read used only for
// discovery/inventory. It never returns database credentials or arbitrary
// file content beyond the declared size limit.
type ReadProbeResult struct {
	Exists    bool
	SizeBytes int64
	ModTime   time.Time
}

// ExecProbeResult is the bounded, sanitization-friendly output of a
// credential-free version/help/status subprocess probe.
type ExecProbeResult struct {
	ExitCode int
	Stdout   string
}

const maxExecProbeOutputBytes = 4096

// HostView is the only surface an Adapter uses to touch the host. It never
// exposes a database connection string, credential or unscoped filesystem
// handle -- only permission-checked read/exec probes scoped to declared
// roots, plus a device-scoped path pseudonymizer.
type HostView struct {
	allowedRoots []string
	allowedExecs map[string]struct{}
	pseudonymKey []byte
	execCommand  func(ctx context.Context, name string, args ...string) ([]byte, int, error)
}

// NewHostView builds a HostView scoped to exactly the given allowed roots
// (already resolved from documented env/config, never a home-directory
// default) and a closed allowlist of credential-free exec probe binaries.
// pseudonymKey must be at least 32 bytes; it is the same class of
// device-scoped HMAC key internal/privacy's Lineage construction uses, and
// it is never itself exposed to an Adapter.
func NewHostView(allowedRoots []string, allowedExecs []string, pseudonymKey []byte) (*HostView, error) {
	if len(pseudonymKey) < 32 {
		return nil, errors.New("invalid_pseudonym_key")
	}
	cleanRoots := make([]string, 0, len(allowedRoots))
	for _, root := range allowedRoots {
		if !filepath.IsAbs(root) {
			return nil, errors.New("allowed_root_must_be_absolute")
		}
		cleanRoots = append(cleanRoots, filepath.Clean(root))
	}
	execSet := make(map[string]struct{}, len(allowedExecs))
	for _, name := range allowedExecs {
		execSet[name] = struct{}{}
	}
	return &HostView{
		allowedRoots: cleanRoots,
		allowedExecs: execSet,
		pseudonymKey: append([]byte(nil), pseudonymKey...),
		execCommand:  defaultExecCommand,
	}, nil
}

// SetExecCommandForTest overrides the subprocess runner so conformance
// suites can exercise ExecProbe deterministically without depending on any
// binary actually being installed on the test host.
func (h *HostView) SetExecCommandForTest(fn func(ctx context.Context, name string, args ...string) ([]byte, int, error)) {
	h.execCommand = fn
}

// AllowedRoots returns a copy of the resolved, already-allowed state roots.
func (h *HostView) AllowedRoots() []string { return append([]string(nil), h.allowedRoots...) }

func (h *HostView) withinAllowedRoots(path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	clean := filepath.Clean(path)
	for _, root := range h.allowedRoots {
		if clean == root || strings.HasPrefix(clean, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// ReadProbe performs a bounded stat-only read of path. It refuses to
// follow the path at all -- not even to stat -- unless it resolves inside
// an already-allowed root, and it never returns file content.
func (h *HostView) ReadProbe(path string) (ReadProbeResult, error) {
	if !h.withinAllowedRoots(path) {
		return ReadProbeResult{}, ErrOutsideAllowedRoots
	}
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ReadProbeResult{Exists: false}, nil
		}
		return ReadProbeResult{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return ReadProbeResult{}, err
		}
		resolved := target
		if !filepath.IsAbs(resolved) {
			resolved = filepath.Join(filepath.Dir(path), resolved)
		}
		if !h.withinAllowedRoots(resolved) {
			return ReadProbeResult{}, ErrOutsideAllowedRoots
		}
		info, err = os.Stat(resolved)
		if err != nil {
			if os.IsNotExist(err) {
				return ReadProbeResult{Exists: false}, nil
			}
			return ReadProbeResult{}, err
		}
	}
	return ReadProbeResult{Exists: true, SizeBytes: info.Size(), ModTime: info.ModTime()}, nil
}

// ExecProbe runs a credential-free version/help/status subprocess. name
// must be on the HostView's closed exec allowlist; no environment is
// inherited from the parent process, and output is truncated to a bounded
// byte ceiling before it is ever returned to an adapter.
func (h *HostView) ExecProbe(ctx context.Context, name string, args ...string) (ExecProbeResult, error) {
	if _, ok := h.allowedExecs[name]; !ok {
		return ExecProbeResult{}, ErrDisallowedExec
	}
	output, exitCode, err := h.execCommand(ctx, name, args...)
	if err != nil {
		return ExecProbeResult{}, err
	}
	if len(output) > maxExecProbeOutputBytes {
		output = output[:maxExecProbeOutputBytes]
	}
	return ExecProbeResult{ExitCode: exitCode, Stdout: string(output)}, nil
}

func defaultExecCommand(ctx context.Context, name string, args ...string) ([]byte, int, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = []string{} // explicit allowlist only: no inherited parent environment
	output, err := cmd.Output()
	exitCode := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
		err = nil
	}
	return output, exitCode, err
}

// PseudonymizePath derives a stable, non-reversible pseudonym for a
// filesystem path. This is the only durable representation of a path an
// Adapter may place in a Node.PathPseudonym field; the raw path itself is
// never persisted.
func (h *HostView) PseudonymizePath(path string) string {
	mac := hmac.New(sha256.New, h.pseudonymKey)
	_, _ = mac.Write([]byte("adaptersdk-path-pseudonym/1\x00" + filepath.Clean(path)))
	return "hmac-sha256:" + hex.EncodeToString(mac.Sum(nil))
}
