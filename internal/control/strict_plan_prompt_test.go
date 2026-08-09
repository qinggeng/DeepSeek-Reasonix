package control

import (
	"strings"
	"testing"
)

// TC-LIMIT-04（control 侧）制定提示词要求清单保持精简（plan_submit
// Description 侧断言见 builtin 包 TestPlanSubmitDescriptionDemandsConcise）。
func TestPlanPromptDemandsConcise(t *testing.T) {
	if !strings.Contains(strictPlanPrompt, "concise") {
		t.Fatal("strictPlanPrompt must ask for concise write scope / change manifest")
	}
}

// Sprint 10 prompt-clarity fixes (registry entries 1/2/3/5): the executor and
// drafter prompts must state (a) plan_submit is forbidden during execution,
// (b) what a change request may contain, (c) the strictness layering (steps are
// guidance; scope + script are the contract), and (d) that gitignored paths are
// invisible to the change comparison. These are text-level contracts the model
// reads verbatim, so they are asserted as exact substrings.

// TC-P1 执行提示词包含 plan_submit 禁令：执行/锁定中的计划不可被 plan_submit
// 修改，变更只能走 plan_request_change。
func TestExecPromptForbidsPlanSubmit(t *testing.T) {
	if !strings.Contains(strictExecPrompt, "Do not call plan_submit") {
		t.Fatal("strictExecPrompt must explicitly forbid plan_submit during execution")
	}
	if !strings.Contains(strictExecPrompt, "plan_request_change") {
		t.Fatal("strictExecPrompt must point to plan_request_change as the only change channel")
	}
	if !strings.Contains(strictExecPrompt, "locked/executing plan") {
		t.Fatal("strictExecPrompt must say why plan_submit is useless (cannot modify a locked/executing plan)")
	}
}

// TC-P2 执行提示词明示变更 payload：新 write_scope 与新 validate_script（均需
// reason），获批后重试计数重置。
func TestExecPromptStatesChangePayload(t *testing.T) {
	if !strings.Contains(strictExecPrompt, "write scope") {
		t.Fatal("strictExecPrompt must name the write scope as a changeable artifact")
	}
	if !strings.Contains(strictExecPrompt, "validate_script") && !strings.Contains(strictExecPrompt, "acceptance script") {
		t.Fatal("strictExecPrompt must name the acceptance script as a changeable artifact")
	}
	if !strings.Contains(strictExecPrompt, "reason") {
		t.Fatal("strictExecPrompt must require a reason for each change payload part")
	}
	if !strings.Contains(strictExecPrompt, "resets") && !strings.Contains(strictExecPrompt, "reset") {
		t.Fatal("strictExecPrompt must state that an approved change resets the retry counter")
	}
}

// TC-P3 严格度分层：steps 是参考非契约；被严格执行的是 write scope 与
// acceptance script（CompareBaseline 按 manifest 判实际改动，步骤不参与）。
func TestExecPromptLayersStrictness(t *testing.T) {
	if !strings.Contains(strictExecPrompt, "guidance") {
		t.Fatal("strictExecPrompt must label the steps as guidance")
	}
	if !strings.Contains(strictExecPrompt, "contract") {
		t.Fatal("strictExecPrompt must contrast the steps against a contract")
	}
	if !strings.Contains(strictExecPrompt, "strictly enforced") && !strings.Contains(strictExecPrompt, "strictly") {
		t.Fatal("strictExecPrompt must say what is strictly enforced (write scope + acceptance script)")
	}
}

// TC-P4 gitignore 盲区提示：被忽略路径不参与变更比较，实现与验收脚本须作用
// 在 git 可见范围。制定与执行两处提示词都要明示。
func TestPromptsWarnGitignoredPaths(t *testing.T) {
	for name, prompt := range map[string]string{
		"strictExecPrompt": strictExecPrompt,
		"strictPlanPrompt": strictPlanPrompt,
	} {
		if !strings.Contains(prompt, "ignored") {
			t.Fatalf("%s must state that .gitignore-ignored paths are invisible to the change comparison", name)
		}
	}
}
