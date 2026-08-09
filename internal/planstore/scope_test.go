package planstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scopeFixture builds a workspace with src/deep/ and README.md, plus a scope
// granting src/ (recursive) and README.md (exact).
func scopeFixture(t *testing.T) (string, Scope) {
	t.Helper()
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "src", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "README.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	scope := Scope{
		{Path: "src/", Reason: "main code"},
		{Path: "README.md", Reason: "docs"},
	}
	return ws, scope
}

// TC-WS-01 范围白名单基本判定：目录条目递归允许、文件条目精确匹配、范围外拒绝
// 且错误提示引导 plan_request_change。
func TestScopeAllowsRecursiveAndExact(t *testing.T) {
	ws, scope := scopeFixture(t)

	for _, inside := range []string{
		filepath.Join(ws, "src", "a.go"),
		filepath.Join(ws, "src", "deep", "b.go"),
		filepath.Join(ws, "src"), // 目录本身
		filepath.Join(ws, "README.md"),
		"src/new.go", // 相对路径
	} {
		if err := scope.Allows(ws, inside); err != nil {
			t.Fatalf("Allows(%q) must pass: %v", inside, err)
		}
	}
	for _, outside := range []string{
		filepath.Join(ws, "other.txt"),
		filepath.Join(ws, "src2", "x.go"),
		filepath.Join(ws, "README.md.bak"),
	} {
		err := scope.Allows(ws, outside)
		if err == nil {
			t.Fatalf("Allows(%q) must be refused", outside)
		}
		if !strings.Contains(err.Error(), "plan_request_change") {
			t.Fatalf("refusal must point at plan_request_change, got: %v", err)
		}
	}
}

// TC-WS-02 范围校验与逃逸：空 path 拒绝、.. 逃逸拒绝、绝对路径越界拒绝；
// 工作区内的绝对路径条目允许。
func TestScopeValidateAndEscape(t *testing.T) {
	ws, _ := scopeFixture(t)

	if err := ValidateScope(ws, []ScopeEntry{{Path: "", Reason: "x"}}); err == nil {
		t.Fatal("empty path must be rejected")
	}
	if err := ValidateScope(ws, []ScopeEntry{{Path: "ok", Reason: ""}}); err == nil {
		t.Fatal("empty reason must be rejected")
	}
	if err := ValidateScope(ws, []ScopeEntry{{Path: "../escape", Reason: "x"}}); err == nil {
		t.Fatal("parent traversal must be rejected")
	}
	if err := ValidateScope(ws, []ScopeEntry{{Path: "sub/../../up", Reason: "x"}}); err == nil {
		t.Fatal("embedded traversal must be rejected")
	}
	if err := ValidateScope(ws, []ScopeEntry{{Path: filepath.Join(filepath.Dir(ws), "outside"), Reason: "x"}}); err == nil {
		t.Fatal("absolute path outside the workspace must be rejected")
	}
	absOK := filepath.Join(ws, "inside")
	if err := os.MkdirAll(absOK, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ValidateScope(ws, []ScopeEntry{{Path: absOK, Reason: "abs inside ok"}}); err != nil {
		t.Fatalf("absolute path inside the workspace must pass: %v", err)
	}
}

// TC-WS-03 写工具挂载拦截：write_file/edit_file/multi_edit/delete_range/
// notebook_edit/move_file 的目标路径在范围内放行、范围外拒绝。
func TestWriteGateBlocksOutside(t *testing.T) {
	ws, scope := scopeFixture(t)
	gate := WriteGate{WSRoot: ws, Scope: scope}

	inside := []struct {
		name string
		args string
	}{
		{"write_file", `{"path":"src/a.go","content":"x"}`},
		{"edit_file", `{"path":"src/a.go"}`},
		{"multi_edit", `{"path":"src/deep/b.go"}`},
		{"delete_range", `{"path":"README.md"}`},
		{"delete_symbol", `{"path":"src/a.go"}`},
		{"notebook_edit", `{"path":"src/note.ipynb"}`},
	}
	for _, c := range inside {
		if err := gate.Check(c.name, json.RawMessage(c.args)); err != nil {
			t.Fatalf("%s inside scope must pass: %v", c.name, err)
		}
	}
	// move_file 源在范围内、目的在范围外 → 拒绝
	if err := gate.Check("move_file", json.RawMessage(`{"source_path":"src/a.go","destination_path":"evil.txt"}`)); err == nil {
		t.Fatal("move_file destination outside scope must be rejected")
	}
	if err := gate.Check("move_file", json.RawMessage(`{"source_path":"src/a.go","destination_path":"src/b.go"}`)); err != nil {
		t.Fatalf("move_file fully inside scope must pass: %v", err)
	}
	if err := gate.Check("write_file", json.RawMessage(`{"path":"evil.txt"}`)); err == nil {
		t.Fatal("write_file outside scope must be rejected")
	}
}

// TC-WS-04 非写工具不受影响：读工具与 grep 等非写工具不被 WriteGate 拦截。
func TestWriteGateIgnoresNonWriters(t *testing.T) {
	ws, scope := scopeFixture(t)
	gate := WriteGate{WSRoot: ws, Scope: scope}
	for _, c := range []struct {
		name string
		args string
	}{
		{"read_file", `{"path":"anywhere/secret.txt"}`},
		{"grep", `{"pattern":"x","path":"."}`},
		{"glob", `{"pattern":"**"}`},
		{"bash", `{"command":"echo hi"}`},
	} {
		if err := gate.Check(c.name, json.RawMessage(c.args)); err != nil {
			t.Fatalf("%s must not be intercepted by the write gate: %v", c.name, err)
		}
	}
	if paths := ExtractWritePaths("read_file", json.RawMessage(`{"path":"x"}`)); len(paths) != 0 {
		t.Fatalf("read_file must extract no write paths, got %v", paths)
	}
}

// TC-WS-05 与 planstore 集成：从已锁定计划读 scope 构造 gate，范围外写入被拒。
func TestWriteGateFromPlanScope(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := mustOpen(t, ws)
	files := sampleFiles() // scope 条目 src/ + manifest src/main.go
	if err := s.WritePlan("plan-001", files); err != nil {
		t.Fatal(err)
	}
	s.Transition("plan-001", StageDrafting, StageSubmitted)
	s.Transition("plan-001", StageSubmitted, StageLocked)

	stored, err := s.ReadPlan("plan-001")
	if err != nil {
		t.Fatal(err)
	}
	gate := WriteGate{WSRoot: ws, Scope: Scope(stored.Scope)}
	if err := gate.Check("write_file", json.RawMessage(`{"path":"src/impl.go"}`)); err != nil {
		t.Fatalf("scope from locked plan must allow src/: %v", err)
	}
	if err := gate.Check("write_file", json.RawMessage(`{"path":"secret.txt"}`)); err == nil {
		t.Fatal("scope from locked plan must refuse outside paths")
	}
}
