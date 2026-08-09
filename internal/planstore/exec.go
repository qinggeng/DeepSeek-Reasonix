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
// driver). The feedback threshold and summary bounds live on the Store
// (MaxScriptOutput / SummaryHeadLines / SummaryErrorLines, defaults
// DefaultMaxScriptOutput etc. — Sprint 11 A1), so tests can inject small
// values; the timeout stays a constant because nothing is configurable about
// it in practice.
const acceptanceScriptTimeout = 60 * time.Second

// acceptanceOutputRel is the driver's full-output spill directory under the
// workspace: .reasonix/acceptance-output/<plan-id>/round-<N>.log. It lives
// under .reasonix (excluded from the baseline comparison, so it never pollutes
// the change manifest) but OUTSIDE .reasonix/plans, so the model's bash
// sandbox can read it (strict mode only forbids the Reasonix home and the
// plans dir — see builtin/bash.go strictBashSpec). The spill file is written
// by the driver, never by the model; DeletePlan removes it with the plan.
const acceptanceOutputRel = ".reasonix/acceptance-output"

// ScriptResult is the outcome of running the acceptance script: the process
// exit code (0 = accepted, non-zero = rejected) plus the feedback injected
// into the next turn. A non-zero exit code is a *rejection result*, not an
// error — errors are reserved for "could not execute at all" (missing
// interpreter, timeout, write failure).
type ScriptResult struct {
	ExitCode int
	// Output is what the driver injects as feedback: the full combined output
	// when it fits within Store.MaxScriptOutput, otherwise a bounded summary
	// (head lines + line count + error lines) that names the full-output file.
	Output string
	// OutputFile is the absolute path of the spilled full output when the
	// output exceeded the threshold ("" when it was small enough to inject
	// whole). The model's bash can read it; CompareBaseline ignores it.
	OutputFile string
}

// acceptanceOutputDir returns the spill directory for one plan's acceptance
// outputs (<ws>/.reasonix/acceptance-output/<id>).
func (s *Store) acceptanceOutputDir(id string) string {
	return filepath.Join(s.wsRoot, acceptanceOutputRel, id)
}

