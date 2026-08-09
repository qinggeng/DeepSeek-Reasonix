package planstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const lockBackupDir = ".lockbackup"

// validTransitions is the plan lifecycle state machine. See CONTEXT.md
// [[计划阶段]]: drafting → submitted → rejected (re-submittable) / locked →
// executing → done | failed. locked is the only executable entry; change
// requests never alter the state machine.
var validTransitions = map[Stage][]Stage{
	StageDrafting:  {StageSubmitted},
	StageSubmitted: {StageRejected, StageLocked},
	StageRejected:  {StageSubmitted},
	StageLocked:    {StageExecuting},
	StageExecuting: {StageDone, StageFailed},
	StageDone:      {},
	StageFailed:    {},
}

// CanTransition reports whether the state machine allows from → to.
func CanTransition(from, to Stage) bool {
	for _, s := range validTransitions[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Transition moves a plan from one stage to another after validating the
// current stage and the transition table, then persists the new run-state
// atomically. Entering locked also records the review approval in locks.json
// and takes the authoritative ciphertext backup used by Verify.
func (s *Store) Transition(id string, from, to Stage) error {
	if err := validPlanID(id); err != nil {
		return err
	}
	dir := filepath.Join(s.plansDir, id)
	rsPath := filepath.Join(dir, runStateFile)
	var rs RunState
	if err := readJSON(rsPath, &rs); err != nil {
		return fmt.Errorf("planstore: read run-state: %w", err)
	}
	if rs.Stage != from {
		return fmt.Errorf("planstore: transition %s->%s rejected: current stage is %s (not %s)",
			from, to, rs.Stage, from)
	}
	if !CanTransition(from, to) {
		return fmt.Errorf("planstore: transition %s->%s is not allowed by the state machine", from, to)
	}
	rs.Stage = to
	if err := atomicWriteJSON(rsPath, rs); err != nil {
		return fmt.Errorf("planstore: persist run-state: %w", err)
	}
	switch to {
	case StageLocked:
		if err := s.approveLock(id, dir); err != nil {
			return err
		}
	case StageRejected:
		if err := s.markReview(id, dir, ReviewRejected); err != nil {
			return err
		}
	}
	return nil
}

// UpdateRunState applies fn to the plan's run state (rounds, retries, change
// requests). Terminal stages refuse further updates.
func (s *Store) UpdateRunState(id string, fn func(*RunState)) error {
	if err := validPlanID(id); err != nil {
		return err
	}
	dir := filepath.Join(s.plansDir, id)
	rsPath := filepath.Join(dir, runStateFile)
	var rs RunState
	if err := readJSON(rsPath, &rs); err != nil {
		return fmt.Errorf("planstore: read run-state: %w", err)
	}
	if rs.Stage == StageDone || rs.Stage == StageFailed {
		return fmt.Errorf("planstore: run state of %q is terminal (%s); no further updates", id, rs.Stage)
	}
	fn(&rs)
	return atomicWriteJSON(rsPath, rs)
}

// ReadRunState returns the current run state of a plan.
func (s *Store) ReadRunState(id string) (RunState, error) {
	if err := validPlanID(id); err != nil {
		return RunState{}, err
	}
	var rs RunState
	if err := readJSON(filepath.Join(s.plansDir, id, runStateFile), &rs); err != nil {
		return RunState{}, fmt.Errorf("planstore: read run-state: %w", err)
	}
	return rs, nil
}

// ReadLock returns the lock snapshot (hashes, review, retries) of a plan.
func (s *Store) ReadLock(id string) (LockState, error) {
	if err := validPlanID(id); err != nil {
		return LockState{}, err
	}
	var lk LockState
	if err := readJSON(filepath.Join(s.plansDir, id, locksFile), &lk); err != nil {
		return LockState{}, fmt.Errorf("planstore: read locks: %w", err)
	}
	return lk, nil
}

// Verify compares the on-disk artifact hashes against locks.json. A mismatch
// means the ciphertext was touched outside the driver: it is restored from the
// authoritative .lockbackup copy taken at lock time. Restore fails closed when
// no backup exists.
func (s *Store) Verify(id string) error {
	if err := validPlanID(id); err != nil {
		return err
	}
	dir := filepath.Join(s.plansDir, id)
	lk, err := s.ReadLock(id)
	if err != nil {
		return err
	}
	for _, name := range []string{planFile, scriptFile, scopeFile, manifestFile} {
		want, ok := lk.Files[name]
		if !ok {
			return fmt.Errorf("planstore: locks.json has no hash for %s", name)
		}
		got, err := sha256File(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if got != want {
			if err := s.restoreFromBackup(dir, name); err != nil {
				return err
			}
		}
	}
	// Re-verify after any restore: every file must now match the lock hashes.
	for _, name := range []string{planFile, scriptFile, scopeFile, manifestFile} {
		got, err := sha256File(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if got != lk.Files[name] {
			return fmt.Errorf("planstore: %s still mismatches its lock hash after restore", name)
		}
	}
	return nil
}

func (s *Store) restoreFromBackup(dir, name string) error {
	data, err := os.ReadFile(filepath.Join(dir, lockBackupDir, name))
	if err != nil {
		return fmt.Errorf("planstore: tamper detected in %s but no lock backup to restore from", name)
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		return fmt.Errorf("planstore: restore %s: %w", name, err)
	}
	return nil
}

// approveLock marks the review approved and takes the authoritative ciphertext
// backup. Called when a plan enters locked.
func (s *Store) approveLock(id, dir string) error {
	lk, err := s.ReadLock(id)
	if err != nil {
		return err
	}
	lk.Review = ReviewApproved
	lk.LockedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writeJSON(filepath.Join(dir, locksFile), lk); err != nil {
		return err
	}
	bakDir := filepath.Join(dir, lockBackupDir)
	if err := os.MkdirAll(bakDir, 0o700); err != nil {
		return fmt.Errorf("planstore: create lock backup: %w", err)
	}
	for _, name := range []string{planFile, scriptFile, scopeFile, manifestFile} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(bakDir, name), data, 0o600); err != nil {
			return fmt.Errorf("planstore: backup %s: %w", name, err)
		}
	}
	return nil
}

func (s *Store) markReview(id, dir string, r ReviewStatus) error {
	lk, err := s.ReadLock(id)
	if err != nil {
		return err
	}
	lk.Review = r
	return writeJSON(filepath.Join(dir, locksFile), lk)
}

func readJSON(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

// atomicWriteJSON writes via a temp file + rename so a crash mid-write cannot
// leave a truncated run-state/locks file.
func atomicWriteJSON(path string, v any) error {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
