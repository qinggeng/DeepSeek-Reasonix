package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"reasonix/internal/planstore"
	"reasonix/internal/tool"
)

// Plan toolset: the strict plan-execute mode's model-visible tools, implemented
// as kernel built-ins (only the kernel can reach the encrypted store and driver
// state — see docs/arch/plan-execute.md §5). The driver injects the workspace
// store via planstore.WithStore; outside strict plan mode every tool fails
// closed. Command wiring (/strict-plan etc.) lands in a later sprint; this
// sprint only registers the tools.

func init() {
	tool.RegisterBuiltin(planSubmit{pyCheck: pythonSyntaxCheck})
	tool.RegisterBuiltin(planGet{})
	tool.RegisterBuiltin(planScope{})
	tool.RegisterBuiltin(planStatus{})
	tool.RegisterBuiltin(planList{})
	tool.RegisterBuiltin(planRequestChange{pyCheck: pythonSyntaxCheck})
}

func storeFrom(ctx context.Context) (*planstore.Store, error) {
	s, ok := planstore.StoreFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("plan store is not available in this context; the strict plan tools only work under /strict-plan and /strict-plan-exec")
	}
	return s, nil
}

// ---- plan_submit ----

type planSubmit struct {
	// pyCheck validates the acceptance script's Python syntax. Test instances
	// inject a mock; the registered instance uses python -m py_compile.
	pyCheck func(src string) error
}

type submitScopeEntry struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type submitManifestEntry struct {
	Path   string `json:"path"`
	Action string `json:"action"`
}

type submitArgs struct {
	PlanID         string                `json:"plan_id"`
	Steps          []string              `json:"steps"`
	ValidateScript string                `json:"validate_script"`
	WriteScope     []submitScopeEntry    `json:"write_scope"`
	ChangeManifest []submitManifestEntry `json:"change_manifest"`
}

func (planSubmit) Name() string { return "plan_submit" }

func (planSubmit) Description() string {
	return "Submit the plan-stage artifacts (steps, acceptance script, write scope, change manifest) through the plan submission gate. This is the ONLY channel that enters review: validation runs automatically (steps non-empty, script non-empty and Python-parseable, scope format valid, plan id unique) and failures return the concrete error without entering review. After approval the plan is locked and becomes executable via /strict-plan-exec."
}

