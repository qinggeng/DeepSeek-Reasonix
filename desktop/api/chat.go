package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"reasonix/desktop/gateway"
	"reasonix/internal/event"

	"github.com/google/uuid"
)

// HandleSubmitPrompt submits a prompt to the Agent for a given topic.
// POST /api/workspaces/{path}/topics/{id}/chat  {"text":"..."}
// Returns 202 with a streamId for SSE event consumption.
func HandleSubmitPrompt(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ChatRequest
		if err := gateway.DecodeBody(r, &req); err != nil {
			gateway.WriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if strings.TrimSpace(req.Text) == "" {
			gateway.WriteError(w, http.StatusBadRequest, "text is required")
			return
		}

		// Extract topic ID from the URL via dispatch context (already extracted).
		id, _ := extractTopicAction(r.URL.Path)
		if id == "" {
			gateway.WriteError(w, http.StatusBadRequest, "topic id is required")
			return
		}

		streamID, err := ctrl.SubmitPrompt(id, req.Text)
		if err != nil {
			if strings.Contains(err.Error(), "not found") || strings.Contains(err.Error(), "not ready") {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else {
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		gateway.WriteJSON(w, http.StatusAccepted, ChatSubmitResponse{StreamID: streamID})
	}
}

// HandleCancelRun cancels the current Agent run for a topic.
// POST /api/workspaces/{path}/topics/{id}/chat/cancel
func HandleCancelRun(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := extractTopicAction(r.URL.Path)
		if id == "" {
			gateway.WriteError(w, http.StatusBadRequest, "topic id is required")
			return
		}

		if err := ctrl.CancelRun(id); err != nil {
			if strings.Contains(err.Error(), "not found") {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else {
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		gateway.WriteJSON(w, http.StatusOK, map[string]bool{"cancelled": true})
	}
}

// HandleSteerRun sends a steer/intervention to a running Agent.
// POST /api/workspaces/{path}/topics/{id}/chat/steer  {"text":"..."}
func HandleSteerRun(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req SteerRequest
		if err := gateway.DecodeBody(r, &req); err != nil {
			gateway.WriteError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if strings.TrimSpace(req.Text) == "" {
			gateway.WriteError(w, http.StatusBadRequest, "text is required")
			return
		}

		id, _ := extractTopicAction(r.URL.Path)
		if id == "" {
			gateway.WriteError(w, http.StatusBadRequest, "topic id is required")
			return
		}

		if err := ctrl.SteerRun(id, req.Text); err != nil {
			if strings.Contains(err.Error(), "not found") {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else if strings.Contains(err.Error(), "not running") {
				gateway.WriteError(w, http.StatusBadRequest, err.Error())
			} else {
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		gateway.WriteJSON(w, http.StatusOK, map[string]bool{"steered": true})
	}
}

// HandleChatStatus returns the enhanced runtime status of a topic's Agent.
// GET /api/workspaces/{path}/topics/{id}/chat/status
func HandleChatStatus(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, _ := extractTopicAction(r.URL.Path)
		if id == "" {
			gateway.WriteError(w, http.StatusBadRequest, "topic id is required")
			return
		}

		status, err := ctrl.ChatStatus(id)
		if err != nil {
			if strings.Contains(err.Error(), "not found") {
				gateway.WriteError(w, http.StatusNotFound, err.Error())
			} else {
				gateway.WriteError(w, http.StatusInternalServerError, err.Error())
			}
			return
		}
		gateway.WriteJSON(w, http.StatusOK, status)
	}
}

// HandleEventStream returns a Server-Sent Events stream for a given streamId.
// The stream delivers Agent events (TurnStarted, Reasoning, Text, ToolDispatch,
// ToolResult, TurnDone, etc.) as SSE frames.
// GET /api/workspaces/{path}/topics/{id}/chat/events?streamId=xxx
func HandleEventStream(ctrl DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		streamID := r.URL.Query().Get("streamId")
		if streamID == "" {
			gateway.WriteError(w, http.StatusBadRequest, "streamId is required")
			return
		}

		hub := ctrl.StreamHub()
		if hub == nil {
			gateway.WriteError(w, http.StatusInternalServerError, "stream hub not available")
			return
		}

		ch := hub.Lookup(streamID)
		if ch == nil {
			gateway.WriteError(w, http.StatusNotFound, "stream not found")
			return
		}

		// Set SSE headers.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			gateway.WriteError(w, http.StatusInternalServerError, "streaming not supported")
			return
		}

		// Notify channel for client disconnect.
		ctx := r.Context()

		for {
			select {
			case <-ctx.Done():
				// Client disconnected.
				return
			case e, ok := <-ch:
				if !ok {
					// Channel closed (TurnDone processed or hub unregistered).
					return
				}
				if err := writeSSEEvent(w, e); err != nil {
					return
				}
				flusher.Flush()

				// If TurnDone, close the stream after the event.
				if e.Kind == event.TurnDone {
					return
				}
			}
		}
	}
}

// writeSSEEvent writes a single event.Event as an SSE data frame,
// including the relevant payload fields for each event kind.
func writeSSEEvent(w http.ResponseWriter, e event.Event) error {
	eventName := eventKindName(e.Kind)
	payload := eventToJSON(e)
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", eventName, data)
	return err
}

// eventToJSON converts an event.Event to a map suitable for JSON serialization.
// Only the relevant fields for the event's Kind are included.
func eventToJSON(e event.Event) map[string]any {
	m := map[string]any{
		"kind":  int(e.Kind),
		"event": eventKindName(e.Kind),
	}

	switch e.Kind {
	case event.Reasoning, event.Text:
		m["text"] = e.Text

	case event.Message:
		m["text"] = e.Text
		if e.Reasoning != "" {
			m["reasoning"] = e.Reasoning
		}
		if e.MemoryCitations != nil {
			m["memoryCitations"] = e.MemoryCitations
		}

	case event.ToolDispatch:
		m["toolId"] = e.Tool.ID
		m["name"] = e.Tool.Name
		if e.Tool.Args != "" {
			m["args"] = e.Tool.Args
		}
		m["readOnly"] = e.Tool.ReadOnly

	case event.ToolResult:
		m["toolId"] = e.Tool.ID
		if e.Tool.Output != "" {
			m["output"] = e.Tool.Output
		}
		if e.Tool.Err != "" {
			m["error"] = e.Tool.Err
		}
		m["durationMs"] = e.Tool.DurationMs
		m["truncated"] = e.Tool.Truncated

	case event.ToolProgress:
		m["toolId"] = e.Tool.ID
		m["output"] = e.Tool.Output

	case event.Usage:
		if e.Usage != nil {
			m["promptTokens"] = e.Usage.PromptTokens
			m["completionTokens"] = e.Usage.CompletionTokens
			m["totalTokens"] = e.Usage.TotalTokens
			m["cacheHitTokens"] = e.Usage.CacheHitTokens
			m["cacheMissTokens"] = e.Usage.CacheMissTokens
			m["reasoningTokens"] = e.Usage.ReasoningTokens
		}

	case event.Notice:
		m["text"] = e.Text
		m["level"] = int(e.Level)

	case event.Phase:
		m["text"] = e.Text

	case event.TurnDone:
		if e.Err != nil {
			m["error"] = e.Err.Error()
		}

	case event.Steer:
		m["text"] = e.Text

	case event.ApprovalRequest:
		m["approvalId"] = e.Approval.ID
		m["toolName"] = e.Approval.Tool
		if e.Approval.Subject != "" {
			m["subject"] = e.Approval.Subject
		}
		if e.Approval.Reason != "" {
			m["reason"] = e.Approval.Reason
		}

	case event.AskRequest:
		m["askId"] = e.Ask.ID
		if e.Ask.Questions != nil {
			m["questions"] = e.Ask.Questions
		}

	case event.Retrying:
		m["attempt"] = e.RetryAttempt
		m["maxAttempts"] = e.RetryMax
	}

	return m
}

// eventKindName returns a human-readable name for an event kind.
func eventKindName(k event.Kind) string {
	switch k {
	case event.TurnStarted:
		return "TurnStarted"
	case event.Reasoning:
		return "Reasoning"
	case event.Text:
		return "Text"
	case event.Message:
		return "Message"
	case event.ToolDispatch:
		return "ToolDispatch"
	case event.ToolResult:
		return "ToolResult"
	case event.ToolProgress:
		return "ToolProgress"
	case event.Usage:
		return "Usage"
	case event.Notice:
		return "Notice"
	case event.Phase:
		return "Phase"
	case event.ApprovalRequest:
		return "ApprovalRequest"
	case event.AskRequest:
		return "AskRequest"
	case event.TurnDone:
		return "TurnDone"
	case event.CompactionStarted:
		return "CompactionStarted"
	case event.CompactionDone:
		return "CompactionDone"
	case event.MCPSurfaceReady:
		return "MCPSurfaceReady"
	case event.Retrying:
		return "Retrying"
	case event.Steer:
		return "Steer"
	case event.GuardianAssessment:
		return "GuardianAssessment"
	default:
		return fmt.Sprintf("Kind(%d)", k)
	}
}

// --- stream ID generation ---

// newStreamID generates a unique stream ID.
var newStreamID = func() string {
	return uuid.New().String()
}
