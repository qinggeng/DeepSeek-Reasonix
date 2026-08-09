package control

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"reasonix/internal/planstore"
)

// This file implements the user-side batch plan cleanup command:
//   /strict-plan-clear [--yes]
// Sprint 11 A4: done/failed plans pile up and /strict-plan-delete only removes
// one at a time. UAT decisions: clear removes every terminal plan (done AND
// failed — both are leftovers); executing is never cleared (same refusal
// semantics as the single delete); because batch deletion is irreversible, the
// command first lists the would-be-deleted plans and only deletes with an
// explicit --yes (contrast the single delete's "explicit id is the
// confirmation" contract — a batch has no id to name, so the confirmation is
// the --yes flag itself). Run-state-missing leftovers are cleared too (they
// are terminal by definition and cleanup is the point); an unreadable
// run-state is skipped (cannot confirm it is not executing — fail closed, same
// as the single delete).

// applyStrictPlanClear implements /strict-plan-clear [--yes].
func (c *Controller) applyStrictPlanClear(input, display string) {
	rest := strings.TrimSpace(strings.TrimPrefix(input, "/strict-plan-clear"))
	yes := rest == "--yes"
	if rest != "" && rest != "--yes" {
		c.strictPlanNotice("usage: /strict-plan-clear [--yes] — 无 --yes 时只列出将清除的终态计划（done/failed），加 --yes 才真正批量删除（不可恢复）")
		return
	}
	c.runGuarded(func(ctx context.Context) error {
		store, err := planstore.Open(c.workspaceRoot)
		if err != nil {
			c.strictPlanNotice("strict-plan-clear: " + err.Error())
			return nil
		}
		return c.clearTerminalPlans(store, yes)
	})
}

// clearTerminalPlans enumerates every plan, buckets it (cleared / skipped), and
// either reports the pending list (no --yes) or deletes the terminal ones.
func (c *Controller) clearTerminalPlans(store *planstore.Store, yes bool) error {
	ids, err := store.ListPlans()
	if err != nil {
		c.strictPlanNotice("strict-plan-clear: " + err.Error())
		return nil
	}
	var toClear []string
	var skipped []string
	var skippedExec []string
	for _, id := range ids {
		dir := filepath.Join(planstore.PlansDir(store.WorkspaceRoot()), id)
		if _, err := os.Stat(dir); err != nil {
			skipped = append(skipped, id) // 不可读目录：跳过
			continue
		}
		rs, err := store.ReadRunState(id)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				// 半写残留（无 run-state.json）：终态残留，允许清理（同单删语义）。
				toClear = append(toClear, id)
			} else {
				// 运行状态不可读：无法确认未在执行中 → fail-closed 跳过。
				skipped = append(skipped, id)
			}
			continue
		}
		switch rs.Stage {
		case planstore.StageDone, planstore.StageFailed:
			toClear = append(toClear, id)
		case planstore.StageExecuting:
			skippedExec = append(skippedExec, id)
		default:
			skipped = append(skipped, id)
		}
	}

	if len(toClear) == 0 {
		c.strictPlanNotice("strict-plan-clear: 没有可清除的终态计划（done/failed）")
		return nil
	}
	if !yes {
		var b strings.Builder
		fmt.Fprintf(&b, "strict-plan-clear: 将清除以下 %d 个终态计划（不可恢复）：", len(toClear))
		for _, id := range toClear {
			fmt.Fprintf(&b, "\n- %s", id)
		}
		b.WriteString("\n确认请执行 /strict-plan-clear --yes")
		c.strictPlanNotice(b.String())
		return nil
	}
	for _, id := range toClear {
		if err := store.DeletePlan(id); err != nil {
			c.strictPlanNotice(fmt.Sprintf("strict-plan-clear: 删除 %q 失败（%v），其余继续", id, err))
		}
	}
	msg := fmt.Sprintf("strict-plan-clear: 已清除 %d 个计划（%s）", len(toClear), strings.Join(toClear, ", "))
	if len(skippedExec) > 0 {
		msg += fmt.Sprintf("；跳过 %d 个执行中计划（%s，请等待终态后再清）", len(skippedExec), strings.Join(skippedExec, ", "))
	}
	if len(skipped) > 0 {
		msg += fmt.Sprintf("；跳过 %d 个非终态/不可读计划（%s）", len(skipped), strings.Join(skipped, ", "))
	}
	c.strictPlanNotice(msg)
	return nil
}
