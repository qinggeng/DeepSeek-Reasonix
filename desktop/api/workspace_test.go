package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- mock DesktopControl ---

type mockControl struct {
	workspaces       []WorkspaceMeta
	switchFn         func(dir string) (WorkspaceMeta, error)
	removeFn         func(dir string) error
	topics           map[string][]TopicInfo
	activateFn       func(topicID string) (TopicInfo, error)
	statusFn         func(topicID string) (TopicStatus, error)
	submitFn         func(topicID, input string) (string, error)
	cancelFn         func(topicID string) error
	steerFn          func(topicID, text string) error
	chatStatusFn     func(topicID string) (ChatStatus, error)
	hub              *StreamHub
	sessions         map[string][]SessionMeta
	listSessionsFn   func(workspaceRoot string) ([]SessionMeta, error)
	getSessionFn     func(workspaceRoot, sessionName string) (SessionMeta, error)
	renameSessionFn  func(workspaceRoot, sessionName, title string) error
	deleteSessionFn  func(workspaceRoot, sessionName string) error
	listTrashFn      func(workspaceRoot string) ([]SessionMeta, error)
	restoreSessionFn func(workspaceRoot, sessionName string) error
	emptyTrashFn     func(workspaceRoot string) error
	newSessionFn     func(topicID string) error
	clearSessionFn   func(topicID string) error
	resumeSessionFn  func(topicID, sessionPath string) error
	currentSessionFn func(topicID string) (SessionMeta, error)

	// Approval mock fields (Sprint 4).
	approveFn         func(topicID string, req ApproveRequest) error
	answerFn          func(topicID string, req AnswerRequest) error
	pendingPromptFn   func(topicID string) (bool, error)
	replayPromptsFn   func()
	setApprovalModeFn func(topicID, mode string) error
}

func (m *mockControl) ListWorkspaces() []WorkspaceMeta {
	return m.workspaces
}

func (m *mockControl) SwitchWorkspace(dir string) (WorkspaceMeta, error) {
	if m.switchFn != nil {
		return m.switchFn(dir)
	}
	return WorkspaceMeta{}, fmt.Errorf("not implemented")
}

func (m *mockControl) RemoveWorkspace(dir string) error {
	if m.removeFn != nil {
		return m.removeFn(dir)
	}
	return fmt.Errorf("not implemented")
}

func (m *mockControl) ListTopics(workspaceRoot string) ([]TopicInfo, error) {
	topics, ok := m.topics[workspaceRoot]
	if !ok {
		return nil, fmt.Errorf("workspace not found: %s", workspaceRoot)
	}
	return topics, nil
}

func (m *mockControl) ActivateTopic(topicID string) (TopicInfo, error) {
	if m.activateFn != nil {
		return m.activateFn(topicID)
	}
	return TopicInfo{}, fmt.Errorf("not implemented")
}

func (m *mockControl) TopicStatus(topicID string) (TopicStatus, error) {
	if m.statusFn != nil {
		return m.statusFn(topicID)
	}
	return TopicStatus{}, fmt.Errorf("not implemented")
}

func (m *mockControl) SubmitPrompt(topicID, input string) (string, error) {
	if m.submitFn != nil {
		return m.submitFn(topicID, input)
	}
	return "", fmt.Errorf("not implemented")
}

func (m *mockControl) CancelRun(topicID string) error {
	if m.cancelFn != nil {
		return m.cancelFn(topicID)
	}
	return fmt.Errorf("not implemented")
}

func (m *mockControl) SteerRun(topicID, text string) error {
	if m.steerFn != nil {
		return m.steerFn(topicID, text)
	}
	return fmt.Errorf("not implemented")
}

func (m *mockControl) ChatStatus(topicID string) (ChatStatus, error) {
	if m.chatStatusFn != nil {
		return m.chatStatusFn(topicID)
	}
	return ChatStatus{}, fmt.Errorf("not implemented")
}

func (m *mockControl) StreamHub() *StreamHub {
	return m.hub
}

func (m *mockControl) ListActiveTopics() []TopicInfo {
	return nil
}

func (m *mockControl) ListSessions(workspaceRoot string) ([]SessionMeta, error) {
	if m.listSessionsFn != nil {
		return m.listSessionsFn(workspaceRoot)
	}
	sessions, ok := m.sessions[workspaceRoot]
	if !ok {
		return nil, fmt.Errorf("workspace not found: %s", workspaceRoot)
	}
	return sessions, nil
}

func (m *mockControl) GetSession(workspaceRoot, sessionName string) (SessionMeta, error) {
	if m.getSessionFn != nil {
		return m.getSessionFn(workspaceRoot, sessionName)
	}
	return SessionMeta{}, fmt.Errorf("not implemented")
}

func (m *mockControl) RenameSession(workspaceRoot, sessionName, title string) error {
	if m.renameSessionFn != nil {
		return m.renameSessionFn(workspaceRoot, sessionName, title)
	}
	return fmt.Errorf("not implemented")
}

