package planstore

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// nowUTC returns the current UTC time in RFC3339 (the planstore timestamp
// convention shared by locks, run-state and change history).
func nowUTC() string {
	return time.Now().UTC().Format(time.RFC3339)
}

// ApplyChange applies an approved change request to a locked plan: it rewrites
// the changed artifacts (validate_script / write_scope) as new ciphertext,
// refreshes locks.json hashes and the .lockbackup authoritative copy, resets
// the retry counter (the acceptance standard changed, so the old retry budget
// no longer applies), clears the pending change and records the approval in
// ChangeHistory. The plan's stage is untouched — a change request never moves
// the state machine.
func (s *Store) ApplyChange(id string, pc PendingChange, retriesLeft int) error {
	if err := validPlanID(id); err != nil {
		return err
	}
	if pc.ValidateScript == "" && len(pc.WriteScope) == 0 {
		return fmt.Errorf("planstore: change request %q has no content to apply", id)
	}
	if len(pc.WriteScope) > 0 {
		if err := ValidateScope(s.wsRoot, pc.WriteScope); err != nil {
			return fmt.Errorf("planstore: change scope invalid: %w", err)
		}
	}
	dir := filepath.Join(s.plansDir, id)

	// 1. Rewrite the changed artifacts as new ciphertext.
	if pc.ValidateScript != "" {
		enc, err := encryptGCM(s.projectKey, []byte(pc.ValidateScript))
		if err != nil {
			return fmt.Errorf("planstore: encrypt new validate.py: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, scriptFile), []byte(enc+"\n"), 0o600); err != nil {
			return fmt.Errorf("planstore: write new validate.py: %w", err)
		}
	}
	if len(pc.WriteScope) > 0 {
		enc, err := encryptGCM(s.projectKey, []byte(jsonText(pc.WriteScope)))
		if err != nil {
			return fmt.Errorf("planstore: encrypt new write-scope: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dir, scopeFile), []byte(enc+"\n"), 0o600); err != nil {
			return fmt.Errorf("planstore: write new write-scope: %w", err)
		}
	}

	// 2. Refresh the lock hashes and the authoritative backup copy.
	lk, err := s.ReadLock(id)
	if err != nil {
		return err
	}
	for _, name := range []string{planFile, scriptFile, scopeFile, manifestFile} {
		h, err := sha256File(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		lk.Files[name] = h
	}
	if err := writeJSON(filepath.Join(dir, locksFile), lk); err != nil {
		return err
	}
	bakDir := filepath.Join(dir, lockBackupDir)
	for _, name := range []string{planFile, scriptFile, scopeFile, manifestFile} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(bakDir, name), data, 0o600); err != nil {
			return fmt.Errorf("planstore: refresh lock backup %s: %w", name, err)
		}
	}

	// 3. Clear the pending change, reset retries, record the approval.
	what := "change approved"
	if pc.Reason != "" {
		what = pc.Reason
	}
	return s.UpdateRunState(id, func(rs *RunState) {
		rs.ChangePending = false
		rs.PendingChange = nil
		rs.RetriesLeft = retriesLeft
		rs.ChangeHistory = append(rs.ChangeHistory, ChangeRecord{
			At:       pc.At,
			What:     what,
			Approved: true,
		})
	})
}

// DenyChange clears an outstanding change request without touching the locked
// artifacts: the driver injects a rejection message and the execution loop
// continues under the original acceptance standard.
func (s *Store) DenyChange(id string) error {
	if err := validPlanID(id); err != nil {
		return err
	}
	return s.UpdateRunState(id, func(rs *RunState) {
		var what string
		if rs.PendingChange != nil {
			what = rs.PendingChange.Reason
			if what == "" {
				what = "change request"
			}
		} else {
			what = "change request"
		}
		rs.ChangePending = false
		rs.PendingChange = nil
		rs.ChangeHistory = append(rs.ChangeHistory, ChangeRecord{
			At:       nowUTC(),
			What:     what,
			Approved: false,
		})
	})
}
