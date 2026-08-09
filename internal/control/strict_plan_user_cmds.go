package control

import (
	"context"
	"fmt"
	"strings"

	"reasonix/internal/planstore"
)

// This file implements the user-side plan viewing commands:
//   /strict-plan-detail [plan-id]  — full plan document (readability mechanism)
//   /strict-plan-list [plan-id]    — lightweight plan list; with an id, same as
//                                    detail
// Both reuse the planstore rendering mechanism (RenderPlanDetail /
// RenderPlanListEntry) — never hand-rolled text — so the review card, the
// detail command and (future) the exec progress display stay consistent.

// applyStrictPlanDetail implements /strict-plan-detail [plan-id]: renders the
// full plan document. Omitting the id falls back to the most recent locked
// plan; a nonexistent id surfaces a clear error.
func (c *Controller) applyStrictPlanDetail(input, display string) {
	id := strings.TrimSpace(strings.TrimPrefix(input, "/strict-plan-detail"))
	c.runGuarded(func(ctx context.Context) error {
		store, err := planstore.Open(c.workspaceRoot)
		if err != nil {
			c.notice("strict-plan-detail: " + err.Error())
			return nil
		}
		return c.showPlanDetail(store, id)
	})
}

// showPlanDetail renders one plan's full document as a Notice. With an empty
// id it resolves the most recent locked plan; otherwise the id must name an
// existing plan.
func (c *Controller) showPlanDetail(store *planstore.Store, id string) error {
	if id == "" {
		locked, err := store.MostRecentLocked()
		if err != nil {
			c.notice("strict-plan-detail: " + err.Error())
			return nil
		}
		id = locked
	}
	files, err := store.ReadPlan(id)
	if err != nil {
		c.notice(fmt.Sprintf("strict-plan-detail: plan %q 不存在或不可读（%v）", id, err))
		return nil
	}
	state, err := store.ReadRunState(id)
	if err != nil {
		c.notice(fmt.Sprintf("strict-plan-detail: plan %q 状态不可读: %v", id, err))
		return nil
	}
	lock, err := store.ReadLock(id)
	if err != nil {
		c.notice(fmt.Sprintf("strict-plan-detail: plan %q 锁定信息不可读: %v", id, err))
		return nil
	}
	c.notice(planstore.RenderPlanDetail(files, state, lock))
	return nil
}

// applyStrictPlanList implements /strict-plan-list [plan-id]: without an id it
// lists every plan as one lightweight line (id + stage + review/lock time);
// with an id it behaves like /strict-plan-detail.
func (c *Controller) applyStrictPlanList(input, display string) {
	id := strings.TrimSpace(strings.TrimPrefix(input, "/strict-plan-list"))
	c.runGuarded(func(ctx context.Context) error {
		store, err := planstore.Open(c.workspaceRoot)
		if err != nil {
			c.notice("strict-plan-list: " + err.Error())
			return nil
		}
		if id != "" {
			return c.showPlanDetail(store, id)
		}
		return c.listPlans(store)
	})
}

// listPlans renders one line per plan through RenderPlanListEntry. A plan
// whose sidecars are unreadable is marked rather than failing the whole list.
func (c *Controller) listPlans(store *planstore.Store) error {
	ids, err := store.ListPlans()
	if err != nil {
		c.notice("strict-plan-list: " + err.Error())
		return nil
	}
	if len(ids) == 0 {
		c.notice("strict-plan-list: 该工作区暂无计划")
		return nil
	}
	var b strings.Builder
	for _, id := range ids {
		state, err := store.ReadRunState(id)
		if err != nil {
			fmt.Fprintf(&b, "- %s  (stage unavailable: %v)\n", id, err)
			continue
		}
		lock, err := store.ReadLock(id)
		if err != nil {
			fmt.Fprintf(&b, "- %s  (lock info unavailable: %v)\n", id, err)
			continue
		}
		b.WriteString(planstore.RenderPlanListEntry(id, state, lock))
		b.WriteString("\n")
	}
	c.notice(strings.TrimSuffix(b.String(), "\n"))
	return nil
}
