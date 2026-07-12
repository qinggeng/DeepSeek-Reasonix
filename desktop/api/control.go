package api

// DesktopControl defines the operations the desktop App exposes to the
// HTTP API layer. This interface isolates handler tests from the real App.
type DesktopControl interface {
	ListWorkspaces() []WorkspaceMeta
	SwitchWorkspace(dir string) (WorkspaceMeta, error)
	RemoveWorkspace(dir string) error
	ListTopics(workspaceRoot string) ([]TopicInfo, error)
	ActivateTopic(topicID string) (TopicInfo, error)
	TopicStatus(topicID string) (TopicStatus, error)

	// Chat interaction — core Agent interaction.
	SubmitPrompt(topicID, input string) (streamID string, err error)
	CancelRun(topicID string) error
	SteerRun(topicID, text string) error
	ChatStatus(topicID string) (ChatStatus, error)

	// StreamHub returns the event-stream hub for SSE connections.
	StreamHub() *StreamHub

	// ListActiveTopics returns all currently active topics (tabs).
	ListActiveTopics() []TopicInfo

	// Session management.
	ListSessions(workspaceRoot string) ([]SessionMeta, error)
	GetSession(workspaceRoot, sessionName string) (SessionMeta, error)
	RenameSession(workspaceRoot, sessionName, title string) error
	NewSession(topicID string) error
	ClearSessionForTopic(topicID string) error
	ResumeSession(topicID, sessionPath string) error
	CurrentSession(topicID string) (SessionMeta, error)

	// Trash management.
	DeleteSession(workspaceRoot, sessionName string) error
	ListTrash(workspaceRoot string) ([]SessionMeta, error)
	RestoreSession(workspaceRoot, sessionName string) error
	EmptyTrash(workspaceRoot string) error

	// Approvals (Sprint 4).
	Approve(topicID string, req ApproveRequest) error
	AnswerQuestion(topicID string, req AnswerRequest) error
	PendingPrompt(topicID string) (bool, error)
	ReplayPendingPrompts()
	SetApprovalMode(topicID, mode string) error

	// History & Traceback (Sprint 5).
	History(topicID string, beforeTurn, limit int) (HistoryResponse, error)
	Checkpoints(topicID string) ([]CheckpointMeta, error)
	Branches(topicID string) ([]BranchInfo, error)
	Rewind(topicID string, turn int, scope string) error
	ForkSession(topicID string, turn int, name string) (string, error)
	CompactSession(topicID string) error
	SummarizeFrom(topicID string, turn int) error
	SummarizeUpTo(topicID string, turn int) error
	ToolResult(topicID, toolID string) (ToolResultResponse, error)
}
