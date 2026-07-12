package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"reasonix/desktop/gateway"
)

// --- TC-S1-1: 列出 Session——正常有数据 ---

func TestHandleListSessions_HasSessions(t *testing.T) {
	ctrl := &mockControl{
		sessions: map[string][]SessionMeta{
			"E:/projects/foo": {
				{Path: "session-1.jsonl", Preview: "帮我写一个函数", Turns: 3, Current: false},
				{Path: "session-2.jsonl", Preview: "分析项目结构", Turns: 8, Current: true},
				{Path: "session-3.jsonl", Preview: "调试网络问题", Turns: 5, Current: false},
			},
		},
	}

	req := newEncodedRequest(http.MethodGet, "/api/workspaces/E%3A%2Fprojects%2Ffoo/sessions", nil)
	w := httptest.NewRecorder()
	HandleListSessions(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result []SessionMeta
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(result))
	}
	if result[0].Path != "session-1.jsonl" {
		t.Errorf("expected first session path session-1.jsonl, got %s", result[0].Path)
	}
}

// --- TC-S1-2: 列出 Session——空 workspace ---

func TestHandleListSessions_EmptyWorkspace(t *testing.T) {
	ctrl := &mockControl{
		sessions: map[string][]SessionMeta{
			"E:/projects/foo": {},
		},
	}

	req := newEncodedRequest(http.MethodGet, "/api/workspaces/E%3A%2Fprojects%2Ffoo/sessions", nil)
	w := httptest.NewRecorder()
	HandleListSessions(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result []SessionMeta
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty array, got %d items", len(result))
	}
}

// --- TC-S1-3: 列出 Session——workspace 不存在 ---

