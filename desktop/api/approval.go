package api

import (
	"net/http"

	"reasonix/desktop/gateway"
)

// HandleApprove approves or denies a pending tool call.
// POST /api/workspaces/{path}/topics/{id}/approve
func HandleApprove(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := extractTopicAction(r.URL.Path)
		if id == "" {
			gateway.WriteError(w, http.StatusBadRequest, "topic id is required")
			return
		}

		var req ApproveRequest
		if err := gateway.DecodeBody(r, &req); err != nil {
			gateway.WriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.ID == "" {
			gateway.WriteError(w, http.StatusBadRequest, "approval id is required")
			return
		}

		if err := ctrl.Approve(id, req); err != nil {
			switch {
			case isTopicNotFound(err):
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			case isApprovalNotFound(err):
				// Unknown/consumed approval id: surface a 404 so clients can
				// distinguish a real decision from a no-op (Sprint 11 HTTP
				// interface improvement — E2E id probing relies on it).
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			default:
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}

		gateway.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"status":     "approved",
			"approvalId": req.ID,
			"allow":      req.Allow,
		})
	}
}

// HandleAnswer answers pending ask questions.
// POST /api/workspaces/{path}/topics/{id}/answer
func HandleAnswer(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := extractTopicAction(r.URL.Path)
		if id == "" {
			gateway.WriteError(w, http.StatusBadRequest, "topic id is required")
			return
		}

		var req AnswerRequest
		if err := gateway.DecodeBody(r, &req); err != nil {
			gateway.WriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.ID == "" {
			gateway.WriteError(w, http.StatusBadRequest, "ask id is required")
			return
		}

		if err := ctrl.AnswerQuestion(id, req); err != nil {
			if isTopicNotFound(err) {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else {
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}

		gateway.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"status": "answered",
			"askId":  req.ID,
			"count":  len(req.Answers),
		})
	}
}

// HandlePendingPrompt checks if a topic has a pending approval or ask.
// GET /api/workspaces/{path}/topics/{id}/pending
func HandlePendingPrompt(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := extractTopicAction(r.URL.Path)
		if id == "" {
			gateway.WriteError(w, http.StatusBadRequest, "topic id is required")
			return
		}

		pending, err := ctrl.PendingPrompt(id)
		if err != nil {
			if isTopicNotFound(err) {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else {
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}

		gateway.WriteJSON(w, http.StatusOK, PendingResponse{Pending: pending})
	}
}

// HandleSetApprovalMode sets the approval mode for a topic.
// POST /api/workspaces/{path}/topics/{id}/approval-mode
func HandleSetApprovalMode(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := extractTopicAction(r.URL.Path)
		if id == "" {
			gateway.WriteError(w, http.StatusBadRequest, "topic id is required")
			return
		}

		var req ApprovalModeRequest
		if err := gateway.DecodeBody(r, &req); err != nil {
			gateway.WriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}

		switch req.Mode {
		case "ask", "auto", "yolo":
			// valid modes
		default:
			gateway.WriteError(w, http.StatusBadRequest, "invalid mode: must be 'ask', 'auto', or 'yolo'")
			return
		}

		if err := ctrl.SetApprovalMode(id, req.Mode); err != nil {
			if isTopicNotFound(err) {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else {
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}

		gateway.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"status": "ok",
			"mode":   req.Mode,
		})
	}
}

// HandleReplayPrompts replays all pending prompts across all topics.
// POST /api/approval/replay
func HandleReplayPrompts(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			gateway.WriteError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		ctrl.ReplayPendingPrompts()

		gateway.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"status": "ok",
		})
	}
}
