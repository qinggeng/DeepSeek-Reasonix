// Package planstore implements the encrypted plan storage backing the strict
// plan-execute mode: two-tier key hierarchy (per-project key encrypted under a
// user-level master key), encrypted plan artifacts, locks/hash management,
// baseline snapshots and the run-state state machine.
//
// The package is self-contained (standard library only) and never imports the
// Reasonix config package, so tests can isolate the user-level home through the
// REASONIX_HOME environment variable exactly like the rest of the product.
package planstore

// ScopeEntry is one element of the write scope whitelist: a path plus the
// human-readable reason it may be written. A directory path grants recursive
// write access below it.
type ScopeEntry struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

// ManifestEntry is one expected file change frozen at plan-lock time. Action is
// one of "add", "modify" or "delete".
type ManifestEntry struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}

// PlanFiles is the full set of plan-stage artifacts, plaintext in memory and
// ciphertext on disk. Steps serializes to plan.md, Script to validate.py,
// Scope to write-scope.json and Manifest to change-manifest.json.
type PlanFiles struct {
	ID       string
	Steps    []string
	Script   string
	Scope    []ScopeEntry
	Manifest []ManifestEntry
}

// Stage is the plan lifecycle stage, written by the driver to run-state.json
// (the model cannot change it). See CONTEXT.md [[计划阶段]].
type Stage string

const (
	StageDrafting  Stage = "drafting"
	StageSubmitted Stage = "submitted"
	StageRejected  Stage = "rejected"
	StageLocked    Stage = "locked"
	StageExecuting Stage = "executing"
	StageDone      Stage = "done"
	StageFailed    Stage = "failed"
)

// ReviewStatus records the review conclusion inside locks.json.
type ReviewStatus string

const (
	ReviewPending  ReviewStatus = "pending"
	ReviewApproved ReviewStatus = "approved"
	ReviewRejected ReviewStatus = "rejected"
)

// LockState is the lock snapshot: per-file sha256 hashes of the encrypted
// artifacts, the review conclusion and the retry counter. It contains hashes
// only — never plan plaintext — so it can live unencrypted next to the
// artifacts for hash verification.
type LockState struct {
	PlanID   string            `json:"planId"`
	Files    map[string]string `json:"files"`
	Review   ReviewStatus      `json:"review"`
	Retries  int               `json:"retries"`
	LockedAt string            `json:"lockedAt,omitempty"`
}

// ChangeRecord is one entry of the change-request history in run-state.json.
type ChangeRecord struct {
	At       string `json:"at"`
	What     string `json:"what"`
	Approved bool   `json:"approved"`
}

// PendingChange is an outstanding change request recorded by plan_request_change:
// the model proposes new locked content (acceptance script, write scope) which
// takes effect only after the driver routes it through review and ApplyChange
// applies it. Stored inside RunState so the model-visible tools can read/write
// it through the driver-owned run-state file.
type PendingChange struct {
	At             string       `json:"at"`
	Reason         string       `json:"reason"`
	ValidateScript string       `json:"validateScript,omitempty"`
	WriteScope     []ScopeEntry `json:"writeScope,omitempty"`
}

// RunState is the driver-written execution state (model cannot change it).
// Change requests do not alter the state machine; they only set ChangePending
// while an approval is outstanding.
type RunState struct {
	PlanID        string         `json:"planId"`
	Stage         Stage          `json:"stage"`
	Round         int            `json:"round"`
	RetriesLeft   int            `json:"retriesLeft"`
	ChangePending bool           `json:"changePending"`
	PendingChange *PendingChange `json:"pendingChange,omitempty"`
	ChangeHistory []ChangeRecord `json:"changeHistory,omitempty"`
}
