package planstore

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Acceptance-script execution constants: a runaway script is killed after the
// timeout (the script is model-authored, so a livelock must not hang the
// driver), and the feedback output is capped so a noisy script cannot drown
// the rejection message injected into the next turn.
const (
	acceptanceScriptTimeout = 60 * time.Second
	maxScriptOutput         = 8192
)

// ScriptResult is the outcome of running the acceptance script: the process
// exit code (0 = accepted, non-zero = rejected) plus its combined output for
// feedback. A non-zero exit code is a *rejection result*, not an error — errors
// are reserved for "could not execute at all" (missing interpreter, timeout,
// write failure).
type ScriptResult struct {
	ExitCode int
	Output   string
}

// RunAcceptanceScript decrypts the plan's validate.py and executes it in the
// driver's private temp directory with the workspace as the working directory
// (so the script sees workspace files), then cleans up. The script plaintext
// never lands inside the workspace — the model's bash sandbox cannot read the
// temp directory outside the workspace root. Exit code 0 is returned as a
// zero result; non-zero exits are returned as a ScriptResult with the code; a
// timeout or an unexecutable script is an error.
func (s *Store) RunAcceptanceScript(ctx context.Context, id string) (ScriptResult, error) {
	if err := validPlanID(id); err != nil {
		return ScriptResult{}, err
	}
	script, err := s.decryptFile(filepath.Join(s.plansDir, id, scriptFile))
	if err != nil {
		return ScriptResult{}, err
	}
	tmpDir, err := os.MkdirTemp("", "reasonix-plan-exec-*")
	if err != nil {
		return ScriptResult{}, fmt.Errorf("planstore: create acceptance-script temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	scriptPath := filepath.Join(tmpDir, "validate.py")
	if err := os.WriteFile(scriptPath, script, 0o600); err != nil {
		return ScriptResult{}, fmt.Errorf("planstore: write acceptance script to temp dir: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, acceptanceScriptTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, PythonExecutable(), scriptPath)
	cmd.Dir = s.wsRoot
	// Defense in depth: strip secret-bearing environment before the (model-
	// authored, human-reviewed) script runs — the master-key home must not be
	// reachable through a leaked env var even though the script itself is the
	// review-gated trust boundary. Non-sensitive vars (PATH, PYTHONPATH, …)
	// survive so the interpreter still works.
	cmd.Env = sanitizedExecEnv(os.Environ())
	out, err := cmd.CombinedOutput()
	result := ScriptResult{Output: truncateOutput(string(out))}
	if err == nil {
		return result, nil
	}
	if ctx.Err() == context.DeadlineExceeded {
		return ScriptResult{}, fmt.Errorf("planstore: acceptance script for %q timed out after %s", id, acceptanceScriptTimeout)
	}
	if ee, ok := err.(*exec.ExitError); ok {
		result.ExitCode = ee.ExitCode()
		return result, nil
	}
	return ScriptResult{}, fmt.Errorf("planstore: run acceptance script for %q: %w", id, err)
}

func truncateOutput(s string) string {
	if len(s) <= maxScriptOutput {
		return s
	}
	return s[:maxScriptOutput] + "\n…(output truncated)"
}

// sanitizedExecEnv filters secret-bearing environment variables out of the
// acceptance-script subprocess: REASONIX_HOME (the master-key home) plus any
// var whose uppercase name contains a secret marker. Everything else passes
// through so the interpreter and the workspace behave as expected.
func sanitizedExecEnv(environ []string) []string {
	secretMarkers := []string{"KEY", "TOKEN", "SECRET", "PASSWORD", "API_KEY", "AUTH"}
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name := kv
		if i := strings.IndexByte(kv, '='); i >= 0 {
			name = kv[:i]
		}
		up := strings.ToUpper(name)
		if up == "REASONIX_HOME" {
			continue
		}
		skip := false
		for _, m := range secretMarkers {
			if strings.Contains(up, m) {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, kv)
		}
	}
	return out
}

// PythonExecutable resolves the interpreter used for acceptance scripts and
// syntax checks. It prefers a portable Python installed next to the Reasonix
// binary (the desktop convention: <exe-dir>/python/python.exe, falling back to
// <exe-dir>/python.exe), so a deployed desktop does not depend on PATH having
// python; otherwise it falls back to PATH resolution ("python"). All detection
// is per-call, so a Python added to the install directory after startup is
// picked up immediately.
func PythonExecutable() string {
	exe, err := os.Executable()
	if err != nil {
		return "python"
	}
	return PythonExecutableFrom(filepath.Dir(exe))
}

// PythonExecutableFrom is the pure detection core, split out for tests.
func PythonExecutableFrom(exeDir string) string {
	for _, cand := range []string{
		filepath.Join(exeDir, "python", "python.exe"),
		filepath.Join(exeDir, "python.exe"),
	} {
		if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
			return cand
		}
	}
	return "python"
}
