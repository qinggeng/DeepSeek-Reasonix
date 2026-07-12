package api

import (
	"net/http"
	"net/url"
	"strings"

	"reasonix/desktop/gateway"
)

// RegisterWorkspaceRoutes registers all workspace-related endpoints on the gateway.
func RegisterWorkspaceRoutes(g *gateway.Gateway, ctrl DesktopControl) {
	g.Handle("GET", "/api/workspaces", HandleListWorkspaces(ctrl))
	g.Handle("POST", "/api/workspaces", HandleSwitchWorkspace(ctrl))
	// DELETE /api/workspaces/{path} handled by HandleWorkspaceDispatch.
}

// RegisterTopicRoutes registers all topic-related endpoints on the gateway.
// Topic routes are nested under workspace: /api/workspaces/{path}/topics/{id}/action
// GET /api/topics (exact, without trailing slash) lists active topics globally.
func RegisterTopicRoutes(g *gateway.Gateway, ctrl DesktopControl) {
	// All /api/workspaces/{path}/... routes go through the unified dispatch.
	g.Handle("GET", "/api/workspaces/", HandleWorkspaceDispatch(ctrl))
	g.Handle("POST", "/api/workspaces/", HandleWorkspaceDispatch(ctrl))
	g.Handle("PUT", "/api/workspaces/", HandleWorkspaceDispatch(ctrl))
	g.Handle("DELETE", "/api/workspaces/", HandleWorkspaceDispatch(ctrl))
	// GET /api/topics (exact) = list active topics (tabs), independent of workspace.
	g.Handle("GET", "/api/topics", HandleListActiveTopics(ctrl))

	// Global approval routes (Sprint 4).
	g.Handle("POST", "/api/approval/replay", HandleReplayPrompts(ctrl))
}

// HandleWorkspaceDispatch is a single entry point for all /api/workspaces/{path}/... routes.
// It extracts the workspace path, resource type (topics, sessions, trash), and
// dispatches to the appropriate handler.
func HandleWorkspaceDispatch(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Use percent-encoded path to preserve %2F etc. in workspace paths.
		r.URL.Path = r.URL.EscapedPath()

		wsPath, resourceType, resourceRest := extractWorkspaceResource(r.URL.Path)
		if wsPath == "" {
			gateway.WriteError(w, http.StatusBadRequest, "workspace path is required")
			return
		}

		switch resourceType {
		case "":
			// No resource type = workspace-level operation (remove).
			if r.Method == http.MethodDelete {
				HandleRemoveWorkspace(ctrl)(w, r)
				return
			}
			gateway.WriteError(w, http.StatusMethodNotAllowed, "method not allowed for workspace")
		case "topics":
			handleTopicsResource(ctrl, w, r, resourceRest)
		case "sessions":
			if resourceRest == "" {
				HandleListSessions(ctrl)(w, r)
				return
			}
			// resourceRest = "name.jsonl" or "name.jsonl/rename"
			name, sessionAction := splitSessionAction(resourceRest)
			if name == "" {
				gateway.WriteError(w, http.StatusBadRequest, "session name is required")
				return
			}
			switch sessionAction {
			case "":
				if r.Method == http.MethodGet {
					HandleGetSession(ctrl)(w, r)
					return
				}
				if r.Method == http.MethodDelete {
					HandleDeleteSession(ctrl)(w, r)
					return
				}
			case "rename":
				if r.Method == http.MethodPut {
					HandleRenameSession(ctrl)(w, r)
					return
				}
			}
			gateway.WriteError(w, http.StatusNotFound, "unknown session action: "+sessionAction)
		case "trash":
			if resourceRest == "" {
				if r.Method == http.MethodGet {
					HandleListTrash(ctrl)(w, r)
					return
				}
				if r.Method == http.MethodDelete {
					HandleEmptyTrash(ctrl)(w, r)
					return
				}
				gateway.WriteError(w, http.StatusMethodNotAllowed, "method not allowed for trash")
				return
			}
			// resourceRest = "name.jsonl/restore"
			trashName, trashAction := splitSessionAction(resourceRest)
			if trashName == "" {
				gateway.WriteError(w, http.StatusBadRequest, "session name is required")
				return
			}
			if trashAction == "restore" && r.Method == http.MethodPost {
				HandleRestoreSession(ctrl)(w, r)
				return
			}
			gateway.WriteError(w, http.StatusNotFound, "unknown trash action: "+trashAction)
		default:
			gateway.WriteError(w, http.StatusNotFound, "unknown resource type: "+resourceType)
		}
	}
}