func (planSubmit) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "plan_id":{"type":"string","description":"Project-unique plan id: letters, digits and hyphens (e.g. plan-001). You propose it; a conflict is rejected."},
  "steps":{"type":"array","items":{"type":"string"},"description":"The plan steps, in execution order."},
  "validate_script":{"type":"string","description":"The acceptance script (Python). Exit code 0 = accepted, non-zero = rejected. Must be non-empty and parseable."},
  "write_scope":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string"},"reason":{"type":"string"}},"required":["path","reason"]},"description":"Paths you may write during execution. Directory paths allow everything below them recursively; every entry needs a reason."},
  "change_manifest":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string"},"action":{"type":"string","enum":["add","modify","delete"]}},"required":["path","action"]},"description":"Expected file changes, frozen at lock time."}
},
"required":["plan_id","steps","validate_script","write_scope","change_manifest"]
}`)
}

// ReadOnly is false: plan_submit persists encrypted artifacts and advances the
// plan stage. PlanModeSafe is true: it is the plan gate's only channel.
func (planSubmit) ReadOnly() bool     { return false }
func (planSubmit) PlanModeSafe() bool { return true }

func (t planSubmit) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p submitArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid plan_submit args: %w", err)
	}
	if p.PlanID == "" {
		return "", fmt.Errorf("plan_id is required")
	}
	if len(p.Steps) == 0 {
		return "", fmt.Errorf("steps must not be empty: a plan needs at least one step")
	}
	if strings.TrimSpace(p.ValidateScript) == "" {
		return "", fmt.Errorf("validate_script must not be empty: the acceptance script is the programmatic gate")
	}
	if len(p.WriteScope) == 0 {
		return "", fmt.Errorf("write_scope must not be empty: declare every path you may write, with a reason")
	}
	for i, e := range p.WriteScope {
		if strings.TrimSpace(e.Path) == "" || strings.TrimSpace(e.Reason) == "" {
			return "", fmt.Errorf("write_scope[%d]: path and reason are both required", i)
		}
	}
	for i, e := range p.ChangeManifest {
		if strings.TrimSpace(e.Path) == "" {
			return "", fmt.Errorf("change_manifest[%d]: path is required", i)
		}
		switch e.Action {
		case "add", "modify", "delete":
		default:
			return "", fmt.Errorf("change_manifest[%d]: action must be one of add|modify|delete, got %q", i, e.Action)
		}
	}

	s, err := storeFrom(ctx)
	if err != nil {
		return "", err
	}
	// Uniqueness gate: an existing non-rejected plan with this id is a conflict.
	if ids, err := s.ListPlans(); err != nil {
		return "", err
	} else {
		for _, id := range ids {
			if id != p.PlanID {
				continue
			}
			rs, err := s.ReadRunState(id)
			if err != nil {
				return "", err
			}
			if rs.Stage != planstore.StageRejected {
				return "", fmt.Errorf("plan id %q already exists (stage %s); propose a different id or wait for rejection before re-submitting", id, rs.Stage)
			}
		}
	}

	if t.pyCheck != nil {
		if err := t.pyCheck(p.ValidateScript); err != nil {
			return "", err
		}
	}

	scope := make([]planstore.ScopeEntry, 0, len(p.WriteScope))
	for _, e := range p.WriteScope {
		scope = append(scope, planstore.ScopeEntry{Path: e.Path, Reason: e.Reason})
	}
	manifest := make([]planstore.ManifestEntry, 0, len(p.ChangeManifest))
	for _, e := range p.ChangeManifest {
		manifest = append(manifest, planstore.ManifestEntry{Path: e.Path, Action: e.Action})
	}
	files := planstore.PlanFiles{
		ID:       p.PlanID,
		Steps:    p.Steps,
		Script:   p.ValidateScript,
		Scope:    scope,
		Manifest: manifest,
	}
	if err := s.WritePlan(p.PlanID, files); err != nil {
		return "", err
	}
	if err := s.Transition(p.PlanID, planstore.StageDrafting, planstore.StageSubmitted); err != nil {
		return "", err
	}
	return fmt.Sprintf("plan_submit: plan %q submitted (stage submitted) — validation passed, review dispatched", p.PlanID), nil
}

// pythonSyntaxCheck parses the acceptance script with python -m py_compile. A
// missing interpreter or syntax error both fail closed: the gate never accepts
// an unverifiable script.
func pythonSyntaxCheck(src string) error {
	tmp, err := os.CreateTemp("", "plan-validate-*.py")
	if err != nil {
		return fmt.Errorf("validate_script syntax check: %w", err)
	}
	path := tmp.Name()
	defer os.Remove(path)
	if _, err := tmp.WriteString(src); err != nil {
		tmp.Close()
		return fmt.Errorf("validate_script syntax check: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("validate_script syntax check: %w", err)
	}
	cmd := exec.Command(planstore.PythonExecutable(), "-m", "py_compile", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("validate_script is not Python-parseable: %s", msg)
	}
	// py_compile drops <tmp>/__pycache__/*.pyc next to the source; remove it so
	// no acceptance-script bytecode lingers in the shared temp dir.
	os.RemoveAll(filepath.Join(filepath.Dir(path), "__pycache__"))
	return nil
}

// ---- plan_get ----

type planGet struct{}

type getArgs struct {
	PlanID string `json:"plan_id"`
}

func (planGet) Name() string { return "plan_get" }
func (planGet) Description() string {
	return "Read the current plan content (steps, acceptance script, write scope, change manifest) for a plan id. The script is yours and remains readable; writing it is refused by the write scope."
}
func (planGet) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"plan_id":{"type":"string"}},"required":["plan_id"]}`)
}
func (planGet) ReadOnly() bool     { return true }
func (planGet) PlanModeSafe() bool { return true }