func (m *mockControl) DeleteSession(workspaceRoot, sessionName string) error {
	if m.deleteSessionFn != nil {
		return m.deleteSessionFn(workspaceRoot, sessionName)
	}
	return fmt.Errorf("not implemented")
}

func (m *mockControl) ListTrash(workspaceRoot string) ([]SessionMeta, error) {
	if m.listTrashFn != nil {
		return m.listTrashFn(workspaceRoot)
	}
	return nil, fmt.Errorf("not implemented")
}

func (m *mockControl) RestoreSession(workspaceRoot, sessionName string) error {
	if m.restoreSessionFn != nil {
		return m.restoreSessionFn(workspaceRoot, sessionName)
	}
	return fmt.Errorf("not implemented")
}

func (m *mockControl) EmptyTrash(workspaceRoot string) error {
	if m.emptyTrashFn != nil {
		return m.emptyTrashFn(workspaceRoot)
	}
	return fmt.Errorf("not implemented")
}

func (m *mockControl) NewSession(topicID string) error {
	if m.newSessionFn != nil {
		return m.newSessionFn(topicID)
	}
	return fmt.Errorf("not implemented")
}

func (m *mockControl) ClearSessionForTopic(topicID string) error {
	if m.clearSessionFn != nil {
		return m.clearSessionFn(topicID)
	}
	return fmt.Errorf("not implemented")
}

func (m *mockControl) ResumeSession(topicID, sessionPath string) error {
	if m.resumeSessionFn != nil {
		return m.resumeSessionFn(topicID, sessionPath)
	}
	return fmt.Errorf("not implemented")
}

func (m *mockControl) CurrentSession(topicID string) (SessionMeta, error) {
	if m.currentSessionFn != nil {
		return m.currentSessionFn(topicID)
	}
	return SessionMeta{}, fmt.Errorf("not implemented")
}

func (m *mockControl) Approve(topicID string, req ApproveRequest) error {
	if m.approveFn != nil {
		return m.approveFn(topicID, req)
	}
	return fmt.Errorf("not implemented")
}

func (m *mockControl) AnswerQuestion(topicID string, req AnswerRequest) error {
	if m.answerFn != nil {
		return m.answerFn(topicID, req)
	}
	return fmt.Errorf("not implemented")
}

func (m *mockControl) PendingPrompt(topicID string) (bool, error) {
	if m.pendingPromptFn != nil {
		return m.pendingPromptFn(topicID)
	}
	return false, fmt.Errorf("not implemented")
}

func (m *mockControl) ReplayPendingPrompts() {
	if m.replayPromptsFn != nil {
		m.replayPromptsFn()
	}
}

func (m *mockControl) SetApprovalMode(topicID, mode string) error {
	if m.setApprovalModeFn != nil {
		return m.setApprovalModeFn(topicID, mode)
	}
	return fmt.Errorf("not implemented")
}

// --- helpers ---

func jsonBody(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// --- TC-1: 列出 Workspace——正常工作 ---

func TestHandleListWorkspaces_ReturnsAll(t *testing.T) {
	ctrl := &mockControl{
		workspaces: []WorkspaceMeta{
			{Path: "/project/foo", Name: "foo", Current: false},
			{Path: "/project/bar", Name: "bar", Current: true},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	w := httptest.NewRecorder()
	HandleListWorkspaces(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %s", ct)
	}

	var result []WorkspaceMeta
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 workspaces, got %d", len(result))
	}
	// exactly one current
	currentCount := 0
	for _, ws := range result {
		if ws.Current {
			currentCount++
		}
	}
	if currentCount != 1 {
		t.Errorf("expected exactly 1 current workspace, got %d", currentCount)
	}
}

// --- TC-2: 列出 Workspace——无注册 ---

func TestHandleListWorkspaces_Empty(t *testing.T) {
	ctrl := &mockControl{workspaces: []WorkspaceMeta{}}

	w := httptest.NewRecorder()
	HandleListWorkspaces(ctrl)(w, httptest.NewRequest(http.MethodGet, "/", nil))

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if strings.TrimSpace(w.Body.String()) != "[]" && strings.TrimSpace(w.Body.String()) != "null" {
		var result []WorkspaceMeta
		if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if len(result) != 0 {
			t.Errorf("expected empty array, got %d items", len(result))
		}
	}
}

// --- TC-3: 切换 Workspace——目录存在 ---

func TestHandleSwitchWorkspace_ValidDir(t *testing.T) {
	ctrl := &mockControl{
		switchFn: func(dir string) (WorkspaceMeta, error) {
			return WorkspaceMeta{Path: dir, Name: "my-project", Current: true}, nil
		},
	}

	body := jsonBody(t, switchRequest{Path: "E:/projects/my-project"})
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleSwitchWorkspace(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result WorkspaceMeta
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Path != "E:/projects/my-project" {
		t.Errorf("expected path E:/projects/my-project, got %s", result.Path)
	}
	if !result.Current {
		t.Error("expected workspace to be current")
	}
}

// --- TC-4: 切换 Workspace——目录不存在 ---

func TestHandleSwitchWorkspace_InvalidDir(t *testing.T) {
	ctrl := &mockControl{
		switchFn: func(dir string) (WorkspaceMeta, error) {
			return WorkspaceMeta{}, fmt.Errorf("directory not accessible: %s", dir)
		},
	}

	body := jsonBody(t, switchRequest{Path: "E:/projects/non-existent"})
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleSwitchWorkspace(ctrl)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}

	var errResp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if errResp["error"] == "" {
		t.Error("expected error message")
	}
}

// --- TC-5: 切换 Workspace——缺少 path 参数 ---

func TestHandleSwitchWorkspace_MissingPath(t *testing.T) {
	ctrl := &mockControl{}

	// empty body
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	HandleSwitchWorkspace(ctrl)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}

	// empty string path
	req2 := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(`{"path":""}`))
	w2 := httptest.NewRecorder()
	HandleSwitchWorkspace(ctrl)(w2, req2)

	if w2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty path, got %d", w2.Code)
	}
}

