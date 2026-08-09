package control

import (
	"fmt"
	"strings"
	"testing"

	"reasonix/internal/planstore"
)

// TC-04 评审 reason 改造：完整脚本不截断——25 行脚本（超过旧 previewLines 20 行
// 阈值）全部呈现，无省略标记；脚本以 ```python 代码块包裹；无字面 \n 转义。
func TestPlanReviewReasonFullScriptNoTruncation(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 25; i++ {
		fmt.Fprintf(&sb, "# line %d\n", i)
	}
	files := planstore.PlanFiles{
		ID:     "plan-001",
		Steps:  []string{"step one", "step two"},
		Script: sb.String(),
		Scope:  []planstore.ScopeEntry{{Path: "src/", Reason: "main code"}},
		Manifest: []planstore.ManifestEntry{
			{Path: "src/main.go", Action: "add"},
		},
	}

	h := newStrictPlanHarness(t, nil, nil)
	if err := h.store.WritePlan("plan-001", files); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}
	if err := h.store.Transition("plan-001", planstore.StageDrafting, planstore.StageSubmitted); err != nil {
		t.Fatalf("Transition: %v", err)
	}
	reason, err := planReviewReason(h.store, "plan-001")
	if err != nil {
		t.Fatalf("planReviewReason: %v", err)
	}

	if !strings.Contains(reason, "```python") {
		t.Errorf("reason must wrap the script in a ```python block, got:\n%s", reason)
	}
	if strings.Contains(reason, "…") {
		t.Errorf("reason must not truncate the script, got:\n%s", reason)
	}
	if !strings.Contains(reason, "# line 25") {
		t.Errorf("reason must contain the last script line (line 25), got:\n%s", reason)
	}
	if strings.Contains(reason, `\n`) {
		t.Errorf("reason must not contain literal backslash-n escapes, got:\n%s", reason)
	}
	for _, step := range files.Steps {
		if !strings.Contains(reason, step) {
			t.Errorf("reason must contain step %q, got:\n%s", step, reason)
		}
	}
	if !strings.Contains(reason, "src/main.go [add]") {
		t.Errorf("reason must contain the change manifest line, got:\n%s", reason)
	}
}
