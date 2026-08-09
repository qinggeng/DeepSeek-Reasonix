package control

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"reasonix/internal/planstore"
)

// planLockReviewTool is the Tool name on the ApprovalRequest the /strict-plan
// driver emits to gate plan locking. It is a fresh-human decision (see
// RequiresFreshHumanApprovalTool): the proposing model can never self-approve,
// and YOLO/auto approval modes must not auto-drain it. The desktop renders it
// as an ordinary approval card (subject = plan id, reason = plan digest).
const planLockReviewTool = "plan_lock_review"

// strictPlanMaxRounds caps how many turns the driver runs under the same
// submission prompt before giving up. The gate counts *submissions*, not talk:
// a model that never calls plan_submit is retried up to this many times, then
// the driver stops and hands control back to the user.
const strictPlanMaxRounds = 3

// strictPlanPrompt is the plan-stage framing given to the model: the artifact
// trio exists only behind the plan_submit gate, so the model cannot bypass the
// gate by writing plan files into the workspace.
const strictPlanPrompt = `You are the plan drafter in strict plan mode. Produce a complete, executable plan for the task below, then submit it through the plan_submit tool — the ONLY channel that enters review. The plan artifact directory is invisible to your write tools, so writing plan files into the workspace is impossible and pointless.

The plan_submit payload has four parts:
1. plan_id — a project-unique id you propose: letters, digits and hyphens (e.g. plan-001). A conflict with an existing non-rejected plan is rejected.
2. steps — the plan steps in execution order (non-empty).
3. validate_script — the acceptance script (Python). Exit code 0 = accepted, non-zero = rejected. It is the programmatic acceptance standard, so make its assertions strong enough to catch a fake implementation.
4. write_scope — the whitelist of paths you may write during execution (path + reason each; a directory path grants everything below it recursively). Declare every path you will touch. Keep the write scope and change manifest concise: a directory prefix covers everything below it recursively — do not list every file individually.
5. change_manifest — the expected file changes (path + action in add|modify|delete), frozen at lock time. Paths ignored by .gitignore are invisible to the change comparison, so declare only git-visible paths that will actually change.

Validation runs automatically on plan_submit: empty steps, empty or non-parseable script, empty scope, malformed entries, an oversized scope or manifest or a duplicate plan id all return the concrete error — fix and re-submit. Stating that the plan is complete without calling plan_submit does not count. Do not modify workspace files during planning; this stage produces the plan only.`

// planRejectedMessage is injected as a synthetic turn when the user rejects the
// review: it sends the model back into the drafting loop to revise and re-submit.
const planRejectedMessage = `Your plan %s did not pass review and was rejected. Revise the plan artifacts (steps, validate_script, write_scope, change_manifest) to address the rejection, then call plan_submit again. Re-submitting with the same plan_id is allowed after a rejection. Do not write plan files into the workspace — plan_submit is the only channel.`

// applyStrictPlan is the /strict-plan command entry: it parses the task, opens
// the workspace plan store, and runs the submission-gate driver loop.
func (c *Controller) applyStrictPlan(input, display string) {
	task := strings.TrimSpace(strings.TrimPrefix(input, "/strict-plan"))
	if task == "" {
		c.notice("usage: /strict-plan <任务描述> — 进入计划制定：plan_submit 工具门提交 → 评审锁定 → 输出 plan-id")
		return
	}
	c.runGuarded(func(ctx context.Context) error {
		store, err := planstore.Open(c.workspaceRoot)
		if err != nil {
			c.notice("strict-plan: " + err.Error())
			return nil
		}
		return c.runStrictPlan(ctx, store, task, display)
	})
}

// runStrictPlan drives the plan-stage loop: submission gate (plan_submit must
// be called and pass validation — observed via the store reaching submitted),
// then review dispatch (user approval → lock + baseline; rejection → injected
// rework turn back into the drafting loop). The revision loop is unbounded by
// design: every review is an explicit user decision, so the user controls when
// it stops by simply not approving.
func (c *Controller) runStrictPlan(ctx context.Context, store *planstore.Store, task, display string) error {
	// The plan_* tools read their store from the call context; without this
	// stamp they fail closed ("plan store is not available in this context").
	// Stamp before every turn round so both the first real turn and the
	// synthetic continuations (drivePlanSubmission + the rework loop) see it.
	ctx = planstore.WithStore(ctx, store)
	prompt := strictPlanPrompt + "\n\n## Task\n\n" + task
	for {
		id, err := c.drivePlanSubmission(ctx, store, prompt, task, display)
		if err != nil {
			return err
		}
		if id == "" {
			c.notice("strict-plan: 计划阶段未在 " + strconv.Itoa(strictPlanMaxRounds) + " 轮内经 plan_submit 提交（工具门未通过），已停下交还你处理")
			return nil
		}
		locked, err := c.reviewPlan(ctx, store, id)
		if err != nil {
			return err
		}
		if locked {
			c.notice("strict-plan: plan-id=" + id + " locked")
			return nil
		}
		// Rejected: send the model back into the drafting loop with feedback.
		if err := newTurnOrchestrator(c).runComposedSyntheticTurn(ctx, fmt.Sprintf(planRejectedMessage, id)); err != nil {
			return err
		}
	}
}

