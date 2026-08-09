package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"reasonix/desktop/api"
	"reasonix/desktop/gateway"
	"reasonix/internal/config"
	"reasonix/internal/event"

	"github.com/google/uuid"
)

// apiPort returns the configured desktop API port.
// 0 means the API is disabled; default is 7777.
func apiPort(cfg *config.Config) int {
	if cfg == nil {
		return 7777
	}
	port := cfg.Desktop.APIPort
	if port == 0 {
		return 7777
	}
	return port
}

// startAPIServer creates and starts the desktop HTTP API gateway.
func startAPIServer(app *App) *gateway.Gateway {
	cfg, err := config.Load()
	if err != nil {
		slog.Warn("api: cannot load config, using default port", "err", err)
	}

	port := apiPort(cfg)
	slog.Info("api: starting desktop API server", "port", port)

	g := gateway.New(port)
	ctrl := &appControl{app: app, hub: api.NewStreamHub(256)}

	api.RegisterWorkspaceRoutes(g, ctrl)
	api.RegisterTopicRoutes(g, ctrl)

	// Debug: dump session/tab/tree state for troubleshooting.
	g.Handle("GET", "/api/debug", handleDebug(ctrl))

	if err := g.Start(); err != nil {
		slog.Error("api: failed to start server", "err", err)
		return nil
	}
	return g
}

// stopAPIServer gracefully shuts down the API server.
func stopAPIServer(g *gateway.Gateway) {
	if g == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := g.Stop(ctx); err != nil {
		slog.Error("api: error shutting down server", "err", err)
	}
}

// appControl wraps *App to satisfy api.DesktopControl.
type appControl struct {
	app *App
	hub *api.StreamHub
}

func (c *appControl) ListWorkspaces() []api.WorkspaceMeta {
	metas := c.app.ListWorkspaces()
	result := make([]api.WorkspaceMeta, len(metas))
	for i, m := range metas {
		result[i] = api.WorkspaceMeta{
			Path:    m.Path,
			Name:    m.Name,
			Current: m.Current,
		}
	}
	return result
}

func (c *appControl) SwitchWorkspace(dir string) (api.WorkspaceMeta, error) {
	path, err := c.app.SwitchWorkspace(dir)
	if err != nil {
		return api.WorkspaceMeta{}, err
	}
	return api.WorkspaceMeta{
		Path:    path,
		Name:    filepath.Base(path),
		Current: true,
	}, nil
}

func (c *appControl) RemoveWorkspace(dir string) error {
	return c.app.RemoveWorkspace(dir)
}

func (c *appControl) ListTopics(workspaceRoot string) ([]api.TopicInfo, error) {
	tree := c.app.ListProjectTree()

	var result []api.TopicInfo
	var collect func(nodes []ProjectNode)
	collect = func(nodes []ProjectNode) {
		for _, n := range nodes {
			if n.Kind == "topic" && n.Root == workspaceRoot {
				result = append(result, api.TopicInfo{
					ID:            n.TopicID,
					Title:         n.Label,
					Kind:          n.Kind,
					WorkspaceRoot: n.Root,
					SessionPath:   n.SessionPath,
					Pinned:        n.Pinned,
				})
			}
			if n.Children != nil {
				collect(n.Children)
			}
		}
	}
	collect(tree)

	if result == nil {
		result = []api.TopicInfo{}
	}
	return result, nil
}

func (c *appControl) ActivateTopic(topicID string) (api.TopicInfo, error) {
	// Find the topic in the project tree to get its metadata.
	tree := c.app.ListProjectTree()
	var found *ProjectNode
	var search func(nodes []ProjectNode) bool
	search = func(nodes []ProjectNode) bool {
		for _, n := range nodes {
			if (n.Kind == "topic" || n.Kind == "global_topic") && n.TopicID == topicID {
				found = &n
				return true
			}
			if n.Children != nil && search(n.Children) {
				return true
			}
		}
		return false
	}
	search(tree)

	if found == nil {
		return api.TopicInfo{}, fmt.Errorf("topic not found: %s", topicID)
	}

	scope := "project"
	root := found.Root
	if found.Kind == "global_topic" {
		scope = "global"
		root = ""
	}

	_, err := c.app.ActivateTopic(scope, root, topicID, found.SessionPath)
	if err != nil {
		return api.TopicInfo{}, fmt.Errorf("failed to activate topic: %w", err)
	}

	return api.TopicInfo{
		ID:            found.TopicID,
		Title:         found.Label,
		Kind:          found.Kind,
		WorkspaceRoot: found.Root,
		SessionPath:   found.SessionPath,
		Pinned:        found.Pinned,
	}, nil
}

