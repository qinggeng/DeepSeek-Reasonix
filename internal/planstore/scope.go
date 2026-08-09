package planstore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Scope is the write-scope whitelist: (path, reason) pairs where a directory
// path grants recursive access. See CONTEXT.md [[可写范围]].
type Scope []ScopeEntry

// writeToolsWithPaths maps file-writing built-ins to the JSON arg fields that
// carry the target paths. Non-writers are absent, so ExtractWritePaths returns
// nothing for them and WriteGate does not intercept reads/shell.
var writeToolsWithPaths = map[string][]string{
	"write_file":    {"path"},
	"edit_file":     {"path"},
	"multi_edit":    {"path"},
	"move_file":     {"source_path", "destination_path"},
	"delete_range":  {"path"},
	"delete_symbol": {"path"},
	"notebook_edit": {"path"},
}

// ValidateScope checks every entry: non-empty path and reason, and the path
// resolving inside the workspace root (no absolute escapes, no ".." hops).
func ValidateScope(wsRoot string, entries []ScopeEntry) error {
	for i, e := range entries {
		if strings.TrimSpace(e.Path) == "" {
			return fmt.Errorf("write_scope[%d]: path is required", i)
		}
		if strings.TrimSpace(e.Reason) == "" {
			return fmt.Errorf("write_scope[%d]: reason is required", i)
		}
		resolved, err := realScopePath(wsRoot, e.Path)
		if err != nil {
			return fmt.Errorf("write_scope[%d] path %q: %w", i, e.Path, err)
		}
		root, _ := filepath.Abs(wsRoot)
		if !within(root, resolved) {
			return fmt.Errorf("write_scope[%d] path %q escapes the workspace root %q", i, e.Path, root)
		}
	}
	return nil
}

// Allows reports whether target may be written under this scope. target may
// not exist yet (write_file creates it); the deepest existing ancestor is
// resolved so symlinks cannot smuggle a write outside the scope. A scope entry
// that is a directory (or ends with a separator) grants everything below it.
func (s Scope) Allows(wsRoot, target string) error {
	root, err := filepath.Abs(wsRoot)
	if err != nil {
		return fmt.Errorf("write blocked: resolve workspace root: %w", err)
	}
	full := target
	if !filepath.IsAbs(full) {
		full = filepath.Join(root, full)
	}
	abs, err := realPath(full)
	if err != nil {
		return fmt.Errorf("write blocked: resolve %q: %w", target, err)
	}
	if !within(root, abs) {
		return fmt.Errorf("write blocked: path %q is outside the workspace; write inside the plan write scope only", target)
	}
	for _, e := range s {
		entry, err := realScopePath(wsRoot, e.Path)
		if err != nil {
			continue
		}
		if isDirEntry(wsRoot, e.Path) || strings.HasSuffix(e.Path, string(filepath.Separator)) || strings.HasSuffix(e.Path, "/") {
			if within(entry, abs) {
				return nil
			}
			continue
		}
		if filepath.Clean(entry) == filepath.Clean(abs) {
			return nil
		}
	}
	return fmt.Errorf("write blocked: path %q is outside the plan write scope (allowed: %s); request a scope change via plan_request_change",
		target, scopeSummary(s))
}

// WriteGate is the agent-layer check: extract the target paths of a write-tool
// call and reject any that fall outside the scope. Non-writer tools pass
// through untouched (reads and shell are governed by their own gates).
type WriteGate struct {
	WSRoot string
	Scope  Scope
}

// Check enforces the scope for one tool call. toolName is the canonical
// (unprefixed) built-in name.
func (g WriteGate) Check(toolName string, args json.RawMessage) error {
	fields, ok := writeToolsWithPaths[toolName]
	if !ok {
		return nil
	}
	if g.WSRoot == "" {
		return fmt.Errorf("write blocked: plan write scope is not bound to a workspace root")
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return fmt.Errorf("write blocked: cannot inspect %s args: %w", toolName, err)
	}
	for _, f := range fields {
		val, ok := raw[f]
		if !ok {
			continue
		}
		var p string
		if err := json.Unmarshal(val, &p); err != nil || p == "" {
			continue
		}
		if err := g.Scope.Allows(g.WSRoot, p); err != nil {
			return err
		}
	}
	return nil
}

// ExtractWritePaths returns the non-empty target paths of a write-tool call,
// for diagnostics and previews. Empty for non-writers.
func ExtractWritePaths(toolName string, args json.RawMessage) []string {
	fields, ok := writeToolsWithPaths[toolName]
	if !ok {
		return nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return nil
	}
	var out []string
	for _, f := range fields {
		val, ok := raw[f]
		if !ok {
			continue
		}
		var p string
		if err := json.Unmarshal(val, &p); err != nil || p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func scopeSummary(s Scope) string {
	parts := make([]string, 0, len(s))
	for _, e := range s {
		parts = append(parts, e.Path)
	}
	if len(parts) == 0 {
		return "(empty)"
	}
	return strings.Join(parts, ", ")
}

// realScopePath resolves a scope entry against the workspace root to an
// absolute, symlink-free path.
func realScopePath(wsRoot, entry string) (string, error) {
	root, err := filepath.Abs(wsRoot)
	if err != nil {
		return "", err
	}
	full := entry
	if !filepath.IsAbs(full) {
		full = filepath.Join(root, full)
	}
	return realPath(full)
}

// isDirEntry reports whether the scope entry points at an existing directory.
func isDirEntry(wsRoot, entry string) bool {
	full := entry
	if !filepath.IsAbs(full) {
		root, err := filepath.Abs(wsRoot)
		if err != nil {
			return false
		}
		full = filepath.Join(root, full)
	}
	info, err := os.Stat(full)
	return err == nil && info.IsDir()
}

// realPath resolves path to an absolute, symlink-free form, resolving the
// deepest existing ancestor (write targets may not exist yet).
func realPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	tail := ""
	cur := abs
	for {
		if real, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(real, tail), nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs, nil
		}
		tail = filepath.Join(filepath.Base(cur), tail)
		cur = parent
	}
}

// within reports whether path is at or below root (both absolute, cleaned).
func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
