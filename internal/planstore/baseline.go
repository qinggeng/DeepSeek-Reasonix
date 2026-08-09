package planstore

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const baselineName = "baseline.json"

// baselineFile is the on-disk baseline snapshot: the git HEAD commit hash
// captured at lock-freeze time. CompareBaseline diffs the working tree against
// this commit, so strict plan execution requires the workspace to be a git
// repository — both TakeBaseline and the /strict-plan-exec command fail closed
// on a non-git workspace (the user is responsible for making a workspace
// strict-plan-ready, the driver does not substitute a hash walk for git).
type baselineFile struct {
	Head string `json:"head"`
}

// CheckGitWorkspace verifies the workspace is a git repository with a HEAD
// commit — the precondition of strict plan execution (baseline freeze +
// change-manifest comparison both diff against git). Fails closed with a
// diagnosable message; the user is responsible for making the workspace
// strict-plan-ready.
func CheckGitWorkspace(wsRoot string) error {
	if _, err := gitHead(wsRoot); err != nil {
		return fmt.Errorf("strict plan execution needs a git workspace (git rev-parse HEAD failed): %w", err)
	}
	return nil
}

// TakeBaseline freezes the workspace baseline at lock time: it records the
// current git HEAD commit into the plan's baseline.json. Later execution turns
// call CompareBaseline, which diffs the working tree against this commit and
// attributes every actual file change to the frozen change manifest.
func (s *Store) TakeBaseline(id string) error {
	if err := validPlanID(id); err != nil {
		return err
	}
	dir := filepath.Join(s.plansDir, id)
	// fail closed on a missing plan: never snapshot against a phantom id.
	if _, err := os.Stat(filepath.Join(dir, runStateFile)); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("planstore: plan %q does not exist; cannot take a baseline", id)
		}
		return fmt.Errorf("planstore: stat plan dir: %w", err)
	}
	head, err := gitHead(s.wsRoot)
	if err != nil {
		return fmt.Errorf("planstore: strict plan baseline needs a git workspace (git rev-parse HEAD failed): %w", err)
	}
	if err := writeJSON(filepath.Join(dir, baselineName), baselineFile{Head: head}); err != nil {
		return fmt.Errorf("planstore: write baseline: %w", err)
	}
	return nil
}

// BaselineDiff is one actual file change observed by CompareBaseline. Path is
// workspace-relative with forward slashes; Action is add|modify|delete.
// InManifest reports whether the change matches a frozen manifest entry.
type BaselineDiff struct {
	Path       string `json:"path"`
	Action     string `json:"action"`
	InManifest bool   `json:"inManifest"`
}

// BaselineComparison is the outcome of CompareBaseline: every actual change
// with its manifest match status, plus every manifest entry that never
// happened (missing). Both directions matter — out-of-manifest changes are
// scope violations ("touched what the plan did not declare"), missing entries
// are incomplete-execution violations ("declared but not done").
type BaselineComparison struct {
	Actual  []BaselineDiff
	Missing []ManifestEntry
}

// OutOfManifest returns the actual changes that fall outside the frozen
// manifest (scope violations).
func (c BaselineComparison) OutOfManifest() []BaselineDiff {
	var out []BaselineDiff
	for _, d := range c.Actual {
		if !d.InManifest {
			out = append(out, d)
		}
	}
	return out
}

// OK reports whether the comparison passes: no out-of-manifest change and no
// missing manifest entry.
func (c BaselineComparison) OK() bool {
	return len(c.OutOfManifest()) == 0 && len(c.Missing) == 0
}

// CompareBaseline diffs the current workspace against the baseline commit
// frozen at lock time and attributes every actual file change to the plan's
// change manifest. It returns the actual changes (each flagged InManifest) and
// the manifest entries that never happened. A workspace that is no longer a
// git repository, a plan without a baseline, or a bad plan id all fail closed.
func (s *Store) CompareBaseline(id string) (BaselineComparison, error) {
	if err := validPlanID(id); err != nil {
		return BaselineComparison{}, err
	}
	dir := filepath.Join(s.plansDir, id)
	var bl baselineFile
	if err := readJSON(filepath.Join(dir, baselineName), &bl); err != nil {
		if os.IsNotExist(err) {
			return BaselineComparison{}, fmt.Errorf("planstore: plan %q has no baseline (was it locked without TakeBaseline?): %w", id, err)
		}
		return BaselineComparison{}, fmt.Errorf("planstore: read baseline: %w", err)
	}
	if bl.Head == "" {
		return BaselineComparison{}, fmt.Errorf("planstore: plan %q baseline has no head commit; workspace is not strict-plan-ready", id)
	}

	actual, err := s.actualChanges(bl.Head)
	if err != nil {
		return BaselineComparison{}, err
	}
	files, err := s.ReadPlan(id)
	if err != nil {
		return BaselineComparison{}, err
	}

	comp := BaselineComparison{Actual: make([]BaselineDiff, 0, len(actual))}
	seen := make(map[int]bool, len(files.Manifest)) // manifest indices that happened
	for path, action := range actual {
		d := BaselineDiff{Path: path, Action: action}
		for i, m := range files.Manifest {
			if manifestMatches(m, path, action) {
				d.InManifest = true
				seen[i] = true
				break
			}
		}
		comp.Actual = append(comp.Actual, d)
	}
	for i, m := range files.Manifest {
		if !seen[i] {
			comp.Missing = append(comp.Missing, m)
		}
	}
	return comp, nil
}

