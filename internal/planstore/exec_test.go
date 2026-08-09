package planstore

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeExecPlan creates a plan whose acceptance script is src and freezes its
// baseline (locked-plan precondition of the exec tests).
func writeExecPlan(t *testing.T, s *Store, id, script string) {
	t.Helper()
	files := PlanFiles{
		ID:       id,
		Steps:    []string{"step one"},
		Script:   script,
		Scope:    []ScopeEntry{{Path: "src/", Reason: "main code"}},
		Manifest: []ManifestEntry{{Path: "src/a.txt", Action: "modify"}},
	}
	if err := s.WritePlan(id, files); err != nil {
		t.Fatalf("WritePlan(%s): %v", id, err)
	}
	if err := s.TakeBaseline(id); err != nil {
		t.Fatalf("TakeBaseline(%s): %v", id, err)
	}
}

// TC-EX-01 脚本退出码 0 → 通过结果：ExitCode==0、Output 含脚本输出、err 为 nil。
func TestRunAcceptanceScriptPass(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	writeWorkspaceFile(t, ws, ".gitkeep", "")
	gitInit(t, ws)
	s := mustOpen(t, ws)
	writeExecPlan(t, s, "plan-001", "print('ok')")

	res, err := s.RunAcceptanceScript(context.Background(), "plan-001")
	if err != nil {
		t.Fatalf("RunAcceptanceScript: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (accepted)", res.ExitCode)
	}
	if !strings.Contains(res.Output, "ok") {
		t.Fatalf("output must contain the script's stdout, got %q", res.Output)
	}
}

// TC-EX-02 脚本显式退出非 0 → 失败结果（非 error）：ExitCode==3、err 为 nil。
func TestRunAcceptanceScriptExplicitFailure(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	writeWorkspaceFile(t, ws, ".gitkeep", "")
	gitInit(t, ws)
	s := mustOpen(t, ws)
	writeExecPlan(t, s, "plan-001", "import sys\nsys.exit(3)")

	res, err := s.RunAcceptanceScript(context.Background(), "plan-001")
	if err != nil {
		t.Fatalf("a non-zero exit is a rejection result, not an error: %v", err)
	}
	if res.ExitCode != 3 {
		t.Fatalf("exit code = %d, want 3", res.ExitCode)
	}
}

