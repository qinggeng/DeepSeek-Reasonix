package control

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/planstore"
)

// gitInitWorkspace makes ws a git repository with an initial commit, so
// TakeBaseline/CompareBaseline (which diff against git HEAD) work in the
// harness. Runs before planstore.Open so .reasonix stays untracked.
func gitInitWorkspace(t *testing.T, ws string) {
	t.Helper()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = ws
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	run("config", "user.email", "planstore@test")
	run("config", "user.name", "planstore")
	run("config", "core.autocrlf", "false")
	if err := os.WriteFile(filepath.Join(ws, ".gitkeep"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "baseline")
}

// strictPlanRunner records every Run call; scripts simulate the model's turn
// output by mutating the real plan store (the stand-in for plan_submit's side
// effects, which the built-in tool itself is tested against).
type strictPlanRunner struct {
	exec     *agent.Agent
	scripts  []func(input string)
	runCount int
	inputs   []string
}

func (r *strictPlanRunner) Run(_ context.Context, input string) error {
	r.runCount++
	r.inputs = append(r.inputs, input)
	if len(r.scripts) == 0 {
		return nil
	}
	next := r.scripts[0]
	r.scripts = r.scripts[1:]
	next(input)
	return nil
}

// submitPlan simulates a successful plan_submit call: artifacts written and the
// plan advanced to submitted (validation itself lives in the builtin tool).
func submitPlan(t *testing.T, s *planstore.Store, id string) {
	t.Helper()
	files := planstore.PlanFiles{
		ID:       id,
		Steps:    []string{"step one", "step two"},
		Script:   "print('validate')",
		Scope:    []planstore.ScopeEntry{{Path: "src/", Reason: "main code"}},
		Manifest: []planstore.ManifestEntry{{Path: "src/main.go", Action: "modify"}},
	}
	if err := s.WritePlan(id, files); err != nil {
		t.Fatalf("simulate plan_submit WritePlan: %v", err)
	}
	if err := s.Transition(id, planstore.StageDrafting, planstore.StageSubmitted); err != nil {
		t.Fatalf("simulate plan_submit Transition: %v", err)
	}
}

// reworkAndResubmit simulates the model's revision after a rejection: the same
// plan id is re-submitted from the rejected stage.
func reworkAndResubmit(t *testing.T, s *planstore.Store, id string) {
	t.Helper()
	if err := s.Transition(id, planstore.StageRejected, planstore.StageSubmitted); err != nil {
		t.Fatalf("simulate rework resubmit: %v", err)
	}
}

// strictPlanHarness binds a controller to a real plan store on a temp workspace
// with a scripted runner. reviewResponder answers each plan_lock_review
// approval (return allow); a nil responder leaves approvals pending for manual
// answering.
type strictPlanHarness struct {
	mu        sync.Mutex // guards events/approvals (sink appends on driver/Approve goroutines)
	c         *Controller
	runner    *strictPlanRunner
	store     *planstore.Store
	events    []event.Event
	approvals []event.Event // plan_lock_review ApprovalRequest events, in order
	// opinionResponder (A3), when set, answers every plan_lock_review /
	// plan_change_review with (allow, opinion) through ApproveWithOpinion,
	// replacing the plain bool responder.
	opinionResponder func(e event.Event) (bool, string)
}

func newStrictPlanHarness(t *testing.T, scripts []func(input string), reviewResponder func(e event.Event) bool) *strictPlanHarness {
	return newStrictPlanHarnessGit(t, scripts, reviewResponder, true)
}

// newStrictPlanHarnessGit is the parameterized core: git=false skips the
// workspace git initialization so callers can exercise the non-git fail-closed
// paths of the strict plan commands.
func newStrictPlanHarnessGit(t *testing.T, scripts []func(input string), reviewResponder func(e event.Event) bool, git bool) *strictPlanHarness {
	t.Helper()
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	ws := t.TempDir()
	if git {
		gitInitWorkspace(t, ws)
	}

	exec := agent.New(nil, nil, agent.NewSession("sys"), agent.Options{}, event.Discard)
	runner := &strictPlanRunner{exec: exec, scripts: scripts}

	h := &strictPlanHarness{runner: runner}
	var c *Controller
	sink := event.FuncSink(func(e event.Event) {
		isReview := e.Kind == event.ApprovalRequest &&
			(e.Approval.Tool == planLockReviewTool || e.Approval.Tool == planChangeReviewTool)
		h.mu.Lock()
		h.events = append(h.events, e)
		if isReview {
			h.approvals = append(h.approvals, e)
		}
		opinionResponder := h.opinionResponder
		h.mu.Unlock()
		if !isReview {
			return
		}
		if opinionResponder != nil {
			allow, opinion := opinionResponder(e)
			go c.ApproveWithOpinion(e.Approval.ID, allow, false, false, opinion)
			return
		}
		if reviewResponder != nil {
			allow := reviewResponder(e)
			go c.Approve(e.Approval.ID, allow, false, false)
		}
	})
	c = New(Options{
		Runner:        runner,
		Executor:      exec,
		Sink:          sink,
		WorkspaceRoot: ws,
	})
	store, err := planstore.Open(ws)
	if err != nil {
		t.Fatalf("planstore.Open: %v", err)
	}
	h.c = c
	h.store = store
	return h
}

func (h *strictPlanHarness) runStrictPlan(task string) error {
	return h.c.runStrictPlan(context.Background(), h.store, task, "")
}

func (h *strictPlanHarness) hasNotice(needle string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, e := range h.events {
		if e.Kind == event.Notice && strings.Contains(e.Text, needle) {
			return true
		}
	}
	return false
}

// eventsSnapshot returns a copy of the event log under the harness lock, so
// pollers on the test goroutine never race the sink's concurrent appends.
func (h *strictPlanHarness) eventsSnapshot() []event.Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]event.Event(nil), h.events...)
}

