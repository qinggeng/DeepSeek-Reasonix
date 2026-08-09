package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"reasonix/internal/planstore"
	"reasonix/internal/tool"
)

func planToolCtx(t *testing.T) (context.Context, *planstore.Store) {
	t.Helper()
	t.Setenv("REASONIX_HOME", t.TempDir())
	s, err := planstore.Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return planstore.WithStore(context.Background(), s), s
}

func submitRaw(t *testing.T, planID string) json.RawMessage {
	t.Helper()
	return json.RawMessage(`{
  "plan_id": "` + planID + `",
  "steps": ["step one", "step two"],
  "validate_script": "print('validate')",
  "write_scope": [{"path": "src/", "reason": "main code"}],
  "change_manifest": [{"path": "src/main.go", "action": "modify"}]
}`)
}

func mustSubmit(t *testing.T, ctx context.Context, planID string) {
	t.Helper()
	tl := planSubmit{pyCheck: func(string) error { return nil }}
	if _, err := tl.Execute(ctx, submitRaw(t, planID)); err != nil {
		t.Fatalf("plan_submit(%s): %v", planID, err)
	}
}

// TC-TL-01 plan_submit 格式校验：合法提交进入 submitted；空步骤/空脚本/空范围
// path/脚本语法错误（mock）各自返回具体错误且状态不变。
func TestPlanSubmitValidatesAndTransitions(t *testing.T) {
	ctx, s := planToolCtx(t)
	tl := planSubmit{pyCheck: func(string) error { return nil }}

	if _, err := tl.Execute(ctx, submitRaw(t, "plan-001")); err != nil {
		t.Fatalf("valid submit: %v", err)
	}
	rs, err := s.ReadRunState("plan-001")
	if err != nil {
		t.Fatal(err)
	}
	if rs.Stage != planstore.StageSubmitted {
		t.Fatalf("stage: want submitted, got %s", rs.Stage)
	}

	cases := []struct {
		name string
		raw  string
	}{
		{"empty steps", `{"plan_id":"p","steps":[],"validate_script":"x","write_scope":[{"path":"a","reason":"r"}],"change_manifest":[]}`},
		{"empty script", `{"plan_id":"p","steps":["s"],"validate_script":"  ","write_scope":[{"path":"a","reason":"r"}],"change_manifest":[]}`},
		{"empty scope", `{"plan_id":"p","steps":["s"],"validate_script":"x","write_scope":[],"change_manifest":[]}`},
		{"scope missing reason", `{"plan_id":"p","steps":["s"],"validate_script":"x","write_scope":[{"path":"a"}],"change_manifest":[]}`},
		{"bad manifest action", `{"plan_id":"p","steps":["s"],"validate_script":"x","write_scope":[{"path":"a","reason":"r"}],"change_manifest":[{"path":"a","action":"rename"}]}`},
	}
	for _, c := range cases {
		if _, err := tl.Execute(ctx, json.RawMessage(c.raw)); err == nil {
			t.Fatalf("%s: must be rejected", c.name)
		}
	}

	// Python 语法错误（mock 注入）→ 拒绝且不落盘、不进入评审
	badPy := planSubmit{pyCheck: func(string) error { return errors.New("syntax error in validate.py") }}
	if _, err := badPy.Execute(ctx, submitRaw(t, "plan-002")); err == nil {
		t.Fatal("Python syntax error must reject the submit")
	}
	ids, err := s.ListPlans()
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if id == "plan-002" {
			t.Fatal("a rejected submit must not create the plan or advance its stage")
		}
	}
}

// TC-TL-02 plan_submit 唯一性冲突：已存在（非 rejected）同 id 拒绝并提示换名；
// 新 id 正常提交。
func TestPlanSubmitUniqueness(t *testing.T) {
	ctx, _ := planToolCtx(t)
	mustSubmit(t, ctx, "plan-001")
	tl := planSubmit{pyCheck: func(string) error { return nil }}
	_, err := tl.Execute(ctx, submitRaw(t, "plan-001"))
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("duplicate id must be rejected with a conflict hint, got: %v", err)
	}
	mustSubmit(t, ctx, "plan-002")
}

// TC-TL-03 plan_get / plan_scope：读回提交的内容与范围；不存在的 id 报错。
func TestPlanGetAndScope(t *testing.T) {
	ctx, _ := planToolCtx(t)
	mustSubmit(t, ctx, "plan-001")

	out, err := planGet{}.Execute(ctx, json.RawMessage(`{"plan_id":"plan-001"}`))
	if err != nil {
		t.Fatalf("plan_get: %v", err)
	}
	for _, want := range []string{"step one", "print('validate')", "src/", "main code", "change manifest"} {
		if !strings.Contains(out, want) {
			t.Fatalf("plan_get output missing %q:\n%s", want, out)
		}
	}

	scope, err := planScope{}.Execute(ctx, json.RawMessage(`{"plan_id":"plan-001"}`))
	if err != nil {
		t.Fatalf("plan_scope: %v", err)
	}
	if !strings.Contains(scope, "src/") || !strings.Contains(scope, "main code") {
		t.Fatalf("plan_scope output missing path/reason:\n%s", scope)
	}

	if _, err := (planGet{}).Execute(ctx, json.RawMessage(`{"plan_id":"nope"}`)); err == nil {
		t.Fatal("plan_get of unknown id must fail")
	}
}

