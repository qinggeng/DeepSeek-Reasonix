package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"reasonix/internal/planmode"
	"reasonix/internal/planstore"
)

// strictPlanCtx builds a store with an executing plan (scope: src/ recursive)
// and returns a strict-mode context bound to it.
func strictPlanCtx(t *testing.T) (context.Context, *planstore.Store) {
	t.Helper()
	t.Setenv("REASONIX_HOME", t.TempDir())
	ws := t.TempDir()
	s, err := planstore.Open(ws)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	files := planstore.PlanFiles{
		ID:       "plan-001",
		Steps:    []string{"step one"},
		Script:   "print('validate')",
		Scope:    []planstore.ScopeEntry{{Path: "src/", Reason: "main code"}},
		Manifest: []planstore.ManifestEntry{{Path: "src/main.go", Action: "modify"}},
	}
	if err := s.WritePlan("plan-001", files); err != nil {
		t.Fatal(err)
	}
	for _, st := range [][2]planstore.Stage{
		{planstore.StageDrafting, planstore.StageSubmitted},
		{planstore.StageSubmitted, planstore.StageLocked},
		{planstore.StageLocked, planstore.StageExecuting},
	} {
		if err := s.Transition("plan-001", st[0], st[1]); err != nil {
			t.Fatal(err)
		}
	}
	ctx := planmode.WithStrict(planstore.WithStore(context.Background(), s), true)
	return ctx, s
}

// TC-WS-03b 挂载：strict 模式下写工具范围外被拒（提示 plan_request_change）、
// 范围内放行；非 strict 不拦截；读工具不拦截；缺 store / 无 executing 计划
// 时 fail-closed。
func TestApplyPlanWriteScope(t *testing.T) {
	ctx, _ := strictPlanCtx(t)
	a := &Agent{}

	// 范围内放行
	plan := &toolCallPlan{canonicalName: "write_file", execArgs: json.RawMessage(`{"path":"src/a.go"}`)}
	if _, blocked := a.applyPlanWriteScope(ctx, plan); blocked {
		t.Fatal("write inside scope must pass")
	}
	// move_file 双路径
	mv := &toolCallPlan{canonicalName: "move_file", execArgs: json.RawMessage(`{"source_path":"src/a.go","destination_path":"src/b.go"}`)}
	if _, blocked := a.applyPlanWriteScope(ctx, mv); blocked {
		t.Fatal("move_file inside scope must pass")
	}

	// 范围外拒绝 + 提示
	out, blocked := a.applyPlanWriteScope(ctx, &toolCallPlan{canonicalName: "write_file", execArgs: json.RawMessage(`{"path":"evil.txt"}`)})
	if !blocked {
		t.Fatal("write outside scope must be blocked")
	}
	if !strings.Contains(out.output, "plan_request_change") {
		t.Fatalf("block message must hint plan_request_change, got: %s", out.output)
	}
	// move_file 目的范围外拒绝
	if _, blocked := a.applyPlanWriteScope(ctx, &toolCallPlan{canonicalName: "move_file", execArgs: json.RawMessage(`{"source_path":"src/a.go","destination_path":"evil.txt"}`)}); !blocked {
		t.Fatal("move_file destination outside scope must be blocked")
	}

	// 非 strict 模式不拦截
	ctxPlain := planstore.WithStore(context.Background(), mustStoreFrom(t, ctx))
	if _, blocked := a.applyPlanWriteScope(ctxPlain, &toolCallPlan{canonicalName: "write_file", execArgs: json.RawMessage(`{"path":"evil.txt"}`)}); blocked {
		t.Fatal("non-strict mode must not be blocked by the write scope")
	}

	// 读工具 strict 下也不拦截
	if _, blocked := a.applyPlanWriteScope(ctx, &toolCallPlan{canonicalName: "read_file", execArgs: json.RawMessage(`{"path":"evil.txt"}`)}); blocked {
		t.Fatal("read tools must not be blocked by the write gate")
	}

	// strict 但无 store → fail-closed
	noStore := planmode.WithStrict(context.Background(), true)
	if _, blocked := a.applyPlanWriteScope(noStore, &toolCallPlan{canonicalName: "write_file", execArgs: json.RawMessage(`{"path":"x"}`)}); !blocked {
		t.Fatal("strict without a bound store must fail closed")
	}

	// strict 但无 executing 计划 → fail-closed
	t.Setenv("REASONIX_HOME", t.TempDir())
	s2, err := planstore.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := s2.WritePlan("plan-001", planstore.PlanFiles{ID: "plan-001", Steps: []string{"s"}, Script: "x", Scope: []planstore.ScopeEntry{{Path: "src/", Reason: "r"}}}); err != nil {
		t.Fatal(err)
	}
	ctxNoExec := planmode.WithStrict(planstore.WithStore(context.Background(), s2), true)
	if _, blocked := a.applyPlanWriteScope(ctxNoExec, &toolCallPlan{canonicalName: "write_file", execArgs: json.RawMessage(`{"path":"x"}`)}); !blocked {
		t.Fatal("strict without an executing plan must fail closed")
	}
}

func mustStoreFrom(t *testing.T, ctx context.Context) *planstore.Store {
	t.Helper()
	s, ok := planstore.StoreFromContext(ctx)
	if !ok {
		t.Fatal("no store in context")
	}
	return s
}
