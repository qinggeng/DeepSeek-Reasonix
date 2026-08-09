package planstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func readRunState(t *testing.T, ws, id string) RunState {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(planDir(ws, id), runStateFile))
	if err != nil {
		t.Fatalf("read run-state.json: %v", err)
	}
	var rs RunState
	if err := json.Unmarshal(data, &rs); err != nil {
		t.Fatalf("run-state.json invalid: %v", err)
	}
	return rs
}

func readLockState(t *testing.T, ws, id string) LockState {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(planDir(ws, id), locksFile))
	if err != nil {
		t.Fatalf("read locks.json: %v", err)
	}
	var lk LockState
	if err := json.Unmarshal(data, &lk); err != nil {
		t.Fatalf("locks.json invalid: %v", err)
	}
	return lk
}

// TC-SM-01 状态机合法转移全链路：drafting→submitted→locked→executing→done 逐步
// 落盘；locked 时 locks.json 评审通过并建立 .lockbackup 权威副本。
func TestStateMachineFullChain(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	s := mustOpen(t, ws)
	if err := s.WritePlan("plan-001", sampleFiles()); err != nil {
		t.Fatalf("WritePlan: %v", err)
	}

	seq := []struct {
		from, to Stage
	}{
		{StageDrafting, StageSubmitted},
		{StageSubmitted, StageLocked},
		{StageLocked, StageExecuting},
		{StageExecuting, StageDone},
	}
	for _, st := range seq {
		if err := s.Transition("plan-001", st.from, st.to); err != nil {
			t.Fatalf("Transition %s->%s: %v", st.from, st.to, err)
		}
		rs := readRunState(t, ws, "plan-001")
		if rs.Stage != st.to {
			t.Fatalf("stage after %s->%s: want %s, got %s", st.from, st.to, st.to, rs.Stage)
		}
	}

	lk := readLockState(t, ws, "plan-001")
	if lk.Review != ReviewApproved {
		t.Fatalf("locked plan review: want approved, got %q", lk.Review)
	}
	if lk.LockedAt == "" {
		t.Fatal("locked plan must record lockedAt")
	}
	for _, name := range []string{planFile, scriptFile, scopeFile, manifestFile} {
		if _, err := os.Stat(filepath.Join(planDir(ws, "plan-001"), lockBackupDir, name)); err != nil {
			t.Fatalf("lock backup %s missing: %v", name, err)
		}
	}
}

// TC-SM-02 非法转移拒绝且原子：locked 态下的非法转移全部报错，run-state.json
// 不被破坏（stage 保持 locked，JSON 可解析）。
func TestIllegalTransitionsRejected(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	s := mustOpen(t, ws)
	if err := s.WritePlan("plan-001", sampleFiles()); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition("plan-001", StageDrafting, StageSubmitted); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition("plan-001", StageSubmitted, StageLocked); err != nil {
		t.Fatal(err)
	}

	bad := [][2]Stage{
		{StageLocked, StageSubmitted},
		{StageLocked, StageDrafting},
		{StageLocked, StageDone},
		{StageLocked, StageFailed},
		{StageDrafting, StageLocked}, // 当前是 locked，from 不匹配
		{StageSubmitted, StageLocked}, // 当前是 locked，from 不匹配
	}
	for _, st := range bad {
		if err := s.Transition("plan-001", st[0], st[1]); err == nil {
			t.Fatalf("Transition %s->%s must be rejected", st[0], st[1])
		}
	}
	rs := readRunState(t, ws, "plan-001")
	if rs.Stage != StageLocked {
		t.Fatalf("run-state must stay locked after illegal transitions, got %q", rs.Stage)
	}
}

// TC-SM-03 拒绝回退与变更叠加：rejected 可回 drafting 修订重提；executing 时
// ChangePending 置位不改变 stage。
func TestRejectedReworkAndChangePending(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	s := mustOpen(t, ws)
	if err := s.WritePlan("plan-001", sampleFiles()); err != nil {
		t.Fatal(err)
	}
	s.Transition("plan-001", StageDrafting, StageSubmitted)
	if err := s.Transition("plan-001", StageSubmitted, StageRejected); err != nil {
		t.Fatalf("reject: %v", err)
	}
	if rs := readRunState(t, ws, "plan-001"); rs.Stage != StageRejected {
		t.Fatalf("stage: want rejected, got %s", rs.Stage)
	}
	// 修订重提
	if err := s.Transition("plan-001", StageRejected, StageSubmitted); err != nil {
		t.Fatalf("resubmit: %v", err)
	}
	if err := s.Transition("plan-001", StageSubmitted, StageLocked); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition("plan-001", StageLocked, StageExecuting); err != nil {
		t.Fatal(err)
	}
	// 变更请求叠加，不改状态机
	if err := s.UpdateRunState("plan-001", func(rs *RunState) { rs.ChangePending = true }); err != nil {
		t.Fatalf("set change pending: %v", err)
	}
	rs := readRunState(t, ws, "plan-001")
	if rs.Stage != StageExecuting {
		t.Fatalf("ChangePending must not change stage: want executing, got %s", rs.Stage)
	}
	if !rs.ChangePending {
		t.Fatal("ChangePending must be true after update")
	}
}

