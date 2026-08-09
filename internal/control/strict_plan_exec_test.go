package control

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"reasonix/internal/event"
	"reasonix/internal/planstore"
)

// validateDataContains builds a validate.py asserting data.txt contains want.
func validateDataContains(want string) string {
	return "with open('data.txt') as f:\n    assert '" + want + "' in f.read()\n"
}

// lockPlan writes and locks a plan (submitted → locked + baseline) in the
// harness workspace — the precondition of /strict-plan-exec.
func lockPlan(t *testing.T, h *strictPlanHarness, id, script string, manifest []planstore.ManifestEntry) {
	t.Helper()
	files := planstore.PlanFiles{
		ID:       id,
		Steps:    []string{"step one"},
		Script:   script,
		Scope:    []planstore.ScopeEntry{{Path: "src/", Reason: "main code"}},
		Manifest: manifest,
	}
	if err := h.store.WritePlan(id, files); err != nil {
		t.Fatalf("WritePlan(%s): %v", id, err)
	}
	if err := h.store.Transition(id, planstore.StageDrafting, planstore.StageSubmitted); err != nil {
		t.Fatalf("Transition submitted(%s): %v", id, err)
	}
	if err := h.store.Transition(id, planstore.StageSubmitted, planstore.StageLocked); err != nil {
		t.Fatalf("Transition locked(%s): %v", id, err)
	}
	if err := h.store.TakeBaseline(id); err != nil {
		t.Fatalf("TakeBaseline(%s): %v", id, err)
	}
}