// RunAcceptanceScript decrypts the plan's validate.py and executes it in the
// driver's private temp directory with the workspace as the working directory
// (so the script sees workspace files), then cleans up. The script plaintext
// never lands inside the workspace — the model's bash sandbox cannot read the
// temp directory outside the workspace root. Exit code 0 is returned as a
// zero result; non-zero exits are returned as a ScriptResult with the code; a
// timeout or an unexecutable script is an error.
//
// Output handling (Sprint 11 A1): outputs within Store.MaxScriptOutput are
// injected whole (pre-Sprint-11 behavior); larger outputs are summarized and
// the full output is spilled to .reasonix/acceptance-output/<id>/round-<N>.log
// (round is the caller's loop round, kept in the file name so a later round
// never overwrites a path an earlier feedback referenced). If the spill write
// itself fails, the run degrades to a hard cap with a warning instead of
// failing the acceptance result.
func (s *Store) RunAcceptanceScript(ctx context.Context, id string, round int) (ScriptResult, error) {
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
	result := ScriptResult{Output: string(out)}
	if len(result.Output) > s.MaxScriptOutput {
		result = s.summarizeAndSpill(id, round, result.Output)
	}
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

// summarizeAndSpill replaces an oversized script output with a bounded summary
// and writes the full output to <ws>/.reasonix/acceptance-output/<id>/round-<N>.log.
// The summary names the file so the model knows where the full output is. A
// spill failure degrades to a hard cap (the model still gets a bounded view)
// rather than failing the acceptance result.
func (s *Store) summarizeAndSpill(id string, round int, full string) ScriptResult {
	spillDir := s.acceptanceOutputDir(id)
	if err := os.MkdirAll(spillDir, 0o700); err == nil {
		spillPath := filepath.Join(spillDir, fmt.Sprintf("round-%d.log", round))
		if err := os.WriteFile(spillPath, []byte(full), 0o600); err == nil {
			return ScriptResult{Output: summarizeScriptOutput(full, s.SummaryHeadLines, s.SummaryErrorLines, spillPath, s.MaxScriptOutput), OutputFile: spillPath}
		}
	}
	// Degrade: hard cap + warning (pre-Sprint-11 behavior with a reason).
	return ScriptResult{Output: capScriptOutput(full, s.MaxScriptOutput) + "\n(warning: could not spill the full acceptance output to disk)"}
}

// summaryLineCap caps a single summary line in runes so one oversized line
// (e.g. a giant single-line JSON blob) cannot blow the summary byte budget —
// the size-based split must bound the INJECTED feedback, not just the line
// count (review finding, Sprint 11).
const summaryLineCap = 200

// summarizeScriptOutput builds the injected feedback for an oversized script
// output: a header (line/byte count + the full-output file path), the first
// headLines lines, and up to errorLines lines matching common failure markers.
// It is bounded and deterministic — the total stays within budget bytes (never
// a hard character cut of the body, but a byte-bounded excerpt).
func summarizeScriptOutput(s string, headLines, errorLines int, spillPath string, budget int) string {
	lines := splitOutputLines(s)
	// head gets ~3/5 of the budget, error lines ~2/5; the fixed header/footer
	// overhead is a few hundred bytes, so the joined result stays under budget.
	head := boundedLines(lines, headLines, budget*3/5)
	errs := boundedLines(errorLinesFrom(lines, errorLines), errorLines, budget*2/5)
	var b strings.Builder
	fmt.Fprintf(&b, "(script output too large: %d lines, %d bytes — full output saved to %s)\n", len(lines), len(s), spillPath)
	b.WriteString("--- first lines ---\n")
	b.WriteString(strings.Join(head, "\n"))
	if len(errs) > 0 {
		fmt.Fprintf(&b, "\n--- error lines (%d matched) ---\n", len(errs))
		b.WriteString(strings.Join(errs, "\n"))
	}
	if len(lines) > len(head) {
		fmt.Fprintf(&b, "\n... (%d more lines)", len(lines)-len(head))
	}
	return b.String()
}

// boundedLines returns at most maxLines lines from lines, each truncated to
// summaryLineCap runes, while the joined byte size stays within maxBytes. It
// always keeps at least the first line (truncated), so the summary is never
// empty.
func boundedLines(lines []string, maxLines, maxBytes int) []string {
	if maxLines <= 0 {
		maxLines = 1
	}
	if maxBytes <= 0 {
		maxBytes = 1
	}
	out := make([]string, 0, maxLines)
	used := 0
	for _, l := range lines {
		if len(out) >= maxLines {
			break
		}
		t := truncateRunes(l, summaryLineCap)
		if used+len(t)+1 > maxBytes && len(out) > 0 {
			break
		}
		out = append(out, t)
		used += len(t) + 1
	}
	return out
}

// truncateRunes cuts s to at most n runes, appending a single ellipsis when
// anything was cut. rune-based so multi-byte (CJK) content is never split.
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// splitOutputLines splits combined output into lines, dropping a trailing
// empty segment so "a\nb\n" counts as 2 lines, not 3.
func splitOutputLines(s string) []string {
	lines := strings.Split(s, "\n")
	if n := len(lines); n > 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	return lines
}

// errorLineMarkers are case-insensitive failure patterns that make a line a
// "key error line" for the oversized-output summary.
var errorLineMarkers = []string{"error", "fail", "traceback", "exception", "panic", "assert"}

// errorLinesFrom returns the lines matching any failure marker, capped at cap.
func errorLinesFrom(lines []string, cap int) []string {
	var out []string
	for _, l := range lines {
		lower := strings.ToLower(l)
		for _, m := range errorLineMarkers {
			if strings.Contains(lower, m) {
				out = append(out, l)
				break
			}
		}
		if len(out) >= cap {
			return out
		}
	}
	return out
}

// capScriptOutput is the degraded fallback when the full output cannot be
// spilled: a hard character cap with a truncation marker (the pre-Sprint-11
// behavior).
func capScriptOutput(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n…(output truncated)"
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
