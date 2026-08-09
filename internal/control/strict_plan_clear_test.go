package control

import (
	"os"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/planstore"
)

// Sprint 11 A4: /strict-plan-clear — batch cleanup of terminal plans. UAT
// decisions: done AND failed are cleared (both are terminal leftovers);
// executing is never cleared (reuses the single-delete refusal semantics);
// batch deletion is irreversible, so the command lists the would-be-deleted
// plans and demands an explicit --yes to actually delete (contrast the
// single-delete "explicit id is the confirmation" contract).

// clearViaCommand submits /strict-plan-clear [--yes] and returns whether a
// notice containing needle appeared.
func clearViaCommand(h *strictPlanHarness, from int, args, needle string) (int, bool) {
	cmd := "/strict-plan-clear"
	if args != "" {
		cmd += " " + args
	}
	h.c.submitCommandOrTurn(cmd, cmd, "", false, "", "")
	return h.nextNotice(from, needle, 3*time.Second)
}

// TC-A4-01 无参确认门：存在 done/failed 计划时执行 /strict-plan-clear（不带
// --yes）→ 列出将删计划清单（id + stage）与用法提示，不删除任何计划。
func TestStrictPlanClearWithoutYesOnlyLists(t *testing.T) {
	h := newStrictPlanHarness(t, nil, nil)
	writePlanAt(t, h, "plan-001", planstore.StageDone)
	writePlanAt(t, h, "plan-002", planstore.StageFailed)

	from := len(h.eventsSnapshot())
	if _, ok := clearViaCommand(h, from, "", "plan-001"); !ok {
		t.Fatal("confirm gate must list the would-be-deleted plans")
	}
	snap := h.eventsSnapshot()
	joined := ""
	for i := from; i < len(snap); i++ {
		if snap[i].Kind == event.Notice {
			joined += snap[i].Text
		}
	}
	if !strings.Contains(joined, "plan-002") || !strings.Contains(joined, "--yes") {
		t.Fatalf("confirm gate must name every candidate and the --yes usage, got %q", joined)
	}
	for _, id := range []string{"plan-001", "plan-002"} {
		if _, err := os.Stat(h.planDir(id)); err != nil {
			t.Fatalf("confirm gate must NOT delete anything (%s gone: %v)", id, err)
		}
	}
	if ids, err := h.store.ListPlans(); err != nil || len(ids) != 2 {
		t.Fatalf("ListPlans after confirm gate = %v (err %v), want both plans intact", ids, err)
	}
}

// TC-A4-02 --yes 批量清除：done 与 failed 全部删除，list 为空，Notice 报删除数量。
func TestStrictPlanClearYesRemovesTerminal(t *testing.T) {
	h := newStrictPlanHarness(t, nil, nil)
	writePlanAt(t, h, "plan-001", planstore.StageDone)
	writePlanAt(t, h, "plan-002", planstore.StageFailed)
	writePlanAt(t, h, "plan-003", planstore.StageDone)

	from := len(h.eventsSnapshot())
	if _, ok := clearViaCommand(h, from, "--yes", "已清除 3 个"); !ok {
		t.Fatal("--yes must report the cleared count")
	}
	if ids, err := h.store.ListPlans(); err != nil || len(ids) != 0 {
		t.Fatalf("ListPlans after clear --yes = %v (err %v), want empty", ids, err)
	}
	for _, id := range []string{"plan-001", "plan-002", "plan-003"} {
		if _, err := os.Stat(h.planDir(id)); !os.IsNotExist(err) {
			t.Fatalf("plan dir %s must be removed, stat err = %v", id, err)
		}
	}
}

// TC-A4-03 executing 跳过：executing + done 并存时执行 --yes → executing 目录
// 保留，done 被清除，Notice 明确"跳过 N 个（执行中）"。
func TestStrictPlanClearYesSkipsExecuting(t *testing.T) {
	h := newStrictPlanHarness(t, nil, nil)
	writePlanAt(t, h, "plan-run", planstore.StageExecuting)
	writePlanAt(t, h, "plan-001", planstore.StageDone)

	from := len(h.eventsSnapshot())
	if _, ok := clearViaCommand(h, from, "--yes", "已清除 1 个"); !ok {
		t.Fatal("--yes must report the cleared count")
	}
	if _, ok := h.nextNotice(from, "跳过 1 个", 3*time.Second); !ok {
		t.Fatal("clear must report the skipped executing plan")
	}
	if _, err := os.Stat(h.planDir("plan-run")); err != nil {
		t.Fatalf("executing plan dir must survive clear, stat err = %v", err)
	}
	if _, err := os.Stat(h.planDir("plan-001")); !os.IsNotExist(err) {
		t.Fatalf("done plan dir must be removed, stat err = %v", err)
	}
}

// TC-A4-04 无终态计划：工作区只有非终态计划（locked/submitted 等）或无计划，
// 执行 clear --yes → 明确提示无可清除的终态计划，不报错不误删。
func TestStrictPlanClearNoTerminal(t *testing.T) {
	h := newStrictPlanHarness(t, nil, nil)
	writePlanAt(t, h, "plan-locked", planstore.StageLocked)
	writePlanAt(t, h, "plan-drafting", planstore.StageDrafting)

	from := len(h.eventsSnapshot())
	if _, ok := clearViaCommand(h, from, "--yes", "没有可清除"); !ok {
		t.Fatal("clear with no terminal plans must say so explicitly")
	}
	if _, err := os.Stat(h.planDir("plan-locked")); err != nil {
		t.Fatalf("locked plan must never be touched by clear, stat err = %v", err)
	}
	if _, err := os.Stat(h.planDir("plan-drafting")); err != nil {
		t.Fatalf("drafting plan must never be touched by clear, stat err = %v", err)
	}

	// 空工作区：同样明确提示。
	h2 := newStrictPlanHarness(t, nil, nil)
	from = len(h2.eventsSnapshot())
	if _, ok := clearViaCommand(h2, from, "--yes", "没有可清除"); !ok {
		t.Fatal("clear on an empty workspace must say so explicitly")
	}
}

// TC-REG-01 命令注册：/strict-plan-clear 进 controller 路由（desktop Commands()
// 补全列表由 desktop TestCommands 覆盖，i18n 三语言由 catalog parity 测试
// 自动覆盖 CmdStrictPlanClear）。
func TestStrictPlanClearRegistered(t *testing.T) {
	h := newStrictPlanHarness(t, nil, nil)
	writePlanAt(t, h, "plan-001", planstore.StageDone)
	from := len(h.eventsSnapshot())
	// 无 --yes → 确认门 notice 出现，证明命令被 submitCommandOrTurn 路由解释。
	if _, ok := clearViaCommand(h, from, "", "--yes"); !ok {
		t.Fatal("controller must route /strict-plan-clear as a builtin command")
	}
	// 非法参数 → usage 提示（命令入口存在且校验参数）。
	from = len(h.eventsSnapshot())
	if _, ok := clearViaCommand(h, from, "bogus", "usage"); !ok {
		t.Fatal("unknown args must surface the usage notice")
	}
}