// TC-SM-04 轮次与重试计数：Round/RetriesLeft 逐步更新与落盘；变更获批后重试重置；
// 终态后拒绝更新。
func TestRoundAndRetryCounting(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	s := mustOpen(t, ws)
	if err := s.WritePlan("plan-001", sampleFiles()); err != nil {
		t.Fatal(err)
	}
	s.Transition("plan-001", StageDrafting, StageSubmitted)
	s.Transition("plan-001", StageSubmitted, StageLocked)
	s.Transition("plan-001", StageLocked, StageExecuting)

	set := func(round, retries int) {
		t.Helper()
		if err := s.UpdateRunState("plan-001", func(rs *RunState) { rs.Round = round; rs.RetriesLeft = retries }); err != nil {
			t.Fatalf("UpdateRunState(%d,%d): %v", round, retries, err)
		}
	}
	set(1, 3)
	if rs := readRunState(t, ws, "plan-001"); rs.Round != 1 || rs.RetriesLeft != 3 {
		t.Fatalf("after first round: want round=1 retries=3, got %+v", rs)
	}
	set(2, 2)
	if rs := readRunState(t, ws, "plan-001"); rs.Round != 2 || rs.RetriesLeft != 2 {
		t.Fatalf("after second round: want round=2 retries=2, got %+v", rs)
	}
	// 变更获批后重试重置
	set(2, 3)
	if rs := readRunState(t, ws, "plan-001"); rs.RetriesLeft != 3 {
		t.Fatalf("after change approval reset: want retries=3, got %d", rs.RetriesLeft)
	}

	// 终态后拒绝更新
	if err := s.Transition("plan-001", StageExecuting, StageDone); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateRunState("plan-001", func(rs *RunState) { rs.Round = 99 }); err == nil {
		t.Fatal("UpdateRunState on terminal state must be rejected")
	}
	if rs := readRunState(t, ws, "plan-001"); rs.Round != 2 {
		t.Fatalf("terminal update must not mutate run-state: round=%d", rs.Round)
	}
}

// TC-SM-05 hash 校验与自动恢复：篡改产物 → Verify 检测 hash 不匹配 → 从
// .lockbackup 恢复 → 再次 Verify 通过、ReadPlan 还原原始内容；无备份时篡改
// Verify 报错。
func TestVerifyRestoresFromLockBackup(t *testing.T) {
	testEnv(t)
	ws := t.TempDir()
	s := mustOpen(t, ws)
	if err := s.WritePlan("plan-001", sampleFiles()); err != nil {
		t.Fatal(err)
	}
	s.Transition("plan-001", StageDrafting, StageSubmitted)
	s.Transition("plan-001", StageSubmitted, StageLocked)

	encPath := filepath.Join(planDir(ws, "plan-001"), planFile)
	enc, err := os.ReadFile(encPath)
	if err != nil {
		t.Fatal(err)
	}
	enc[len(enc)/2] ^= 0x01
	if err := os.WriteFile(encPath, enc, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify("plan-001"); err != nil {
		t.Fatalf("Verify must detect and restore tampering: %v", err)
	}
	if err := s.Verify("plan-001"); err != nil {
		t.Fatalf("second Verify must pass after restore: %v", err)
	}
	got, err := s.ReadPlan("plan-001")
	if err != nil {
		t.Fatalf("ReadPlan after restore: %v", err)
	}
	if !reflect.DeepEqual(got.Steps, sampleFiles().Steps) {
		t.Fatal("steps not restored from lock backup")
	}

	// 无备份且篡改 → 无法恢复 → 报错
	if err := os.RemoveAll(filepath.Join(planDir(ws, "plan-001"), lockBackupDir)); err != nil {
		t.Fatal(err)
	}
	enc, _ = os.ReadFile(encPath)
	enc[len(enc)/2] ^= 0x01
	if err := os.WriteFile(encPath, enc, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify("plan-001"); err == nil {
		t.Fatal("Verify must fail when tampering cannot be restored")
	}
}
