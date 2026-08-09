package planstore

import (
	"strings"
	"testing"
)

// applyPlan is a locked plan whose artifacts are stable (used by change tests).
func applyPlan(t *testing.T, s *Store, id string) {
	t.Helper()
	files := PlanFiles{
		ID:       id,
		Steps:    []string{"step one"},
		Script:   "print('v1')",
		Scope:    []ScopeEntry{{Path: "src/", Reason: "main code"}},
		Manifest: []ManifestEntry{{Path: "src/a.txt", Action: "modify"}},
	}
	if err := s.WritePlan(id, files); err != nil {
		t.Fatalf("WritePlan(%s): %v", id, err)
	}
	if err := s.Transition(id, StageDrafting, StageSubmitted); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(id, StageSubmitted, StageLocked); err != nil {
		t.Fatal(err)
	}
}

// TC-CH-01 ApplyChange 只更新脚本：新脚本密文落盘 + locks hash 更新 + retries 重置
// + ChangePending 清除 + ChangeHistory approved 记录。
func TestApplyChangeScriptOnly(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	writeWorkspaceFile(t, ws, ".gitkeep", "")
	gitInit(t, ws)
	s := mustOpen(t, ws)
	applyPlan(t, s, "plan-001")

	pc := PendingChange{At: nowUTC(), Reason: "script too strict", ValidateScript: "print('v2')"}
	if err := s.ApplyChange("plan-001", pc, 2); err != nil {
		t.Fatalf("ApplyChange: %v", err)
	}
	files, err := s.ReadPlan("plan-001")
	if err != nil {
		t.Fatal(err)
	}
	if files.Script != "print('v2')" {
		t.Fatalf("script after change = %q, want print('v2')", files.Script)
	}
	// locks hash 与 .lockbackup 同步，Verify 不误报
	if err := s.Verify("plan-001"); err != nil {
		t.Fatalf("Verify after change: %v", err)
	}
	rs, _ := s.ReadRunState("plan-001")
	if rs.ChangePending || rs.PendingChange != nil {
		t.Fatal("pending change must be cleared after ApplyChange")
	}
	if rs.RetriesLeft != 2 {
		t.Fatalf("retriesLeft = %d, want 2", rs.RetriesLeft)
	}
	if n := len(rs.ChangeHistory); n != 1 || !rs.ChangeHistory[0].Approved {
		t.Fatalf("ChangeHistory must hold one approved record, got %+v", rs.ChangeHistory)
	}
}

// TC-CH-02 ApplyChange 只更新范围：合法 scope 应用成功；逃逸工作区的 scope 被拒
// 且不动已有产物。
func TestApplyChangeScopeOnlyValidates(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	writeWorkspaceFile(t, ws, ".gitkeep", "")
	gitInit(t, ws)
	s := mustOpen(t, ws)
	applyPlan(t, s, "plan-001")

	// 合法新范围
	pc := PendingChange{At: nowUTC(), Reason: "need scripts dir",
		WriteScope: []ScopeEntry{{Path: "scripts/", Reason: "extra scripts"}}}
	if err := s.ApplyChange("plan-001", pc, 2); err != nil {
		t.Fatalf("ApplyChange with valid scope: %v", err)
	}
	files, _ := s.ReadPlan("plan-001")
	if len(files.Scope) != 1 || files.Scope[0].Path != "scripts/" {
		t.Fatalf("scope after change = %+v, want scripts/", files.Scope)
	}

	// 逃逸工作区的范围 → 拒绝
	bad := PendingChange{At: nowUTC(), Reason: "escape",
		WriteScope: []ScopeEntry{{Path: "../outside/", Reason: "escape"}}}
	if err := s.ApplyChange("plan-001", bad, 2); err == nil {
		t.Fatal("escaped write scope must be rejected")
	} else if !strings.Contains(err.Error(), "escape") && !strings.Contains(err.Error(), "outside") {
		t.Fatalf("rejection should explain the escape, got %v", err)
	}
	// 拒绝不落盘：scope 保持 scripts/
	files, _ = s.ReadPlan("plan-001")
	if len(files.Scope) != 1 || files.Scope[0].Path != "scripts/" {
		t.Fatalf("rejected scope must not touch artifacts, got %+v", files.Scope)
	}
}

// TC-CH-03 DenyChange：清除 pending、记录 rejected、不动产物。
func TestDenyChange(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	writeWorkspaceFile(t, ws, ".gitkeep", "")
	gitInit(t, ws)
	s := mustOpen(t, ws)
	applyPlan(t, s, "plan-001")
	if err := s.UpdateRunState("plan-001", func(rs *RunState) {
		rs.ChangePending = true
		rs.PendingChange = &PendingChange{At: nowUTC(), Reason: "relax", ValidateScript: "print('v2')"}
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.DenyChange("plan-001"); err != nil {
		t.Fatalf("DenyChange: %v", err)
	}
	rs, _ := s.ReadRunState("plan-001")
	if rs.ChangePending || rs.PendingChange != nil {
		t.Fatal("pending change must be cleared after DenyChange")
	}
	if n := len(rs.ChangeHistory); n != 1 || rs.ChangeHistory[0].Approved {
		t.Fatalf("ChangeHistory must hold one rejected record, got %+v", rs.ChangeHistory)
	}
	files, _ := s.ReadPlan("plan-001")
	if files.Script != "print('v1')" {
		t.Fatalf("denied change must not touch the locked script, got %q", files.Script)
	}
}