func (c *appControl) TopicStatus(topicID string) (api.TopicStatus, error) {
	// Search tabs for the topic
	tabs := c.app.ListTabs()
	for _, tab := range tabs {
		if tab.TopicID != topicID {
			continue
		}
		return api.TopicStatus{
			Running:          tab.Running,
			Model:            tab.Label,
			HasPendingPrompt: tab.PendingPrompt,
		}, nil
	}

	return api.TopicStatus{}, fmt.Errorf("topic not found: %s", topicID)
}

// findTabIDByTopicID searches the tab list for a tab with the given topic ID.
func (c *appControl) findTabIDByTopicID(topicID string) string {
	tabs := c.app.ListTabs()
	for _, tab := range tabs {
		if tab.TopicID == topicID {
			return tab.ID
		}
	}
	return ""
}

// SubmitPrompt submits a prompt to the agent for the given topic.
func (c *appControl) SubmitPrompt(topicID, input string) (string, error) {
	tabID := c.findTabIDByTopicID(topicID)
	if tabID == "" {
		return "", fmt.Errorf("topic not found: %s", topicID)
	}

	streamID := uuid.New().String()
	ch := c.hub.Register(streamID)

	sink := event.FuncSink(func(e event.Event) {
		select {
		case ch <- e:
		default:
			// channel full; drop event to avoid blocking the agent
		}
	})

	ok := c.app.submitPromptToTabWithSink(tabID, input, sink)
	if !ok {
		c.hub.Unregister(streamID)
		return "", fmt.Errorf("topic not ready: %s", topicID)
	}
	// Sprint 11 HTTP interface improvement: a fresh SSE client must see any
	// approval/ask prompt that is already registered (e.g. a tool approval that
	// landed before this stream attached, or was dropped on a full hub buffer).
	// The desktop frontend gets this replay on attach (tabs.go); the HTTP API
	// path now does the same so pending prompts are never invisible to API
	// clients — without it the run blocks on a prompt the client never sees.
	// Scoped to this tab only: a multi-tab session must not leak another tab's
	// approval card into this client's stream.
	c.app.ReplayPendingPromptsToForTab(tabID, sink)

	return streamID, nil
}

// CancelRun cancels the current agent run for the given topic.
func (c *appControl) CancelRun(topicID string) error {
	tabID := c.findTabIDByTopicID(topicID)
	if tabID == "" {
		return fmt.Errorf("topic not found: %s", topicID)
	}
	c.app.CancelTab(tabID)
	return nil
}

// SteerRun sends a steer/intervention to a running agent for the given topic.
func (c *appControl) SteerRun(topicID, text string) error {
	tabID := c.findTabIDByTopicID(topicID)
	if tabID == "" {
		return fmt.Errorf("topic not found: %s", topicID)
	}
	return c.app.SteerForTab(tabID, text)
}

// ChatStatus returns the enhanced runtime status of a topic's agent.
func (c *appControl) ChatStatus(topicID string) (api.ChatStatus, error) {
	tabs := c.app.ListTabs()
	for _, tab := range tabs {
		if tab.TopicID != topicID {
			continue
		}
		return api.ChatStatus{
			Running:          tab.Running,
			Model:            tab.Label,
			Effort:           tab.Mode,
			HasPendingPrompt: tab.PendingPrompt,
			CancelRequested:  tab.CancelRequested,
			Cancellable:      tab.Cancellable,
			BackgroundJobs:   tab.BackgroundJobs,
		}, nil
	}
	return api.ChatStatus{}, fmt.Errorf("topic not found: %s", topicID)
}

// StreamHub returns the event-stream hub for SSE connections.
func (c *appControl) StreamHub() *api.StreamHub {
	return c.hub
}

// ListActiveTopics returns all currently active topics (tabs).
func (c *appControl) ListActiveTopics() []api.TopicInfo {
	tabs := c.app.ListTabs()
	result := make([]api.TopicInfo, 0, len(tabs))
	for _, tab := range tabs {
		result = append(result, api.TopicInfo{
			ID:            tab.TopicID,
			Title:         tab.Label,
			Kind:          "topic",
			WorkspaceRoot: tab.WorkspaceRoot,
			SessionPath:   tab.SessionPath,
			Pinned:        false,
		})
	}
	return result
}

// --- Session management ---

func (c *appControl) ListSessions(workspaceRoot string) ([]api.SessionMeta, error) {
	metas := c.app.ListSessions()
	result := make([]api.SessionMeta, 0, len(metas))
	for _, m := range metas {
		result = append(result, sessionMetaToAPI(m))
	}
	return result, nil
}