func (planGet) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p getArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid plan_get args: %w", err)
	}
	s, err := storeFrom(ctx)
	if err != nil {
		return "", err
	}
	files, err := s.ReadPlan(p.PlanID)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "plan %s:\n\n## steps\n", files.ID)
	for _, st := range files.Steps {
		fmt.Fprintf(&b, "- %s\n", st)
	}
	fmt.Fprintf(&b, "\n## acceptance script (validate.py)\n%s\n", files.Script)
	fmt.Fprintf(&b, "\n## write scope\n")
	for _, e := range files.Scope {
		fmt.Fprintf(&b, "- %s  (reason: %s)\n", e.Path, e.Reason)
	}
	fmt.Fprintf(&b, "\n## change manifest\n")
	for _, m := range files.Manifest {
		fmt.Fprintf(&b, "- %s  [%s]\n", m.Path, m.Action)
	}
	return b.String(), nil
}

// ---- plan_scope ----

type planScope struct{}

func (planScope) Name() string { return "plan_scope" }
func (planScope) Description() string {
	return "Read the write scope boundary (paths + reasons) of the active plan. Paths outside it are refused by the write gate; request a scope change with plan_request_change if you need more."
}
func (planScope) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"plan_id":{"type":"string","description":"Omit to use the most recent locked plan."}},"required":[]}`)
}
func (planScope) ReadOnly() bool     { return true }
func (planScope) PlanModeSafe() bool { return true }

func (planScope) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p getArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid plan_scope args: %w", err)
	}
	s, err := storeFrom(ctx)
	if err != nil {
		return "", err
	}
	id, err := resolvePlanID(s, p.PlanID)
	if err != nil {
		return "", err
	}
	files, err := s.ReadPlan(id)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "write scope of plan %s:\n", id)
	for _, e := range files.Scope {
		fmt.Fprintf(&b, "- %s  (reason: %s)\n", e.Path, e.Reason)
	}
	return b.String(), nil
}

// ---- plan_status ----

type planStatus struct{}

func (planStatus) Name() string { return "plan_status" }
func (planStatus) Description() string {
	return "Query the plan's lifecycle status: stage, current round, retries left and whether a change request is pending. The stage is driver-written and cannot be changed by the model."
}
func (planStatus) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"plan_id":{"type":"string","description":"Omit to use the most recent locked plan."}},"required":[]}`)
}
func (planStatus) ReadOnly() bool     { return true }
func (planStatus) PlanModeSafe() bool { return true }

func (planStatus) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p getArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid plan_status args: %w", err)
	}
	s, err := storeFrom(ctx)
	if err != nil {
		return "", err
	}
	id, err := resolvePlanID(s, p.PlanID)
	if err != nil {
		return "", err
	}
	rs, err := s.ReadRunState(id)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("plan %s: stage=%s round=%d retriesLeft=%d changePending=%t",
		id, rs.Stage, rs.Round, rs.RetriesLeft, rs.ChangePending), nil
}

// ---- plan_list ----

type planList struct{}

