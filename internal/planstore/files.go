package planstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	planFile     = "plan.md.enc"
	scriptFile   = "validate.py.enc"
	scopeFile    = "write-scope.json.enc"
	manifestFile = "change-manifest.json.enc"
	locksFile    = "locks.json"
	runStateFile = "run-state.json"
)

// planIDRE matches CONTEXT.md [[计划 ID]]: English letters, digits and hyphens.
var planIDRE = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]*$`)

func validPlanID(id string) error {
	if id == "" {
		return fmt.Errorf("plan id is empty")
	}
	if !planIDRE.MatchString(id) {
		return fmt.Errorf("plan id %q is invalid: use letters, digits and hyphens (e.g. plan-001)", id)
	}
	return nil
}

// WritePlan encrypts and persists the plan artifacts under
// .reasonix/plans/<id>/, then (re)writes the plaintext sidecars locks.json and
// run-state.json. Re-writing an existing plan is allowed (idempotent).
func (s *Store) WritePlan(id string, files PlanFiles) error {
	if err := validPlanID(id); err != nil {
		return err
	}
	dir := filepath.Join(s.plansDir, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("planstore: create plan dir: %w", err)
	}
	payloads := map[string]string{
		planFile:     strings.Join(files.Steps, "\n"),
		scriptFile:   files.Script,
		scopeFile:    jsonText(files.Scope),
		manifestFile: jsonText(files.Manifest),
	}
	for name, plain := range payloads {
		enc, err := encryptGCM(s.projectKey, []byte(plain))
		if err != nil {
			return fmt.Errorf("planstore: encrypt %s: %w", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(enc+"\n"), 0o600); err != nil {
			return fmt.Errorf("planstore: write %s: %w", name, err)
		}
	}
	return s.writeSidecars(id, dir)
}

// ReadPlan decrypts and reassembles the plan artifacts for id.
func (s *Store) ReadPlan(id string) (PlanFiles, error) {
	if err := validPlanID(id); err != nil {
		return PlanFiles{}, err
	}
	dir := filepath.Join(s.plansDir, id)
	steps, err := s.decryptFile(filepath.Join(dir, planFile))
	if err != nil {
		return PlanFiles{}, err
	}
	script, err := s.decryptFile(filepath.Join(dir, scriptFile))
	if err != nil {
		return PlanFiles{}, err
	}
	scope, err := s.decryptFile(filepath.Join(dir, scopeFile))
	if err != nil {
		return PlanFiles{}, err
	}
	manifest, err := s.decryptFile(filepath.Join(dir, manifestFile))
	if err != nil {
		return PlanFiles{}, err
	}
	var files PlanFiles
	files.ID = id
	files.Steps = splitLines(string(steps))
	files.Script = string(script)
	if err := json.Unmarshal(scope, &files.Scope); err != nil {
		return PlanFiles{}, fmt.Errorf("planstore: decode scope: %w", err)
	}
	if err := json.Unmarshal(manifest, &files.Manifest); err != nil {
		return PlanFiles{}, fmt.Errorf("planstore: decode manifest: %w", err)
	}
	return files, nil
}

// DeletePlan removes the encrypted plan directory for id.
func (s *Store) DeletePlan(id string) error {
	if err := validPlanID(id); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(s.plansDir, id))
}

// ListPlans returns the plan ids present in this workspace.
func (s *Store) ListPlans() ([]string, error) {
	entries, err := os.ReadDir(s.plansDir)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if e.IsDir() && validPlanID(e.Name()) == nil {
			ids = append(ids, e.Name())
		}
	}
	return ids, nil
}

// decryptFile decrypts one artifact file. Exposed at package level for
// cross-workspace isolation tests (a foreign store must not decrypt it).
func (s *Store) decryptFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("planstore: read %s: %w", filepath.Base(path), err)
	}
	pt, err := decryptGCM(s.projectKey, strings.TrimSpace(string(data)))
	if err != nil {
		return nil, fmt.Errorf("planstore: decrypt %s: %w", filepath.Base(path), err)
	}
	return pt, nil
}

// writeSidecars writes the plaintext hashes-only lock snapshot and the initial
// run state (drafting) after artifacts land on disk.
func (s *Store) writeSidecars(id, dir string) error {
	hashes := make(map[string]string, 4)
	for _, name := range []string{planFile, scriptFile, scopeFile, manifestFile} {
		h, err := sha256File(filepath.Join(dir, name))
		if err != nil {
			return err
		}
		hashes[name] = h
	}
	lock := LockState{PlanID: id, Files: hashes, Review: ReviewPending}
	if err := writeJSON(filepath.Join(dir, locksFile), lock); err != nil {
		return err
	}
	rs := RunState{PlanID: id, Stage: StageDrafting}
	return writeJSON(filepath.Join(dir, runStateFile), rs)
}

func sha256File(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func writeJSON(path string, v any) error {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	return os.WriteFile(path, body, 0o600)
}

func jsonText(v any) string {
	body, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(body)
}

func splitLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