// handleTopicsResource dispatches topic-specific operations.
// resourceRest is the portion after /api/workspaces/{path}/topics/.
// If empty: list topics; otherwise: extract topic ID + action and dispatch.
func handleTopicsResource(ctrl DesktopControl, w http.ResponseWriter, r *http.Request, resourceRest string) {
	if resourceRest == "" {
		if r.Method == http.MethodGet {
			HandleListTopics(ctrl)(w, r)
			return
		}
		gateway.WriteError(w, http.StatusMethodNotAllowed, "method not allowed for topics")
		return
	}

	id, action := extractTopicActionFromResource(resourceRest)
	if id == "" {
		gateway.WriteError(w, http.StatusBadRequest, "topic id is required")
		return
	}

	switch action {
	case "activate":
		if r.Method == http.MethodPost {
			HandleActivateTopic(ctrl)(w, r)
			return
		}
	case "status":
		if r.Method == http.MethodGet {
			HandleTopicStatus(ctrl)(w, r)
			return
		}
	case "chat":
		if r.Method == http.MethodPost {
			HandleSubmitPrompt(ctrl)(w, r)
			return
		}
	case "chat/cancel":
		if r.Method == http.MethodPost {
			HandleCancelRun(ctrl)(w, r)
			return
		}
	case "chat/steer":
		if r.Method == http.MethodPost {
			HandleSteerRun(ctrl)(w, r)
			return
		}
	case "chat/status":
		if r.Method == http.MethodGet {
			HandleChatStatus(ctrl)(w, r)
			return
		}
	case "chat/events":
		if r.Method == http.MethodGet {
			HandleEventStream(ctrl)(w, r)
			return
		}
	// Session operations for a topic (Sprint 3).
	case "session/new":
		if r.Method == http.MethodPost {
			HandleNewSession(ctrl)(w, r)
			return
		}
	case "session/clear":
		if r.Method == http.MethodPost {
			HandleClearSession(ctrl)(w, r)
			return
		}
	case "session/resume":
		if r.Method == http.MethodPost {
			HandleResumeSession(ctrl)(w, r)
			return
		}
	case "session":
		if r.Method == http.MethodGet {
			HandleCurrentSession(ctrl)(w, r)
			return
		}
	// Approval actions (Sprint 4).
	case "approve":
		if r.Method == http.MethodPost {
			HandleApprove(ctrl)(w, r)
			return
		}
	case "answer":
		if r.Method == http.MethodPost {
			HandleAnswer(ctrl)(w, r)
			return
		}
	case "pending":
		if r.Method == http.MethodGet {
			HandlePendingPrompt(ctrl)(w, r)
			return
		}
	case "approval-mode":
		if r.Method == http.MethodPost {
			HandleSetApprovalMode(ctrl)(w, r)
			return
		}
	// History & traceback actions (Sprint 5).
	case "history":
		if r.Method == http.MethodGet {
			HandleHistory(ctrl)(w, r)
			return
		}
	case "history/checkpoints":
		if r.Method == http.MethodGet {
			HandleCheckpoints(ctrl)(w, r)
			return
		}
	case "history/branches":
		if r.Method == http.MethodGet {
			HandleBranches(ctrl)(w, r)
			return
		}
	case "history/rewind":
		if r.Method == http.MethodPost {
			HandleRewind(ctrl)(w, r)
			return
		}
	case "history/fork":
		if r.Method == http.MethodPost {
			HandleFork(ctrl)(w, r)
			return
		}
	case "history/compact":
		if r.Method == http.MethodPost {
			HandleCompact(ctrl)(w, r)
			return
		}
	case "history/summarize":
		if r.Method == http.MethodPost {
			HandleSummarize(ctrl)(w, r)
			return
		}
	case "history/tool-result":
		if r.Method == http.MethodGet {
			HandleToolResult(ctrl)(w, r)
			return
		}
	}
	gateway.WriteError(w, http.StatusNotFound, "unknown topic action: "+action)
}

