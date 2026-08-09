package control

import (
	"context"
	"fmt"
	"strings"

	"reasonix/internal/planmode"
	"reasonix/internal/planstore"
)

// planChangeReviewTool is the Tool name on the ApprovalRequest the exec driver
// emits to gate a change request. Like planLockReviewTool it is a fresh-human
// decision: the proposing model can never self-approve, and YOLO/auto approval
// modes must not auto-drain it.
const planChangeReviewTool = "plan_change_review"

// strictExecMaxRounds caps how many model turns the acceptance loop runs under
// the same acceptance standard. The first turn is the real user turn; failed
// turns are retried with the rejection feedback injected, up to this many
// rounds total (so 2 retries). A change request approved mid-loop resets the
// retry counter (ApplyChange), per CONTEXT.md [[验收循环]].
const strictExecMaxRounds = 3

// execInitialRetriesLeft is the retry budget at loop start and after an
// approved change: maxRounds-1 retries after the first attempt.
const execInitialRetriesLeft = strictExecMaxRounds - 1

// strictExecPrompt frames the execution loop for the model: implement the
// locked plan, stay inside the write scope and the change manifest, and let
// the driver's acceptance gates judge. The plan document is rendered through
// the shared readability mechanism (RenderPlanDetail).
// Sprint 10 clarity fixes: plan_submit is forbidden during execution; the
// change payload (write scope + acceptance script, each with a reason) is
// stated explicitly; the strictness layering is explicit — steps are guidance,
// the write scope and the acceptance script are the contract; and gitignored
// paths are called out as invisible to the change comparison.
const strictExecPrompt = `You are the executor in strict plan mode. Implement the locked plan below in the workspace. The numbered steps are guidance, not a contract — what is strictly enforced is the write scope and the acceptance script. After each of your turns the driver runs the acceptance script and compares the actual file changes against the frozen change manifest; both must pass for the plan to be accepted.

Rules:
- Do not call plan_submit during execution — it cannot modify a locked/executing plan; request a change via plan_request_change instead.
- Write only files declared in the write scope; out-of-scope writes are refused by the write gate.
- Change only files declared in the change manifest; an out-of-manifest change, or a declared change that never happened, fails the baseline comparison and the turn is not accepted.
- Paths ignored by .gitignore are invisible to the change comparison; implement and assert on git-visible paths only.
- The acceptance script is readable (plan_get) but not writable.
- If the locked plan itself is wrong (script too strict, scope missing a path, manifest misdeclared), request a change via plan_request_change — the payload is a new write_scope and/or a new validate_script, each with a reason. The request goes through review and takes effect only if approved; an approved change resets the retry counter.
- You have at most %d rounds under the same acceptance standard (first attempt plus retries; an approved change request resets the budget). When the acceptance output is injected back, fix the implementation accordingly.

## Plan

%s
`

// execRejectedMessage is injected as the next turn after a failed acceptance:
// it carries the script output and the baseline-comparison violations so the
// model can either fix the implementation or request a change.
const execRejectedMessage = `Your implementation did not pass acceptance in round %d (retries left: %d).

Acceptance script exit code: %d
Script output:
%s

Change-manifest comparison:
%s

Assess the violation details above. Fix the implementation and try again. If the locked plan itself is wrong (acceptance script too strict, scope missing a path, manifest misdeclared), call plan_request_change instead — it goes through review and, if approved, resets the retry counter.`

// applyStrictPlanExec implements /strict-plan-exec [plan-id]: parse the id
// (omitted → most recent locked plan), open the store, run the acceptance
// loop.
func (c *Controller) applyStrictPlanExec(input, display string) {
	id := strings.TrimSpace(strings.TrimPrefix(input, "/strict-plan-exec"))
	c.runGuarded(func(ctx context.Context) error {
		store, err := planstore.Open(c.workspaceRoot)
		if err != nil {
			c.notice("strict-plan-exec: " + err.Error())
			return nil
		}
		return c.runStrictPlanExec(ctx, store, id, display)
	})
}

