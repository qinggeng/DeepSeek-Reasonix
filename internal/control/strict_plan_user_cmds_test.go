package control

import (
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/planstore"
)

// nextNotice scans the harness event log from cursor `from` for a Notice
// containing needle, polling until it appears or the timeout elapses. Command
// bodies run through runGuarded asynchronously, so assertions must (a) wait
// for the new notice and (b) skip earlier events — otherwise a stale notice
// from a previous command (or runStrictPlan) fakes a pass and a command whose
// body was dropped by the running guard goes unnoticed.
func (h *strictPlanHarness) nextNotice(from int, needle string, timeout time.Duration) (int, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		snap := h.eventsSnapshot()
		for i := from; i < len(snap); i++ {
			e := snap[i]
			if e.Kind == event.Notice && strings.Contains(e.Text, needle) {
				return i + 1, true
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	// 最后一次扫描，避免 deadline 边界后到达的事件被漏掉。
	snap := h.eventsSnapshot()
	for i := from; i < len(snap); i++ {
		if snap[i].Kind == event.Notice && strings.Contains(snap[i].Text, needle) {
			return i + 1, true
		}
	}
	return len(snap), false
}

// TC-05 命令 /strict-plan-detail：省略 id → 最近锁定计划兜底；显式 id →
// 对应计划完整详情；不存在的 id → 明确错误。
func TestStrictPlanDetailCommand(t *testing.T) {
	var h *strictPlanHarness
	h = newStrictPlanHarness(t, []func(string){
		func(string) { submitPlan(t, h.store, "plan-001") },
		func(string) { submitPlan(t, h.store, "plan-002") },
	}, func(event.Event) bool { return true })
	if err := h.runStrictPlan("task a"); err != nil {
		t.Fatalf("runStrictPlan a: %v", err)
	}
	// 保证 plan-002 的目录 modtime 严格晚于 plan-001（最近锁定判定依据）。
	time.Sleep(1100 * time.Millisecond)
	if err := h.runStrictPlan("task b"); err != nil {
		t.Fatalf("runStrictPlan b: %v", err)
	}

	// 省略 id → 最近锁定计划 plan-002 的完整详情
	from := len(h.eventsSnapshot())
	h.c.submitCommandOrTurn("/strict-plan-detail", "/strict-plan-detail", "", false, "", "")
	to, ok := h.nextNotice(from, "```python", 3*time.Second)
	if !ok {
		t.Fatal("detail output must render the script as a code block")
	}
	snap := h.eventsSnapshot()
	detail := ""
	for i := from; i < to && i < len(snap); i++ {
		if snap[i].Kind == event.Notice {
			detail += snap[i].Text
		}
	}
	if !strings.Contains(detail, "plan-002") {
		t.Fatalf("omitted id must fall back to the most recent locked plan (plan-002), got %q", detail)
	}
	if !strings.Contains(detail, "step one") || !strings.Contains(detail, "step two") {
		t.Fatalf("detail output must contain the plan steps, got %q", detail)
	}

	// 显式 id → 对应计划详情（新 notice 以 # Plan plan-001 开头）
	from = len(h.eventsSnapshot())
	h.c.submitCommandOrTurn("/strict-plan-detail plan-001", "/strict-plan-detail plan-001", "", false, "", "")
	if from, ok = h.nextNotice(from, "# Plan plan-001", 3*time.Second); !ok {
		t.Fatal("explicit id must show that plan's detail")
	}

	// 不存在的 id → 明确错误（含该 id），无计划内容输出
	from = len(h.eventsSnapshot())
	h.c.submitCommandOrTurn("/strict-plan-detail nope-999", "/strict-plan-detail nope-999", "", false, "", "")
	to, ok = h.nextNotice(from, "nope-999", 3*time.Second)
	if !ok {
		t.Fatal("nonexistent plan id must surface a clear error naming the id")
	}
	snap = h.eventsSnapshot()
	for i := from; i < to && i < len(snap); i++ {
		if snap[i].Kind == event.Notice && strings.Contains(snap[i].Text, "```python") {
			t.Fatal("error notice must not contain plan content")
		}
	}
}

// TC-06 命令 /strict-plan-list：无参列出所有计划（id+stage，无正文）；带 id 同
// detail（含脚本代码块）；空 store 明确提示。
func TestStrictPlanListCommand(t *testing.T) {
	h := newStrictPlanHarness(t, nil, nil)
	write := func(id string, stage planstore.Stage) {
		t.Helper()
		files := planstore.PlanFiles{
			ID:       id,
			Steps:    []string{"step one"},
			Script:   "print('ok')",
			Scope:    []planstore.ScopeEntry{{Path: "src/", Reason: "main code"}},
			Manifest: []planstore.ManifestEntry{{Path: "src/main.go", Action: "add"}},
		}
		if err := h.store.WritePlan(id, files); err != nil {
			t.Fatalf("WritePlan %s: %v", id, err)
		}
		if stage == planstore.StageLocked || stage == planstore.StageRejected {
			if err := h.store.Transition(id, planstore.StageDrafting, planstore.StageSubmitted); err != nil {
				t.Fatalf("Transition %s submitted: %v", id, err)
			}
		}
		if stage == planstore.StageLocked {
			if err := h.store.Transition(id, planstore.StageSubmitted, planstore.StageLocked); err != nil {
				t.Fatalf("Transition %s locked: %v", id, err)
			}
		}
		if stage == planstore.StageRejected {
			if err := h.store.Transition(id, planstore.StageSubmitted, planstore.StageRejected); err != nil {
				t.Fatalf("Transition %s rejected: %v", id, err)
			}
		}
	}
	write("plan-001", planstore.StageLocked)
	write("plan-002", planstore.StageRejected)
	write("plan-003", planstore.StageDrafting)

	// 无参 → 三个计划 id + stage 都出现在同一条列表 notice 中，无计划正文
	from := len(h.eventsSnapshot())
	h.c.submitCommandOrTurn("/strict-plan-list", "/strict-plan-list", "", false, "", "")
	to, ok := h.nextNotice(from, "plan-001", 3*time.Second)
	if !ok {
		t.Fatal("list must contain plan-001")
	}
	snap := h.eventsSnapshot()
	joined := ""
	for i := from; i < to && i < len(snap); i++ {
		if snap[i].Kind == event.Notice {
			joined += snap[i].Text
		}
	}
	for _, id := range []string{"plan-001", "plan-002", "plan-003"} {
		if !strings.Contains(joined, id) {
			t.Fatalf("list must contain %s, got %q", id, joined)
		}
	}
	for _, stage := range []string{"locked", "rejected", "drafting"} {
		if !strings.Contains(joined, stage) {
			t.Fatalf("list must contain stage %s, got %q", stage, joined)
		}
	}
	if strings.Contains(joined, "```python") {
		t.Fatal("bare list must not render plan bodies (script code blocks)")
	}

	// 带 id → 与 detail 等价的完整详情
	from = len(h.eventsSnapshot())
	h.c.submitCommandOrTurn("/strict-plan-list plan-001", "/strict-plan-list plan-001", "", false, "", "")
	if _, ok = h.nextNotice(from, "# Plan plan-001", 3*time.Second); !ok {
		t.Fatal("list with plan id must render the full detail")
	}
	if _, ok = h.nextNotice(from, "```python", 3*time.Second); !ok {
		t.Fatal("list with plan id must render the script code block")
	}

	// 空 store → 明确提示
	h2 := newStrictPlanHarness(t, nil, nil)
	h2.c.submitCommandOrTurn("/strict-plan-list", "/strict-plan-list", "", false, "", "")
	if _, ok := h2.nextNotice(0, "暂无计划", 3*time.Second); !ok {
		t.Fatal("empty store list must surface a clear 'no plans' style notice")
	}
}
