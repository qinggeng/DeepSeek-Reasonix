package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"reasonix/internal/planstore"
)

// Sprint 10 registry entry 4: plan_submit must reject write_scope /
// change_manifest lists larger than a configurable cap (default 50, shared by
// both lists per UAT). The cap lives on the Store (planstore.MaxEntries), so
// tests inject a small value and production gets the default.

// submitRawLimit builds a plan_submit payload with n scope entries and m
// manifest entries.
func submitRawLimit(t *testing.T, planID string, n, m int) json.RawMessage {
	t.Helper()
	scope := make([]string, n)
	for i := range scope {
		scope[i] = fmt.Sprintf(`{"path":"src/dir%d/","reason":"part %d"}`, i, i)
	}
	manifest := make([]string, m)
	for i := range manifest {
		manifest[i] = fmt.Sprintf(`{"path":"src/f%d.go","action":"modify"}`, i)
	}
	raw := fmt.Sprintf(`{
  "plan_id": %q,
  "steps": ["step one"],
  "validate_script": "print('validate')",
  "write_scope": [%s],
  "change_manifest": [%s]
}`, planID, strings.Join(scope, ","), strings.Join(manifest, ","))
	return json.RawMessage(raw)
}

// planToolCtxLimit opens a store with a tiny entry cap so limit tests never
// need 50+ artifacts.
func planToolCtxLimit(t *testing.T, cap int) (context.Context, *planstore.Store) {
	t.Helper()
	ctx, s := planToolCtx(t)
	s.MaxEntries = cap
	return ctx, s
}

// TC-LIMIT-01 write_scope 超过上限 → 明确报错，不进入评审，store 无残留。
func TestPlanSubmitScopeOverLimit(t *testing.T) {
	ctx, s := planToolCtxLimit(t, 2)
	tl := planSubmit{pyCheck: func(string) error { return nil }}
	_, err := tl.Execute(ctx, submitRawLimit(t, "plan-001", 3, 1))
	if err == nil {
		t.Fatal("write_scope over the cap must be rejected")
	}
	if !strings.Contains(err.Error(), "write_scope") || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("over-limit error must name the field and the limit, got: %v", err)
	}
	if _, err := s.ReadRunState("plan-001"); err == nil {
		t.Fatal("over-limit submit must not create the plan")
	}
}

// TC-LIMIT-02 change_manifest 超过上限 → 明确报错，不进入评审，store 无残留。
func TestPlanSubmitManifestOverLimit(t *testing.T) {
	ctx, s := planToolCtxLimit(t, 2)
	tl := planSubmit{pyCheck: func(string) error { return nil }}
	_, err := tl.Execute(ctx, submitRawLimit(t, "plan-001", 2, 3))
	if err == nil {
		t.Fatal("change_manifest over the cap must be rejected")
	}
	if !strings.Contains(err.Error(), "change_manifest") || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("over-limit error must name the field and the limit, got: %v", err)
	}
	if _, err := s.ReadRunState("plan-001"); err == nil {
		t.Fatal("over-limit submit must not create the plan")
	}
}

// TC-LIMIT-03 恰好等于上限通过；默认上限为 50（planstore 导出常量，非魔法数）。
func TestPlanSubmitAtLimitAndDefault(t *testing.T) {
	ctx, s := planToolCtxLimit(t, 2)
	tl := planSubmit{pyCheck: func(string) error { return nil }}
	if _, err := tl.Execute(ctx, submitRawLimit(t, "plan-001", 2, 2)); err != nil {
		t.Fatalf("exactly-at-limit submit must pass, got: %v", err)
	}
	if rs, err := s.ReadRunState("plan-001"); err != nil || rs.Stage != planstore.StageSubmitted {
		t.Fatalf("at-limit submit must reach submitted, got stage=%v err=%v", rs.Stage, err)
	}
	if planstore.DefaultMaxEntries != 50 {
		t.Fatalf("planstore.DefaultMaxEntries = %d, want 50", planstore.DefaultMaxEntries)
	}
}

// TC-LIMIT-04 工具描述要求清单保持精简（strictPlanPrompt 侧的断言见
// control 包 TestPlanPromptDemandsConcise）。
func TestPlanSubmitDescriptionDemandsConcise(t *testing.T) {
	var tl planSubmit
	if !strings.Contains(tl.Description(), "concise") {
		t.Fatal("plan_submit Description must ask for concise write scope / change manifest")
	}
}