func (planList) Name() string { return "plan_list" }
func (planList) Description() string {
	return "List the plan ids in this workspace with their stages."
}
func (planList) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{},"required":[]}`)
}
func (planList) ReadOnly() bool     { return true }
func (planList) PlanModeSafe() bool { return true }

func (planList) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	s, err := storeFrom(ctx)
	if err != nil {
		return "", err
	}
	ids, err := s.ListPlans()
	if err != nil {
		return "", err
	}
	if len(ids) == 0 {
		return "no plans in this workspace", nil
	}
	var b strings.Builder
	for _, id := range ids {
		rs, err := s.ReadRunState(id)
		if err != nil {
			fmt.Fprintf(&b, "- %s  (stage unavailable: %v)\n", id, err)
			continue
		}
		fmt.Fprintf(&b, "- %s  [%s]\n", id, rs.Stage)
	}
	return strings.TrimSuffix(b.String(), "\n"), nil
}

// ---- plan_request_change ----

type planRequestChange struct {
	// pyCheck validates a proposed new acceptance script's Python syntax.
	// Test instances inject a mock; the registered instance uses the same
	// python -m py_compile check as plan_submit.
	pyCheck func(src string) error
}

type changeArgs struct {
	PlanID         string             `json:"plan_id"`
	Reason         string             `json:"reason"`
	ValidateScript string             `json:"validate_script,omitempty"`
	WriteScope     []submitScopeEntry `json:"write_scope,omitempty"`
}

func (planRequestChange) Name() string { return "plan_request_change" }
func (planRequestChange) Description() string {
	return "Request a change to locked plan content (acceptance script, write scope) during execution. The request goes through review; nothing changes until approved — unapproved changes are ignored. Provide at least one change and a reason."
}
func (planRequestChange) Schema() json.RawMessage {
	return json.RawMessage(`{
"type":"object",
"properties":{
  "plan_id":{"type":"string"},
  "reason":{"type":"string","description":"Why the locked content must change."},
  "validate_script":{"type":"string","description":"New acceptance script (optional)."},
  "write_scope":{"type":"array","items":{"type":"object","properties":{"path":{"type":"string"},"reason":{"type":"string"}},"required":["path","reason"]},"description":"New write scope (optional)."}
},
"required":["plan_id","reason"]
}`)
}
func (planRequestChange) ReadOnly() bool     { return false }
func (planRequestChange) PlanModeSafe() bool { return true }

func (t planRequestChange) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var p changeArgs
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("invalid plan_request_change args: %w", err)
	}
	if strings.TrimSpace(p.Reason) == "" {
		return "", fmt.Errorf("reason is required for a change request")
	}
	if strings.TrimSpace(p.ValidateScript) == "" && len(p.WriteScope) == 0 {
		return "", fmt.Errorf("provide at least one change: a new validate_script or a new write_scope")
	}
	for i, e := range p.WriteScope {
		if strings.TrimSpace(e.Path) == "" || strings.TrimSpace(e.Reason) == "" {
			return "", fmt.Errorf("write_scope[%d]: path and reason are both required", i)
		}
	}
	s, err := storeFrom(ctx)
	if err != nil {
		return "", err
	}
	rs, err := s.ReadRunState(p.PlanID)
	if err != nil {
		return "", err
	}
	// A change request only makes sense for a plan currently being executed:
	// it would otherwise sit stale until that plan is (maybe) executed. And one
	// outstanding request at a time — a second call would silently overwrite
	// the first before the user ever reviewed it. Guarded before the (heavier)
	// syntax check so a rejected request never runs py_compile.
	if rs.Stage != planstore.StageExecuting {
		return "", fmt.Errorf("plan_request_change: plan %q is %s; change requests only apply to an executing plan (run /strict-plan-exec first)", p.PlanID, rs.Stage)
	}
	if rs.ChangePending {
		return "", fmt.Errorf("plan_request_change: plan %q already has a pending change request awaiting review", p.PlanID)
	}
	// A proposed new acceptance script must be Python-parseable before it can
	// be routed through review — an unverifiable script must not reach the
	// approval card.
	if strings.TrimSpace(p.ValidateScript) != "" && t.pyCheck != nil {
		if err := t.pyCheck(p.ValidateScript); err != nil {
			return "", err
		}
	}
	scope := make([]planstore.ScopeEntry, 0, len(p.WriteScope))
	for _, e := range p.WriteScope {
		scope = append(scope, planstore.ScopeEntry{Path: e.Path, Reason: e.Reason})
	}
	err = s.UpdateRunState(p.PlanID, func(st *planstore.RunState) {
		st.ChangePending = true
		st.PendingChange = &planstore.PendingChange{
			At:             time.Now().UTC().Format(time.RFC3339),
			Reason:         p.Reason,
			ValidateScript: p.ValidateScript,
			WriteScope:     scope,
		}
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("plan_request_change: change request for %q recorded (stage unchanged: %s); it goes through review and takes effect only if approved", p.PlanID, rs.Stage), nil
}

// resolvePlanID resolves an optional plan id to the most recent locked plan.
func resolvePlanID(s *planstore.Store, id string) (string, error) {
	if id != "" {
		return id, nil
	}
	return s.MostRecentLocked()
}
