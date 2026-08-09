// Package api provides the HTTP handler implementations for the desktop API.
// Each handler receives a DesktopControl interface — the set of operations
// the desktop App exposes — and returns gateway.HandlerFunc.
package api

// WorkspaceMeta is the public representation of a registered workspace.
type WorkspaceMeta struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Current bool   `json:"current"`
}

// TopicInfo is the public representation of a topic within a workspace.
type TopicInfo struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Kind          string `json:"kind"`
	WorkspaceRoot string `json:"workspaceRoot,omitempty"`
	SessionPath   string `json:"sessionPath,omitempty"`
	Pinned        bool   `json:"pinned"`
}

// TopicStatus is the runtime status of a topic (its active session).
type TopicStatus struct {
	Running          bool   `json:"running"`
	Model            string `json:"model"`
	Effort           string `json:"effort"`
	HasPendingPrompt bool   `json:"hasPendingPrompt"`
	CurrentStep      string `json:"currentStep,omitempty"`
}

// ChatRequest is the request body for submitting a prompt.
type ChatRequest struct {
	Text string `json:"text"`
}

// ChatSubmitResponse is the response after submitting a prompt.
type ChatSubmitResponse struct {
	StreamID string `json:"streamId"`
}

// SessionMeta is the public representation of a saved session.
type SessionMeta struct {
	Path           string `json:"path"`
	Preview        string `json:"preview"`
	Title          string `json:"title,omitempty"`
	Turns          int    `json:"turns"`
	CreatedAt      int64  `json:"createdAt"`
	LastActivityAt int64  `json:"lastActivityAt"`
	DeletedAt      int64  `json:"deletedAt,omitempty"`
	Current        bool   `json:"current"`
	Open           bool   `json:"open"`
	Scope          string `json:"scope,omitempty"`
	WorkspaceRoot  string `json:"workspaceRoot,omitempty"`
	TopicID        string `json:"topicId,omitempty"`
	TopicTitle     string `json:"topicTitle,omitempty"`
	Kind           string `json:"kind,omitempty"`
}

// RenameSessionRequest is the request body for renaming a session.
type RenameSessionRequest struct {
	Title string `json:"title"`
}

// ResumeSessionRequest is the request body for resuming a saved session.
type ResumeSessionRequest struct {
	Path string `json:"path"`
}
type SteerRequest struct {
	Text string `json:"text"`
}

// ChatStatus is the enhanced runtime status of a topic's agent.
type ChatStatus struct {
	Running          bool   `json:"running"`
	Model            string `json:"model"`
	Effort           string `json:"effort"`
	HasPendingPrompt bool   `json:"hasPendingPrompt"`
	CancelRequested  bool   `json:"cancelRequested"`
	Cancellable      bool   `json:"cancellable"`
	BackgroundJobs   int    `json:"backgroundJobs"`
	CurrentStep      string `json:"currentStep,omitempty"`
	Turn             int    `json:"turn"`
}

// --- Approval types (Sprint 4) ---

// ApproveRequest is the request body for approving/denying a pending tool call.
// Opinion (Sprint 11 A3) is optional free-text guidance attached to the
// decision; empty means the legacy behavior.
type ApproveRequest struct {
	ID      string `json:"id"`
	Allow   bool   `json:"allow"`
	Session bool   `json:"session"`
	Persist bool   `json:"persist"`
	Opinion string `json:"opinion,omitempty"`
}

// AnswerRequest is the request body for answering pending ask questions.
type AnswerRequest struct {
	ID      string       `json:"id"`
	Answers []AnswerItem `json:"answers"`
}

// AnswerItem is one answer to one question in an ask request.
type AnswerItem struct {
	QuestionID string   `json:"questionId"`
	Selected   []string `json:"selected"`
}

// ApprovalModeRequest is the request body for setting the approval mode.
type ApprovalModeRequest struct {
	Mode string `json:"mode"`
}

// PendingResponse is the response for a pending-status query.
type PendingResponse struct {
	Pending bool `json:"pending"`
}

// --- History & Traceback types (Sprint 5) ---

// HistoryToolCall represents a tool invocation in history.
type HistoryToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// HistoryMessage represents a single message in the conversation history.
type HistoryMessage struct {
	Role              string            `json:"role"`
	Content           string            `json:"content"`
	Reasoning         string            `json:"reasoning,omitempty"`
	ToolCalls         []HistoryToolCall `json:"toolCalls,omitempty"`
	ToolCallID        string            `json:"toolCallId,omitempty"`
	ToolName          string            `json:"toolName,omitempty"`
	Pending           bool              `json:"pending,omitempty"`
	CheckpointTurn    *int              `json:"checkpointTurn,omitempty"`
	Messages          int               `json:"messages,omitempty"`
	Summary           string            `json:"summary,omitempty"`
	WorkDurationMs    int64             `json:"workDurationMs,omitempty"`
}

// HistoryResponse is the response for a history query.
type HistoryResponse struct {
	Messages   []HistoryMessage `json:"messages"`
	TotalTurns int              `json:"totalTurns"`
	HasMore    bool             `json:"hasMore"`
}

// CheckpointMeta represents an automatic checkpoint in the session.
type CheckpointMeta struct {
	Turn   int      `json:"turn"`
	Time   int64    `json:"time"`
	Prompt string   `json:"prompt"`
	Paths  []string `json:"paths"`
}

// BranchInfo represents a session branch.
type BranchInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name,omitempty"`
	ParentID  string `json:"parentId,omitempty"`
	ForkTurn  int    `json:"forkTurn,omitempty"`
	Turns     int    `json:"turns"`
	Preview   string `json:"preview,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

// RewindRequest is the request body for rewinding to a specific turn.
type RewindRequest struct {
	Turn  int    `json:"turn"`
	Scope string `json:"scope,omitempty"`
}

// ForkRequest is the request body for forking from a specific turn.
type ForkRequest struct {
	Turn int    `json:"turn"`
	Name string `json:"name,omitempty"`
}

// ForkResponse is the response after forking a session.
type ForkResponse struct {
	SessionPath string `json:"sessionPath"`
}

// SummarizeRequest is the request body for summarizing history.
type SummarizeRequest struct {
	Turn      int    `json:"turn"`
	Direction string `json:"direction"` // "from" or "upto"
}

// ToolResultResponse is the response for a tool result query.
type ToolResultResponse struct {
	ToolID string `json:"toolId"`
	Args   string `json:"args"`
	Output string `json:"output"`
}
