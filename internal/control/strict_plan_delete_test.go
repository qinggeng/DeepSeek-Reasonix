package control

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/planstore"
)

// Sprint 10 registry entry 6: /strict-plan-delete — user-side cleanup for
// plans that can no longer be executed. UAT decisions: any stage deletable
// except executing; an explicit plan id IS the confirmation (omitted id →
// usage); run-state-corrupt leftovers (the E2E plan-subtract-001 shape) are
// deletable because cleanup is the point.

// writePlanAt creates a plan at the given stage through the store API (a
// stand-in for a fully formed plan).
func writePlanAt(t *testing.T, h *strictPlanHarness, id string, stage planstore.Stage) {
	t.Helper()
	files := planstore.PlanFiles{
		ID:       id,
		Steps:    []string{"step one"},
		Script:   "print('ok')",
		Scope:    []planstore.ScopeEntry{{Path: "src/", Reason: "main code"}},
		Manifest: []planstore.ManifestEntry{{Path: "src/main.go", Action: "modify"}},
	}
	if err := h.store.WritePlan(id, files); err != nil {
		t.Fatalf("WritePlan %s: %v", id, err)
	}
	switch stage {
	case planstore.StageSubmitted:
		if err := h.store.Transition(id, planstore.StageDrafting, planstore.StageSubmitted); err != nil {
			t.Fatalf("Transition submitted: %v", err)
		}
	case planstore.StageLocked:
		if err := h.store.Transition(id, planstore.StageDrafting, planstore.StageSubmitted); err != nil {
			t.Fatalf("Transition submitted: %v", err)
		}
		if err := h.store.Transition(id, planstore.StageSubmitted, planstore.StageLocked); err != nil {
			t.Fatalf("Transition locked: %v", err)
		}
	case planstore.StageRejected:
		if err := h.store.Transition(id, planstore.StageDrafting, planstore.StageSubmitted); err != nil {
			t.Fatalf("Transition submitted: %v", err)
		}
		if err := h.store.Transition(id, planstore.StageSubmitted, planstore.StageRejected); err != nil {
			t.Fatalf("Transition rejected: %v", err)
		}
	case planstore.StageExecuting:
		if err := h.store.Transition(id, planstore.StageDrafting, planstore.StageSubmitted); err != nil {
			t.Fatalf("Transition submitted: %v", err)
		}
		if err := h.store.Transition(id, planstore.StageSubmitted, planstore.StageLocked); err != nil {
			t.Fatalf("Transition locked: %v", err)
		}
		if err := h.store.Transition(id, planstore.StageLocked, planstore.StageExecuting); err != nil {
			t.Fatalf("Transition executing: %v", err)
		}
	case planstore.StageDone:
		if err := h.store.Transition(id, planstore.StageDrafting, planstore.StageSubmitted); err != nil {
			t.Fatalf("Transition submitted: %v", err)
		}
		if err := h.store.Transition(id, planstore.StageSubmitted, planstore.StageLocked); err != nil {
			t.Fatalf("Transition locked: %v", err)
		}
		if err := h.store.Transition(id, planstore.StageLocked, planstore.StageExecuting); err != nil {
			t.Fatalf("Transition executing: %v", err)
		}
		if err := h.store.Transition(id, planstore.StageExecuting, planstore.StageDone); err != nil {
			t.Fatalf("Transition done: %v", err)
		}
	case planstore.StageFailed:
		if err := h.store.Transition(id, planstore.StageDrafting, planstore.StageSubmitted); err != nil {
			t.Fatalf("Transition submitted: %v", err)
		}
		if err := h.store.Transition(id, planstore.StageSubmitted, planstore.StageLocked); err != nil {
			t.Fatalf("Transition locked: %v", err)
		}
		if err := h.store.Transition(id, planstore.StageLocked, planstore.StageExecuting); err != nil {
			t.Fatalf("Transition executing: %v", err)
		}
		if err := h.store.Transition(id, planstore.StageExecuting, planstore.StageFailed); err != nil {
			t.Fatalf("Transition failed: %v", err)
		}
	}
}

// deleteViaCommand submits /strict-plan-delete through the real command entry
// and returns whether a notice containing needle appeared.
func deleteViaCommand(h *strictPlanHarness, from int, id, needle string) (int, bool) {
	cmd := "/strict-plan-delete"
	if id != "" {
		cmd += " " + id
	}
	h.c.submitCommandOrTurn(cmd, cmd, "", false, "", "")
	return h.nextNotice(from, needle, 3*time.Second)
}

// TC-DEL-01 删除终态计划（done）：成功删除，目录消失，Notice 明确。
func TestStrictPlanDeleteTerminal(t *testing.T) {
	h := newStrictPlanHarness(t, nil, nil)
	writePlanAt(t, h, "plan-001", planstore.StageDone)

	from := len(h.eventsSnapshot())
	if _, ok := deleteViaCommand(h, from, "plan-001", "plan-001"); !ok {
		t.Fatal("delete must surface a notice naming the plan id")
	}
	if _, err := os.Stat(h.planDir("plan-001")); !os.IsNotExist(err) {
		t.Fatalf("plan dir must be removed after delete, stat err = %v", err)
	}
	if ids, err := h.store.ListPlans(); err != nil || len(ids) != 0 {
		t.Fatalf("ListPlans after delete = %v (err %v), want empty", ids, err)
	}
}