// actualChanges computes the working-tree changes relative to a baseline
// commit: git diff --name-status (tracked M/D/A, staged + unstaged) plus
// untracked files from git status --porcelain (add). The driver's own state
// under .reasonix is filtered out — the plan store and keys are never part of
// the change comparison.
func (s *Store) actualChanges(head string) (map[string]string, error) {
	diff, err := gitCmd(s.wsRoot, "-c", "core.quotepath=false", "diff", "--name-status", "--no-renames", head)
	if err != nil {
		return nil, fmt.Errorf("planstore: git diff against baseline: %w", err)
	}
	status, err := gitCmd(s.wsRoot, "-c", "core.quotepath=false", "status", "--porcelain", "-uall")
	if err != nil {
		return nil, fmt.Errorf("planstore: git status: %w", err)
	}

	changes := make(map[string]string)
	for _, line := range strings.Split(diff, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) != 2 {
			continue
		}
		action, ok := mapGitAction(parts[0])
		if !ok {
			continue
		}
		addChange(changes, filepath.ToSlash(parts[1]), action)
	}
	for _, line := range strings.Split(status, "\n") {
		if !strings.HasPrefix(line, "?? ") {
			continue
		}
		addChange(changes, filepath.ToSlash(strings.TrimPrefix(line, "?? ")), "add")
	}
	return changes, nil
}

func addChange(changes map[string]string, path, action string) {
	if excludedFromBaseline(path) {
		return
	}
	// A file can appear in both diff and untracked output only in weird
	// intermediate states; the diff (tracked) view wins for determinism.
	if _, exists := changes[path]; !exists {
		changes[path] = action
	}
}

// mapGitAction maps a git --name-status status letter to the manifest action
// vocabulary (add|modify|delete). Renames/copies are disabled via --no-renames,
// so only A/M/D are expected.
func mapGitAction(status string) (string, bool) {
	switch status {
	case "A":
		return "add", true
	case "M":
		return "modify", true
	case "D":
		return "delete", true
	}
	return "", false
}

// manifestMatches reports whether a manifest entry declares exactly the actual
// change (path + action). Paths are compared in forward-slash form; on Windows
// the comparison is case-insensitive (git and the plan toolset both live on
// the same filesystem, where case folding is the norm), elsewhere exact.
func manifestMatches(m ManifestEntry, path, action string) bool {
	if normalizeAction(m.Action) != action {
		return false
	}
	declared := filepath.ToSlash(m.Path)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(declared, path)
	}
	return declared == path
}

func normalizeAction(a string) string {
	switch strings.ToLower(strings.TrimSpace(a)) {
	case "add":
		return "add"
	case "modify":
		return "modify"
	case "delete":
		return "delete"
	}
	return a
}

// excludedFromBaseline reports whether a workspace-relative path (forward
// slashes) belongs to .reasonix or .git, either as the directory itself or
// anything below it. The driver's plan store, keys and run state live under
// .reasonix and must never enter the change comparison.
func excludedFromBaseline(rel string) bool {
	if rel == ".reasonix" || rel == ".git" {
		return true
	}
	return strings.HasPrefix(rel, ".reasonix/") || strings.HasPrefix(rel, ".git/")
}

// gitHead resolves the workspace's current HEAD commit.
func gitHead(wsRoot string) (string, error) {
	out, err := gitCmd(wsRoot, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if out == "" {
		return "", fmt.Errorf("git rev-parse HEAD returned empty (no commits yet?)")
	}
	return out, nil
}

// gitCmd runs git inside wsRoot and returns trimmed stdout. The executable is
// resolved like the portable python: prefer a git installed next to the
// Reasonix binary (<exe-dir>/git/cmd/git.exe, then <exe-dir>/git/bin/git.exe),
// falling back to PATH — a deployed desktop must not depend on the system PATH
// having git. Errors carry the command's stderr so a non-git workspace, unborn
// branch or broken repo surfaces a diagnosable message.
func gitCmd(wsRoot string, args ...string) (string, error) {
	cmd := exec.Command(gitExecutable(), args...)
	cmd.Dir = wsRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(string(out)), nil
}

// gitExecutable resolves the git binary for baseline/compare operations. The
// search mirrors the portable-python convention but also covers standard Git
// for Windows install locations, because a desktop process started detached
// (envmgr, services) may inherit a PATH that omits Program Files even when Git
// is installed. Order: portable git next to the binary, then Program Files /
// LocalAppData installs, then PATH.
func gitExecutable() string {
	exe, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exe)
		for _, cand := range []string{
			filepath.Join(dir, "git", "cmd", "git.exe"),
			filepath.Join(dir, "git", "bin", "git.exe"),
		} {
			if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
				return cand
			}
		}
	}
	// Standard Git for Windows installs (detached processes may lack PATH).
	if programFiles := os.Getenv("ProgramFiles"); programFiles != "" {
		if cand := filepath.Join(programFiles, "Git", "cmd", "git.exe"); isFile(cand) {
			return cand
		}
		if cand := filepath.Join(programFiles, "Git", "bin", "git.exe"); isFile(cand) {
			return cand
		}
	}
	if localApp := os.Getenv("LOCALAPPDATA"); localApp != "" {
		if cand := filepath.Join(localApp, "Programs", "Git", "cmd", "git.exe"); isFile(cand) {
			return cand
		}
	}
	return "git"
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