func (c *appControl) GetSession(workspaceRoot, sessionName string) (api.SessionMeta, error) {
	metas := c.app.ListSessions()
	for _, m := range metas {
		if m.Path == sessionName || filepath.Base(m.Path) == sessionName || filepath.Base(m.Path) == filepath.Base(sessionName) {
			return sessionMetaToAPI(m), nil
		}
	}
	return api.SessionMeta{}, fmt.Errorf("session not found: %s", sessionName)
}

func (c *appControl) RenameSession(workspaceRoot, sessionName, title string) error {
	metas := c.app.ListSessions()
	for _, m := range metas {
		if m.Path == sessionName || filepath.Base(m.Path) == sessionName || filepath.Base(m.Path) == filepath.Base(sessionName) {
			dir := filepath.Dir(m.Path)
			setSessionTitle(dir, m.Path, title)
			return nil
		}
	}
	return fmt.Errorf("session not found: %s", sessionName)
}

func (c *appControl) DeleteSession(workspaceRoot, sessionName string) error {
	metas := c.app.ListSessions()
	for _, m := range metas {
		if m.Path == sessionName || filepath.Base(m.Path) == sessionName || filepath.Base(m.Path) == filepath.Base(sessionName) {
			dir := filepath.Dir(m.Path)
			return deleteSessionFile(dir, m.Path)
		}
	}
	return fmt.Errorf("session not found: %s", sessionName)
}

func (c *appControl) ListTrash(workspaceRoot string) ([]api.SessionMeta, error) {
	dir := c.app.activeSessionDir()
	trashDir := sessionTrashPath(dir)
	entries, err := os.ReadDir(trashDir)
	if err != nil {
		if os.IsNotExist(err) {
			return []api.SessionMeta{}, nil
		}
		return nil, err
	}
	var result []api.SessionMeta
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Each trash item is in a subdirectory: trash/<key>/<key>.jsonl
		itemDir := filepath.Join(trashDir, e.Name())
		itemFiles, err := os.ReadDir(itemDir)
		if err != nil {
			continue
		}
		for _, f := range itemFiles {
			if !f.IsDir() && strings.HasSuffix(f.Name(), ".jsonl") {
				result = append(result, api.SessionMeta{
					Path: filepath.Join(itemDir, f.Name()),
				})
				break
			}
		}
	}
	return result, nil
}

func (c *appControl) RestoreSession(workspaceRoot, sessionName string) error {
	// Construct the original session path and delegate to App.RestoreSession
	dir := c.app.activeSessionDir()
	sessionPath := filepath.Join(dir, sessionName)
	return c.app.RestoreSession(sessionPath)
}

func (c *appControl) EmptyTrash(workspaceRoot string) error {
	dir := c.app.activeSessionDir()
	trashDir := sessionTrashPath(dir)
	if err := os.RemoveAll(trashDir); err != nil {
		return err
	}
	return nil
}

func (c *appControl) NewSession(topicID string) error {
	tabID := c.findTabIDByTopicID(topicID)
	if tabID == "" {
		return fmt.Errorf("topic not found: %s", topicID)
	}
	return c.app.NewSessionForTab(tabID)
}

func (c *appControl) ClearSessionForTopic(topicID string) error {
	tabID := c.findTabIDByTopicID(topicID)
	if tabID == "" {
		return fmt.Errorf("topic not found: %s", topicID)
	}
	return c.app.ClearSessionForTab(tabID)
}

func (c *appControl) ResumeSession(topicID, sessionPath string) error {
	tabID := c.findTabIDByTopicID(topicID)
	if tabID == "" {
		return fmt.Errorf("topic not found: %s", topicID)
	}
	_, err := c.app.ResumeSessionForTab(tabID, sessionPath)
	return err
}

func (c *appControl) CurrentSession(topicID string) (api.SessionMeta, error) {
	tabs := c.app.ListTabs()
	for _, tab := range tabs {
		if tab.TopicID == topicID {
			return api.SessionMeta{
				Path:    tab.SessionPath,
				Current: true,
				Open:    true,
			}, nil
		}
	}
	return api.SessionMeta{}, fmt.Errorf("topic not found: %s", topicID)
}

