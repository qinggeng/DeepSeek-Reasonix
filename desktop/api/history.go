package api

import (
	"net/http"
	"strconv"

	"reasonix/desktop/gateway"
)

// HandleHistory returns a handler that gets the conversation history for a topic.
// GET /api/workspaces/{path}/topics/{id}/history?beforeTurn=N&limit=N
func HandleHistory(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := extractTopicID(r.URL.Path)
		if id == "" {
			gateway.WriteError(w, http.StatusBadRequest, "topic id is required")
			return
		}

		beforeTurn := 0
		limit := 0
		if s := r.URL.Query().Get("beforeTurn"); s != "" {
			if v, err := strconv.Atoi(s); err == nil {
				beforeTurn = v
			}
		}
		if s := r.URL.Query().Get("limit"); s != "" {
			if v, err := strconv.Atoi(s); err == nil {
				limit = v
			}
		}

		result, err := ctrl.History(id, beforeTurn, limit)
		if err != nil {
			if isTopicNotFound(err) {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else {
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		gateway.WriteJSON(w, http.StatusOK, result)
	}
}

// HandleCheckpoints returns a handler that lists checkpoints for a topic.
// GET /api/workspaces/{path}/topics/{id}/history/checkpoints
func HandleCheckpoints(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := extractTopicID(r.URL.Path)
		if id == "" {
			gateway.WriteError(w, http.StatusBadRequest, "topic id is required")
			return
		}

		checkpoints, err := ctrl.Checkpoints(id)
		if err != nil {
			if isTopicNotFound(err) {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else {
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		if checkpoints == nil {
			checkpoints = []CheckpointMeta{}
		}
		gateway.WriteJSON(w, http.StatusOK, checkpoints)
	}
}

// HandleBranches returns a handler that lists branches for a topic.
// GET /api/workspaces/{path}/topics/{id}/history/branches
func HandleBranches(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := extractTopicID(r.URL.Path)
		if id == "" {
			gateway.WriteError(w, http.StatusBadRequest, "topic id is required")
			return
		}

		branches, err := ctrl.Branches(id)
		if err != nil {
			if isTopicNotFound(err) {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else {
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		if branches == nil {
			branches = []BranchInfo{}
		}
		gateway.WriteJSON(w, http.StatusOK, branches)
	}
}

// HandleRewind returns a handler that rewinds a session to a specified turn.
// POST /api/workspaces/{path}/topics/{id}/history/rewind
func HandleRewind(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := extractTopicID(r.URL.Path)
		if id == "" {
			gateway.WriteError(w, http.StatusBadRequest, "topic id is required")
			return
		}

		var req RewindRequest
		if err := gateway.DecodeBody(r, &req); err != nil {
			gateway.WriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Turn < 0 {
			gateway.WriteError(w, http.StatusBadRequest, "turn must be >= 0")
			return
		}
		if req.Scope == "" {
			req.Scope = "both"
		}
		if req.Scope != "both" && req.Scope != "code" && req.Scope != "conversation" {
			gateway.WriteError(w, http.StatusBadRequest, "scope must be 'both', 'code', or 'conversation'")
			return
		}

		err := ctrl.Rewind(id, req.Turn, req.Scope)
		if err != nil {
			if isTopicNotFound(err) {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else if isTurnRunningError(err) {
				gateway.WriteError(w, http.StatusConflict, err.Error())
			} else if isNotReadyError(err) {
				gateway.WriteError(w, http.StatusConflict, err.Error())
			} else {
				gateway.WriteError(w, http.StatusBadRequest, err.Error())
			}
			return
		}
		gateway.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
	}
}

// HandleFork returns a handler that forks a session from a specified turn.
// POST /api/workspaces/{path}/topics/{id}/history/fork
func HandleFork(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := extractTopicID(r.URL.Path)
		if id == "" {
			gateway.WriteError(w, http.StatusBadRequest, "topic id is required")
			return
		}

		var req ForkRequest
		if err := gateway.DecodeBody(r, &req); err != nil {
			gateway.WriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Turn < 0 {
			gateway.WriteError(w, http.StatusBadRequest, "turn must be >= 0")
			return
		}

		sessionPath, err := ctrl.ForkSession(id, req.Turn, req.Name)
		if err != nil {
			if isTopicNotFound(err) {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else if isTurnRunningError(err) {
				gateway.WriteError(w, http.StatusConflict, err.Error())
			} else {
				gateway.WriteError(w, http.StatusBadRequest, err.Error())
			}
			return
		}
		gateway.WriteJSON(w, http.StatusOK, ForkResponse{SessionPath: sessionPath})
	}
}

// HandleCompact returns a handler that triggers context compaction.
// POST /api/workspaces/{path}/topics/{id}/history/compact
func HandleCompact(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := extractTopicID(r.URL.Path)
		if id == "" {
			gateway.WriteError(w, http.StatusBadRequest, "topic id is required")
			return
		}

		err := ctrl.CompactSession(id)
		if err != nil {
			if isTopicNotFound(err) {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else if isTurnRunningError(err) {
				gateway.WriteError(w, http.StatusConflict, err.Error())
			} else {
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		gateway.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
	}
}

// HandleSummarize returns a handler that summarizes session history.
// POST /api/workspaces/{path}/topics/{id}/history/summarize
func HandleSummarize(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := extractTopicID(r.URL.Path)
		if id == "" {
			gateway.WriteError(w, http.StatusBadRequest, "topic id is required")
			return
		}

		var req SummarizeRequest
		if err := gateway.DecodeBody(r, &req); err != nil {
			gateway.WriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Turn < 0 {
			gateway.WriteError(w, http.StatusBadRequest, "turn must be >= 0")
			return
		}
		if req.Direction == "" {
			gateway.WriteError(w, http.StatusBadRequest, "direction is required ('from' or 'upto')")
			return
		}
		if req.Direction != "from" && req.Direction != "upto" {
			gateway.WriteError(w, http.StatusBadRequest, "direction must be 'from' or 'upto'")
			return
		}

		var err error
		if req.Direction == "from" {
			err = ctrl.SummarizeFrom(id, req.Turn)
		} else {
			err = ctrl.SummarizeUpTo(id, req.Turn)
		}

		if err != nil {
			if isTopicNotFound(err) {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else if isTurnRunningError(err) {
				gateway.WriteError(w, http.StatusConflict, err.Error())
			} else {
				gateway.WriteError(w, http.StatusBadRequest, err.Error())
			}
			return
		}
		gateway.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
	}
}

// HandleToolResult returns a handler that queries a tool invocation result.
// GET /api/workspaces/{path}/topics/{id}/history/tool-result?toolId=xxx
func HandleToolResult(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := extractTopicID(r.URL.Path)
		if id == "" {
			gateway.WriteError(w, http.StatusBadRequest, "topic id is required")
			return
		}

		toolID := r.URL.Query().Get("toolId")
		if toolID == "" {
			gateway.WriteError(w, http.StatusBadRequest, "toolId is required")
			return
		}

		result, err := ctrl.ToolResult(id, toolID)
		if err != nil {
			if isTopicNotFound(err) {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			}
			return
		}
		gateway.WriteJSON(w, http.StatusOK, result)
	}
}
