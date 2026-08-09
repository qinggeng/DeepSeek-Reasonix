package planstore

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitRun runs a git command inside dir and returns trimmed stdout.
func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// readBaseline reads the plan's baseline.json as a head-only snapshot.
func readBaseline(t *testing.T, ws, id string) baselineFile {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(planDir(ws, id), baselineName))
	if err != nil {
		t.Fatalf("read baseline.json: %v", err)
	}
	var b baselineFile
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatalf("baseline.json invalid: %v", err)
	}
	return b
}

func writeWorkspaceFile(t *testing.T, ws, rel, content string) {
	t.Helper()
	p := filepath.Join(ws, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// gitInit makes ws a git repository with a first commit (branch-agnostic for
// old git), autocrlf off so content writes map 1:1 onto the index.
func gitInit(t *testing.T, ws string) {
	t.Helper()
	gitRun(t, ws, "init", "-q")
	gitRun(t, ws, "config", "user.email", "planstore@test")
	gitRun(t, ws, "config", "user.name", "planstore")
	gitRun(t, ws, "config", "core.autocrlf", "false")
	gitRun(t, ws, "add", "-A")
	gitRun(t, ws, "commit", "-q", "-m", "baseline")
}

// gitHeadOf returns the current HEAD commit hash of ws (test helper; the
// production gitHead(wsRoot) has a different signature).
func gitHeadOf(t *testing.T, ws string) string {
	t.Helper()
	return gitRun(t, ws, "rev-parse", "HEAD")
}

// gitStageCommit stages all changes and commits them, returning the new HEAD.
func gitStageCommit(t *testing.T, ws string) string {
	t.Helper()
	gitRun(t, ws, "add", "-A")
	gitRun(t, ws, "commit", "-q", "-m", "snapshot")
	return gitHeadOf(t, ws)
}

// TC-BL-01 TakeBaseline 记录锁定时刻的 git HEAD：baseline.json 含 head 字段且等于
// 当前 git rev-parse HEAD；不含任何文件哈希表（旧 walk 格式废止）。
func TestTakeBaselineRecordsGitHead(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	s := mustOpen(t, ws)

	writeWorkspaceFile(t, ws, "README.md", "# readme")
	writeWorkspaceFile(t, ws, "src/main.go", "package main")
	gitInit(t, ws)
	head := gitHeadOf(t, ws)

	if err := s.WritePlan("plan-001", sampleFiles()); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	if err := s.TakeBaseline("plan-001"); err != nil {
		t.Fatalf("TakeBaseline: %v", err)
	}

	got := readBaseline(t, ws, "plan-001")
	if got.Head != head {
		t.Fatalf("baseline head = %q, want %q", got.Head, head)
	}
	data, err := os.ReadFile(filepath.Join(planDir(ws, "plan-001"), baselineName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "sha256") || strings.Contains(string(data), `"src/`) {
		t.Fatalf("baseline must be a head-only snapshot (old file-hash walk format abolished): %s", data)
	}
}

// TC-BL-02 TakeBaseline 反映提交变化：新增 commit 后重新 TakeBaseline，head 更新为
// 新 commit；未 commit 的工作区改动不影响 head。
func TestTakeBaselineReflectsCommits(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	s := mustOpen(t, ws)

	writeWorkspaceFile(t, ws, "a.txt", "v1")
	gitInit(t, ws)
	if err := s.WritePlan("plan-001", sampleFiles()); err != nil {
		t.Fatal(err)
	}
	if err := s.TakeBaseline("plan-001"); err != nil {
		t.Fatal(err)
	}
	before := readBaseline(t, ws, "plan-001")

	// 未 commit 的改动不影响 head
	writeWorkspaceFile(t, ws, "a.txt", "v2-uncommitted")
	if err := s.TakeBaseline("plan-001"); err != nil {
		t.Fatal(err)
	}
	if got := readBaseline(t, ws, "plan-001"); got.Head != before.Head {
		t.Fatalf("uncommitted change must not move the baseline head (before=%s after=%s)", before.Head, got.Head)
	}

	// commit 后 head 更新
	head := gitStageCommit(t, ws)
	if err := s.TakeBaseline("plan-001"); err != nil {
		t.Fatal(err)
	}
	if got := readBaseline(t, ws, "plan-001"); got.Head != head {
		t.Fatalf("baseline head after commit = %q, want %q", got.Head, head)
	}
}

// TC-BL-03 TakeBaseline 边界：非 git 仓库（未 init）fail-closed 报错，不写
// baseline.json；非法 id 与缺失计划目录同样报错。
func TestTakeBaselineFailsClosedWithoutGit(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	s := mustOpen(t, ws)
	if err := s.WritePlan("plan-001", sampleFiles()); err != nil {
		t.Fatal(err)
	}

	// 无 git 仓库 → 明确报错（strict plan 依赖 git 做变动清单校验）
	if err := s.TakeBaseline("plan-001"); err == nil {
		t.Fatal("TakeBaseline on a non-git workspace must fail closed")
	} else if !strings.Contains(err.Error(), "git") {
		t.Fatalf("error must explain the git requirement, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(planDir(ws, "plan-001"), baselineName)); !os.IsNotExist(err) {
		t.Fatal("baseline.json must not be written when git is unavailable")
	}

	// 非法 id 与缺失计划目录
	for _, bad := range []string{"", "../../evil", "plan..001", "has space"} {
		if err := s.TakeBaseline(bad); err == nil {
			t.Fatalf("TakeBaseline(%q) must be rejected", bad)
		}
	}
	if err := s.TakeBaseline("no-such-plan"); err == nil {
		t.Fatal("TakeBaseline on a missing plan must fail")
	}
}

// TC-BL-04 TakeBaseline 幂等：HEAD 不变时重复调用字节级一致；无临时文件残留。
func TestTakeBaselineIdempotent(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	s := mustOpen(t, ws)

	writeWorkspaceFile(t, ws, "a.txt", "hello")
	gitInit(t, ws)
	if err := s.WritePlan("plan-001", sampleFiles()); err != nil {
		t.Fatal(err)
	}
	if err := s.TakeBaseline("plan-001"); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(planDir(ws, "plan-001"), baselineName))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.TakeBaseline("plan-001"); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(planDir(ws, "plan-001"), baselineName))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("idempotent TakeBaseline must produce byte-identical baseline.json")
	}
	entries, err := os.ReadDir(planDir(ws, "plan-001"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
}

// writeLockedPlan creates a plan whose manifest matches the given entries and
// freezes its baseline at the current git HEAD.
func writeLockedPlan(t *testing.T, s *Store, id string, manifest []ManifestEntry) {
	t.Helper()
	files := PlanFiles{
		ID:       id,
		Steps:    []string{"step one"},
		Script:   "print('ok')",
		Scope:    []ScopeEntry{{Path: "src/", Reason: "main code"}},
		Manifest: manifest,
	}
	if err := s.WritePlan(id, files); err != nil {
		t.Fatalf("WritePlan(%s): %v", id, err)
	}
	if err := s.TakeBaseline(id); err != nil {
		t.Fatalf("TakeBaseline(%s): %v", id, err)
	}
}

// assertComparison compares a BaselineComparison against the expected actual
// diffs (path → action) and the expected missing manifest entries.
func assertComparison(t *testing.T, got BaselineComparison, wantActual map[string]string, wantMissing []ManifestEntry) {
	t.Helper()
	if len(got.Actual) != len(wantActual) {
		t.Fatalf("Actual = %+v, want %d entries %v", got.Actual, len(wantActual), wantActual)
	}
	byPath := make(map[string]string, len(got.Actual))
	inManifest := make(map[string]bool, len(got.Actual))
	for _, d := range got.Actual {
		byPath[d.Path] = d.Action
		inManifest[d.Path] = d.InManifest
	}
	for path, action := range wantActual {
		if byPath[path] != action {
			t.Fatalf("Actual[%q] action = %q, want %q (actual %+v)", path, byPath[path], action, got.Actual)
		}
	}
	if len(got.Missing) != len(wantMissing) {
		t.Fatalf("Missing = %+v, want %d entries %v", got.Missing, len(wantMissing), wantMissing)
	}
	for i, m := range wantMissing {
		if got.Missing[i].Path != m.Path || got.Missing[i].Action != m.Action {
			t.Fatalf("Missing[%d] = %+v, want %+v", i, got.Missing[i], m)
		}
	}
}

// TC-BL-05 CompareBaseline 工作区无变动 → Actual 空、manifest 声明项全部 Missing。
func TestCompareBaselineNoChangesAllManifestMissing(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	s := mustOpen(t, ws)

	writeWorkspaceFile(t, ws, "src/a.txt", "v1")
	writeWorkspaceFile(t, ws, "src/b.txt", "b")
	gitInit(t, ws)
	writeLockedPlan(t, s, "plan-001", []ManifestEntry{{Path: "src/a.txt", Action: "modify"}})

	got, err := s.CompareBaseline("plan-001")
	if err != nil {
		t.Fatalf("CompareBaseline: %v", err)
	}
	assertComparison(t, got, map[string]string{}, []ManifestEntry{{Path: "src/a.txt", Action: "modify"}})
	if got.OK() {
		t.Fatal("a manifest-declared change that never happened must fail the comparison")
	}
}

// TC-BL-06 CompareBaseline 实际变动与 manifest 完全匹配 → 无越界无漏改，OK。
func TestCompareBaselineMatchesManifest(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	s := mustOpen(t, ws)

	writeWorkspaceFile(t, ws, "src/a.txt", "v1")
	gitInit(t, ws)
	writeLockedPlan(t, s, "plan-001", []ManifestEntry{
		{Path: "src/a.txt", Action: "modify"},
		{Path: "src/b.txt", Action: "add"},
	})

	writeWorkspaceFile(t, ws, "src/a.txt", "v2")
	writeWorkspaceFile(t, ws, "src/b.txt", "new file")
	got, err := s.CompareBaseline("plan-001")
	if err != nil {
		t.Fatalf("CompareBaseline: %v", err)
	}
	assertComparison(t, got, map[string]string{
		"src/a.txt": "modify",
		"src/b.txt": "add",
	}, nil)
	for _, d := range got.Actual {
		if !d.InManifest {
			t.Fatalf("diff %+v must be marked InManifest", d)
		}
	}
	if !got.OK() {
		t.Fatal("matching actual changes must pass the comparison")
	}
}

// TC-BL-07 CompareBaseline 越界改动被标记违规：实际新增未声明的文件 → InManifest=false。
func TestCompareBaselineOutOfManifestChange(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	s := mustOpen(t, ws)

	writeWorkspaceFile(t, ws, "src/a.txt", "v1")
	gitInit(t, ws)
	writeLockedPlan(t, s, "plan-001", []ManifestEntry{{Path: "src/a.txt", Action: "modify"}})

	writeWorkspaceFile(t, ws, "src/a.txt", "v2")
	writeWorkspaceFile(t, ws, "src/evil.txt", "smuggled")
	got, err := s.CompareBaseline("plan-001")
	if err != nil {
		t.Fatalf("CompareBaseline: %v", err)
	}
	assertComparison(t, got, map[string]string{
		"src/a.txt":    "modify",
		"src/evil.txt": "add",
	}, nil)
	evil, ok := findDiff(got, "src/evil.txt")
	if !ok || evil.InManifest {
		t.Fatal("out-of-manifest change must be flagged (InManifest=false)")
	}
	if got.OK() {
		t.Fatal("an out-of-manifest change must fail the comparison")
	}
}

// TC-BL-08 CompareBaseline 漏改列为 Missing 违规：manifest 声明的 delete 未发生。
func TestCompareBaselineMissingDeletion(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	s := mustOpen(t, ws)

	writeWorkspaceFile(t, ws, "src/a.txt", "v1")
	writeWorkspaceFile(t, ws, "src/b.txt", "to-delete")
	gitInit(t, ws)
	writeLockedPlan(t, s, "plan-001", []ManifestEntry{
		{Path: "src/a.txt", Action: "modify"},
		{Path: "src/b.txt", Action: "delete"},
	})

	writeWorkspaceFile(t, ws, "src/a.txt", "v2")
	got, err := s.CompareBaseline("plan-001")
	if err != nil {
		t.Fatalf("CompareBaseline: %v", err)
	}
	assertComparison(t, got, map[string]string{
		"src/a.txt": "modify",
	}, []ManifestEntry{{Path: "src/b.txt", Action: "delete"}})
	if got.OK() {
		t.Fatal("a declared deletion that never happened must fail the comparison")
	}
}

// TC-BL-09 CompareBaseline 边界：.reasonix 目录的 untracked 文件不进入实际变动
// （驱动器自身状态与计划目录被排除）。
func TestCompareBaselineIgnoresReasonix(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	s := mustOpen(t, ws)

	writeWorkspaceFile(t, ws, "src/a.txt", "v1")
	gitInit(t, ws)
	writeLockedPlan(t, s, "plan-001", []ManifestEntry{{Path: "notes.md", Action: "add"}})

	// 计划目录自身（.reasonix 下）会产生大量 untracked 文件；工作区再新增一个
	// 计划外文件，但必须只看到后者。
	writeWorkspaceFile(t, ws, ".reasonix/plans/plan-001/extra.tmp", "driver state")
	writeWorkspaceFile(t, ws, "notes.md", "user file")
	got, err := s.CompareBaseline("plan-001")
	if err != nil {
		t.Fatalf("CompareBaseline: %v", err)
	}
	assertComparison(t, got, map[string]string{
		"notes.md": "add",
	}, nil)
	if _, ok := findDiff(got, ".reasonix/plans/plan-001/extra.tmp"); ok {
		t.Fatal(".reasonix must be excluded from the actual-change list")
	}
}

// findDiff returns the diff for a path (forward-slash) from a comparison.
func findDiff(c BaselineComparison, path string) (BaselineDiff, bool) {
	for _, d := range c.Actual {
		if d.Path == path {
			return d, true
		}
	}
	return BaselineDiff{}, false
}

// TC-GIT-01 gitExecutable 探测：exe 同目录便携 git 优先；其次 ProgramFiles /
// LOCALAPPDATA 标准安装；皆无回退 "git"（PATH）。覆盖"桌面版由 envmgr 等
// detached 启动、PATH 缺 Program Files"的部署场景。
func TestGitExecutableProbing(t *testing.T) {
	// 1. 无任何探测目标 → 回退 PATH
	t.Setenv("ProgramFiles", "")
	t.Setenv("LOCALAPPDATA", "")
	if got := gitExecutable(); got != "git" {
		t.Fatalf("no git anywhere: got %q, want \"git\"", got)
	}

	// 2. ProgramFiles 标准安装命中（Git for Windows 默认位置）
	pf := t.TempDir()
	install := filepath.Join(pf, "Git", "cmd")
	if err := os.MkdirAll(install, 0o755); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(install, "git.exe")
	if err := os.WriteFile(fake, []byte("MZ"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ProgramFiles", pf)
	t.Setenv("LOCALAPPDATA", "")
	if got := gitExecutable(); got != fake {
		t.Fatalf("ProgramFiles install must be found: got %q, want %q", got, fake)
	}

	// 3. LocalAppData 用户级安装命中
	la := t.TempDir()
	uInstall := filepath.Join(la, "Programs", "Git", "cmd")
	if err := os.MkdirAll(uInstall, 0o755); err != nil {
		t.Fatal(err)
	}
	uFake := filepath.Join(uInstall, "git.exe")
	if err := os.WriteFile(uFake, []byte("MZ"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ProgramFiles", "")
	t.Setenv("LOCALAPPDATA", la)
	if got := gitExecutable(); got != uFake {
		t.Fatalf("LocalAppData install must be found: got %q, want %q", got, uFake)
	}
}

// TC-BL-11 CompareBaseline 边界：新目录下的新增文件（git status 默认折叠为
// `?? dir/`）必须逐文件列出（-uall），manifest 声明 `dir/new.txt add` 正常匹配，
// 不产生假越界/假漏改。
func TestCompareBaselineNewDirectoryFiles(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	s := mustOpen(t, ws)

	writeWorkspaceFile(t, ws, "src/a.txt", "v1")
	gitInit(t, ws)
	writeLockedPlan(t, s, "plan-001", []ManifestEntry{{Path: "generated/new.txt", Action: "add"}})

	writeWorkspaceFile(t, ws, "generated/new.txt", "fresh")
	got, err := s.CompareBaseline("plan-001")
	if err != nil {
		t.Fatalf("CompareBaseline: %v", err)
	}
	assertComparison(t, got, map[string]string{
		"generated/new.txt": "add",
	}, nil)
	for _, d := range got.Actual {
		if !d.InManifest {
			t.Fatalf("diff %+v must be InManifest (git -uall must list the file, not fold the dir)", d)
		}
	}
	if !got.OK() {
		t.Fatal("a file added under a brand-new directory must pass when declared")
	}
}

// TC-BL-10 CompareBaseline 错误路径：非法 id、缺失计划、缺失 baseline（未锁定）均
// fail-closed 报错。
func TestCompareBaselineFailsClosed(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	s := mustOpen(t, ws)

	writeWorkspaceFile(t, ws, "a.txt", "x")
	gitInit(t, ws)
	writeLockedPlan(t, s, "plan-001", []ManifestEntry{{Path: "a.txt", Action: "modify"}})

	for _, bad := range []string{"", "../../evil", "plan..001", "has space"} {
		if _, err := s.CompareBaseline(bad); err == nil {
			t.Fatalf("CompareBaseline(%q) must be rejected", bad)
		}
	}
	if _, err := s.CompareBaseline("no-such-plan"); err == nil {
		t.Fatal("CompareBaseline on a missing plan must fail")
	}
	// 已锁定但从未 TakeBaseline（模拟旧数据/漏调用）→ baseline 缺失 → fail-closed
	if err := s.WritePlan("plan-002", sampleFiles()); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition("plan-002", StageDrafting, StageSubmitted); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition("plan-002", StageSubmitted, StageLocked); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CompareBaseline("plan-002"); err == nil {
		t.Fatal("CompareBaseline without a baseline.json must fail closed")
	}
}