// TC-EX-03 脚本运行时错误 → ExitCode 非 0 + stderr 反馈（可注入模型）。
func TestRunAcceptanceScriptRuntimeError(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	writeWorkspaceFile(t, ws, ".gitkeep", "")
	gitInit(t, ws)
	s := mustOpen(t, ws)
	writeExecPlan(t, s, "plan-001", "raise NameError('boom')")

	res, err := s.RunAcceptanceScript(context.Background(), "plan-001")
	if err != nil {
		t.Fatalf("a runtime error is a rejection result, not an error: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatal("a raising script must exit non-zero")
	}
	if !strings.Contains(res.Output, "NameError") {
		t.Fatalf("output must carry the traceback for model feedback, got %q", res.Output)
	}
}

// TC-EX-04 脚本在驱动器私有临时目录执行、cwd 为工作区、执行后清理：脚本可读工作区
// 文件；脚本明文路径不在工作区下；执行后临时目录删除、工作区无 validate.py 明文。
func TestRunAcceptanceScriptExecIsolation(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	writeWorkspaceFile(t, ws, ".gitkeep", "")
	gitInit(t, ws)
	s := mustOpen(t, ws)
	writeExecPlan(t, s, "plan-001", `import sys, os
with open("data.txt") as f:
    assert f.read().strip() == "hello"
print("ARGV0=" + sys.argv[0])
print("CWD=" + os.getcwd())
`)
	writeWorkspaceFile(t, ws, "data.txt", "hello")

	res, err := s.RunAcceptanceScript(context.Background(), "plan-001")
	if err != nil {
		t.Fatalf("RunAcceptanceScript: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0 (script must see workspace files via cwd)", res.ExitCode)
	}
	cwd := lineValue(res.Output, "CWD=")
	if cwd == "" || !strings.EqualFold(filepath.Clean(cwd), filepath.Clean(ws)) {
		t.Fatalf("cwd must be the workspace root (got %q, ws %q)", cwd, ws)
	}
	argv0 := lineValue(res.Output, "ARGV0=")
	if argv0 == "" {
		t.Fatalf("output must reveal sys.argv[0], got %q", res.Output)
	}
	if filepath.Clean(argv0) == filepath.Clean(filepath.Join(ws, "validate.py")) ||
		strings.HasPrefix(filepath.ToSlash(filepath.Clean(argv0)), filepath.ToSlash(ws)+"/") {
		t.Fatalf("script plaintext must NOT live under the workspace (argv0=%q, ws=%q)", argv0, ws)
	}
	// 执行后临时目录已删除；工作区无脚本明文。
	if _, err := os.Stat(filepath.Dir(argv0)); !os.IsNotExist(err) {
		t.Fatalf("exec temp dir must be cleaned up after the run (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(ws, "validate.py")); !os.IsNotExist(err) {
		t.Fatal("validate.py plaintext must not remain in the workspace")
	}
}

// TC-EX-06 sanitizedExecEnv：剥离 REASONIX_HOME 与含密钥标记的变量，保留普通
// 变量（PATH 等）——验收脚本子进程拿不到主密钥主目录的 env 线索。
func TestSanitizedExecEnv(t *testing.T) {
	in := []string{
		"PATH=C:/bin",
		"REASONIX_HOME=C:/Users/x/AppData/Roaming/reasonix",
		"OPENAI_API_KEY=sk-secret",
		"MY_AUTH_TOKEN=abc",
		"PYTHONPATH=C:/lib",
	}
	got := sanitizedExecEnv(in)
	joined := strings.Join(got, "\n")
	if strings.Contains(joined, "REASONIX_HOME") {
		t.Fatalf("REASONIX_HOME must be stripped, got %q", joined)
	}
	if strings.Contains(joined, "API_KEY") || strings.Contains(joined, "sk-secret") {
		t.Fatalf("API key env must be stripped, got %q", joined)
	}
	if strings.Contains(joined, "AUTH_TOKEN") {
		t.Fatalf("auth token env must be stripped, got %q", joined)
	}
	if !strings.Contains(joined, "PATH=") || !strings.Contains(joined, "PYTHONPATH=") {
		t.Fatalf("benign env must survive, got %q", joined)
	}
}

// lineValue extracts the value after prefix up to the end of its line.
func lineValue(output, prefix string) string {
	idx := strings.Index(output, prefix)
	if idx < 0 {
		return ""
	}
	rest := output[idx+len(prefix):]
	if nl := strings.Index(rest, "\n"); nl >= 0 {
		rest = rest[:nl]
	}
	return strings.TrimSpace(rest)
}

// TC-EX-05 PythonExecutableFrom 探测顺序：<dir>/python/python.exe 优先，其次
// <dir>/python.exe，皆无回退 "python"。
func TestPythonExecutableFromPortableInstall(t *testing.T) {
	dir := t.TempDir()

	// 1. 无任何安装 → 回退 PATH
	if got := PythonExecutableFrom(dir); got != "python" {
		t.Fatalf("no portable python: got %q, want \"python\"", got)
	}

	// 2. 仅 <dir>/python.exe → 命中
	flat := filepath.Join(dir, "python.exe")
	if err := os.WriteFile(flat, []byte("MZ"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := PythonExecutableFrom(dir); got != flat {
		t.Fatalf("flat python.exe: got %q, want %q", got, flat)
	}

	// 3. 子目录 python/python.exe 优先于平铺（目录形式为约定形态）
	sub := filepath.Join(dir, "python")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	subExe := filepath.Join(sub, "python.exe")
	if err := os.WriteFile(subExe, []byte("MZ"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := PythonExecutableFrom(dir); got != subExe {
		t.Fatalf("portable python/ subdir must win: got %q, want %q", got, subExe)
	}

	// 4. python.exe 是目录 → 跳过，回退子目录探测结果
	bad := filepath.Join(dir, "python.exe")
	if err := os.Remove(bad); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(bad, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := PythonExecutableFrom(dir); got != subExe {
		t.Fatalf("directory named python.exe must be skipped: got %q, want %q", got, subExe)
	}
}