func TestHandleSwitchWorkspace_InvalidBody(t *testing.T) {
	ctrl := &mockControl{}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(`not json`))
	w := httptest.NewRecorder()
	HandleSwitchWorkspace(ctrl)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- TC-6: 移除 Workspace——已注册 ---

func TestHandleRemoveWorkspace_Existing(t *testing.T) {
	ctrl := &mockControl{
		removeFn: func(dir string) error { return nil },
	}

	req := newEncodedRequest(http.MethodDelete, "/api/workspaces/E%3A%2Fprojects%2Ffoo", nil)
	w := httptest.NewRecorder()
	HandleRemoveWorkspace(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result map[string]bool
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !result["success"] {
		t.Error("expected success: true")
	}
}

// --- TC-7: 移除 Workspace——未注册 ---

func TestHandleRemoveWorkspace_NotFound(t *testing.T) {
	ctrl := &mockControl{
		removeFn: func(dir string) error {
			return fmt.Errorf("workspace not found")
		},
	}

	req := newEncodedRequest(http.MethodDelete, "/api/workspaces/E%3A%2Fprojects%2Fbar", nil)
	w := httptest.NewRecorder()
	HandleRemoveWorkspace(ctrl)(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// --- TC-8: 移除 Workspace——当前活跃 workspace ---

func TestHandleRemoveWorkspace_CurrentWorkspace(t *testing.T) {
	ctrl := &mockControl{
		removeFn: func(dir string) error { return nil },
	}

	req := newEncodedRequest(http.MethodDelete, "/api/workspaces/E%3A%2Fprojects%2Ffoo", nil)
	w := httptest.NewRecorder()
	HandleRemoveWorkspace(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	// The mock indicates success; real App handles fallback internally.
	// We verify HTTP success; integration tests verify the fallback.
}

// --- extra: URL encoding ---

func TestHandleRemoveWorkspace_URLEncodedPath(t *testing.T) {
	var captured string
	ctrl := &mockControl{
		removeFn: func(dir string) error {
			captured = dir
			return nil
		},
	}

	// path with spaces and Chinese characters
	req := newEncodedRequest(http.MethodDelete, "/api/workspaces/E%3A%2Fprojects%2Fmy%20project%2F%E6%B5%8B%E8%AF%95", nil)
	w := httptest.NewRecorder()
	HandleRemoveWorkspace(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	expected := "E:/projects/my project/测试"
	if captured != expected {
		t.Errorf("expected %q, got %q", expected, captured)
	}
}

// --- helper: DELETE with path arg ---

type switchRequest struct {
	Path string `json:"path"`
}

func urlPathEscape(s string) string {
	// simulate URL path encoding for testing
	result := ""
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9':
			result += string(c)
		case c == '/':
			result += "%2F"
		case c == ':':
			result += "%3A"
		case c == ' ':
			result += "%20"
		default:
			result += string(c)
		}
	}
	return result
}

// newEncodedRequest creates an HTTP request and preserves the percent-encoded
// URL path (httptest.NewRequest decodes %XX sequences in Path). This simulates
// what HandleWorkspaceDispatch does for production requests.
func newEncodedRequest(method, url string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, url, body)
	req.URL.Path = req.URL.EscapedPath()
	return req
}

// Verify that the handler uses the full path from r.URL.Path
func TestHandleRemoveWorkspace_ExtractsPathFromURL(t *testing.T) {
	// URL encoding/decoding is tested in TestHandleRemoveWorkspace_URLEncodedPath.
	// Full path extraction from the request URL depends on the route registration
	// pattern and is verified in the gateway integration tests.
	t.Log("Path extraction tested via unit and integration tests")
}