// TC-TL-04 plan_status / plan_list：状态反映 run-state；列表列出全部计划。
func TestPlanStatusAndList(t *testing.T) {
	ctx, s := planToolCtx(t)
	mustSubmit(t, ctx, "plan-001")
	mustSubmit(t, ctx, "plan-002")

	list, err := planList{}.Execute(ctx, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("plan_list: %v", err)
	}
	for _, want := range []string{"plan-001", "plan-002"} {
		if !strings.Contains(list, want) {
			t.Fatalf("plan_list missing %q:\n%s", want, list)
		}
	}

	st, err := planStatus{}.Execute(ctx, json.RawMessage(`{"plan_id":"plan-001"}`))
	if err != nil {
		t.Fatalf("plan_status: %v", err)
	}
	if !strings.Contains(st, "stage=submitted") || !strings.Contains(st, "round=0") {
		t.Fatalf("plan_status output wrong:\n%s", st)
	}

	if err := s.UpdateRunState("plan-001", func(rs *planstore.RunState) { rs.Round = 2; rs.RetriesLeft = 1 }); err != nil {
		t.Fatal(err)
	}
	st, err = planStatus{}.Execute(ctx, json.RawMessage(`{"plan_id":"plan-001"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st, "round=2") || !strings.Contains(st, "retriesLeft=1") {
		t.Fatalf("plan_status must reflect run-state:\n%s", st)
	}

	if _, err := (planStatus{}).Execute(ctx, json.RawMessage(`{"plan_id":"nope"}`)); err == nil {
		t.Fatal("plan_status of unknown id must fail")
	}
}

// TC-TL-05 plan_request_change：执行中提交变更 → ChangePending + ChangeHistory，
// stage 不变；空 reason / 无变更内容被拒。
func TestPlanRequestChange(t *testing.T) {
	ctx, s := planToolCtx(t)
	mustSubmit(t, ctx, "plan-001")
	s.Transition("plan-001", planstore.StageSubmitted, planstore.StageLocked)
	s.Transition("plan-001", planstore.StageLocked, planstore.StageExecuting)

	_, err := planRequestChange{}.Execute(ctx, json.RawMessage(`{
  "plan_id":"plan-001",
  "reason":"need broader scope",
  "write_scope":[{"path":"scripts/","reason":"extra scripts"}]
}`))
	if err != nil {
		t.Fatalf("plan_request_change: %v", err)
	}
	rs, err := s.ReadRunState("plan-001")
	if err != nil {
		t.Fatal(err)
	}
	if rs.Stage != planstore.StageExecuting {
		t.Fatalf("change request must not change stage, got %s", rs.Stage)
	}
	if !rs.ChangePending {
		t.Fatal("ChangePending must be set")
	}
	if rs.PendingChange == nil {
		t.Fatal("PendingChange must carry the request content for the review/application step")
	}
	if rs.PendingChange.Reason != "need broader scope" || len(rs.PendingChange.WriteScope) != 1 ||
		rs.PendingChange.WriteScope[0].Path != "scripts/" {
		t.Fatalf("PendingChange must persist the proposed scope, got %+v", rs.PendingChange)
	}
	// ChangeHistory is written by the driver's review outcome (ApplyChange /
	// DenyChange), not at submission time.
	if len(rs.ChangeHistory) != 0 {
		t.Fatalf("ChangeHistory must stay empty until the review concludes, got %d", len(rs.ChangeHistory))
	}

	if _, err := (planRequestChange{}).Execute(ctx, json.RawMessage(`{"plan_id":"plan-001","reason":"","validate_script":"x"}`)); err == nil {
		t.Fatal("empty reason must be rejected")
	}
	if _, err := (planRequestChange{}).Execute(ctx, json.RawMessage(`{"plan_id":"plan-001","reason":"no change"}`)); err == nil {
		t.Fatal("no change content must be rejected")
	}
}

// TC-TL-06 plan_request_change 执行期约束：只有 executing 计划可提变更；已有
// pending 时二次请求被拒（防覆盖）。
func TestPlanRequestChangeExecutingOnlyAndNoOverwrite(t *testing.T) {
	ctx, s := planToolCtx(t)
	mustSubmit(t, ctx, "plan-001")
	s.Transition("plan-001", planstore.StageSubmitted, planstore.StageLocked)

	// locked（未执行）→ 拒绝
	if _, err := (planRequestChange{}).Execute(ctx, json.RawMessage(`{"plan_id":"plan-001","reason":"early","validate_script":"x"}`)); err == nil {
		t.Fatal("change request on a locked (not executing) plan must be rejected")
	} else if !strings.Contains(err.Error(), "executing") {
		t.Fatalf("rejection must explain the executing-only rule, got %v", err)
	}

	s.Transition("plan-001", planstore.StageLocked, planstore.StageExecuting)
	if _, err := (planRequestChange{}).Execute(ctx, json.RawMessage(`{"plan_id":"plan-001","reason":"first","validate_script":"x"}`)); err != nil {
		t.Fatalf("first change request on an executing plan: %v", err)
	}
	// 已有 pending → 拒绝
	if _, err := (planRequestChange{}).Execute(ctx, json.RawMessage(`{"plan_id":"plan-001","reason":"second","validate_script":"y"}`)); err == nil {
		t.Fatal("a second change request while one is pending must be rejected")
	} else if !strings.Contains(err.Error(), "pending") {
		t.Fatalf("rejection must mention the pending request, got %v", err)
	}
}

// 注册校验：6 个计划工具在内建注册表中可见（仅注册，命令联动后续 Sprint）。
func TestPlanToolsRegistered(t *testing.T) {
	for _, name := range []string{"plan_submit", "plan_get", "plan_scope", "plan_status", "plan_list", "plan_request_change"} {
		if _, ok := tool.LookupBuiltin(name); !ok {
			t.Fatalf("builtin %q not registered", name)
		}
	}
}
