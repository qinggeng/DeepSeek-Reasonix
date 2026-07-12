package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"reasonix/desktop/gateway"
)

// --- TC-9: 列出 Topic——workspace 已注册且有 topic ---

func TestHandleListTopics_HasTopics(t *testing.T) {
	ctrl := &mockControl{
		topics: map[string][]TopicInfo{
			"E:/projects/foo": {
				{ID: "topic-dev", Title: "开发", Kind: "topic", WorkspaceRoot: "E:/projects/foo", SessionPath: "E:/projects/foo/.reasonix/sessions/dev.jsonl"},
				{ID: "topic-research", Title: "研究", Kind: "topic", WorkspaceRoot: "E:/projects/foo", SessionPath: "E:/projects/foo/.reasonix/sessions/research.jsonl"},
			},
		},
	}

	req := newEncodedRequest(http.MethodGet, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics", nil)
	w := httptest.NewRecorder()
	HandleListTopics(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result []TopicInfo
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 topics, got %d", len(result))
	}
	if result[0].ID != "topic-dev" {
		t.Errorf("expected first topic id topic-dev, got %s", result[0].ID)
	}
	if result[1].Title != "研究" {
		t.Errorf("expected second topic title 研究, got %s", result[1].Title)
	}
}

// --- TC-10: 列出 Topic——workspace 已注册但无 topic ---

func TestHandleListTopics_EmptyTopics(t *testing.T) {
	ctrl := &mockControl{
		topics: map[string][]TopicInfo{
			"E:/projects/foo": {},
		},
	}

	req := newEncodedRequest(http.MethodGet, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics", nil)
	w := httptest.NewRecorder()
	HandleListTopics(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result []TopicInfo
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty array, got %d items", len(result))
	}
}

// --- TC-10b: 列出 Topic——workspace 未注册 ---

func TestHandleListTopics_WorkspaceNotFound(t *testing.T) {
	ctrl := &mockControl{
		topics: map[string][]TopicInfo{
			"/project/foo": {},
		},
	}

	req := newEncodedRequest(http.MethodGet, "/api/workspaces/E%3A%2Fprojects%2Fbar/topics", nil)
	w := httptest.NewRecorder()
	HandleListTopics(ctrl)(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}

	var errResp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if errResp["error"] == "" {
		t.Error("expected error message")
	}
}

// --- TC-11: 激活 Topic——存在且可用 ---
// URL format: POST /api/workspaces/{path}/topics/{id}/activate

func TestHandleActivateTopic_Valid(t *testing.T) {
	ctrl := &mockControl{
		activateFn: func(id string) (TopicInfo, error) {
			return TopicInfo{ID: id, Title: "开发", Kind: "topic", WorkspaceRoot: "/project/foo"}, nil
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/test-ws/topics/topic-dev/activate", nil)
	w := httptest.NewRecorder()
	HandleActivateTopic(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result TopicInfo
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.ID != "topic-dev" {
		t.Errorf("expected topic-dev, got %s", result.ID)
	}
}

// --- TC-12: 激活 Topic——topic 不存在 ---

func TestHandleActivateTopic_NotFound(t *testing.T) {
	ctrl := &mockControl{
		activateFn: func(id string) (TopicInfo, error) {
			return TopicInfo{}, errTopicNotFound
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/test-ws/topics/nonexistent/activate", nil)
	w := httptest.NewRecorder()
	HandleActivateTopic(ctrl)(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}

	var errResp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if errResp["error"] == "" {
		t.Error("expected error message")
	}
}

// --- TC-13: 查询 Topic 状态——空闲 ---
// URL format: GET /api/workspaces/{path}/topics/{id}/status

func TestHandleTopicStatus_Idle(t *testing.T) {
	ctrl := &mockControl{
		statusFn: func(id string) (TopicStatus, error) {
			return TopicStatus{
				Running:          false,
				Model:            "deepseek-v4",
				Effort:           "medium",
				HasPendingPrompt: false,
			}, nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/topics/topic-dev/status", nil)
	w := httptest.NewRecorder()
	HandleTopicStatus(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result TopicStatus
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Running {
		t.Error("expected running=false")
	}
	if result.Model != "deepseek-v4" {
		t.Errorf("expected model deepseek-v4, got %s", result.Model)
	}
	if result.HasPendingPrompt {
		t.Error("expected hasPendingPrompt=false")
	}
	if result.CurrentStep != "" {
		t.Errorf("expected empty currentStep, got %s", result.CurrentStep)
	}
}

// --- TC-14: 查询 Topic 状态——正在运行 ---

func TestHandleTopicStatus_Running(t *testing.T) {
	ctrl := &mockControl{
		statusFn: func(id string) (TopicStatus, error) {
			return TopicStatus{
				Running:          true,
				Model:            "deepseek-v4",
				Effort:           "high",
				HasPendingPrompt: false,
				CurrentStep:      "正在分析文件结构...",
			}, nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/topics/topic-dev/status", nil)
	w := httptest.NewRecorder()
	HandleTopicStatus(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result TopicStatus
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !result.Running {
		t.Error("expected running=true")
	}
	if result.CurrentStep == "" {
		t.Error("expected currentStep to be non-empty")
	}
}

// --- TC-15: 查询 Topic 状态——topic 不存在 ---

func TestHandleTopicStatus_NotFound(t *testing.T) {
	ctrl := &mockControl{
		statusFn: func(id string) (TopicStatus, error) {
			return TopicStatus{}, errTopicNotFound
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/topics/nonexistent/status", nil)
	w := httptest.NewRecorder()
	HandleTopicStatus(ctrl)(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}

	var errResp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if errResp["error"] == "" {
		t.Error("expected error message")
	}
}

// --- Test handler registration wiring ---

func TestWorkspaceRoutesRegistration(t *testing.T) {
	ctrl := &mockControl{
		workspaces: []WorkspaceMeta{
			{Path: "/test", Name: "test", Current: true},
		},
		topics: map[string][]TopicInfo{
			"/test": {},
		},
	}

	g := gateway.New(7777)

	// Register all workspace routes
	RegisterWorkspaceRoutes(g, ctrl)
	RegisterTopicRoutes(g, ctrl)

	// Test workspace list
	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("GET /api/workspaces: expected 200, got %d", w.Code)
	}

	// Test workspace switch with empty body → 400
	req2 := httptest.NewRequest(http.MethodPost, "/api/workspaces", strings.NewReader(`{}`))
	w2 := httptest.NewRecorder()
	g.Handler().ServeHTTP(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("POST /api/workspaces (empty): expected 400, got %d", w2.Code)
	}

	// Test 404 on unknown
	req3 := httptest.NewRequest(http.MethodGet, "/api/workspaces/unknown/topics", nil)
	w3 := httptest.NewRecorder()
	g.Handler().ServeHTTP(w3, req3)
	if w3.Code != http.StatusNotFound {
		t.Errorf("GET /api/workspaces/unknown/topics: expected 404, got %d", w3.Code)
	}
}

// --- errSentinel ---

var errTopicNotFound = &topicNotFoundError{}

type topicNotFoundError struct{}

func (e *topicNotFoundError) Error() string { return "topic not found" }

func TestTopicNotFoundError(t *testing.T) {
	// verify the sentinel is recognized by handler error classification
	err := errTopicNotFound
	if !isTopicNotFound(err) {
		t.Error("expected isTopicNotFound to return true for errTopicNotFound")
	}
	if isTopicNotFound(fmt.Errorf("some other error")) {
		t.Error("expected isTopicNotFound to return false for other errors")
	}
}
