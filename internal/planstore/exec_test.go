package planstore

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

	res, err := s.RunAcceptanceScript(context.Background(), "plan-001", 1)
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

	res, err := s.RunAcceptanceScript(context.Background(), "plan-001", 1)
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

	res, err := s.RunAcceptanceScript(context.Background(), "plan-001", 1)
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

	res, err := s.RunAcceptanceScript(context.Background(), "plan-001", 1)
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

// TC-A1-01 小输出完整保留：验收脚本输出 ≤ 阈值（默认 8192）时，Output 为完整
// 输出（无截断标记），OutputFile 为空，.reasonix/acceptance-output/ 下不产生
// 文件——现状行为完全保留（fail-safe）。
func TestRunAcceptanceScriptSmallOutputKeptWhole(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	writeWorkspaceFile(t, ws, ".gitkeep", "")
	gitInit(t, ws)
	s := mustOpen(t, ws)
	body := "print('ok')" + "\nprint('line2')"
	writeExecPlan(t, s, "plan-001", body)

	res, err := s.RunAcceptanceScript(context.Background(), "plan-001", 1)
	if err != nil {
		t.Fatalf("RunAcceptanceScript: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", res.ExitCode)
	}
	if !strings.Contains(res.Output, "ok") || !strings.Contains(res.Output, "line2") {
		t.Fatalf("small output must be injected whole, got %q", res.Output)
	}
	if strings.Contains(res.Output, "output too large") || strings.Contains(res.Output, "output truncated") {
		t.Fatalf("small output must carry no truncation/summary marker, got %q", res.Output)
	}
	if res.OutputFile != "" {
		t.Fatalf("small output must not spill to disk, OutputFile = %q", res.OutputFile)
	}
	spillRoot := filepath.Join(ws, ".reasonix", "acceptance-output")
	if entries, err := os.ReadDir(spillRoot); err == nil && len(entries) > 0 {
		t.Fatalf("no spill dir must be created for small output, found %d entries", len(entries))
	}
}