// runStrictPlanExec validates the preconditions (git workspace, locked plan,
// no concurrent execution), moves the plan to executing, then drives the
// acceptance loop to done/failed.
func (c *Controller) runStrictPlanExec(ctx context.Context, store *planstore.Store, id, display string) error {
	if err := planstore.CheckGitWorkspace(store.WorkspaceRoot()); err != nil {
		c.notice("strict-plan-exec: " + err.Error())
		return nil
	}
	if id == "" {
		locked, err := store.MostRecentLocked()
		if err != nil {
			c.notice("strict-plan-exec: " + err.Error())
			return nil
		}
		id = locked
	}
	rs, err := store.ReadRunState(id)
	if err != nil {
		c.notice(fmt.Sprintf("strict-plan-exec: plan %q 不存在或不可读（%v）", id, err))
		return nil
	}
	if rs.Stage != planstore.StageLocked {
		c.notice(fmt.Sprintf("strict-plan-exec: plan %q 处于 %s 阶段，仅 locked 可执行（请先 /strict-plan 制定并批准）", id, rs.Stage))
		return nil
	}
	// 同一工作区禁止并发执行（文件冲突风险）；不同工作区天然隔离。
	executing, err := executingPlan(store)
	if err != nil {
		c.notice("strict-plan-exec: " + err.Error())
		return nil
	}
	if executing != "" && executing != id {
		c.notice(fmt.Sprintf("strict-plan-exec: 计划 %q 正在执行中（同工作区禁止并发执行），拒绝启动 %q", executing, id))
		return nil
	}
	if err := store.Transition(id, planstore.StageLocked, planstore.StageExecuting); err != nil {
		c.notice("strict-plan-exec: " + err.Error())
		return nil
	}
	if err := store.UpdateRunState(id, func(rs *planstore.RunState) {
		rs.Round = 0
		rs.RetriesLeft = execInitialRetriesLeft
	}); err != nil {
		// The plan is now executing but the loop never starts: land it in
		// failed so the workspace is not left with a zombie executing plan
		// that would block every future execution (the driveExecLoop defer
		// only covers errors raised after this point).
		if terr := store.Transition(id, planstore.StageExecuting, planstore.StageFailed); terr == nil {
			c.notice(fmt.Sprintf("strict-plan-exec: plan-id=%s failed — 初始化失败（%v），已停在失败态", id, err))
		}
		return err
	}
	// Stamp the store and strict mode before every turn round: the plan tools
	// need the store from ctx, and the write gate / bash sandbox need strict
	// active to enforce the scope.
	ctx = planstore.WithStore(ctx, store)
	ctx = planmode.WithStrict(ctx, true)
	c.notice(fmt.Sprintf("strict-plan-exec: 计划 %s 开始执行（最多 %d 轮）", id, strictExecMaxRounds))
	return c.driveExecLoop(ctx, store, id, display)
}

