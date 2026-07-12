package api

import (
	"net/http"
	"strings"

	"reasonix/desktop/gateway"
)

// HandleListSessions returns a handler that lists all sessions for a workspace.
// GET /api/workspaces/{path}/sessions
func HandleListSessions(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := extractWorkspacePath(r.URL.Path)
		if path == "" {
			gateway.WriteError(w, http.StatusBadRequest, "workspace path is required")
			return
		}

		sessions, err := ctrl.ListSessions(path)
		if err != nil {
			if isNotFoundError(err) {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else {
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		if sessions == nil {
			sessions = []SessionMeta{}
		}
		gateway.WriteJSON(w, http.StatusOK, sessions)
	}
}

// HandleGetSession returns a handler that gets a single session's metadata.
// GET /api/workspaces/{path}/sessions/{name}
func HandleGetSession(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := extractWorkspacePath(r.URL.Path)
		if path == "" {
			gateway.WriteError(w, http.StatusBadRequest, "workspace path is required")
			return
		}

		name := extractSessionName(r.URL.Path)
		if name == "" {
			gateway.WriteError(w, http.StatusBadRequest, "session name is required")
			return
		}

		session, err := ctrl.GetSession(path, name)
		if err != nil {
			if isNotFoundError(err) {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else {
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		gateway.WriteJSON(w, http.StatusOK, session)
	}
}

// HandleRenameSession returns a handler that renames a session.
// PUT /api/workspaces/{path}/sessions/{name}/rename  {"title":"..."}
func HandleRenameSession(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := extractWorkspacePath(r.URL.Path)
		if path == "" {
			gateway.WriteError(w, http.StatusBadRequest, "workspace path is required")
			return
		}

		name := extractSessionName(r.URL.Path)
		if name == "" {
			gateway.WriteError(w, http.StatusBadRequest, "session name is required")
			return
		}

		var req RenameSessionRequest
		if err := gateway.DecodeBody(r, &req); err != nil {
			gateway.WriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		if err := ctrl.RenameSession(path, name, req.Title); err != nil {
			if isNotFoundError(err) {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else {
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		gateway.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
	}
}

// extractSessionName extracts the session name from a /api/workspaces/{path}/sessions/ URL.
//
//	/api/workspaces/{path}/sessions/name.jsonl       → "name.jsonl"
//	/api/workspaces/{path}/sessions/name.jsonl/rename → "name.jsonl"
func extractSessionName(urlPath string) string {
	_, resourceType, resourceRest := extractWorkspaceResource(urlPath)
	if resourceType != "sessions" || resourceRest == "" {
		return ""
	}
	if idx := strings.IndexByte(resourceRest, '/'); idx >= 0 {
		return urlPathDecode(resourceRest[:idx])
	}
	return urlPathDecode(resourceRest)
}

// HandleDeleteSession moves a session to the trash (soft delete).
// DELETE /api/workspaces/{path}/sessions/{name}
func HandleDeleteSession(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := extractWorkspacePath(r.URL.Path)
		if path == "" {
			gateway.WriteError(w, http.StatusBadRequest, "workspace path is required")
			return
		}

		name := extractSessionName(r.URL.Path)
		if name == "" {
			gateway.WriteError(w, http.StatusBadRequest, "session name is required")
			return
		}

		if err := ctrl.DeleteSession(path, name); err != nil {
			if isNotFoundError(err) {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else {
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		gateway.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
	}
}

// HandleListTrash returns a handler that lists all trashed sessions.
// GET /api/workspaces/{path}/trash
func HandleListTrash(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := extractWorkspacePath(r.URL.Path)
		if path == "" {
			gateway.WriteError(w, http.StatusBadRequest, "workspace path is required")
			return
		}

		sessions, err := ctrl.ListTrash(path)
		if err != nil {
			if isNotFoundError(err) {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else {
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		if sessions == nil {
			sessions = []SessionMeta{}
		}
		gateway.WriteJSON(w, http.StatusOK, sessions)
	}
}

// HandleRestoreSession restores a session from the trash.
// POST /api/workspaces/{path}/trash/{name}/restore
func HandleRestoreSession(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := extractWorkspacePath(r.URL.Path)
		if path == "" {
			gateway.WriteError(w, http.StatusBadRequest, "workspace path is required")
			return
		}

		name := extractTrashName(r.URL.Path)
		if name == "" {
			gateway.WriteError(w, http.StatusBadRequest, "session name is required")
			return
		}

		if err := ctrl.RestoreSession(path, name); err != nil {
			if isNotFoundError(err) {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else {
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		gateway.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
	}
}

// HandleEmptyTrash permanently empties the trash for a workspace.
// DELETE /api/workspaces/{path}/trash
func HandleEmptyTrash(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := extractWorkspacePath(r.URL.Path)
		if path == "" {
			gateway.WriteError(w, http.StatusBadRequest, "workspace path is required")
			return
		}

		if err := ctrl.EmptyTrash(path); err != nil {
			gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		gateway.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
	}
}

// extractTrashName extracts the session name from a /api/workspaces/{path}/trash/ URL.
//
//	/api/workspaces/{path}/trash/name.jsonl        → "name.jsonl"
//	/api/workspaces/{path}/trash/name.jsonl/restore → "name.jsonl"
func extractTrashName(urlPath string) string {
	_, resourceType, resourceRest := extractWorkspaceResource(urlPath)
	if resourceType != "trash" || resourceRest == "" {
		return ""
	}
	if idx := strings.IndexByte(resourceRest, '/'); idx >= 0 {
		return urlPathDecode(resourceRest[:idx])
	}
	return urlPathDecode(resourceRest)
}

// HandleNewSession creates a new session for the given topic.
// POST /api/workspaces/{path}/topics/{id}/session/new
func HandleNewSession(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := extractTopicAction(r.URL.Path)
		if id == "" {
			gateway.WriteError(w, http.StatusBadRequest, "topic id is required")
			return
		}

		if err := ctrl.NewSession(id); err != nil {
			switch {
			case isTopicNotFound(err):
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			case isTurnRunningError(err):
				gateway.WriteError(w, http.StatusConflict, err.Error())
			case isNotReadyError(err):
				gateway.WriteError(w, http.StatusServiceUnavailable, err.Error())
			default:
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		gateway.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
	}
}

// HandleClearSession clears the current session for the given topic.
// POST /api/workspaces/{path}/topics/{id}/session/clear
func HandleClearSession(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := extractTopicAction(r.URL.Path)
		if id == "" {
			gateway.WriteError(w, http.StatusBadRequest, "topic id is required")
			return
		}

		if err := ctrl.ClearSessionForTopic(id); err != nil {
			switch {
			case isTopicNotFound(err):
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			case isTurnRunningError(err):
				gateway.WriteError(w, http.StatusConflict, err.Error())
			case isNotReadyError(err):
				gateway.WriteError(w, http.StatusServiceUnavailable, err.Error())
			default:
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		gateway.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
	}
}

// HandleResumeSession resumes a saved session for the given topic.
// POST /api/workspaces/{path}/topics/{id}/session/resume  {"path":"..."}
func HandleResumeSession(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := extractTopicAction(r.URL.Path)
		if id == "" {
			gateway.WriteError(w, http.StatusBadRequest, "topic id is required")
			return
		}

		var req ResumeSessionRequest
		if err := gateway.DecodeBody(r, &req); err != nil {
			gateway.WriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Path == "" {
			gateway.WriteError(w, http.StatusBadRequest, "session path is required")
			return
		}

		if err := ctrl.ResumeSession(id, req.Path); err != nil {
			if isTopicNotFound(err) {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else {
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		gateway.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
	}
}

// HandleCurrentSession returns the current session info for the given topic.
// GET /api/workspaces/{path}/topics/{id}/session
func HandleCurrentSession(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := extractTopicAction(r.URL.Path)
		if id == "" {
			gateway.WriteError(w, http.StatusBadRequest, "topic id is required")
			return
		}

		session, err := ctrl.CurrentSession(id)
		if err != nil {
			if isTopicNotFound(err) {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else {
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		gateway.WriteJSON(w, http.StatusOK, session)
	}
}
