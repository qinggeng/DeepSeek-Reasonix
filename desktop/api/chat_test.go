package api

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"reasonix/desktop/gateway"
	"reasonix/internal/event"
)

// --- helpers for chat tests ---
// Workspace path "test-ws" (no special chars) is used for all unit test URLs.

const testWorkspace = "test-ws"

func chatURL(topicID, action string) string {
	return "/api/workspaces/" + testWorkspace + "/topics/" + topicID + "/" + action
}

// --- TC-1: 提交 Prompt——正常提交 ---

func TestHandleSubmitPrompt_Valid(t *testing.T) {
	hub := NewStreamHub(256)
	ctrl := &mockControl{
		submitFn: func(topicID, input string) (string, error) {
			if topicID != "topic-dev" {
				return "", fmt.Errorf("topic not found")
			}
			// Simulate creating a stream
			hub.Register("stream-uuid-1")
			return "stream-uuid-1", nil
		},
		hub: hub,
	}

	body := jsonBody(t, ChatRequest{Text: "帮我写一个快速排序函数"})
	req := httptest.NewRequest(http.MethodPost, chatURL("topic-dev", "chat"), strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleSubmitPrompt(ctrl)(w, req)

	if w.Code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", w.Code)
	}

	var resp ChatSubmitResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.StreamID == "" {
		t.Error("expected non-empty streamId")
	}

	// Verify the stream was registered
	ch := hub.Lookup(resp.StreamID)
	if ch == nil {
		t.Error("expected stream to be registered in hub")
	}
}

// --- TC-2: 提交 Prompt——topic 不存在 ---