func TestHandleListSessions_WorkspaceNotFound(t *testing.T) {
	ctrl := &mockControl{
		sessions: map[string][]SessionMeta{
			"E:/projects/foo": {},
		},
	}

	req := newEncodedRequest(http.MethodGet, "/api/workspaces/E%3A%2Fprojects%2Fnonexistent/sessions", nil)
	w := httptest.NewRecorder()
	HandleListSessions(ctrl)(w, req)

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

// --- TC-S1-4: 列出 Session——标记当前活跃 session ---

func TestHandleListSessions_MarksCurrent(t *testing.T) {
	ctrl := &mockControl{
		sessions: map[string][]SessionMeta{
			"E:/projects/foo": {
				{Path: "old.jsonl", Preview: "旧会话", Turns: 2, Current: false},
				{Path: "active.jsonl", Preview: "当前会话", Turns: 10, Current: true},
				{Path: "archived.jsonl", Preview: "归档", Turns: 7, Current: false},
			},
		},
	}

	req := newEncodedRequest(http.MethodGet, "/api/workspaces/E%3A%2Fprojects%2Ffoo/sessions", nil)
	w := httptest.NewRecorder()
	HandleListSessions(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result []SessionMeta
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 sessions, got %d", len(result))
	}
	for _, s := range result {
		if s.Path == "active.jsonl" && !s.Current {
			t.Errorf("expected active.jsonl to be current")
		}
		if s.Path != "active.jsonl" && s.Current {
			t.Errorf("expected %s to not be current", s.Path)
		}
	}
}

// --- TC-S1-5: 列出 Session——URL 编码路径 ---

func TestHandleListSessions_EncodedPath(t *testing.T) {
	var capturedRoot string
	ctrl := &mockControl{
		listSessionsFn: func(workspaceRoot string) ([]SessionMeta, error) {
			capturedRoot = workspaceRoot
			return []SessionMeta{}, nil
		},
	}

	// Path with spaces and Chinese characters
	req := newEncodedRequest(http.MethodGet, "/api/workspaces/E%3A%2Fprojects%2Fmy%20project%2F%E6%B5%8B%E8%AF%95/sessions", nil)
	w := httptest.NewRecorder()
	HandleListSessions(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	expected := "E:/projects/my project/测试"
	if capturedRoot != expected {
		t.Errorf("expected workspaceRoot %q, got %q", expected, capturedRoot)
	}
}

// --- Error: handler returns nil sessions ---

func TestHandleListSessions_NilResult(t *testing.T) {
	ctrl := &mockControl{
		listSessionsFn: func(workspaceRoot string) ([]SessionMeta, error) {
			return nil, nil
		},
	}

	req := newEncodedRequest(http.MethodGet, "/api/workspaces/E%3A%2Fprojects%2Ffoo/sessions", nil)
	w := httptest.NewRecorder()
	HandleListSessions(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	// Should return [] rather than null
	var result []SessionMeta
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result == nil {
		t.Error("expected non-nil empty array, got null")
	}
	if len(result) != 0 {
		t.Errorf("expected 0 sessions, got %d", len(result))
	}
}

// --- Test via dispatch routing ---

func TestListSessionsRouteRegistration(t *testing.T) {
	ctrl := &mockControl{
		sessions: map[string][]SessionMeta{
			"test-ws": {
				{Path: "s1.jsonl", Preview: "hello", Turns: 1, Current: true},
			},
		},
	}

	g := gateway.New(7777)
	RegisterWorkspaceRoutes(g, ctrl)
	RegisterTopicRoutes(g, ctrl)

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/sessions", nil)
	w := httptest.NewRecorder()
	g.Handler().ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d; body: %s", w.Code, w.Body.String())
	}

	var result []SessionMeta
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Errorf("expected 1 session, got %d", len(result))
	}
}

func TestHandleListSessions_WorkspacePathRequired(t *testing.T) {
	ctrl := &mockControl{}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces//sessions", nil)
	w := httptest.NewRecorder()
	HandleListSessions(ctrl)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- TC-S2-1: 获取 Session 详情——存在 ---

func TestHandleGetSession_Exists(t *testing.T) {
	ctrl := &mockControl{
		getSessionFn: func(root, name string) (SessionMeta, error) {
			if root == "test-ws" && name == "2026-07-12_abc.jsonl" {
				return SessionMeta{
					Path: "2026-07-12_abc.jsonl", Preview: "项目分析", Title: "项目分析",
					Turns: 3, Current: false,
				}, nil
			}
			return SessionMeta{}, fmt.Errorf("not found")
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/sessions/2026-07-12_abc.jsonl", nil)
	w := httptest.NewRecorder()
	HandleGetSession(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result SessionMeta
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Path != "2026-07-12_abc.jsonl" {
		t.Errorf("expected path 2026-07-12_abc.jsonl, got %s", result.Path)
	}
	if result.Preview != "项目分析" {
		t.Errorf("expected preview 项目分析, got %s", result.Preview)
	}
}

// --- TC-S2-2: 获取 Session 详情——不存在 ---

func TestHandleGetSession_NotFound(t *testing.T) {
	ctrl := &mockControl{
		getSessionFn: func(root, name string) (SessionMeta, error) {
			return SessionMeta{}, fmt.Errorf("session not found: %s", name)
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/sessions/nonexistent.jsonl", nil)
	w := httptest.NewRecorder()
	HandleGetSession(ctrl)(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// --- TC-S2-3: 获取 Session——名称含特殊字符 ---

func TestHandleGetSession_EncodedName(t *testing.T) {
	var capturedName string
	ctrl := &mockControl{
		getSessionFn: func(root, name string) (SessionMeta, error) {
			capturedName = name
			return SessionMeta{Path: name, Preview: "test"}, nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/sessions/session%231%3Ftest.jsonl", nil)
	w := httptest.NewRecorder()
	HandleGetSession(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	expected := "session#1?test.jsonl"
	if capturedName != expected {
		t.Errorf("expected name %q, got %q", expected, capturedName)
	}
}

// --- TC-S2-4: 重命名 Session——成功 ---

func TestHandleRenameSession_Success(t *testing.T) {
	var capturedRoot, capturedName, capturedTitle string
	ctrl := &mockControl{
		renameSessionFn: func(root, name, title string) error {
			capturedRoot = root
			capturedName = name
			capturedTitle = title
			return nil
		},
	}

	body := jsonBody(t, RenameSessionRequest{Title: "新标题"})
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/test-ws/sessions/2026-07-12_abc.jsonl/rename", strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleRenameSession(ctrl)(w, req)

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
	if capturedRoot != "test-ws" {
		t.Errorf("expected root test-ws, got %s", capturedRoot)
	}
	if capturedName != "2026-07-12_abc.jsonl" {
		t.Errorf("expected name 2026-07-12_abc.jsonl, got %s", capturedName)
	}
	if capturedTitle != "新标题" {
		t.Errorf("expected title 新标题, got %s", capturedTitle)
	}
}

// --- TC-S2-5: 重命名 Session——空标题清除自定义名 ---

func TestHandleRenameSession_EmptyTitle(t *testing.T) {
	var capturedTitle string
	ctrl := &mockControl{
		renameSessionFn: func(root, name, title string) error {
			capturedTitle = title
			return nil
		},
	}

	body := jsonBody(t, RenameSessionRequest{Title: ""})
	req := httptest.NewRequest(http.MethodPut, "/api/workspaces/test-ws/sessions/2026-07-12_abc.jsonl/rename", strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleRenameSession(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if capturedTitle != "" {
		t.Errorf("expected empty title, got %s", capturedTitle)
	}
}

// --- Route registration tests for session details ---

func TestSessionRoutesRegistration(t *testing.T) {
	ctrl := &mockControl{
		getSessionFn: func(root, name string) (SessionMeta, error) {
			return SessionMeta{Path: name, Preview: "test"}, nil
		},
		renameSessionFn: func(root, name, title string) error {
			return nil
		},
	}

	g := gateway.New(7777)
	RegisterWorkspaceRoutes(g, ctrl)
	RegisterTopicRoutes(g, ctrl)

	// Test GET session detail via routing
	req1 := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/sessions/s1.jsonl", nil)
	w1 := httptest.NewRecorder()
	g.Handler().ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Errorf("GET session: expected 200, got %d; body: %s", w1.Code, w1.Body.String())
	}

	// Test PUT rename via routing
	req2 := httptest.NewRequest(http.MethodPut, "/api/workspaces/test-ws/sessions/s1.jsonl/rename", strings.NewReader(`{"title":"new"}`))
	w2 := httptest.NewRecorder()
	g.Handler().ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("PUT rename: expected 200, got %d; body: %s", w2.Code, w2.Body.String())
	}
}

// --- TC-S3-1: 删除 Session——移入垃圾箱 ---

func TestHandleDeleteSession_Success(t *testing.T) {
	var capturedRoot, capturedName string
	ctrl := &mockControl{
		deleteSessionFn: func(root, name string) error {
			capturedRoot = root
			capturedName = name
			return nil
		},
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/test-ws/sessions/session-to-delete.jsonl", nil)
	w := httptest.NewRecorder()
	HandleDeleteSession(ctrl)(w, req)

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
	if capturedRoot != "test-ws" || capturedName != "session-to-delete.jsonl" {
		t.Errorf("expected test-ws/session-to-delete.jsonl, got %s/%s", capturedRoot, capturedName)
	}
}

// --- TC-S3-2: 删除 Session——不存在 ---

func TestHandleDeleteSession_NotFound(t *testing.T) {
	ctrl := &mockControl{
		deleteSessionFn: func(root, name string) error {
			return fmt.Errorf("session not found: %s", name)
		},
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/test-ws/sessions/nonexistent.jsonl", nil)
	w := httptest.NewRecorder()
	HandleDeleteSession(ctrl)(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// --- TC-S3-3: 列出垃圾箱——有内容 ---

func TestHandleListTrash_HasItems(t *testing.T) {
	ctrl := &mockControl{
		listTrashFn: func(root string) ([]SessionMeta, error) {
			return []SessionMeta{
				{Path: "old1.jsonl", Preview: "旧会话1", DeletedAt: 1000},
				{Path: "old2.jsonl", Preview: "旧会话2", DeletedAt: 2000},
			}, nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/trash", nil)
	w := httptest.NewRecorder()
	HandleListTrash(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var result []SessionMeta
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Fatalf("expected 2 items, got %d", len(result))
	}
	if result[0].DeletedAt == 0 {
		t.Error("expected non-zero deletedAt")
	}
}

// --- TC-S3-4: 列出垃圾箱——空 ---

func TestHandleListTrash_Empty(t *testing.T) {
	ctrl := &mockControl{
		listTrashFn: func(root string) ([]SessionMeta, error) {
			return []SessionMeta{}, nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/trash", nil)
	w := httptest.NewRecorder()
	HandleListTrash(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var result []SessionMeta
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty array, got %d", len(result))
	}
}

// --- TC-S3-5: 从垃圾箱恢复 ---

func TestHandleRestoreSession_Success(t *testing.T) {
	var capturedRoot, capturedName string
	ctrl := &mockControl{
		restoreSessionFn: func(root, name string) error {
			capturedRoot = root
			capturedName = name
			return nil
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/test-ws/trash/deleted.jsonl/restore", nil)
	w := httptest.NewRecorder()
	HandleRestoreSession(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if capturedRoot != "test-ws" || capturedName != "deleted.jsonl" {
		t.Errorf("expected test-ws/deleted.jsonl, got %s/%s", capturedRoot, capturedName)
	}
}

// --- TC-S3-6: 清空垃圾箱 ---

func TestHandleEmptyTrash_Success(t *testing.T) {
	var capturedRoot string
	ctrl := &mockControl{
		emptyTrashFn: func(root string) error {
			capturedRoot = root
			return nil
		},
	}

	req := httptest.NewRequest(http.MethodDelete, "/api/workspaces/test-ws/trash", nil)
	w := httptest.NewRecorder()
	HandleEmptyTrash(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if capturedRoot != "test-ws" {
		t.Errorf("expected root test-ws, got %s", capturedRoot)
	}
}

// --- Trash route registration ---

func TestTrashRoutesRegistration(t *testing.T) {
	ctrl := &mockControl{
		listTrashFn: func(root string) ([]SessionMeta, error) {
			return []SessionMeta{{Path: "x.jsonl", DeletedAt: 1}}, nil
		},
		emptyTrashFn:    func(root string) error { return nil },
		restoreSessionFn: func(root, name string) error { return nil },
		deleteSessionFn: func(root, name string) error { return nil },
	}

	g := gateway.New(7777)
	RegisterWorkspaceRoutes(g, ctrl)
	RegisterTopicRoutes(g, ctrl)

	// GET trash
	req1 := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/trash", nil)
	w1 := httptest.NewRecorder()
	g.Handler().ServeHTTP(w1, req1)
	if w1.Code != http.StatusOK {
		t.Errorf("GET trash: expected 200, got %d; body: %s", w1.Code, w1.Body.String())
	}

	// DELETE empty trash
	req2 := httptest.NewRequest(http.MethodDelete, "/api/workspaces/test-ws/trash", nil)
	w2 := httptest.NewRecorder()
	g.Handler().ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Errorf("DELETE trash: expected 200, got %d; body: %s", w2.Code, w2.Body.String())
	}

	// POST restore
	req3 := httptest.NewRequest(http.MethodPost, "/api/workspaces/test-ws/trash/s1.jsonl/restore", nil)
	w3 := httptest.NewRecorder()
	g.Handler().ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Errorf("POST restore: expected 200, got %d; body: %s", w3.Code, w3.Body.String())
	}

	// DELETE session via routing (with encoded path)
	req4 := httptest.NewRequest(http.MethodDelete, "/api/workspaces/test-ws/sessions/s1.jsonl", nil)
	w4 := httptest.NewRecorder()
	g.Handler().ServeHTTP(w4, req4)
	if w4.Code != http.StatusOK {
		t.Errorf("DELETE session: expected 200, got %d; body: %s", w4.Code, w4.Body.String())
	}
}

// --- TC-S4-1: 新建会话——成功 ---

func TestHandleNewSession_Success(t *testing.T) {
	var capturedTopic string
	ctrl := &mockControl{
		newSessionFn: func(topicID string) error {
			capturedTopic = topicID
			return nil
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/test-ws/topics/topic-dev/session/new", nil)
	w := httptest.NewRecorder()
	HandleNewSession(ctrl)(w, req)

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
	if capturedTopic != "topic-dev" {
		t.Errorf("expected topic-dev, got %s", capturedTopic)
	}
}

// --- TC-S4-2: 新建会话——topic 无活跃 tab ---

func TestHandleNewSession_TopicNotFound(t *testing.T) {
	ctrl := &mockControl{
		newSessionFn: func(topicID string) error {
			return fmt.Errorf("topic not found: %s", topicID)
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/test-ws/topics/nonexistent/session/new", nil)
	w := httptest.NewRecorder()
	HandleNewSession(ctrl)(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// --- TC-S4-3: 清空会话——成功 ---

func TestHandleClearSession_Success(t *testing.T) {
	var capturedTopic string
	ctrl := &mockControl{
		clearSessionFn: func(topicID string) error {
			capturedTopic = topicID
			return nil
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/test-ws/topics/topic-dev/session/clear", nil)
	w := httptest.NewRecorder()
	HandleClearSession(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if capturedTopic != "topic-dev" {
		t.Errorf("expected topic-dev, got %s", capturedTopic)
	}
}

// --- TC-S4-4: 恢复会话——成功 ---

func TestHandleResumeSession_Success(t *testing.T) {
	var capturedTopic, capturedPath string
	ctrl := &mockControl{
		resumeSessionFn: func(topicID, sessionPath string) error {
			capturedTopic = topicID
			capturedPath = sessionPath
			return nil
		},
	}

	body := jsonBody(t, ResumeSessionRequest{Path: "2026-07-12_abc.jsonl"})
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/test-ws/topics/topic-dev/session/resume", strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleResumeSession(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if capturedTopic != "topic-dev" {
		t.Errorf("expected topic-dev, got %s", capturedTopic)
	}
	if capturedPath != "2026-07-12_abc.jsonl" {
		t.Errorf("expected session path 2026-07-12_abc.jsonl, got %s", capturedPath)
	}
}

// --- TC-S4-5: 恢复会话——缺少 path 参数 ---

func TestHandleResumeSession_MissingPath(t *testing.T) {
	ctrl := &mockControl{}

	body := jsonBody(t, ResumeSessionRequest{})
	req := httptest.NewRequest(http.MethodPost, "/api/workspaces/test-ws/topics/topic-dev/session/resume", strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleResumeSession(ctrl)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d; body: %s", w.Code, w.Body.String())
	}
}

// --- Current session info ---

func TestHandleCurrentSession_Success(t *testing.T) {
	ctrl := &mockControl{
		currentSessionFn: func(topicID string) (SessionMeta, error) {
			return SessionMeta{Path: "active.jsonl", Preview: "当前会话", Turns: 5, Current: true}, nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/topics/topic-dev/session", nil)
	w := httptest.NewRecorder()
	HandleCurrentSession(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	var result SessionMeta
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Path != "active.jsonl" {
		t.Errorf("expected active.jsonl, got %s", result.Path)
	}
	if !result.Current {
		t.Error("expected current=true")
	}
}

func TestHandleCurrentSession_TopicNotFound(t *testing.T) {
	ctrl := &mockControl{
		currentSessionFn: func(topicID string) (SessionMeta, error) {
			return SessionMeta{}, fmt.Errorf("topic not found: %s", topicID)
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/test-ws/topics/nonexistent/session", nil)
	w := httptest.NewRecorder()
	HandleCurrentSession(ctrl)(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// --- Topic session routes registration ---

func TestTopicSessionRoutesRegistration(t *testing.T) {
	ctrl := &mockControl{
		newSessionFn:     func(id string) error { return nil },
		clearSessionFn:   func(id string) error { return nil },
		resumeSessionFn:  func(id, path string) error { return nil },
		currentSessionFn: func(id string) (SessionMeta, error) {
			return SessionMeta{Path: "active.jsonl"}, nil
		},
		activateFn: func(id string) (TopicInfo, error) {
			return TopicInfo{ID: id}, nil
		},
	}

	g := gateway.New(7777)
	RegisterWorkspaceRoutes(g, ctrl)
	RegisterTopicRoutes(g, ctrl)

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		code   int
	}{
		{"new session", http.MethodPost, "/api/workspaces/test-ws/topics/topic-dev/session/new", "", http.StatusOK},
		{"clear session", http.MethodPost, "/api/workspaces/test-ws/topics/topic-dev/session/clear", "", http.StatusOK},
		{"resume session", http.MethodPost, "/api/workspaces/test-ws/topics/topic-dev/session/resume", `{"path":"s1.jsonl"}`, http.StatusOK},
		{"current session", http.MethodGet, "/api/workspaces/test-ws/topics/topic-dev/session", "", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var bodyReader io.Reader
			if tt.body != "" {
				bodyReader = strings.NewReader(tt.body)
			}
			req := httptest.NewRequest(tt.method, tt.path, bodyReader)
			w := httptest.NewRecorder()
			g.Handler().ServeHTTP(w, req)
			if w.Code != tt.code {
				t.Errorf("expected %d, got %d; body: %s", tt.code, w.Code, w.Body.String())
			}
		})
	}
}
