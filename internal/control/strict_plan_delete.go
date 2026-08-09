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

// This file implements the user-side plan deletion command:
//   /strict-plan-delete <plan-id>
// Sprint 10 registry entry 6: planstore.DeletePlan was ready but had no caller,
// so abandoned/failed/residual plans could not be cleaned up from the user
// side. UAT decisions: any stage is deletable except executing; an explicit
// plan id IS the confirmation (omitted id → usage, no most-recent fallback —
// deletion is irreversible); run-state-corrupt leftovers are deletable because
// cleanup is their whole point (E2E left such a plan on the test machine).

// applyStrictPlanDelete implements /strict-plan-delete <plan-id>. The id is
// mandatory — there is deliberately no "most recent plan" fallback, so a stray
// command can never delete an unintended plan.
func (c *Controller) applyStrictPlanDelete(input, display string) {
	id := strings.TrimSpace(strings.TrimPrefix(input, "/strict-plan-delete"))
	if id == "" {
		c.notice("usage: /strict-plan-delete <plan-id> — 必须显式指定计划 id 才能删除（删除不可恢复，省略 id 不兜底最近计划）")
		return
	}
	c.runGuarded(func(ctx context.Context) error {
		store, err := planstore.Open(c.workspaceRoot)
		if err != nil {
			c.notice("strict-plan-delete: " + err.Error())
			return nil
		}
		return c.deletePlan(store, id)
	})
}

// deletePlan validates the deletion preconditions and removes the plan
// directory. A plan whose run-state.json is absent (corrupt leftover — the
// cleanup scenario) is still deletable; any *other* run-state read error
// (permission, corrupt JSON) fails closed so an unreadable executing plan can
// never be deleted by accident.
func (c *Controller) deletePlan(store *planstore.Store, id string) error {
	if err := planstore.ValidatePlanID(id); err != nil {
		c.notice("strict-plan-delete: " + err.Error())
		return nil
	}
	dir := filepath.Join(planstore.PlansDir(store.WorkspaceRoot()), id)
	if _, err := os.Stat(dir); err != nil {
		c.notice(fmt.Sprintf("strict-plan-delete: plan %q 不存在或不可读（%v）", id, err))
		return nil
	}
	rs, err := store.ReadRunState(id)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// 半写残留（无 run-state.json）：视为非 executing，允许清理。
			c.notice(fmt.Sprintf("strict-plan-delete: plan %q 运行状态缺失（残留计划），仍执行删除", id))
		} else {
			c.notice(fmt.Sprintf("strict-plan-delete: plan %q 运行状态不可读（%v），拒绝删除（无法确认其未在执行中）", id, err))
			return nil
		}
	} else if rs.Stage == planstore.StageExecuting {
		c.notice(fmt.Sprintf("strict-plan-delete: plan %q 正在执行中（stage executing），禁止删除；请等待执行结束（done/failed）后再删除", id))
		return nil
	}
	if err := store.DeletePlan(id); err != nil {
		c.notice("strict-plan-delete: " + err.Error())
		return nil
	}
	c.notice(fmt.Sprintf("strict-plan-delete: plan %q 已删除（不可恢复）", id))
	return nil
}