// driveExecLoop runs the acceptance loop: per round, organize the prompt
// (round > 1 carries the previous rejection feedback), wait for the model turn,
// route any change request through review, then gate the workspace (hash
// verify → acceptance script → change-manifest comparison). Pass → done; fail
// → the feedback becomes the next round's prompt; retries exhausted → failed.
// Any hard error (git failure, script timeout, verify mismatch, turn error)
// also lands the plan in failed — the state machine has no exit from
// executing other than done/failed, and a stuck executing plan would block
// every future execution in the workspace.
func (c *Controller) driveExecLoop(ctx context.Context, store *planstore.Store, id, display string) (err error) {
	defer func() {
		if err != nil {
			if rs, rerr := store.ReadRunState(id); rerr == nil && rs.Stage == planstore.StageExecuting {
				if terr := store.Transition(id, planstore.StageExecuting, planstore.StageFailed); terr == nil {
					c.notice(fmt.Sprintf("strict-plan-exec: plan-id=%s failed — 执行出错（%v），已停在失败态", id, err))
				}
			}
		}
	}()
	files, err := store.ReadPlan(id)
	if err != nil {
		return err
	}
	var lastFeedback string
	round := 0
	for {
		round++
		if err := store.UpdateRunState(id, func(rs *planstore.RunState) { rs.Round = round }); err != nil {
			return err
		}
		prompt, err := c.execPrompt(store, files, id, round, lastFeedback)
		if err != nil {
			return err
		}
		if round == 1 {
			err = c.runTurnWithRawDisplay(ctx, prompt, prompt, display)
		} else {
			err = newTurnOrchestrator(c).runComposedSyntheticTurn(ctx, prompt)
		}
		if err != nil {
			return err
		}

		// A change request recorded during the turn routes through review now;
		// approving applies the new standard (and resets retries), denying
		// keeps the original one. Either way the current round is gated under
		// the effective standard without burning an extra turn.
		if changed, err := c.handlePendingChange(ctx, store, id); err != nil {
			return err
		} else if changed {
			// refresh the plan view for the (possibly) changed artifacts and
			// drop the stale rejection feedback — the acceptance standard
			// changed, so the old complaint no longer describes this round.
			files, err = store.ReadPlan(id)
			if err != nil {
				return err
			}
			lastFeedback = ""
		}

		if err := store.Verify(id); err != nil {
			return err
		}
		res, err := store.RunAcceptanceScript(ctx, id)
		if err != nil {
			return err
		}
		comp, err := store.CompareBaseline(id)
		if err != nil {
			return err
		}

		if res.ExitCode == 0 && comp.OK() {
			if err := store.Transition(id, planstore.StageExecuting, planstore.StageDone); err != nil {
				return err
			}
			c.notice(fmt.Sprintf("strict-plan-exec: plan-id=%s done（轮次 %d）— 验收脚本 exit=0，变动清单无违规（越界 0 / 漏改 0）",
				id, round))
			return nil
		}

		// Failed this round: the retry budget is the arbiter. With no budget
		// left the plan is exhausted → failed; otherwise the rejection
		// feedback becomes the next round's prompt. An approved change
		// (ApplyChange) resets RetriesLeft, granting a fresh budget under the
		// new acceptance standard — each review is an explicit user decision,
		// so the loop cannot spin without user consent.
		if rs, rerr := store.ReadRunState(id); rerr == nil && rs.RetriesLeft <= 0 {
			if err := store.Transition(id, planstore.StageExecuting, planstore.StageFailed); err != nil {
				return err
			}
			c.notice(fmt.Sprintf("strict-plan-exec: plan-id=%s failed — %d 轮重试耗尽，验收失败输出与最后实现状态已交还你处理（计划停在失败态；如需重新尝试请制定新计划）",
				id, round))
			return nil
		}
		if err := store.UpdateRunState(id, func(rs *planstore.RunState) {
			rs.RetriesLeft--
			rs.Round = round + 1
		}); err != nil {
			return err
		}
		retriesLeft := storeRetriesLeft(store, id)
		lastFeedback = fmt.Sprintf(execRejectedMessage, round, retriesLeft, res.ExitCode, res.Output, renderComparison(comp))
		c.notice(fmt.Sprintf("strict-plan-exec: 第 %d 轮未通过 — 验收脚本 exit=%d；变动清单：%s（下一轮 round=%d retriesLeft=%d）",
			round, res.ExitCode, renderComparisonBrief(comp), round+1, retriesLeft))
	}
}

// execPrompt renders the locked plan (readability mechanism) into the executor
// framing for one round; feedback from the previous failed round is prefixed.
func (c *Controller) execPrompt(store *planstore.Store, files planstore.PlanFiles, id string, round int, feedback string) (string, error) {
	state, err := store.ReadRunState(id)
	if err != nil {
		return "", err
	}
	lock, err := store.ReadLock(id)
	if err != nil {
		return "", err
	}
	plan := fmt.Sprintf(strictExecPrompt, strictExecMaxRounds, planstore.RenderPlanDetail(files, state, lock))
	if feedback == "" {
		return plan, nil
	}
	return feedback + "\n\n" + plan, nil
}