// approvalCount returns the number of plan_lock_review approvals seen so far.
func (h *strictPlanHarness) approvalCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.approvals)
}

// setOpinionResponder installs an opinion-carrying review responder (A3), which
// takes precedence over the plain bool responder: every plan review is answered
// through ApproveWithOpinion with the returned (allow, opinion).
func (h *strictPlanHarness) setOpinionResponder(fn func(e event.Event) (bool, string)) {
	h.mu.Lock()
	h.opinionResponder = fn
	h.mu.Unlock()
}

// firstReviewEvent returns the first plan_lock_review ApprovalRequest event in
// a snapshot (zero value when none exists).
func firstReviewEvent(snap []event.Event) event.Event {
	for _, e := range snap {
		if e.Kind == event.ApprovalRequest && e.Approval.Tool == planLockReviewTool {
			return e
		}
	}
	return event.Event{}
}

func (h *strictPlanHarness) planDir(id string) string {
	return filepath.Join(h.store.WorkspaceRoot(), ".reasonix", "plans", id)
}

// TC-RV-01 提交后评审分发：submitted → plan_lock_review 审批事件；该工具属
// fresh-human 白名单（YOLO/auto 不可自动批准，模型不可自我批准）。
func TestReviewDispatchedAfterSubmission(t *testing.T) {
	var h *strictPlanHarness
	h = newStrictPlanHarness(t, []func(string){
		func(string) { submitPlan(t, h.store, "plan-001") },
	}, func(event.Event) bool { return true })
	h.runStrictPlan("append a line to README.md")

	if h.runner.runCount != 1 {
		t.Fatalf("runner calls = %d, want 1", h.runner.runCount)
	}
	if h.approvalCount() != 1 {
		t.Fatalf("plan_lock_review approvals = %d, want 1", h.approvalCount())
	}
	firstApproval := firstReviewEvent(h.eventsSnapshot())
	if !strings.Contains(firstApproval.Approval.Subject, "plan-001") {
		t.Fatalf("approval subject = %q, want it to name plan-001", firstApproval.Approval.Subject)
	}
	if !RequiresFreshHumanApprovalTool(planLockReviewTool) {
		t.Fatal("plan_lock_review must require fresh human approval (no self-approval)")
	}
	rs, _ := h.store.ReadRunState("plan-001")
	if rs.Stage != planstore.StageLocked {
		t.Fatalf("stage after approve = %q, want locked", rs.Stage)
	}
}

