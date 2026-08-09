package planstore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file implements the plan readability mechanism: rendering the plan
// artifacts (PlanFiles) plus their lifecycle state (RunState) and review
// conclusion (LockState) into human-readable Markdown text. It is the single
// rendering mechanism shared by every user-facing surface — the plan-lock
// review reason, /strict-plan-detail, /strict-plan-list and (future) the
// /strict-plan-exec progress display — so readability fixes land in one place.

// RenderPlanDetail renders the full plan document: status/review conclusion,
// numbered step list, the acceptance script as a ```python code block (real
// newlines preserved — never JSON-escaped), the write scope with per-entry
// reasons and the change manifest as path [action] lines.
func RenderPlanDetail(files PlanFiles, state RunState, lock LockState) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Plan %s\n\n", files.ID)

	b.WriteString("## 状态\n")
	fmt.Fprintf(&b, "- stage: %s\n", state.Stage)
	b.WriteString("- 评审: " + reviewLabel(lock.Review))
	if lock.LockedAt != "" && lock.Review == ReviewApproved {
		fmt.Fprintf(&b, "（锁定于 %s）", lock.LockedAt)
	}
	b.WriteString("\n")
	if state.Round > 0 || state.RetriesLeft > 0 || state.ChangePending {
		fmt.Fprintf(&b, "- 轮次: %d · 重试剩余: %d\n", state.Round, state.RetriesLeft)
		fmt.Fprintf(&b, "- 变更待审批: %t\n", state.ChangePending)
	}

	b.WriteString("\n## 步骤\n")
	for i, st := range files.Steps {
		fmt.Fprintf(&b, "%d. %s\n", i+1, st)
	}

	b.WriteString("\n## 验收脚本 (validate.py)\n")
	b.WriteString("```python\n")
	b.WriteString(strings.TrimRight(files.Script, "\n"))
	b.WriteString("\n```\n")

	b.WriteString("\n## 可写范围\n")
	if len(files.Scope) == 0 {
		b.WriteString("（空）\n")
	}
	for _, e := range files.Scope {
		fmt.Fprintf(&b, "- %s —— %s\n", e.Path, e.Reason)
	}

	b.WriteString("\n## 变动清单\n")
	if len(files.Manifest) == 0 {
		b.WriteString("（空）\n")
	}
	for _, m := range files.Manifest {
		fmt.Fprintf(&b, "- %s [%s]\n", m.Path, m.Action)
	}

	return strings.TrimSuffix(b.String(), "\n")
}

// RenderPlanListEntry renders one lightweight list row: plan id, stage and —
// when locked — the review conclusion and lock time. It never includes plan
// body content, so a bare list stays readable at a glance.
func RenderPlanListEntry(id string, state RunState, lock LockState) string {
	line := fmt.Sprintf("- %s [%s] review=%s", id, state.Stage, lock.Review)
	if lock.LockedAt != "" && lock.Review == ReviewApproved {
		line += " lockedAt=" + lock.LockedAt
	}
	return line
}

// reviewLabel maps a review conclusion to a stable label. English values keep
// E2E assertions stable; pending reads naturally as awaiting review.
func reviewLabel(r ReviewStatus) string {
	switch r {
	case ReviewApproved:
		return "approved"
	case ReviewRejected:
		return "rejected"
	default:
		return "pending"
	}
}

// MostRecentLocked returns the plan id whose plan directory was most recently
// modified among the locked plans — the user-side "most recent plan" fallback
// for commands that accept an omitted plan id. It fails closed when no locked
// plan exists.
func (s *Store) MostRecentLocked() (string, error) {
	ids, err := s.ListPlans()
	if err != nil {
		return "", err
	}
	var best string
	var bestTime time.Time
	for _, id := range ids {
		rs, err := s.ReadRunState(id)
		if err != nil || rs.Stage != StageLocked {
			continue
		}
		info, err := os.Stat(filepath.Join(s.plansDir, id))
		if err != nil {
			continue
		}
		if best == "" || info.ModTime().After(bestTime) {
			best, bestTime = id, info.ModTime()
		}
	}
	if best == "" {
		return "", fmt.Errorf("no locked plan found to default to; specify a plan id")
	}
	return best, nil
}