// drivePlanSubmission runs the model under the same drafting prompt until a
// plan reaches submitted or the round cap is exhausted. Returns "" when the cap
// is exhausted without a submission. The first round is a real user turn; later
// rounds are synthetic continuations under the same prompt (mirroring the
// approved-execution retry semantics in runOrchestratedTurn). A submission that
// already landed (e.g. a rework during the injected rejection-feedback turn) is
// picked up immediately without burning a round.
func (c *Controller) drivePlanSubmission(ctx context.Context, store *planstore.Store, prompt, task, display string) (string, error) {
	if id, err := submittedPlan(store); err != nil {
		return "", err
	} else if id != "" {
		return id, nil
	}
	for round := 1; round <= strictPlanMaxRounds; round++ {
		var err error
		if round == 1 {
			err = c.runTurnWithRawDisplay(ctx, prompt, task, display)
		} else {
			err = newTurnOrchestrator(c).runComposedSyntheticTurn(ctx, prompt)
		}
		if err != nil {
			return "", err
		}
		id, err := submittedPlan(store)
		if err != nil {
			return "", err
		}
		if id != "" {
			return id, nil
		}
	}
	return "", nil
}

// submittedPlan returns the plan id currently awaiting review (stage submitted),
// or "" when none exists.
func submittedPlan(s *planstore.Store) (string, error) {
	ids, err := s.ListPlans()
	if err != nil {
		return "", err
	}
	for _, id := range ids {
		rs, err := s.ReadRunState(id)
		if err != nil {
			return "", err
		}
		if rs.Stage == planstore.StageSubmitted {
			return id, nil
		}
	}
	return "", nil
}

// reviewPlan dispatches the plan-lock review to the user through the approval
// channel (the model cannot self-approve). On approval it locks the plan and
// freezes the workspace baseline; on rejection it marks the plan rejected and
// returns locked=false so the driver can inject rework feedback.
func (c *Controller) reviewPlan(ctx context.Context, store *planstore.Store, id string) (bool, error) {
	reason, err := planReviewReason(store, id)
	if err != nil {
		return false, err
	}
	// fresh decision: a user trust/business choice, not an ordinary tool
	// permission — YOLO/auto must not answer or drain it, and the proposing
	// model can never self-approve (RequiresFreshHumanApprovalTool).
	r, err := c.requestFreshApprovalDecision(ctx, planLockReviewTool, "Lock plan "+id+"?", nil, reason)
	if err != nil {
		return false, err
	}
	if r.allow {
		// TakeBaseline before Transition: git-baseline failure (e.g. no git in
		// the workspace) must leave the plan in submitted — a plan frozen as
		// locked without a baseline would be unexecutable and stuck.
		if err := store.TakeBaseline(id); err != nil {
			return false, err
		}
		if err := store.Transition(id, planstore.StageSubmitted, planstore.StageLocked); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := store.Transition(id, planstore.StageSubmitted, planstore.StageRejected); err != nil {
		return false, err
	}
	return false, nil
}

// planReviewReason builds the human-readable review card digest through the
// plan readability mechanism (planstore.RenderPlanDetail): numbered steps, the
// full acceptance script as a ```python code block (never truncated, never
// JSON-escaped), the write scope with reasons and the change manifest. The
// review card is the user's decision basis, so completeness is a requirement,
// not an option.
func planReviewReason(s *planstore.Store, id string) (string, error) {
	files, err := s.ReadPlan(id)
	if err != nil {
		return "", err
	}
	state, err := s.ReadRunState(id)
	if err != nil {
		return "", err
	}
	lock, err := s.ReadLock(id)
	if err != nil {
		return "", err
	}
	return planstore.RenderPlanDetail(files, state, lock), nil
}