// TC-RV-02 批准 → 锁定链路：submitted→locked + locks.json review=approved +
// .lockbackup + baseline.json + 输出 strict-plan: plan-id=<id> locked。
func TestApproveLocksPlanAndBaseline(t *testing.T) {
	var h *strictPlanHarness
	h = newStrictPlanHarness(t, []func(string){
		func(string) { submitPlan(t, h.store, "plan-001") },
	}, func(event.Event) bool { return true })
	h.runStrictPlan("append a line to README.md")

	if err := os.WriteFile(filepath.Join(h.store.WorkspaceRoot(), "README.md"), []byte("readme"), 0o600); err != nil {
		t.Fatal(err)
	}
	rs, err := h.store.ReadRunState("plan-001")
	if err != nil {
		t.Fatal(err)
	}
	if rs.Stage != planstore.StageLocked {
		t.Fatalf("stage = %q, want locked", rs.Stage)
	}
	lk, err := h.store.ReadLock("plan-001")
	if err != nil {
		t.Fatal(err)
	}
	if lk.Review != planstore.ReviewApproved {
		t.Fatalf("locks review = %q, want approved", lk.Review)
	}
	if lk.LockedAt == "" {
		t.Fatal("lockedAt must be recorded")
	}
	for _, name := range []string{"plan.md.enc", "validate.py.enc", "write-scope.json.enc", "change-manifest.json.enc"} {
		if _, err := os.Stat(filepath.Join(h.planDir("plan-001"), ".lockbackup", name)); err != nil {
			t.Fatalf("lock backup %s missing: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(h.planDir("plan-001"), "baseline.json")); err != nil {
		t.Fatalf("baseline.json missing after lock: %v", err)
	}
	if !h.hasNotice("strict-plan: plan-id=plan-001 locked") {
		t.Fatal("output must contain the locked notice")
	}
}

// TC-RV-03 拒绝 → rejected + 修改意见注入（合成 turn 收到 plan-id 与修订语义）。
func TestRejectReturnsToReworkWithFeedback(t *testing.T) {
	var reworkInput string
	var h *strictPlanHarness
	h = newStrictPlanHarness(t, []func(string){
		func(string) { submitPlan(t, h.store, "plan-001") },
		func(input string) { reworkInput = input },
	}, func(event.Event) bool { return false })
	h.runStrictPlan("append a line to README.md")

	rs, _ := h.store.ReadRunState("plan-001")
	if rs.Stage != planstore.StageRejected {
		t.Fatalf("stage = %q, want rejected", rs.Stage)
	}
	lk, _ := h.store.ReadLock("plan-001")
	if lk.Review != planstore.ReviewRejected {
		t.Fatalf("locks review = %q, want rejected", lk.Review)
	}
	if h.runner.runCount != 5 {
		// 1 (initial submit) + 1 (rejected-feedback turn) + 3 (rework round cap)
		t.Fatalf("runner calls = %d, want 5 (submit + feedback + 3 rework rounds)", h.runner.runCount)
	}
	if !strings.Contains(reworkInput, "plan-001") || !strings.Contains(reworkInput, "plan_submit") {
		t.Fatalf("rework input must name the plan and ask to re-submit, got %q", reworkInput)
	}
	if h.hasNotice("strict-plan: plan-id=") {
		t.Fatal("a rejected plan must not emit a locked notice")
	}
}

// TC-RV-04 修订重提闭环：拒绝 → 修订重提（rejected→submitted）→ 再评审批准 → locked；
// 评审事件恰好 2 次。
func TestReworkResubmitLocks(t *testing.T) {
	var h *strictPlanHarness
	h = newStrictPlanHarness(t, []func(string){
		func(string) { submitPlan(t, h.store, "plan-001") },
		func(string) { reworkAndResubmit(t, h.store, "plan-001") },
	}, func(e event.Event) bool {
		// 第一次评审拒绝，第二次批准
		return h.approvalCount() >= 2
	})
	h.runStrictPlan("append a line to README.md")

	if h.approvalCount() != 2 {
		t.Fatalf("review approvals = %d, want 2 (reject then approve)", h.approvalCount())
	}
	rs, _ := h.store.ReadRunState("plan-001")
	if rs.Stage != planstore.StageLocked {
		t.Fatalf("stage after rework loop = %q, want locked", rs.Stage)
	}
	if !h.hasNotice("strict-plan: plan-id=plan-001 locked") {
		t.Fatal("reworked plan must lock with the plan-id notice")
	}
}

// TC-RV-05 YOLO 模式强制人工：plan_lock_review 不被自动批准，阻塞到用户应答；
// 应答拒绝后进入 rejected。
func TestYoloCannotSelfApproveReview(t *testing.T) {
	var h *strictPlanHarness
	h = newStrictPlanHarness(t, []func(string){
		func(string) { submitPlan(t, h.store, "plan-yolo") },
	}, nil) // 不自动应答：证明 YOLO 下评审仍挂起等用户
	h.c.SetToolApprovalMode(ToolApprovalYolo)

	done := make(chan error, 1)
	go func() {
		done <- h.c.runStrictPlan(context.Background(), h.store, "task", "")
	}()

	select {
	case <-time.After(3 * time.Second):
		t.Fatal("plan_lock_review must surface and block even in YOLO mode")
	default:
	}
	// 等审批事件出现
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if h.approvalCount() == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if h.approvalCount() != 1 {
		t.Fatal("plan_lock_review approval must be surfaced in YOLO mode")
	}
	// 审批仍挂起：驱动器阻塞（无自动 drain）
	select {
	case <-done:
		t.Fatal("driver must block on the review until the user answers")
	case <-time.After(100 * time.Millisecond):
	}
	// 用户拒绝
	appr := h.eventsSnapshot()
	var firstReview event.Event
	for _, e := range appr {
		if e.Kind == event.ApprovalRequest && e.Approval.Tool == planLockReviewTool {
			firstReview = e
			break
		}
	}
	if firstReview.Approval.ID == "" {
		t.Fatal("YOLO review approval event must carry an approval id")
	}
	h.c.Approve(firstReview.Approval.ID, false, false, false)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("driver did not continue after the manual denial")
	}
	rs, _ := h.store.ReadRunState("plan-yolo")
	if rs.Stage != planstore.StageRejected {
		t.Fatalf("stage after YOLO manual deny = %q, want rejected", rs.Stage)
	}
}

// TC-DR-01 无工具调用 → 同提示 3 轮封顶：runner 恰好 3 次、无锁定、有耗尽 notice。
func TestNoSubmissionExhaustsThreeRounds(t *testing.T) {
	var h *strictPlanHarness
	h = newStrictPlanHarness(t, []func(string){
		func(string) {}, func(string) {}, func(string) {},
	}, nil)
	h.runStrictPlan("append a line to README.md")

	if h.runner.runCount != 3 {
		t.Fatalf("runner calls = %d, want 3", h.runner.runCount)
	}
	if h.hasNotice("strict-plan: plan-id=") {
		t.Fatal("no plan may lock without a submission")
	}
	if !h.hasNotice("strict-plan") {
		t.Fatal("driver must surface a terminal notice after exhausting rounds")
	}
	ids, _ := h.store.ListPlans()
	if len(ids) != 0 {
		t.Fatalf("no plan may be created without a submission, got %v", ids)
	}
}

// TC-DR-02 首轮提交即锁定：runner 1 次、输出 plan-id locked。
func TestFirstRoundSubmissionLocks(t *testing.T) {
	var h *strictPlanHarness
	h = newStrictPlanHarness(t, []func(string){
		func(string) { submitPlan(t, h.store, "plan-first") },
	}, func(event.Event) bool { return true })
	h.runStrictPlan("append a line to README.md")

	if h.runner.runCount != 1 {
		t.Fatalf("runner calls = %d, want 1", h.runner.runCount)
	}
	rs, _ := h.store.ReadRunState("plan-first")
	if rs.Stage != planstore.StageLocked {
		t.Fatalf("stage = %q, want locked", rs.Stage)
	}
	if !h.hasNotice("strict-plan: plan-id=plan-first locked") {
		t.Fatal("locked notice must carry the model-proposed plan id")
	}
}

// TC-DR-03 第三轮才提交仍成功（3 轮封顶是"无提交"上限，非"提交时机"上限）。
func TestThirdRoundSubmissionStillLocks(t *testing.T) {
	var h *strictPlanHarness
	h = newStrictPlanHarness(t, []func(string){
		func(string) {}, func(string) {},
		func(string) { submitPlan(t, h.store, "plan-third") },
	}, func(event.Event) bool { return true })
	h.runStrictPlan("append a line to README.md")

	if h.runner.runCount != 3 {
		t.Fatalf("runner calls = %d, want 3", h.runner.runCount)
	}
	rs, _ := h.store.ReadRunState("plan-third")
	if rs.Stage != planstore.StageLocked {
		t.Fatalf("stage = %q, want locked", rs.Stage)
	}
}

// TC-DR-04 空任务：不发 model run、有 usage 提示、不产生 store 副作用。
func TestStrictPlanEmptyTask(t *testing.T) {
	var h *strictPlanHarness
	h = newStrictPlanHarness(t, nil, nil)
	h.c.submitCommandOrTurn("/strict-plan", "/strict-plan", "", false, "", "")
	if h.runner.runCount != 0 {
		t.Fatalf("runner must not run for an empty task, calls = %d", h.runner.runCount)
	}
	if !h.hasNotice("/strict-plan") {
		t.Fatal("empty task must surface a usage notice")
	}
	h.c.submitCommandOrTurn("/strict-plan   ", "/strict-plan   ", "", false, "", "")
	if h.runner.runCount != 0 {
		t.Fatal("whitespace task must not run the model")
	}
}

// TC-DR-04b SubmitHTTP（桌面 API 通道）对 /strict-plan 空任务解释命令：
// 输出 usage notice 且不跑模型（证明 HTTP 入口命令解释链路成立）。
func TestSubmitHTTPInterpretsStrictPlan(t *testing.T) {
	h := newStrictPlanHarness(t, nil, nil)
	h.c.SubmitHTTP("/strict-plan   ")
	if h.runner.runCount != 0 {
		t.Fatalf("runner must not run for an empty task via SubmitHTTP, calls = %d", h.runner.runCount)
	}
	if !h.hasNotice("/strict-plan") {
		t.Fatal("SubmitHTTP must surface the usage notice for an empty /strict-plan")
	}
}

// TC-DR-05 校验失败修正重提：第一轮"伪提交"（无 submitted 效果）后驱动器续跑，
// 第二轮合法提交才推进评审并锁定。
func TestInvalidSubmissionThenReworkLocks(t *testing.T) {
	var h *strictPlanHarness
	h = newStrictPlanHarness(t, []func(string){
		// 校验失败 = 不产生 submitted 效果（plan_submit 工具自身返回错误）
		func(string) {},
		func(string) { submitPlan(t, h.store, "plan-fixed") },
	}, func(event.Event) bool { return true })
	h.runStrictPlan("append a line to README.md")

	if h.runner.runCount != 2 {
		t.Fatalf("runner calls = %d, want 2 (failed attempt + rework)", h.runner.runCount)
	}
	rs, _ := h.store.ReadRunState("plan-fixed")
	if rs.Stage != planstore.StageLocked {
		t.Fatalf("stage = %q, want locked", rs.Stage)
	}
}

// TC-A3-01 锁计划拒绝带意见：planRejectedMessage 注入合成轮含"审核意见"段与
// 意见原文，模型可据此修订重提。
func TestLockReviewRejectCarriesOpinion(t *testing.T) {
	var h *strictPlanHarness
	h = newStrictPlanHarness(t, []func(string){
		func(string) { submitPlan(t, h.store, "plan-001") },
		func(input string) {
			if !strings.Contains(input, "审核意见") || !strings.Contains(input, "脚本断言太弱，需覆盖负例") {
				t.Errorf("rejected-with-opinion turn must carry the opinion, got %.400s", input)
			}
		},
	}, nil)
	h.setOpinionResponder(func(e event.Event) (bool, string) {
		return false, "脚本断言太弱，需覆盖负例"
	})
	if err := h.runStrictPlan("task"); err != nil {
		t.Fatalf("runStrictPlan: %v", err)
	}
	rs, _ := h.store.ReadRunState("plan-001")
	if rs.Stage != planstore.StageRejected {
		t.Fatalf("stage = %q, want rejected", rs.Stage)
	}
}

// TC-A3-02 拒绝意见为空（fail-safe）：注入消息与现状完全一致（无"审核意见"
// 段）；旧签名 Approve 与空意见 ApproveWithOpinion 行为相同。
func TestLockReviewRejectWithoutOpinionMatchesLegacy(t *testing.T) {
	var h *strictPlanHarness
	h = newStrictPlanHarness(t, []func(string){
		func(string) { submitPlan(t, h.store, "plan-001") },
		func(input string) {
			if strings.Contains(input, "审核意见") {
				t.Errorf("empty-opinion rejection must not inject an opinion section, got %.400s", input)
			}
			if !strings.Contains(input, "did not pass review and was rejected") {
				t.Errorf("legacy rejection copy must be preserved, got %.400s", input)
			}
		},
	}, nil)
	h.setOpinionResponder(func(e event.Event) (bool, string) { return false, "" })
	if err := h.runStrictPlan("task"); err != nil {
		t.Fatalf("runStrictPlan: %v", err)
	}
}

// TC-A3-03 锁计划批准带意见：锁定 Notice 含"审核意见"段；批准语义与锁定产物
// 不变（E2E/单测依赖的 locked 文案子串保留）。
func TestLockReviewApproveCarriesOpinion(t *testing.T) {
	var h *strictPlanHarness
	h = newStrictPlanHarness(t, []func(string){
		func(string) { submitPlan(t, h.store, "plan-001") },
	}, nil)
	h.setOpinionResponder(func(e event.Event) (bool, string) { return true, "范围合理，开始执行" })
	if err := h.runStrictPlan("task"); err != nil {
		t.Fatalf("runStrictPlan: %v", err)
	}
	if !h.hasNotice("strict-plan: plan-id=plan-001 locked") {
		t.Fatal("locked notice must keep the legacy plan-id=... locked substring")
	}
	if !h.hasNotice("计划制定阶段结束") {
		t.Fatal("B1: locked notice must explicitly signal the plan-stage end")
	}
	if !h.hasNotice("审核意见：范围合理，开始执行") {
		t.Fatal("approved-with-opinion lock must surface the opinion in the locked notice")
	}
	rs, _ := h.store.ReadRunState("plan-001")
	if rs.Stage != planstore.StageLocked {
		t.Fatalf("stage = %q, want locked", rs.Stage)
	}
}

// TC-A3-09 Sprint 11 HTTP 接口改进：未知/已消费审批 id 的
// ApproveWithOpinion 返回 error（客户端可据此区分 404 与成功）。
func TestApproveWithOpinionUnknownIDReturnsError(t *testing.T) {
	var h *strictPlanHarness
	h = newStrictPlanHarness(t, []func(string){
		func(string) { submitPlan(t, h.store, "plan-001") },
	}, func(event.Event) bool { return true })
	h.runStrictPlan("task")
	// plan-001 已锁定（评审被批准，审批 id 已消费）：未知 id 必须报错。
	if err := h.c.ApproveWithOpinion("ghost-approval", true, false, false, "意见"); err == nil {
		t.Fatal("ApproveWithOpinion on unknown id must return an error")
	} else if !strings.Contains(err.Error(), "is not pending") {
		t.Fatalf("unexpected error: %v", err)
	}
	// 已消费的评审 id 再次批准同样报错（不静默成功）。
	if len(h.approvals) == 0 {
		t.Fatal("expected at least one plan_lock_review approval event")
	}
	consumed := h.approvals[0].Approval.ID
	if err := h.c.ApproveWithOpinion(consumed, true, false, false, ""); err == nil {
		t.Fatalf("ApproveWithOpinion on consumed id %q must return an error", consumed)
	}
}