// writeWS writes a workspace file (the scripted runner's stand-in for the
// model's write tools).
func writeWS(t *testing.T, h *strictPlanHarness, rel, content string) {
	t.Helper()
	p := filepath.Join(h.store.WorkspaceRoot(), filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// rmWS removes a workspace file (the stand-in for the model deleting a file).
func rmWS(t *testing.T, h *strictPlanHarness, rel string) {
	t.Helper()
	if err := os.Remove(filepath.Join(h.store.WorkspaceRoot(), filepath.FromSlash(rel))); err != nil {
		t.Fatal(err)
	}
}

// commitWS commits all workspace changes (git add -A + commit), so a file
// written before lock time is git-tracked and later shows as "modify" rather
// than "add" in CompareBaseline.
func commitWS(t *testing.T, h *strictPlanHarness) {
	t.Helper()
	ws := h.store.WorkspaceRoot()
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-q", "-m", "snapshot"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = ws
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// waitPlanStage polls the plan's run-state until it reaches want (command
// bodies run through runGuarded asynchronously).
func waitPlanStage(t *testing.T, h *strictPlanHarness, id string, want planstore.Stage) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		rs, err := h.store.ReadRunState(id)
		if err == nil && rs.Stage == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	rs, _ := h.store.ReadRunState(id)
	t.Fatalf("plan %s stage = %q, want %q", id, rs.Stage, want)
}

// runExec submits /strict-plan-exec via the command entry (async) and returns
// the event cursor to wait on.
func runExec(h *strictPlanHarness, arg string) int {
	from := len(h.eventsSnapshot())
	cmd := "/strict-plan-exec"
	if arg != "" {
		cmd += " " + arg
	}
	h.c.submitCommandOrTurn(cmd, cmd, "", false, "", "")
	return from
}

// TC-EXEC-01 前置校验：非 locked / 不存在 / 非 git 均明确报错，不进入执行。
func TestStrictPlanExecPreconditions(t *testing.T) {
	// drafting 计划 → 明确报错
	h := newStrictPlanHarness(t, nil, nil)
	files := planstore.PlanFiles{
		ID:       "plan-001",
		Steps:    []string{"step one"},
		Script:   "print('ok')",
		Scope:    []planstore.ScopeEntry{{Path: "src/", Reason: "main code"}},
		Manifest: []planstore.ManifestEntry{{Path: "src/a.txt", Action: "modify"}},
	}
	if err := h.store.WritePlan("plan-001", files); err != nil {
		t.Fatal(err)
	}
	from := runExec(h, "plan-001")
	if _, ok := h.nextNotice(from, "仅 locked 可执行", 3*time.Second); !ok {
		t.Fatal("non-locked plan must surface a clear 'locked only' notice")
	}
	if h.runner.runCount != 0 {
		t.Fatalf("no model turn must run for a non-locked plan, calls = %d", h.runner.runCount)
	}

	// 不存在的 id → 明确报错
	from = runExec(h, "nope-999")
	if _, ok := h.nextNotice(from, "nope-999", 3*time.Second); !ok {
		t.Fatal("nonexistent plan id must surface an error naming the id")
	}
	if h.runner.runCount != 0 {
		t.Fatalf("no model turn must run for a nonexistent plan, calls = %d", h.runner.runCount)
	}

	// 非 git 工作区 → 明确报错（含 git 需求），不进入执行（无 git 项目连锁定都
	// 做不到，这里验证命令入口在最早处拦截）。
	h2 := newStrictPlanHarnessGit(t, nil, nil, false)
	files = planstore.PlanFiles{
		ID:       "plan-001",
		Steps:    []string{"step one"},
		Script:   "print('ok')",
		Scope:    []planstore.ScopeEntry{{Path: "src/", Reason: "main code"}},
		Manifest: []planstore.ManifestEntry{{Path: "src/a.txt", Action: "modify"}},
	}
	if err := h2.store.WritePlan("plan-001", files); err != nil {
		t.Fatal(err)
	}
	from = runExec(h2, "plan-001")
	if _, ok := h2.nextNotice(from, "git", 3*time.Second); !ok {
		t.Fatal("non-git workspace must surface a clear git-requirement notice")
	}
	rs, err := h2.store.ReadRunState("plan-001")
	if err != nil {
		t.Fatal(err)
	}
	if rs.Stage == planstore.StageExecuting {
		t.Fatal("exec must not start on a non-git workspace")
	}
	if h2.runner.runCount != 0 {
		t.Fatalf("no model turn must run on a non-git workspace, calls = %d", h2.runner.runCount)
	}
}

// TC-EXEC-02 通过路径：脚本退出 0 且变动清单匹配 → executing→done + notice 含
// 状态流转与验收摘要（轮次、exit=0、无违规）。
func TestStrictPlanExecPassPath(t *testing.T) {
	var h *strictPlanHarness
	h = newStrictPlanHarness(t, []func(string){
		func(string) { writeWS(t, h, "data.txt", "done") },
	}, nil)
	writeWS(t, h, "data.txt", "v0")
	commitWS(t, h)
	lockPlan(t, h, "plan-001", validateDataContains("done"), []planstore.ManifestEntry{{Path: "data.txt", Action: "modify"}})

	from := runExec(h, "plan-001")
	if _, ok := h.nextNotice(from, "plan-id=plan-001 done", 10*time.Second); !ok {
		t.Fatal("passing exec must emit a done notice")
	}
	waitPlanStage(t, h, "plan-001", planstore.StageDone)
	rs, _ := h.store.ReadRunState("plan-001")
	if rs.Round != 1 || rs.RetriesLeft != 2 {
		t.Fatalf("round/retries after first-pass = %d/%d, want 1/2", rs.Round, rs.RetriesLeft)
	}
	snap := h.eventsSnapshot()
	joined := joinNotices(snap)
	if !strings.Contains(joined, "轮次 1") || !strings.Contains(joined, "exit=0") || !strings.Contains(joined, "无违规") {
		t.Fatalf("done notice must carry round / exit=0 / clean manifest summary, got %q", joined)
	}
}

// TC-EXEC-03 失败重试：脚本失败 → 注入验收反馈 → 第二轮修正 → 通过。
func TestStrictPlanExecRetryThenPass(t *testing.T) {
	var h *strictPlanHarness
	h = newStrictPlanHarness(t, []func(string){
		func(string) { writeWS(t, h, "data.txt", "partial") },
		func(input string) {
			// 注入的第二轮提示词必须携带上一轮验收输出（脚本退出码与断言失败）。
			if !strings.Contains(input, "exit code: 1") {
				t.Errorf("round-2 prompt must carry the acceptance failure output")
			}
			writeWS(t, h, "data.txt", "done")
		},
	}, nil)
	writeWS(t, h, "data.txt", "v0")
	commitWS(t, h)
	lockPlan(t, h, "plan-001", validateDataContains("done"), []planstore.ManifestEntry{{Path: "data.txt", Action: "modify"}})

	from := runExec(h, "plan-001")
	if _, ok := h.nextNotice(from, "第 1 轮未通过", 10*time.Second); !ok {
		t.Fatal("failed round must emit a progress notice")
	}
	// 第 1 轮失败后：round=2, retriesLeft=1
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rs, _ := h.store.ReadRunState("plan-001")
		if rs.Round == 2 && rs.RetriesLeft == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	rs, _ := h.store.ReadRunState("plan-001")
	if rs.Round != 2 || rs.RetriesLeft != 1 {
		t.Fatalf("after round-1 failure: round/retries = %d/%d, want 2/1", rs.Round, rs.RetriesLeft)
	}

	if _, ok := h.nextNotice(from, "plan-id=plan-001 done", 10*time.Second); !ok {
		t.Fatal("retried round must pass and emit done")
	}
	waitPlanStage(t, h, "plan-001", planstore.StageDone)
}

// TC-EXEC-04 重试耗尽：3 轮全失败 → executing→failed，停在失败态，无第 4 轮。
func TestStrictPlanExecRetriesExhausted(t *testing.T) {
	var h *strictPlanHarness
	h = newStrictPlanHarness(t, []func(string){
		func(string) { writeWS(t, h, "data.txt", "x") },
		func(string) { writeWS(t, h, "data.txt", "y") },
		func(string) { writeWS(t, h, "data.txt", "z") },
	}, nil)
	writeWS(t, h, "data.txt", "v0")
	commitWS(t, h)
	lockPlan(t, h, "plan-001", validateDataContains("NEVER"), []planstore.ManifestEntry{{Path: "data.txt", Action: "modify"}})

	from := runExec(h, "plan-001")
	if _, ok := h.nextNotice(from, "plan-id=plan-001 failed", 15*time.Second); !ok {
		t.Fatal("exhausted retries must emit a failed notice")
	}
	waitPlanStage(t, h, "plan-001", planstore.StageFailed)
	if h.runner.runCount != 3 {
		t.Fatalf("exactly 3 rounds must run, calls = %d", h.runner.runCount)
	}
	joined := joinNotices(h.eventsSnapshot())
	if !strings.Contains(joined, "重试耗尽") {
		t.Fatalf("failed notice must explain the exhausted retries, got %q", joined)
	}
}

// TC-EXEC-05 越界改动被 CompareBaseline 拦截：脚本 pass 但新增清单外文件 →
// 验收不通过 + 反馈含越界明细；删除后通过。
func TestStrictPlanExecOutOfManifestChange(t *testing.T) {
	var h *strictPlanHarness
	h = newStrictPlanHarness(t, []func(string){
		func(string) {
			writeWS(t, h, "data.txt", "done")
			writeWS(t, h, "evil.txt", "smuggled") // 清单外文件
		},
		func(input string) {
			if !strings.Contains(input, "VIOLATION") {
				t.Errorf("round-2 prompt must carry the manifest-violation detail")
			}
			rmWS(t, h, "evil.txt")
		},
	}, nil)
	writeWS(t, h, "data.txt", "v0")
	commitWS(t, h)
	lockPlan(t, h, "plan-001", validateDataContains("done"), []planstore.ManifestEntry{{Path: "data.txt", Action: "modify"}})

	from := runExec(h, "plan-001")
	if _, ok := h.nextNotice(from, "越界", 10*time.Second); !ok {
		t.Fatal("out-of-manifest change must be flagged as 越界 in the progress notice")
	}
	joined := joinNotices(h.eventsSnapshot())
	if !strings.Contains(joined, "evil.txt") {
		t.Fatalf("the violation detail must name the smuggled path, got %q", joined)
	}
	if _, ok := h.nextNotice(from, "plan-id=plan-001 done", 10*time.Second); !ok {
		t.Fatal("removing the out-of-manifest file must let the plan pass")
	}
	waitPlanStage(t, h, "plan-001", planstore.StageDone)
}

// TC-CR-05b 批准锁定但基线失败（非 git 工作区）→ 计划留在 submitted，不产生
// "locked 无 baseline" 僵尸（reviewPlan 顺序：TakeBaseline 先于 Transition）。
func TestApprovalBaselineFailureKeepsSubmitted(t *testing.T) {
	var h *strictPlanHarness
	h = newStrictPlanHarnessGit(t, []func(string){
		func(string) { submitPlan(t, h.store, "plan-001") },
	}, func(event.Event) bool { return true }, false)
	// 工作区非 git：TakeBaseline 必然失败。
	if err := h.runStrictPlan("task"); err == nil {
		t.Fatal("locking a plan on a non-git workspace must fail")
	}
	rs, err := h.store.ReadRunState("plan-001")
	if err != nil {
		t.Fatal(err)
	}
	if rs.Stage != planstore.StageSubmitted {
		t.Fatalf("stage = %q, want submitted (baseline failure must not leave a locked zombie)", rs.Stage)
	}
}

// TC-CR-05c 执行中硬错误（CompareBaseline 不可用）→ driveExecLoop 错误路径把
// executing 转 failed，不留下僵尸执行态阻塞后续执行。
func TestExecDriverErrorTransitionsFailed(t *testing.T) {
	var h *strictPlanHarness
	h = newStrictPlanHarness(t, []func(string){
		func(string) { writeWS(t, h, "data.txt", "done") }, // plan-001
		func(string) { writeWS(t, h, "data.txt", "done") }, // plan-002
	}, nil)
	lockPlan(t, h, "plan-001", validateDataContains("done"), []planstore.ManifestEntry{{Path: "data.txt", Action: "modify"}})
	// 删除 baseline.json → CompareBaseline fail-closed → 驱动器硬错误。
	if err := os.Remove(filepath.Join(h.planDir("plan-001"), "baseline.json")); err != nil {
		t.Fatal(err)
	}

	from := runExec(h, "plan-001")
	if _, ok := h.nextNotice(from, "执行出错", 15*time.Second); !ok {
		t.Fatal("driver hard error must emit a failed notice")
	}
	waitPlanStage(t, h, "plan-001", planstore.StageFailed)
	// 僵尸清理后，另一 locked 计划可以正常启动（并发锁已释放）。
	writeWS(t, h, "data.txt", "v0")
	commitWS(t, h)
	lockPlan(t, h, "plan-002", validateDataContains("done"), []planstore.ManifestEntry{{Path: "data.txt", Action: "modify"}})
	from = runExec(h, "plan-002")
	if _, ok := h.nextNotice(from, "plan-id=plan-002 done", 15*time.Second); !ok {
		rs, _ := h.store.ReadRunState("plan-002")
		t.Fatalf("after the error, the workspace must not be blocked by a zombie executing plan (plan-002 stage=%q)", rs.Stage)
	}
	waitPlanStage(t, h, "plan-002", planstore.StageDone)
}

// joinNotices concatenates all Notice texts in the snapshot (cursor-agnostic
// helper for multi-line assertion).
func joinNotices(snap []event.Event) string {
	var b strings.Builder
	for _, e := range snap {
		if e.Kind == event.Notice {
			b.WriteString(e.Text)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// firstChangeReviewEvent returns the first plan_change_review ApprovalRequest
// in a snapshot.
func firstChangeReviewEvent(snap []event.Event) event.Event {
	for _, e := range snap {
		if e.Kind == event.ApprovalRequest && e.Approval.Tool == planChangeReviewTool {
			return e
		}
	}
	return event.Event{}
}

// requestChange simulates the model calling plan_request_change inside a turn:
// the workspace gets the new content and the run-state records the pending
// change (the tool's own persistence is tested against the builtin).
func requestChange(t *testing.T, h *strictPlanHarness, id, reason, script string) {
	t.Helper()
	err := h.store.UpdateRunState(id, func(rs *planstore.RunState) {
		rs.ChangePending = true
		rs.PendingChange = &planstore.PendingChange{
			At:             time.Now().UTC().Format(time.RFC3339),
			Reason:         reason,
			ValidateScript: script,
		}
	})
	if err != nil {
		t.Fatalf("requestChange: %v", err)
	}
}

// TC-CR-01 plan_request_change 批准应用：新脚本生效 + 重试计数重置 +
// ChangeHistory 记录 approved；locks hash 更新且 .lockbackup 同步。
func TestStrictPlanChangeApprovedAndApplied(t *testing.T) {
	var h *strictPlanHarness
	h = newStrictPlanHarness(t, []func(string){
		func(string) {
			writeWS(t, h, "data.txt", "v2")
			requestChange(t, h, "plan-001", "script demands v1 but the real fix is v2", validateDataContains("v2"))
		},
	}, func(e event.Event) bool { return true }) // 变更评审也批准
	writeWS(t, h, "data.txt", "v0")
	commitWS(t, h)
	lockPlan(t, h, "plan-001", validateDataContains("v1"), []planstore.ManifestEntry{{Path: "data.txt", Action: "modify"}})
	locksBefore, _ := h.store.ReadLock("plan-001")

	from := runExec(h, "plan-001")
	// 变更评审出现：reason 含新脚本内容（可读化呈现）
	if _, ok := h.nextNotice(from, "变更已批准", 10*time.Second); !ok {
		t.Fatal("approved change must emit an applied notice")
	}
	if _, ok := h.nextNotice(from, "plan-id=plan-001 done", 10*time.Second); !ok {
		t.Fatal("with the approved new script the plan must pass")
	}
	waitPlanStage(t, h, "plan-001", planstore.StageDone)

	rev := firstChangeReviewEvent(h.eventsSnapshot())
	if rev.Approval.ID == "" {
		t.Fatal("a plan_change_review approval request must have been emitted")
	}
	if !strings.Contains(rev.Approval.Reason, "v2") {
		t.Fatalf("change review reason must show the new script, got %q", rev.Approval.Reason)
	}

	// 新脚本生效：locks hash 更新、.lockbackup 同步（Verify 不误报篡改）
	locksAfter, _ := h.store.ReadLock("plan-001")
	if locksAfter.Files["validate.py.enc"] == locksBefore.Files["validate.py.enc"] {
		t.Fatal("locks hash for validate.py must change after ApplyChange")
	}
	if err := h.store.Verify("plan-001"); err != nil {
		t.Fatalf("Verify after change must pass (lockbackup synced): %v", err)
	}
	// 重试计数重置 + 记录
	rs, _ := h.store.ReadRunState("plan-001")
	if rs.RetriesLeft != execInitialRetriesLeft {
		t.Fatalf("retriesLeft after approved change = %d, want %d (reset)", rs.RetriesLeft, execInitialRetriesLeft)
	}
	if rs.ChangePending {
		t.Fatal("ChangePending must be cleared after approval")
	}
	if n := len(rs.ChangeHistory); n == 0 || !rs.ChangeHistory[n-1].Approved {
		t.Fatalf("ChangeHistory must end with an approved record, got %+v", rs.ChangeHistory)
	}
}

// TC-CR-02 变更拒绝：ChangePending 清除 + 拒绝反馈注入 + 执行循环继续（原标准）。
func TestStrictPlanChangeDenied(t *testing.T) {
	var h *strictPlanHarness
	h = newStrictPlanHarness(t, []func(string){
		func(string) {
			writeWS(t, h, "data.txt", "v2")
			requestChange(t, h, "plan-001", "relax the script", validateDataContains("v2"))
		},
		func(input string) {
			// 拒绝后注入的反馈含拒绝语义（下一轮提示词）
			if !strings.Contains(input, "retries left") {
				t.Errorf("post-denial round prompt must carry the rejection feedback framing")
			}
			writeWS(t, h, "data.txt", "v1") // 满足原脚本
		},
	}, func(e event.Event) bool {
		return e.Approval.Tool == planLockReviewTool // 变更评审拒绝，锁定评审（若发生）批准
	})
	writeWS(t, h, "data.txt", "v0")
	commitWS(t, h)
	lockPlan(t, h, "plan-001", validateDataContains("v1"), []planstore.ManifestEntry{{Path: "data.txt", Action: "modify"}})
	locksBefore, _ := h.store.ReadLock("plan-001")

	from := runExec(h, "plan-001")
	if _, ok := h.nextNotice(from, "变更请求被拒绝", 10*time.Second); !ok {
		t.Fatal("denied change must emit a rejection notice")
	}
	if _, ok := h.nextNotice(from, "plan-id=plan-001 done", 10*time.Second); !ok {
		t.Fatal("the loop must continue under the original standard and pass")
	}
	waitPlanStage(t, h, "plan-001", planstore.StageDone)

	rs, _ := h.store.ReadRunState("plan-001")
	if rs.ChangePending {
		t.Fatal("ChangePending must be cleared after denial")
	}
	if n := len(rs.ChangeHistory); n == 0 || rs.ChangeHistory[n-1].Approved {
		t.Fatalf("ChangeHistory must end with a rejected record, got %+v", rs.ChangeHistory)
	}
	locksAfter, _ := h.store.ReadLock("plan-001")
	if locksAfter.Files["validate.py.enc"] != locksBefore.Files["validate.py.enc"] {
		t.Fatal("denied change must not touch the locked artifacts")
	}
}

// TC-CR-03 漏改语义：脚本 pass 但 manifest 声明项未发生 → 验收不通过 + 反馈含
// 漏改明细；补齐后通过。
func TestStrictPlanExecMissingManifestChange(t *testing.T) {
	var h *strictPlanHarness
	h = newStrictPlanHarness(t, []func(string){
		func(string) { writeWS(t, h, "data.txt", "done") }, // 未创建 notes.md
		func(input string) {
			if !strings.Contains(input, "MISSING") {
				t.Errorf("round-2 prompt must carry the missing-manifest detail")
			}
			writeWS(t, h, "notes.md", "notes")
		},
	}, nil)
	writeWS(t, h, "data.txt", "v0")
	commitWS(t, h)
	lockPlan(t, h, "plan-001", validateDataContains("done"), []planstore.ManifestEntry{
		{Path: "data.txt", Action: "modify"},
		{Path: "notes.md", Action: "add"},
	})

	from := runExec(h, "plan-001")
	if _, ok := h.nextNotice(from, "漏改", 10*time.Second); !ok {
		t.Fatal("a missing manifest change must be flagged as 漏改 in the progress notice")
	}
	joined := joinNotices(h.eventsSnapshot())
	if !strings.Contains(joined, "notes.md") {
		t.Fatalf("the missing detail must name the undeclared-done path, got %q", joined)
	}
	if _, ok := h.nextNotice(from, "plan-id=plan-001 done", 10*time.Second); !ok {
		t.Fatal("supplying the missing change must let the plan pass")
	}
	waitPlanStage(t, h, "plan-001", planstore.StageDone)
}

// TC-CR-04 并发拒绝：已有 executing 计划时再执行另一 locked 计划 → 明确报错，
// 目标计划保持 locked，无模型 turn。
func TestStrictPlanExecRejectsConcurrent(t *testing.T) {
	h := newStrictPlanHarness(t, nil, nil)
	writeWS(t, h, "data.txt", "v0")
	commitWS(t, h)
	lockPlan(t, h, "plan-001", "print('ok')", []planstore.ManifestEntry{{Path: "data.txt", Action: "modify"}})
	lockPlan(t, h, "plan-002", "print('ok')", []planstore.ManifestEntry{{Path: "data.txt", Action: "modify"}})
	// plan-001 已在 executing（模拟另一个执行会话）
	if err := h.store.Transition("plan-001", planstore.StageLocked, planstore.StageExecuting); err != nil {
		t.Fatal(err)
	}

	from := runExec(h, "plan-002")
	if _, ok := h.nextNotice(from, "禁止并发执行", 3*time.Second); !ok {
		t.Fatal("concurrent exec must be refused with a clear notice")
	}
	rs, _ := h.store.ReadRunState("plan-002")
	if rs.Stage != planstore.StageLocked {
		t.Fatalf("plan-002 stage = %q, want locked (must not start)", rs.Stage)
	}
	if h.runner.runCount != 0 {
		t.Fatalf("no model turn must run for a refused concurrent exec, calls = %d", h.runner.runCount)
	}
}

// compile-time guard: RequiresFreshHumanApprovalTool must include the change
// review tool (YOLO/auto cannot auto-drain it, the model cannot self-approve).
func TestPlanChangeReviewRequiresFreshHuman(t *testing.T) {
	if !RequiresFreshHumanApprovalTool(planChangeReviewTool) {
		t.Fatal("plan_change_review must be a fresh-human approval tool")
	}
}

var _ = context.Background // keep import when helpers shrink
