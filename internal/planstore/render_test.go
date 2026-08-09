package planstore

import (
	"strconv"
	"strings"
	"testing"
)

// planFixture returns a representative locked plan used across the render
// tests: three steps, a multi-line python acceptance script, two write-scope
// entries and two change-manifest entries.
func planFixture() (PlanFiles, RunState, LockState) {
	files := PlanFiles{
		ID: "plan-001",
		Steps: []string{
			"step one: create src/main.go",
			"step two: implement the adder",
			"step three: run the acceptance script",
		},
		Script: "#!/usr/bin/env python3\nimport sys\n\ndef main():\n    assert True\n\nif __name__ == \"__main__\":\n    main()\n",
		Scope: []ScopeEntry{
			{Path: "src/", Reason: "main code"},
			{Path: "README.md", Reason: "documentation"},
		},
		Manifest: []ManifestEntry{
			{Path: "src/main.go", Action: "add"},
			{Path: "README.md", Action: "modify"},
		},
	}
	state := RunState{PlanID: "plan-001", Stage: StageLocked, Round: 0, RetriesLeft: 3}
	lock := LockState{PlanID: "plan-001", Review: ReviewApproved, LockedAt: "2026-08-08T08:00:00Z"}
	return files, state, lock
}

// TC-01 机制 RenderPlanDetail：锁定计划的完整渲染——状态/评审结论/锁定时间、
// 步骤编号、脚本代码块（真实换行、无字面 \n）、可写范围路径+原因、变动清单 path+action。
func TestRenderPlanDetailLockedPlan(t *testing.T) {
	files, state, lock := planFixture()
	out := RenderPlanDetail(files, state, lock)

	if !strings.Contains(out, "plan-001") {
		t.Errorf("detail must name the plan id, got:\n%s", out)
	}
	if !strings.Contains(out, "stage: locked") {
		t.Errorf("detail must show stage locked, got:\n%s", out)
	}
	if !strings.Contains(out, "评审: approved") {
		t.Errorf("detail must show review approved, got:\n%s", out)
	}
	if !strings.Contains(out, "2026-08-08T08:00:00Z") {
		t.Errorf("detail must show the lock time, got:\n%s", out)
	}
	for i, step := range files.Steps {
		if !strings.Contains(out, step) {
			t.Errorf("detail must contain step %q, got:\n%s", step, out)
		}
		want := strconv.Itoa(i+1) + ". "
		if !strings.Contains(out, want) {
			t.Errorf("detail must number step %d as %q, got:\n%s", i+1, want, out)
		}
	}
	if !strings.Contains(out, "```python") {
		t.Errorf("detail must wrap the script in a ```python block, got:\n%s", out)
	}
	if !strings.Contains(out, "```") {
		t.Errorf("detail must close the script code block, got:\n%s", out)
	}
	if strings.Contains(out, `\n`) {
		t.Errorf("detail must not contain literal backslash-n escapes, got:\n%s", out)
	}
	if !strings.Contains(out, "assert True") {
		t.Errorf("detail must keep the script body line, got:\n%s", out)
	}
	for _, e := range files.Scope {
		if !strings.Contains(out, e.Path) || !strings.Contains(out, e.Reason) {
			t.Errorf("detail must contain scope entry %s (%s), got:\n%s", e.Path, e.Reason, out)
		}
	}
	for _, m := range files.Manifest {
		want := m.Path + " [" + m.Action + "]"
		if !strings.Contains(out, want) {
			t.Errorf("detail must contain manifest line %q, got:\n%s", want, out)
		}
	}
}

// TC-02 机制 RenderPlanDetail：评审中/被拒状态——submitted+pending 显示待评审
// 无锁定时间；rejected 显示 rejected；两态步骤/脚本/范围/清单仍完整。
func TestRenderPlanDetailPendingAndRejected(t *testing.T) {
	files, _, _ := planFixture()

	// 评审中：submitted + pending，无锁定时间
	submitted := RunState{PlanID: "plan-001", Stage: StageSubmitted}
	pending := LockState{PlanID: "plan-001", Review: ReviewPending}
	out := RenderPlanDetail(files, submitted, pending)
	if !strings.Contains(out, "stage: submitted") {
		t.Errorf("submitted detail must show stage submitted, got:\n%s", out)
	}
	if !strings.Contains(out, "评审: pending") {
		t.Errorf("submitted detail must show review pending, got:\n%s", out)
	}
	if strings.Contains(out, "2026-08-08T08:00:00Z") {
		t.Errorf("pending detail must not show a lock time, got:\n%s", out)
	}
	if !strings.Contains(out, "```python") || !strings.Contains(out, "assert True") {
		t.Errorf("pending detail must still render the script block, got:\n%s", out)
	}

	// 被拒：rejected + rejected
	rejected := RunState{PlanID: "plan-001", Stage: StageRejected}
	rejLock := LockState{PlanID: "plan-001", Review: ReviewRejected}
	out = RenderPlanDetail(files, rejected, rejLock)
	if !strings.Contains(out, "stage: rejected") {
		t.Errorf("rejected detail must show stage rejected, got:\n%s", out)
	}
	if !strings.Contains(out, "评审: rejected") {
		t.Errorf("rejected detail must show review rejected, got:\n%s", out)
	}
	if strings.Contains(out, "2026-08-08T08:00:00Z") {
		t.Errorf("rejected detail must not show a lock time, got:\n%s", out)
	}
	for _, step := range files.Steps {
		if !strings.Contains(out, step) {
			t.Errorf("rejected detail must keep step %q, got:\n%s", step, out)
		}
	}
}

// TC-03 机制 RenderPlanListEntry：轻量列表行——id+stage、locked 含评审结论与
// 锁定时间、rejected 含结论、drafting 无时间、单行无计划正文。
func TestRenderPlanListEntry(t *testing.T) {
	locked := RenderPlanListEntry("plan-001", RunState{PlanID: "plan-001", Stage: StageLocked}, LockState{PlanID: "plan-001", Review: ReviewApproved, LockedAt: "2026-08-08T08:00:00Z"})
	if !strings.Contains(locked, "plan-001") || !strings.Contains(locked, "locked") {
		t.Errorf("locked list entry must carry id and stage, got %q", locked)
	}
	if !strings.Contains(locked, "approved") || !strings.Contains(locked, "2026-08-08T08:00:00Z") {
		t.Errorf("locked list entry must carry review + lock time, got %q", locked)
	}

	rejected := RenderPlanListEntry("plan-002", RunState{PlanID: "plan-002", Stage: StageRejected}, LockState{PlanID: "plan-002", Review: ReviewRejected})
	if !strings.Contains(rejected, "plan-002") || !strings.Contains(rejected, "rejected") {
		t.Errorf("rejected list entry must carry id and rejected stage/review, got %q", rejected)
	}

	drafting := RenderPlanListEntry("plan-003", RunState{PlanID: "plan-003", Stage: StageDrafting}, LockState{PlanID: "plan-003", Review: ReviewPending})
	if !strings.Contains(drafting, "plan-003") || !strings.Contains(drafting, "drafting") {
		t.Errorf("drafting list entry must carry id and stage, got %q", drafting)
	}
	if strings.Contains(drafting, "2026-08-08") {
		t.Errorf("drafting list entry must not show a lock time, got %q", drafting)
	}
	if strings.Contains(drafting, "\n") {
		t.Errorf("list entry must be a single line, got %q", drafting)
	}
}

