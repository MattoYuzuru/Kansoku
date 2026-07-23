package claudeadapter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"regexp"
	"sort"

	"kansoku.local/kansoku/internal/adaptersdk"
)

// versionProbeArgs is the exact, credential-free claude --version invocation.
// contracts/claude/manifest.yaml's installation_discovery.steps requires this
// probe to never capture login/auth output; --version is the only argument
// ever passed, and HostView.ExecProbe strips the parent environment and
// truncates output before this package ever sees it.
var versionProbeArgs = []string{"--version"}

// claudeVersionPattern extracts a bare semver-shaped token from claude
// --version output without assuming any surrounding text. It is deliberately
// permissive about a leading "claude" label and trailing build metadata but
// never treats the whole stdout blob as a version.
var claudeVersionPattern = regexp.MustCompile(`\d+\.\d+\.\d+(?:[-+][0-9A-Za-z.\-]+)?`)

// surfaceProbe names a documented, observable marker for one Claude Code
// surface beneath a resolved config root. Detecting a marker never merges the
// candidate into another surface's candidate purely because they share a
// config root; each surface that is observable produces its own
// InstallationCandidate.
type surfaceProbe struct {
	surface   Surface
	relPath   string
	surfaceID string
}

var surfaceProbes = []surfaceProbe{
	{surface: SurfaceCLI, relPath: "settings.json", surfaceID: "claude-cli"},
	{surface: SurfaceIDEExtension, relPath: "ide-extension.json", surfaceID: "claude-ide-extension"},
	{surface: SurfaceApp, relPath: "app.json", surfaceID: "claude-app"},
}

// fingerprintTargets are the settings/hook/skill/plugin manifest locations
// contracts/claude/manifest.yaml's installation_discovery step 4 requires
// Discover to fingerprint without ever recording their values -- only
// existence, size and modification time (via HostView.ReadProbe) ever leave
// this function.
var fingerprintTargets = []string{
	"settings.json",
	"hooks",
	"skills",
	"plugins",
	"commands",
	"agents",
}

// Discover resolves Claude Code installation candidates strictly from
// HostView.AllowedRoots -- which the caller must already have populated from
// the documented claude_user_settings/claude_project_settings/
// claude_managed_settings locations before any documented default, never from
// a speculative home-directory scan -- and reports one candidate per
// observable surface beneath each resolved root. Version and fingerprint
// evidence are attached without ever exposing a login/auth token or a raw
// settings/hook/skill value. Two candidates sharing one config root but
// differing surface or detection method remain distinct installation
// candidates, matching contracts/claude/manifest.yaml's
// installation_merge_rule verbatim.
func (a *Adapter) Discover(ctx context.Context, host *adaptersdk.HostView) ([]adaptersdk.InstallationCandidate, error) {
	if host == nil {
		return nil, errors.New("claude_discover_requires_host_view")
	}
	var candidates []adaptersdk.InstallationCandidate
	detectedVersion, versionMethod := probeClaudeVersion(ctx, host)
	for _, root := range host.AllowedRoots() {
		for _, probe := range surfaceProbes {
			result, err := host.ReadProbe(filepath.Join(root, probe.relPath))
			if err != nil {
				if errors.Is(err, adaptersdk.ErrOutsideAllowedRoots) {
					continue
				}
				return nil, err
			}
			if !result.Exists {
				continue
			}
			method := adaptersdk.DetectionDocumentedStateRoot
			confidence := 0.7
			if versionMethod != "" {
				method = versionMethod
				confidence = 0.95
			}
			candidates = append(candidates, adaptersdk.InstallationCandidate{
				CandidateID:     "claudecand_" + stableHex(root, string(probe.surface)),
				AdapterID:       AdapterID,
				SurfaceID:       probe.surfaceID,
				StateRoot:       root,
				DetectedVersion: detectedVersion,
				DetectionMethod: method,
				Confidence:      confidence,
			})
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].CandidateID < candidates[j].CandidateID })
	return candidates, nil
}

// probeClaudeVersion runs the documented, credential-free claude --version
// probe. It never inspects or forwards any login/auth output: HostView's
// ExecProbe already strips the parent environment and bounds stdout size, and
// this function only ever extracts a bare version token via
// claudeVersionPattern, discarding everything else including any surrounding
// banner text that might otherwise carry account information.
func probeClaudeVersion(ctx context.Context, host *adaptersdk.HostView) (string, adaptersdk.DetectionMethod) {
	result, err := host.ExecProbe(ctx, executableName, versionProbeArgs...)
	if err != nil || result.ExitCode != 0 {
		return "unknown", ""
	}
	version := claudeVersionPattern.FindString(result.Stdout)
	if version == "" {
		return "unknown", ""
	}
	return version, adaptersdk.DetectionExecutableOnPath
}

// FingerprintTargets returns the closed, documented list of settings/hook/
// skill/plugin/command/agent locations installation discovery fingerprints
// beneath a resolved Claude Code config root. It is exported so a later
// stage's Inventory implementation reuses the identical closed list rather
// than redeclaring it.
func FingerprintTargets() []string {
	return append([]string(nil), fingerprintTargets...)
}

// FingerprintInstallation bounds-reads (existence, size, mtime only -- never
// content) every fingerprint target beneath root via HostView and returns a
// stable, non-reversible fingerprint of that shape. Two installations with
// identical file shapes but different raw byte content are indistinguishable
// here by design: this function never records a value, only the presence/
// size/mtime shape of the settings/hook/skill/plugin/command/agent surface.
func FingerprintInstallation(host *adaptersdk.HostView, root string) (string, error) {
	if host == nil {
		return "", errors.New("claude_fingerprint_requires_host_view")
	}
	hash := sha256.New()
	hash.Write([]byte("claude-installation-fingerprint/1"))
	for _, target := range fingerprintTargets {
		result, err := host.ReadProbe(filepath.Join(root, target))
		if err != nil {
			if errors.Is(err, adaptersdk.ErrOutsideAllowedRoots) {
				continue
			}
			return "", err
		}
		hash.Write([]byte{0})
		hash.Write([]byte(target))
		hash.Write([]byte{0})
		if result.Exists {
			hash.Write([]byte("present"))
			hash.Write([]byte{0})
			hash.Write([]byte(sizeClass(result.SizeBytes)))
		} else {
			hash.Write([]byte("absent"))
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// sizeClass buckets a byte count into a coarse class so the fingerprint
// changes when a settings/hook/skill file's size changes materially, without
// ever encoding (and thus letting a later stage reconstruct) the exact byte
// count of potentially sensitive file contents.
func sizeClass(size int64) string {
	switch {
	case size <= 0:
		return "empty"
	case size < 1<<10:
		return "lt1k"
	case size < 1<<12:
		return "lt4k"
	case size < 1<<16:
		return "lt64k"
	case size < 1<<20:
		return "lt1m"
	default:
		return "ge1m"
	}
}

func stableHex(values ...string) string {
	hash := sha256.New()
	for _, value := range values {
		hash.Write([]byte{0})
		hash.Write([]byte(value))
	}
	return hex.EncodeToString(hash.Sum(nil))[:32]
}