// sessionMetaToAPI converts an App-level SessionMeta to an API-level SessionMeta.
func sessionMetaToAPI(m SessionMeta) api.SessionMeta {
	return api.SessionMeta{
		Path:           m.Path,
		Preview:        m.Preview,
		Title:          m.Title,
		Turns:          m.Turns,
		CreatedAt:      m.CreatedAt,
		LastActivityAt: m.LastActivityAt,
		DeletedAt:      m.DeletedAt,
		Current:        m.Current,
		Open:           m.Open,
		Scope:          m.Scope,
		WorkspaceRoot:  m.WorkspaceRoot,
		TopicID:        m.TopicID,
		TopicTitle:     m.TopicTitle,
		Kind:           m.Kind,
	}
}

// --- Approval methods (Sprint 4) ---

func (c *appControl) Approve(topicID string, req api.ApproveRequest) error {
	tabID := c.findTabIDByTopicID(topicID)
	if tabID == "" {
		return fmt.Errorf("topic not found: %s", topicID)
	}
	return c.app.ApproveTabWithOpinion(tabID, req.ID, req.Allow, req.Session, req.Persist, req.Opinion)
}

func (c *appControl) AnswerQuestion(topicID string, req api.AnswerRequest) error {
	tabID := c.findTabIDByTopicID(topicID)
	if tabID == "" {
		return fmt.Errorf("topic not found: %s", topicID)
	}
	answers := make([]QuestionAnswer, len(req.Answers))
	for i, a := range req.Answers {
		answers[i] = QuestionAnswer{QuestionID: a.QuestionID, Selected: a.Selected}
	}
	c.app.AnswerQuestionForTab(tabID, req.ID, answers)
	return nil
}

func (c *appControl) PendingPrompt(topicID string) (bool, error) {
	tabs := c.app.ListTabs()
	for _, tab := range tabs {
		if tab.TopicID == topicID {
			return tab.PendingPrompt, nil
		}
	}
	return false, fmt.Errorf("topic not found: %s", topicID)
}

func (c *appControl) ReplayPendingPrompts() {
	c.app.ReplayPendingPrompts()
}

func (c *appControl) SetApprovalMode(topicID, mode string) error {
	tabID := c.findTabIDByTopicID(topicID)
	if tabID == "" {
		return fmt.Errorf("topic not found: %s", topicID)
	}
	c.app.SetToolApprovalModeForTab(tabID, mode)
	return nil
}

// --- History & Traceback methods (Sprint 5) ---

func (c *appControl) History(topicID string, beforeTurn, limit int) (api.HistoryResponse, error) {
	tabID := c.findTabIDByTopicID(topicID)
	if tabID == "" {
		return api.HistoryResponse{}, fmt.Errorf("topic not found: %s", topicID)
	}
	page := c.app.HistoryPageForTab(tabID, beforeTurn, limit)
	messages := make([]api.HistoryMessage, len(page.Messages))
	for i, m := range page.Messages {
		messages[i] = historyMessageToAPI(m)
	}
	return api.HistoryResponse{
		Messages:   messages,
		TotalTurns: page.TotalTurns,
		HasMore:    page.HasOlder,
	}, nil
}

func (c *appControl) Checkpoints(topicID string) ([]api.CheckpointMeta, error) {
	tabID := c.findTabIDByTopicID(topicID)
	if tabID == "" {
		return nil, fmt.Errorf("topic not found: %s", topicID)
	}
	ckpts := c.app.CheckpointsForTab(tabID)
	result := make([]api.CheckpointMeta, len(ckpts))
	for i, cp := range ckpts {
		result[i] = api.CheckpointMeta{
			Turn:   cp.Turn,
			Time:   cp.Time,
			Prompt: cp.Prompt,
			Paths:  cp.Files,
		}
	}
	return result, nil
}

func (c *appControl) Branches(topicID string) ([]api.BranchInfo, error) {
	tabID := c.findTabIDByTopicID(topicID)
	if tabID == "" {
		return nil, fmt.Errorf("topic not found: %s", topicID)
	}
	_, ctrl := c.app.tabAndCtrlByID(tabID)
	if ctrl == nil {
		return nil, fmt.Errorf("topic not ready: %s", topicID)
	}
	branches, err := ctrl.Branches()
	if err != nil {
		return nil, err
	}
	result := make([]api.BranchInfo, len(branches))
	for i, b := range branches {
		createdAt := b.CreatedAt.UnixMilli()
		updatedAt := b.UpdatedAt.UnixMilli()
		result[i] = api.BranchInfo{
			ID:        b.ID,
			Name:      b.Name,
			ParentID:  b.ParentID,
			ForkTurn:  b.ForkTurn,
			Turns:     b.Turns,
			Preview:   b.Preview,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		}
	}
	return result, nil
}

