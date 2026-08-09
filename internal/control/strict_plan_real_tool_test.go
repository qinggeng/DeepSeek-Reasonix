package control

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"reasonix/internal/agent"
	"reasonix/internal/event"
	"reasonix/internal/planstore"
	"reasonix/internal/provider"
	"reasonix/internal/tool"
)

// TestStrictPlanRealPlanSubmitGetsStore drives /strict-plan through a real
// agent and the real registered plan_submit builtin tool. The injected plan
// store must reach the tool execution context — regression guard for the
// missing planstore.WithStore stamp that made plan_submit fail closed with
// "plan store is not available in this context" on the live desktop build
// while the scripted-runner unit tests stayed green (they bypass tool
// execution entirely).
func TestStrictPlanRealPlanSubmitGetsStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("REASONIX_HOME", home)
	ws := t.TempDir()
	gitInitWorkspace(t, ws)

	prov := &scriptedTurns{turns: [][]provider.Chunk{
		{ // 模型第一轮：调用 plan_submit（合法产物）
			{Type: provider.ChunkToolCall, ToolCall: &provider.ToolCall{
				ID:   "call-1",
				Name: "plan_submit",
				Arguments: `{"plan_id":"plan-e2e","steps":["step a","step b"],"validate_script":"print('ok')",` +
					`"write_scope":[{"path":"src/","reason":"main code"}],` +
					`"change_manifest":[{"path":"src/main.go","action":"modify"}]}`,
			}},
			{Type: provider.ChunkDone},
		},
		{ // 后续轮（驱动器检测到 submitted 后不再需要模型输出；备用）
			{Type: provider.ChunkText, Text: "done"},
			{Type: provider.ChunkDone},
		},
	}}
	// 与真实桌面启动一致：registry 先加全部 builtin 工具（含 plan_submit）。
	reg := tool.NewRegistry()
	for _, bt := range tool.Builtins() {
		reg.Add(bt)
	}
	ag := agent.New(prov, reg, agent.NewSession(""), agent.Options{}, event.Discard)

	var c *Controller
	sink := event.FuncSink(func(e event.Event) {
		if e.Kind == event.ApprovalRequest && e.Approval.Tool == planLockReviewTool {
			go c.Approve(e.Approval.ID, true, false, false)
		}
	})
	c = New(Options{Runner: ag, Executor: ag, Sink: sink, WorkspaceRoot: ws})
	store, err := planstore.Open(ws)
	if err != nil {
		t.Fatalf("planstore.Open: %v", err)
	}

	if err := c.runStrictPlan(context.Background(), store, "append a line to README.md", ""); err != nil {
		t.Fatalf("runStrictPlan: %v", err)
	}
	// 状态断言即真实工具执行证据：plan_submit 必须从注入的 ctx 取到 store 才能
	// 写入加密产物并推进到 submitted → locked（scripted-runner 测试直接调 store
	// API 模拟这一效果，本测试走真实工具执行路径）。
	rs, err := store.ReadRunState("plan-e2e")
	if err != nil {
		t.Fatalf("plan-e2e run-state missing: %v", err)
	}
	if rs.Stage != planstore.StageLocked {
		t.Fatalf("stage = %q, want locked (real plan_submit must have seen the store)", rs.Stage)
	}
	lk, err := store.ReadLock("plan-e2e")
	if err != nil {
		t.Fatalf("plan-e2e locks missing: %v", err)
	}
	if lk.Review != planstore.ReviewApproved {
		t.Fatalf("locks review = %q, want approved", lk.Review)
	}
	// 产物由 plan_submit 工具真实写入：密文文件存在且不含明文子串。
	dir := filepath.Join(store.WorkspaceRoot(), ".reasonix", "plans", "plan-e2e")
	for _, name := range []string{"plan.md.enc", "validate.py.enc", "write-scope.json.enc", "change-manifest.json.enc"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("artifact %s missing (plan_submit did not write it): %v", name, err)
		}
		if bytes.Contains(data, []byte("step a")) || bytes.Contains(data, []byte("print('ok')")) {
			t.Fatalf("artifact %s must be ciphertext, found plaintext", name)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "baseline.json")); err != nil {
		t.Fatalf("baseline.json missing after lock: %v", err)
	}
}