// HandleListWorkspaces returns a handler that lists all registered workspaces.
// GET /api/workspaces
func HandleListWorkspaces(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		workspaces := ctrl.ListWorkspaces()
		gateway.WriteJSON(w, http.StatusOK, workspaces)
	}
}

// HandleSwitchWorkspace returns a handler that switches the active workspace.
// POST /api/workspaces
func HandleSwitchWorkspace(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Path string `json:"path"`
		}
		if err := gateway.DecodeBody(r, &req); err != nil {
			gateway.WriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Path == "" {
			gateway.WriteError(w, http.StatusBadRequest, "path is required")
			return
		}

		ws, err := ctrl.SwitchWorkspace(req.Path)
		if err != nil {
			gateway.WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		gateway.WriteJSON(w, http.StatusOK, ws)
	}
}

// HandleRemoveWorkspace returns a handler that removes a registered workspace.
// The workspace path is extracted from the URL path:
// DELETE /api/workspaces/{path}
func HandleRemoveWorkspace(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := extractWorkspacePath(r.URL.Path)
		if path == "" {
			gateway.WriteError(w, http.StatusBadRequest, "workspace path is required")
			return
		}

		if err := ctrl.RemoveWorkspace(path); err != nil {
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

// HandleListTopics returns a handler that lists topics for a workspace.
// The workspace path is extracted from the URL:
// GET /api/workspaces/{path}/topics
func HandleListTopics(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := extractWorkspacePath(r.URL.Path)
		if path == "" {
			gateway.WriteError(w, http.StatusBadRequest, "workspace path is required")
			return
		}

		topics, err := ctrl.ListTopics(path)
		if err != nil {
			if isNotFoundError(err) {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else {
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		if topics == nil {
			topics = []TopicInfo{}
		}
		gateway.WriteJSON(w, http.StatusOK, topics)
	}
}

// HandleListActiveTopics returns a handler that lists all currently active topics (tabs).
// GET /api/topics
func HandleListActiveTopics(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		topics := ctrl.ListActiveTopics()
		if topics == nil {
			topics = []TopicInfo{}
		}
		gateway.WriteJSON(w, http.StatusOK, topics)
	}
}

// HandleActivateTopic returns a handler that activates a topic.
// The topic ID is extracted from the URL:
// POST /api/workspaces/{path}/topics/{id}/activate
func HandleActivateTopic(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := extractTopicID(r.URL.Path)
		if id == "" {
			gateway.WriteError(w, http.StatusBadRequest, "topic id is required")
			return
		}

		info, err := ctrl.ActivateTopic(id)
		if err != nil {
			if isTopicNotFound(err) {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else {
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		gateway.WriteJSON(w, http.StatusOK, info)
	}
}

// HandleTopicStatus returns a handler that queries the runtime status of a topic.
// The topic ID is extracted from the URL:
// GET /api/workspaces/{path}/topics/{id}/status
func HandleTopicStatus(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := extractTopicID(r.URL.Path)
		if id == "" {
			gateway.WriteError(w, http.StatusBadRequest, "topic id is required")
			return
		}

		status, err := ctrl.TopicStatus(id)
		if err != nil {
			if isTopicNotFound(err) {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else {
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		gateway.WriteJSON(w, http.StatusOK, status)
	}
}

// --- URL path extraction ---

// prefixWorkspaces is the URL prefix for workspace-related paths.
const prefixWorkspaces = "/api/workspaces/"

// extractWorkspacePath extracts and URL-decodes the workspace path from a URL.
// It takes the first path segment after /api/workspaces/ as the workspace path,
// ignoring any subsequent resource segments. Works with both bare and resource URLs:
//
//	/api/workspaces/E%3A%2Fprojects%2Ffoo           → E:/projects/foo
//	/api/workspaces/E%3A%2Fprojects%2Ffoo/topics     → E:/projects/foo
//	/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/id  → E:/projects/foo
func extractWorkspacePath(urlPath string) string {
	if !strings.HasPrefix(urlPath, prefixWorkspaces) {
		return ""
	}
	wsPath, _, _ := extractWorkspaceResource(urlPath)
	return wsPath
}

// extractWorkspaceResource parses /api/workspaces/{path}/{type}/{rest}.
// Returns the decoded workspace path, resource type (e.g. "topics", "sessions"),
// and the remaining path after the type segment.
//
//	/api/workspaces/E%3A%2Ffoo                         → ("E:/foo", "", "")
//	/api/workspaces/E%3A%2Ffoo/topics                   → ("E:/foo", "topics", "")
//	/api/workspaces/E%3A%2Ffoo/topics/id/activate       → ("E:/foo", "topics", "id/activate")
//	/api/workspaces/E%3A%2Ffoo/topics/id/chat/cancel    → ("E:/foo", "topics", "id/chat/cancel")
func extractWorkspaceResource(urlPath string) (workspacePath, resourceType, rest string) {
	if !strings.HasPrefix(urlPath, prefixWorkspaces) {
		return "", "", ""
	}
	s := strings.TrimPrefix(urlPath, prefixWorkspaces)
	idx := strings.IndexByte(s, '/')
	if idx < 0 {
		workspacePath = urlPathDecode(s)
		return
	}
	workspacePath = urlPathDecode(s[:idx])
	remainder := s[idx+1:]
	slash := strings.IndexByte(remainder, '/')
	if slash < 0 {
		resourceType = remainder
		return
	}
	resourceType = remainder[:slash]
	rest = remainder[slash+1:]
	return
}

// extractTopicID extracts the topic ID from a URL path.
// Supports: /api/workspaces/{path}/topics/{id}/{action}
//
//	/api/workspaces/E%3A%2Ffoo/topics/topic-dev/activate  → "topic-dev"
func extractTopicID(urlPath string) string {
	if !strings.HasPrefix(urlPath, prefixWorkspaces) {
		return ""
	}
	_, resourceType, resourceRest := extractWorkspaceResource(urlPath)
	if resourceType != "topics" || resourceRest == "" {
		return ""
	}
	if idx := strings.IndexByte(resourceRest, '/'); idx >= 0 {
		return urlPathDecode(resourceRest[:idx])
	}
	return urlPathDecode(resourceRest)
}

// extractTopicAction extracts both the topic ID and the action segment from a
// /api/workspaces/{path}/topics/ URL. Examples:
//
//	/api/workspaces/{path}/topics/topic-dev/activate      → ("topic-dev", "activate")
//	/api/workspaces/{path}/topics/topic-dev/chat          → ("topic-dev", "chat")
//	/api/workspaces/{path}/topics/topic-dev/chat/cancel   → ("topic-dev", "chat/cancel")
func extractTopicAction(urlPath string) (topicID, action string) {
	_, resourceType, resourceRest := extractWorkspaceResource(urlPath)
	if resourceType != "topics" || resourceRest == "" {
		return "", ""
	}
	return extractTopicActionFromResource(resourceRest)
}

// extractTopicActionFromResource extracts topic ID and action from the remainder
// after /api/workspaces/{path}/topics/.
//
//	"topic-dev/status"       → ("topic-dev", "status")
//	"topic-dev/chat/cancel"  → ("topic-dev", "chat/cancel")
func extractTopicActionFromResource(resourceRest string) (topicID, action string) {
	idx := strings.IndexByte(resourceRest, '/')
	if idx < 0 {
		return urlPathDecode(resourceRest), ""
	}
	topicID = urlPathDecode(resourceRest[:idx])
	action = resourceRest[idx+1:]
	return
}

// splitSessionAction splits the session resource rest into name and optional action.
//
//	"name.jsonl"         → ("name.jsonl", "")
//	"name.jsonl/rename"  → ("name.jsonl", "rename")
func splitSessionAction(resourceRest string) (name, action string) {
	idx := strings.IndexByte(resourceRest, '/')
	if idx < 0 {
		return urlPathDecode(resourceRest), ""
	}
	name = urlPathDecode(resourceRest[:idx])
	action = resourceRest[idx+1:]
	return
}

// urlPathDecode is a percent-decoder for URL path segments.
func urlPathDecode(s string) string {
	decoded, err := url.PathUnescape(s)
	if err != nil {
		return s
	}
	return decoded
}

// urlPathEncode percent-encodes a path segment for use in URLs.
func urlPathEncode(s string) string {
	return url.PathEscape(s)
}

// --- error classification ---

func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "not found")
}

func isTopicNotFound(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "topic not found")
}

// isTurnRunningError checks if the error indicates a running turn prevents the operation.
func isTurnRunningError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "turn is running")
}

// isNotReadyError checks if the error indicates the resource is not ready.
func isNotReadyError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "not ready")
}