func (c *appControl) Rewind(topicID string, turn int, scope string) error {
	tabID := c.findTabIDByTopicID(topicID)
	if tabID == "" {
		return fmt.Errorf("topic not found: %s", topicID)
	}
	return c.app.RewindForTab(tabID, turn, scope)
}

func (c *appControl) ForkSession(topicID string, turn int, name string) (string, error) {
	tabID := c.findTabIDByTopicID(topicID)
	if tabID == "" {
		return "", fmt.Errorf("topic not found: %s", topicID)
	}
	meta, err := c.app.ForkForTab(tabID, turn)
	if err != nil {
		return "", err
	}
	return meta.SessionPath, nil
}

func (c *appControl) CompactSession(topicID string) error {
	tabID := c.findTabIDByTopicID(topicID)
	if tabID == "" {
		return fmt.Errorf("topic not found: %s", topicID)
	}
	return c.app.CompactForTab(tabID)
}

func (c *appControl) SummarizeFrom(topicID string, turn int) error {
	tabID := c.findTabIDByTopicID(topicID)
	if tabID == "" {
		return fmt.Errorf("topic not found: %s", topicID)
	}
	return c.app.SummarizeFromForTab(tabID, turn)
}

func (c *appControl) SummarizeUpTo(topicID string, turn int) error {
	tabID := c.findTabIDByTopicID(topicID)
	if tabID == "" {
		return fmt.Errorf("topic not found: %s", topicID)
	}
	return c.app.SummarizeUpToForTab(tabID, turn)
}

func (c *appControl) ToolResult(topicID, toolID string) (api.ToolResultResponse, error) {
	tabID := c.findTabIDByTopicID(topicID)
	if tabID == "" {
		return api.ToolResultResponse{}, fmt.Errorf("topic not found: %s", topicID)
	}
	data := c.app.ToolResultForTab(tabID, toolID)
	if data == nil {
		return api.ToolResultResponse{}, fmt.Errorf("tool not found: %s", toolID)
	}
	return api.ToolResultResponse{
		ToolID: toolID,
		Args:   data.Args,
		Output: data.Output,
	}, nil
}

// historyMessageToAPI converts an App-level HistoryMessage to an API-level HistoryMessage.
func historyMessageToAPI(m HistoryMessage) api.HistoryMessage {
	toolCalls := make([]api.HistoryToolCall, len(m.ToolCalls))
	for i, tc := range m.ToolCalls {
		toolCalls[i] = api.HistoryToolCall{
			ID:        tc.ID,
			Name:      tc.Name,
			Arguments: tc.Arguments,
		}
	}
	return api.HistoryMessage{
		Role:           m.Role,
		Content:        m.Content,
		Reasoning:      m.Reasoning,
		ToolCalls:      toolCalls,
		ToolCallID:     m.ToolCallID,
		ToolName:       m.ToolName,
		Pending:        m.Pending,
		CheckpointTurn: m.CheckpointTurn,
		Messages:       m.Messages,
		Summary:        m.Summary,
		WorkDurationMs: m.WorkDurationMs,
	}
}

// handleDebug returns a handler that dumps diagnostic state for troubleshooting.
func handleDebug(ctrl api.DesktopControl) gateway.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// We need the concrete appControl to access App methods not in DesktopControl.
		ac, ok := ctrl.(*appControl)
		if !ok {
			gateway.WriteError(w, http.StatusInternalServerError, "debug not available")
			return
		}
		app := ac.app

		host, _ := os.Hostname()

		type debugInfo struct {
			Host       string              `json:"host"`
			Platform   string              `json:"platform"`
			Workspaces []api.WorkspaceMeta `json:"workspaces"`
			Sessions   []SessionMeta       `json:"sessions"`
			Tabs       []struct {
				ID      string `json:"id"`
				TopicID string `json:"topicId"`
				Label   string `json:"label"`
				Running bool   `json:"running"`
			} `json:"tabs"`
		}

		info := debugInfo{
			Host:       host,
			Platform:   runtime.GOOS + "/" + runtime.GOARCH,
			Workspaces: ctrl.ListWorkspaces(),
			Sessions:   app.ListSessions(),
		}
		for _, tab := range app.ListTabs() {
			info.Tabs = append(info.Tabs, struct {
				ID      string `json:"id"`
				TopicID string `json:"topicId"`
				Label   string `json:"label"`
				Running bool   `json:"running"`
			}{
				ID:      tab.ID,
				TopicID: tab.TopicID,
				Label:   tab.Label,
				Running: tab.Running,
			})
		}

		gateway.WriteJSON(w, http.StatusOK, info)
	}
}