func TestHandleSubmitPrompt_TopicNotFound(t *testing.T) {
	ctrl := &mockControl{
		submitFn: func(topicID, input string) (string, error) {
			return "", fmt.Errorf("topic not found: %s", topicID)
		},
	}

	body := jsonBody(t, ChatRequest{Text: "hello"})
	req := httptest.NewRequest(http.MethodPost, chatURL("nonexistent", "chat"), strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleSubmitPrompt(ctrl)(w, req)

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

// --- TC-3: 取消运行——正在运行 ---

func TestHandleCancelRun_Running(t *testing.T) {
	ctrl := &mockControl{
		cancelFn: func(topicID string) error {
			if topicID != "topic-dev" {
				return fmt.Errorf("topic not found")
			}
			return nil
		},
	}

	req := httptest.NewRequest(http.MethodPost, chatURL("topic-dev", "chat/cancel"), nil)
	w := httptest.NewRecorder()
	HandleCancelRun(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result map[string]bool
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !result["cancelled"] {
		t.Error("expected cancelled: true")
	}
}

// --- TC-3b: 取消运行——topic 不存在 ---

func TestHandleCancelRun_TopicNotFound(t *testing.T) {
	ctrl := &mockControl{
		cancelFn: func(topicID string) error {
			return fmt.Errorf("topic not found: %s", topicID)
		},
	}

	req := httptest.NewRequest(http.MethodPost, chatURL("nonexistent", "chat/cancel"), nil)
	w := httptest.NewRecorder()
	HandleCancelRun(ctrl)(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// --- TC-4: 查询运行时状态——运行中 ---

func TestHandleChatStatus_Running(t *testing.T) {
	ctrl := &mockControl{
		chatStatusFn: func(topicID string) (ChatStatus, error) {
			return ChatStatus{
				Running:          true,
				Model:            "deepseek-v4",
				Effort:           "high",
				HasPendingPrompt: false,
				CancelRequested:  false,
				Cancellable:      true,
				BackgroundJobs:   0,
				CurrentStep:      "正在分析文件结构...",
				Turn:             5,
			}, nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, chatURL("topic-dev", "chat/status"), nil)
	w := httptest.NewRecorder()
	HandleChatStatus(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var status ChatStatus
	if err := json.NewDecoder(w.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if !status.Running {
		t.Error("expected running=true")
	}
	if status.Model != "deepseek-v4" {
		t.Errorf("expected model deepseek-v4, got %s", status.Model)
	}
	if status.Effort != "high" {
		t.Errorf("expected effort high, got %s", status.Effort)
	}
	if !status.Cancellable {
		t.Error("expected cancellable=true")
	}
	if status.Turn != 5 {
		t.Errorf("expected turn=5, got %d", status.Turn)
	}
	if status.CurrentStep == "" {
		t.Error("expected non-empty currentStep")
	}
}

// --- TC-4b: 查询状态——空闲 ---

func TestHandleChatStatus_Idle(t *testing.T) {
	ctrl := &mockControl{
		chatStatusFn: func(topicID string) (ChatStatus, error) {
			return ChatStatus{
				Running:          false,
				Model:            "deepseek-v4",
				Effort:           "medium",
				HasPendingPrompt: false,
				Cancellable:      false,
				Turn:             3,
			}, nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, chatURL("topic-dev", "chat/status"), nil)
	w := httptest.NewRecorder()
	HandleChatStatus(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var status ChatStatus
	if err := json.NewDecoder(w.Body).Decode(&status); err != nil {
		t.Fatal(err)
	}
	if status.Running {
		t.Error("expected running=false")
	}
	if status.Cancellable {
		t.Error("expected cancellable=false")
	}
}

// --- TC-4c: 查询状态——topic 不存在 ---

func TestHandleChatStatus_TopicNotFound(t *testing.T) {
	ctrl := &mockControl{
		chatStatusFn: func(topicID string) (ChatStatus, error) {
			return ChatStatus{}, fmt.Errorf("topic not found: %s", topicID)
		},
	}

	req := httptest.NewRequest(http.MethodGet, chatURL("nonexistent", "chat/status"), nil)
	w := httptest.NewRecorder()
	HandleChatStatus(ctrl)(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// --- TC-5: SSE 事件流——正常连接 ---

func TestHandleEventStream_ReceivesEvents(t *testing.T) {
	hub := NewStreamHub(10)
	ch := hub.Register("stream-abc")
	defer hub.Unregister("stream-abc")

	ctrl := &mockControl{hub: hub}

	// Write events to the channel (simulating Agent emissions)
	go func() {
		ch <- event.Event{Kind: event.TurnStarted}
		ch <- event.Event{Kind: event.Reasoning, Text: "思考中..."}
		ch <- event.Event{Kind: event.Text, Text: "这是回答"}
		ch <- event.Event{Kind: event.TurnDone}
	}()

	req := httptest.NewRequest(http.MethodGet, chatURL("topic-dev", "chat/events")+"?streamId=stream-abc", nil)
	w := httptest.NewRecorder()
	HandleEventStream(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	ct := w.Header().Get("Content-Type")
	if ct != "text/event-stream" {
		t.Errorf("expected text/event-stream, got %s", ct)
	}

	// Parse SSE output: collect event + data pairs.
	scanner := bufio.NewScanner(w.Body)
	type sseFrame struct{ event, data string }
	var frames []sseFrame
	var current sseFrame
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "event: ") {
			if current.event != "" {
				frames = append(frames, current)
			}
			current = sseFrame{event: strings.TrimPrefix(line, "event: ")}
		} else if strings.HasPrefix(line, "data: ") {
			current.data = strings.TrimPrefix(line, "data: ")
		}
	}
	if current.event != "" {
		frames = append(frames, current)
	}

	if len(frames) < 4 {
		t.Fatalf("expected at least 4 frames, got %d", len(frames))
	}
	if frames[0].event != "TurnStarted" {
		t.Errorf("expected first event TurnStarted, got %s", frames[0].event)
	}
	if frames[len(frames)-1].event != "TurnDone" {
		t.Errorf("expected last event TurnDone, got %s", frames[len(frames)-1].event)
	}

	// Verify text payload is present in Reasoning and Text events.
	for _, f := range frames {
		if f.event == "Reasoning" {
			if !strings.Contains(f.data, `"text":"思考中..."`) {
				t.Errorf("Reasoning data should contain text, got: %s", f.data)
			}
		}
		if f.event == "Text" {
			if !strings.Contains(f.data, `"text":"这是回答"`) {
				t.Errorf("Text data should contain text, got: %s", f.data)
			}
		}
	}
}

// --- TC-5b: SSE——无效 streamId ---

func TestHandleEventStream_StreamNotFound(t *testing.T) {
	hub := NewStreamHub(10)
	ctrl := &mockControl{hub: hub}

	req := httptest.NewRequest(http.MethodGet, chatURL("topic-dev", "chat/events")+"?streamId=nonexistent", nil)
	w := httptest.NewRecorder()
	HandleEventStream(ctrl)(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// --- TC-5c: SSE——缺少 streamId ---

func TestHandleEventStream_MissingStreamID(t *testing.T) {
	ctrl := &mockControl{hub: NewStreamHub(10)}

	req := httptest.NewRequest(http.MethodGet, chatURL("topic-dev", "chat/events"), nil)
	w := httptest.NewRecorder()
	HandleEventStream(ctrl)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- Steer tests ---

func TestHandleSteerRun_Valid(t *testing.T) {
	var capturedTopic, capturedText string
	ctrl := &mockControl{
		steerFn: func(topicID, text string) error {
			capturedTopic = topicID
			capturedText = text
			return nil
		},
	}

	body := jsonBody(t, SteerRequest{Text: "换个思路"})
	req := httptest.NewRequest(http.MethodPost, chatURL("topic-dev", "chat/steer"), strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleSteerRun(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	if capturedTopic != "topic-dev" {
		t.Errorf("expected topic topic-dev, got %s", capturedTopic)
	}
	if capturedText != "换个思路" {
		t.Errorf("expected text '换个思路', got %s", capturedText)
	}

	var result map[string]bool
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !result["steered"] {
		t.Error("expected steered: true")
	}
}

func TestHandleSteerRun_EmptyText(t *testing.T) {
	ctrl := &mockControl{}

	body := jsonBody(t, SteerRequest{Text: ""})
	req := httptest.NewRequest(http.MethodPost, chatURL("topic-dev", "chat/steer"), strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleSteerRun(ctrl)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleSteerRun_TopicNotFound(t *testing.T) {
	ctrl := &mockControl{
		steerFn: func(topicID, text string) error {
			return fmt.Errorf("topic not found: %s", topicID)
		},
	}

	body := jsonBody(t, SteerRequest{Text: "换个思路"})
	req := httptest.NewRequest(http.MethodPost, chatURL("nonexistent", "chat/steer"), strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleSteerRun(ctrl)(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// --- Dispatch routing test ---

func TestChatRoutesRegistration(t *testing.T) {
	hub := NewStreamHub(10)
	ctrl := &mockControl{
		hub: hub,
		submitFn: func(topicID, input string) (string, error) {
			if topicID == "topic-dev" {
				hub.Register("test-stream")
				return "test-stream", nil
			}
			return "", fmt.Errorf("topic not found")
		},
		cancelFn: func(topicID string) error {
			if topicID == "topic-dev" {
				return nil
			}
			return fmt.Errorf("topic not found")
		},
		steerFn: func(topicID, text string) error {
			if topicID == "topic-dev" {
				return nil
			}
			return fmt.Errorf("topic not found")
		},
		chatStatusFn: func(topicID string) (ChatStatus, error) {
			return ChatStatus{Running: true}, nil
		},
		activateFn: func(topicID string) (TopicInfo, error) {
			return TopicInfo{ID: topicID, Title: topicID}, nil
		},
		statusFn: func(topicID string) (TopicStatus, error) {
			return TopicStatus{Running: false}, nil
		},
	}

	g := gateway.New(7777)
	RegisterWorkspaceRoutes(g, ctrl)
	RegisterTopicRoutes(g, ctrl)

	// Build common base path for topic routes under workspace.
	base := "/api/workspaces/" + testWorkspace + "/topics/topic-dev"

	tests := []struct {
		name   string
		method string
		path   string
		body   string
		code   int
	}{
		{
			name:   "submit prompt OK",
			method: http.MethodPost,
			path:   base + "/chat",
			body:   `{"text":"hello"}`,
			code:   http.StatusAccepted,
		},
		{
			name:   "submit prompt empty body",
			method: http.MethodPost,
			path:   base + "/chat",
			body:   `{}`,
			code:   http.StatusBadRequest,
		},
		{
			name:   "cancel OK",
			method: http.MethodPost,
			path:   base + "/chat/cancel",
			code:   http.StatusOK,
		},
		{
			name:   "steer OK",
			method: http.MethodPost,
			path:   base + "/chat/steer",
			body:   `{"text":"steer"}`,
			code:   http.StatusOK,
		},
		{
			name:   "chat status OK",
			method: http.MethodGet,
			path:   base + "/chat/status",
			code:   http.StatusOK,
		},
		{
			name:   "activate still works",
			method: http.MethodPost,
			path:   base + "/activate",
			code:   http.StatusOK, // mock returns empty success for activate
		},
		{
			name:   "status still works",
			method: http.MethodGet,
			path:   base + "/status",
			code:   http.StatusOK,
		},
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

func TestHandleSubmitPrompt_EmptyText(t *testing.T) {
	ctrl := &mockControl{}

	// Empty text
	body := jsonBody(t, ChatRequest{Text: ""})
	req := httptest.NewRequest(http.MethodPost, chatURL("topic-dev", "chat"), strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleSubmitPrompt(ctrl)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty text, got %d", w.Code)
	}

	// Whitespace only
	body2 := jsonBody(t, ChatRequest{Text: "   "})
	req2 := httptest.NewRequest(http.MethodPost, chatURL("topic-dev", "chat"), strings.NewReader(body2))
	w2 := httptest.NewRecorder()
	HandleSubmitPrompt(ctrl)(w2, req2)

	if w2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for whitespace text, got %d", w2.Code)
	}
}

func TestHandleSubmitPrompt_InvalidBody(t *testing.T) {
	ctrl := &mockControl{}

	req := httptest.NewRequest(http.MethodPost, chatURL("topic-dev", "chat"), strings.NewReader(`not json`))
	w := httptest.NewRecorder()
	HandleSubmitPrompt(ctrl)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}