// handlePendingChange routes an outstanding change request through the review
// channel: approve → ApplyChange (new artifacts + lock refresh + retry reset),
// deny → DenyChange (original standard continues). Returns whether a change
// was applied (caller refreshes its plan view).
func (c *Controller) handlePendingChange(ctx context.Context, store *planstore.Store, id string) (bool, error) {
	rs, err := store.ReadRunState(id)
	if err != nil {
		return false, err
	}
	if !rs.ChangePending || rs.PendingChange == nil {
		return false, nil
	}
	pc := *rs.PendingChange
	reason, err := c.changeReviewReason(store, id, pc)
	if err != nil {
		return false, err
	}
	r, err := c.requestFreshApprovalDecision(ctx, planChangeReviewTool, "Apply change to plan "+id+"?", nil, reason)
	if err != nil {
		return false, err
	}
	if r.allow {
		if err := store.ApplyChange(id, pc, execInitialRetriesLeft); err != nil {
			return false, err
		}
		c.notice(fmt.Sprintf("strict-plan-exec: plan-id=%s 变更已批准并应用（重试计数重置为 %d）", id, execInitialRetriesLeft))
		return true, nil
	}
	if err := store.DenyChange(id); err != nil {
		return false, err
	}
	c.notice(fmt.Sprintf("strict-plan-exec: plan-id=%s 变更请求被拒绝，继续按原验收标准执行", id))
	return false, nil
}

// changeReviewReason renders the review card for a change request: the current
// plan document (readability mechanism) plus the proposed change content.
func (c *Controller) changeReviewReason(store *planstore.Store, id string, pc planstore.PendingChange) (string, error) {
	files, err := store.ReadPlan(id)
	if err != nil {
		return "", err
	}
	state, err := store.ReadRunState(id)
	if err != nil {
		return "", err
	}
	lock, err := store.ReadLock(id)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(planstore.RenderPlanDetail(files, state, lock))
	b.WriteString("\n\n## 变更请求\n")
	fmt.Fprintf(&b, "- 理由: %s\n", pc.Reason)
	if pc.ValidateScript != "" {
		b.WriteString("- 新验收脚本 (validate.py):\n```python\n")
		b.WriteString(strings.TrimRight(pc.ValidateScript, "\n"))
		b.WriteString("\n```\n")
	}
	for _, e := range pc.WriteScope {
		fmt.Fprintf(&b, "- 新可写范围: %s —— %s\n", e.Path, e.Reason)
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// executingPlan returns the plan id currently executing in this workspace, or
// "" when none (concurrent execution is forbidden per workspace).
func executingPlan(s *planstore.Store) (string, error) {
	ids, err := s.ListPlans()
	if err != nil {
		return "", err
	}
	for _, id := range ids {
		rs, err := s.ReadRunState(id)
		if err != nil {
			continue
		}
		if rs.Stage == planstore.StageExecuting {
			return id, nil
		}
	}
	return "", nil
}

// storeRetriesLeft re-reads the run state to report retries left (helper for
// notices after the UpdateRunState above).
func storeRetriesLeft(store *planstore.Store, id string) int {
	rs, err := store.ReadRunState(id)
	if err != nil {
		return 0
	}
	return rs.RetriesLeft
}

// renderComparison renders the baseline comparison for the injected feedback:
// every actual change with its manifest match status, plus missing entries.
func renderComparison(comp planstore.BaselineComparison) string {
	var b strings.Builder
	if len(comp.Actual) == 0 && len(comp.Missing) == 0 {
		return "no file changes observed"
	}
	for _, d := range comp.Actual {
		mark := "ok"
		if !d.InManifest {
			mark = "VIOLATION: out of manifest"
		}
		fmt.Fprintf(&b, "- %s [%s] %s\n", d.Path, d.Action, mark)
	}
	for _, m := range comp.Missing {
		fmt.Fprintf(&b, "- %s [%s] MISSING: declared but not done\n", m.Path, m.Action)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// renderComparisonBrief renders a one-line summary of violations for the
// progress notice: counts of out-of-manifest changes and missing entries.
func renderComparisonBrief(comp planstore.BaselineComparison) string {
	out := comp.OutOfManifest()
	var parts []string
	if len(out) > 0 {
		parts = append(parts, fmt.Sprintf("越界 %d（%s）", len(out), out[0].Path))
	}
	if len(comp.Missing) > 0 {
		parts = append(parts, fmt.Sprintf("漏改 %d（%s）", len(comp.Missing), comp.Missing[0].Path))
	}
	if len(parts) == 0 {
		return "无违规"
	}
	return strings.Join(parts, "；")
}