// TC-A1-02 大输出分流落盘：输出超阈值时 Output 为摘要（head 行 + 总行数 + 错误行
// 提取，无硬切标记），完整输出落盘到 .reasonix/acceptance-output/<id>/round-<N>.log，
// OutputFile 为该文件绝对路径且文件内容等于完整输出。
func TestRunAcceptanceScriptLargeOutputSummarizedAndSpilled(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	writeWorkspaceFile(t, ws, ".gitkeep", "")
	gitInit(t, ws)
	s := mustOpen(t, ws)
	// 900 行输出（> 8KB），带 head 内容、错误行与结尾，验证摘要三要素。
	var b strings.Builder
	for i := 0; i < 300; i++ {
		fmt.Fprintf(&b, "INFO line %d\n", i)
	}
	b.WriteString("ERROR: boom at line 300\n")
	for i := 301; i < 600; i++ {
		fmt.Fprintf(&b, "INFO line %d\n", i)
	}
	b.WriteString("Traceback (most recent call last):\n")
	for i := 601; i < 900; i++ {
		fmt.Fprintf(&b, "INFO line %d\n", i)
	}
	full := b.String()
	if len(full) <= DefaultMaxScriptOutput {
		t.Fatalf("test fixture must exceed the default threshold (%d bytes, got %d)", DefaultMaxScriptOutput, len(full))
	}
	writeExecPlan(t, s, "plan-001", "import sys\nsys.stdout.write("+strconv.Quote(full)+")\nsys.exit(1)")

	res, err := s.RunAcceptanceScript(context.Background(), "plan-001", 2)
	if err != nil {
		t.Fatalf("RunAcceptanceScript: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatal("a large-output script that exits non-zero is expected; fixture must fail")
	}
	if res.OutputFile == "" {
		t.Fatal("large output must spill to a file, OutputFile is empty")
	}
	// 摘要含 head 行、行数与错误行提取；不含完整输出。
	if !strings.Contains(res.Output, "INFO line 0") {
		t.Fatalf("summary must include the first head lines, got %q", res.Output)
	}
	if !strings.Contains(res.Output, "line 39") || !strings.Contains(res.Output, "ERROR: boom at line 300") || !strings.Contains(res.Output, "Traceback") {
		t.Fatalf("summary must include head/error-line extraction, got %q", res.Output)
	}
	if strings.Contains(res.Output, "INFO line 800") {
		t.Fatalf("summary must NOT carry the full output body, got %q", res.Output)
	}
	if strings.Contains(res.Output, "output truncated") {
		t.Fatalf("summary must not be a hard cut with the old marker, got %q", res.Output)
	}
	if !strings.Contains(res.Output, "full output saved to") {
		t.Fatalf("summary must tell the model the full-output file path, got %q", res.Output)
	}
	// 落盘位置在 .reasonix 下（CompareBaseline 排除范围）且内容 == 完整输出。
	wantDir := filepath.Join(ws, ".reasonix", "acceptance-output", "plan-001")
	if !strings.HasPrefix(filepath.ToSlash(filepath.Clean(res.OutputFile)), filepath.ToSlash(wantDir)) {
		t.Fatalf("spill file must live under %s, got %q", wantDir, res.OutputFile)
	}
	if !strings.Contains(res.OutputFile, "round-2") {
		t.Fatalf("spill file must be per-round (round-2), got %q", res.OutputFile)
	}
	spilled, err := os.ReadFile(res.OutputFile)
	if err != nil {
		t.Fatalf("read spill file: %v", err)
	}
	// Python 在 Windows 管道下把 \n 翻译成 \r\n，两边归一化后比较。
	normalize := func(x string) string { return strings.ReplaceAll(x, "\r\n", "\n") }
	if normalize(string(spilled)) != normalize(full) {
		t.Fatalf("spill file must contain the FULL output (len %d, got %d)", len(full), len(spilled))
	}
}

// TC-A1-03 大输出无错误行：摘要含 head 行与行数统计，不含错误段；不 panic。
func TestRunAcceptanceScriptLargeOutputWithoutErrorLines(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	writeWorkspaceFile(t, ws, ".gitkeep", "")
	gitInit(t, ws)
	s := mustOpen(t, ws)
	var b strings.Builder
	for i := 0; i < 1200; i++ {
		fmt.Fprintf(&b, "NOTE %d\n", i)
	}
	if len(b.String()) <= DefaultMaxScriptOutput {
		t.Fatalf("fixture must exceed the default threshold")
	}
	writeExecPlan(t, s, "plan-001", "import sys\nsys.stdout.write("+strconv.Quote(b.String())+")")

	res, err := s.RunAcceptanceScript(context.Background(), "plan-001", 1)
	if err != nil {
		t.Fatalf("RunAcceptanceScript: %v", err)
	}
	if res.OutputFile == "" {
		t.Fatal("large output must spill")
	}
	if strings.Contains(res.Output, "error lines") {
		t.Fatalf("summary must not claim error lines when none match, got %q", res.Output)
	}
	if !strings.Contains(res.Output, "NOTE 0") {
		t.Fatalf("summary must still carry head lines, got %q", res.Output)
	}
}

// TC-A1-04 阈值可配置 + 临界边界：注入小阈值（MaxScriptOutput=32、SummaryHeadLines=3、
// SummaryErrorLines=2）时，31 字节 → 完整注入不落盘；33 字节 → 摘要 + 落盘。
func TestRunAcceptanceScriptConfigurableThreshold(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	writeWorkspaceFile(t, ws, ".gitkeep", "")
	gitInit(t, ws)
	s := mustOpen(t, ws)
	s.MaxScriptOutput = 32
	s.SummaryHeadLines = 3
	s.SummaryErrorLines = 2

	// 31 字节 ≤ 阈值 → 完整注入，无落盘。
	small := strings.Repeat("a", 31)
	writeExecPlan(t, s, "plan-small", "import sys\nsys.stdout.write("+strconv.Quote(small)+")")
	smallRes, err := s.RunAcceptanceScript(context.Background(), "plan-small", 1)
	if err != nil {
		t.Fatalf("RunAcceptanceScript small: %v", err)
	}
	if smallRes.Output != small {
		t.Fatalf("31-byte output must be injected whole (len %d, got %d)", len(small), len(smallRes.Output))
	}
	if smallRes.OutputFile != "" {
		t.Fatalf("31-byte output must not spill, got %q", smallRes.OutputFile)
	}

	// 33 字节 > 阈值 → 摘要 + 落盘（多行输出，验证摘要不携带全部行）。
	var large strings.Builder
	for i := 0; i < 12; i++ {
		fmt.Fprintf(&large, "row-%d\n", i)
	}
	if len(large.String()) <= 32 {
		t.Fatalf("fixture must exceed the injected threshold")
	}
	writeExecPlan(t, s, "plan-large", "import sys\nsys.stdout.write("+strconv.Quote(large.String())+")")
	largeRes, err := s.RunAcceptanceScript(context.Background(), "plan-large", 1)
	if err != nil {
		t.Fatalf("RunAcceptanceScript large: %v", err)
	}
	if largeRes.OutputFile == "" {
		t.Fatal("33-byte output must spill when the threshold is 32")
	}
	if strings.Contains(largeRes.Output, "row-9") {
		t.Fatal("summary must not carry rows beyond the head/error bounds")
	}
	if !strings.Contains(largeRes.Output, "row-0") {
		t.Fatal("summary must carry the head rows")
	}
}

// TC-A1-05 落盘随计划清理：DeletePlan 删除计划后 .reasonix/acceptance-output/<id>/
// 一并删除（不残留垃圾）。
func TestDeletePlanRemovesSpilledOutput(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	writeWorkspaceFile(t, ws, ".gitkeep", "")
	gitInit(t, ws)
	s := mustOpen(t, ws)
	writeExecPlan(t, s, "plan-001", "import sys\nsys.stdout.write('x'*9000)")
	if _, err := s.RunAcceptanceScript(context.Background(), "plan-001", 1); err != nil {
		t.Fatalf("RunAcceptanceScript: %v", err)
	}
	spillDir := filepath.Join(ws, ".reasonix", "acceptance-output", "plan-001")
	if _, err := os.Stat(spillDir); err != nil {
		t.Fatalf("spill dir must exist after a large output: %v", err)
	}
	if err := s.DeletePlan("plan-001"); err != nil {
		t.Fatalf("DeletePlan: %v", err)
	}
	if _, err := os.Stat(spillDir); !os.IsNotExist(err) {
		t.Fatalf("spill dir must be removed with the plan, stat err = %v", err)
	}
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

// TC-A1-08（review 补强）单行超大输出：单行 10KB 输出（如巨型单行 JSON）时，
// 摘要必须保持字节有界（≤ 阈值），不能因行数少就把整行灌进提示词。
func TestRunAcceptanceScriptSingleLineBlobBoundedSummary(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	writeWorkspaceFile(t, ws, ".gitkeep", "")
	gitInit(t, ws)
	s := mustOpen(t, ws)
	blob := `{"error":"boom","data":"` + strings.Repeat("x", 10000) + `"}`
	writeExecPlan(t, s, "plan-001", "import sys\nsys.stdout.write("+strconv.Quote(blob)+")\nsys.exit(1)")

	res, err := s.RunAcceptanceScript(context.Background(), "plan-001", 1)
	if err != nil {
		t.Fatalf("RunAcceptanceScript: %v", err)
	}
	if res.OutputFile == "" {
		t.Fatal("oversized output must spill")
	}
	if len(res.Output) > DefaultMaxScriptOutput {
		t.Fatalf("summary must stay byte-bounded (len %d > %d)", len(res.Output), DefaultMaxScriptOutput)
	}
	if !strings.Contains(res.Output, "full output saved to") {
		t.Fatalf("summary must still name the spill file, got %q", res.Output[:120])
	}
}