// TC-DEL-02 删除非终态非 executing 计划（locked/submitted/drafting/rejected）：
// 一律允许。
func TestStrictPlanDeleteNonTerminalStages(t *testing.T) {
	h := newStrictPlanHarness(t, nil, nil)
	for _, tc := range []struct {
		id    string
		stage planstore.Stage
	}{
		{"plan-locked", planstore.StageLocked},
		{"plan-submitted", planstore.StageSubmitted},
		{"plan-drafting", planstore.StageDrafting},
		{"plan-rejected", planstore.StageRejected},
	} {
		writePlanAt(t, h, tc.id, tc.stage)
		from := len(h.eventsSnapshot())
		if _, ok := deleteViaCommand(h, from, tc.id, "已删除"); !ok {
			t.Fatalf("%s: delete notice must confirm removal", tc.id)
		}
		if _, err := os.Stat(h.planDir(tc.id)); !os.IsNotExist(err) {
			t.Fatalf("%s: plan dir must be removed", tc.id)
		}
	}
}

// TC-DEL-03 executing 计划拒绝删除：明确报错，目录仍在。
func TestStrictPlanDeleteRefusesExecuting(t *testing.T) {
	h := newStrictPlanHarness(t, nil, nil)
	writePlanAt(t, h, "plan-001", planstore.StageExecuting)

	from := len(h.eventsSnapshot())
	if _, ok := deleteViaCommand(h, from, "plan-001", "executing"); !ok {
		t.Fatal("delete of an executing plan must surface a clear error naming the stage")
	}
	if _, err := os.Stat(h.planDir("plan-001")); err != nil {
		t.Fatalf("executing plan dir must survive a refused delete, stat err = %v", err)
	}
}

// TC-DEL-04 省略 id → usage 提示（不兜底最近计划）；不存在的 id → 明确错误。
func TestStrictPlanDeleteUsageAndMissing(t *testing.T) {
	h := newStrictPlanHarness(t, nil, nil)
	writePlanAt(t, h, "plan-001", planstore.StageLocked)

	// 省略 id：拒绝并提示用法，最近锁定计划不被误删。
	from := len(h.eventsSnapshot())
	if _, ok := deleteViaCommand(h, from, "", "usage"); !ok {
		t.Fatal("delete without an id must show usage")
	}
	if _, err := os.Stat(h.planDir("plan-001")); err != nil {
		t.Fatal("omitted-id delete must not fall back to the most recent plan")
	}

	// 不存在的 id：明确错误。
	from = len(h.eventsSnapshot())
	if _, ok := deleteViaCommand(h, from, "nope-999", "nope-999"); !ok {
		t.Fatal("delete of a nonexistent id must name the id in the error")
	}
}

// TC-DEL-05 run-state 损坏的残留计划可删（清理场景：run-state 读不出 stage，
// 视为非 executing）。
func TestStrictPlanDeleteCorruptLeftover(t *testing.T) {
	h := newStrictPlanHarness(t, nil, nil)
	// 手工构造半写残留：只有 plan.md.enc，无 run-state.json（E2E 残留形态）。
	dir := filepath.Join(h.store.WorkspaceRoot(), ".reasonix", "plans", "plan-leftover")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plan.md.enc"), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}

	from := len(h.eventsSnapshot())
	if _, ok := deleteViaCommand(h, from, "plan-leftover", "plan-leftover"); !ok {
		t.Fatal("corrupt leftover must be deletable, with a notice naming the id")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("corrupt leftover dir must be removed, stat err = %v", err)
	}
}

// TC-DEL-07 非法 plan id（/、..、. 等）被拒绝（校验先于文件系统访问），
// 且不触碰工作区外部目录。
func TestStrictPlanDeleteInvalidID(t *testing.T) {
	h := newStrictPlanHarness(t, nil, nil)
	for _, bad := range []string{"../escape", "a/b", ".", "..", "a b"} {
		from := len(h.eventsSnapshot())
		if _, ok := deleteViaCommand(h, from, bad, "invalid"); !ok {
			t.Fatalf("invalid id %q must be rejected with a clear error", bad)
		}
	}
	// 外部目录未被触碰：工作区父目录下不应出现 escape 目录。
	if _, err := os.Stat(filepath.Join(filepath.Dir(h.store.WorkspaceRoot()), "escape")); !os.IsNotExist(err) {
		t.Fatal("invalid plan id must never reach the filesystem")
	}
}

// TC-DEL-06 删除后 /strict-plan-list 不再列出该计划。
func TestStrictPlanDeleteRemovesFromList(t *testing.T) {
	h := newStrictPlanHarness(t, nil, nil)
	writePlanAt(t, h, "plan-001", planstore.StageLocked)
	writePlanAt(t, h, "plan-002", planstore.StageFailed)

	from := len(h.eventsSnapshot())
	if _, ok := deleteViaCommand(h, from, "plan-001", "已删除"); !ok {
		t.Fatal("delete must confirm removal")
	}
	from = len(h.eventsSnapshot())
	h.c.submitCommandOrTurn("/strict-plan-list", "/strict-plan-list", "", false, "", "")
	to, ok := h.nextNotice(from, "plan-002", 3*time.Second)
	if !ok {
		t.Fatal("list must still show the surviving plan")
	}
	snap := h.eventsSnapshot()
	joined := ""
	for i := from; i < to && i < len(snap); i++ {
		if snap[i].Kind == event.Notice {
			joined += snap[i].Text
		}
	}
	if strings.Contains(joined, "plan-001") {
		t.Fatalf("list must no longer contain the deleted plan, got %q", joined)
	}
}
