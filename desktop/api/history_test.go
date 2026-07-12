package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- TC-1: 获取历史消息——全部历史 ---

func TestHandleHistory_FullHistory(t *testing.T) {
	ctrl := &mockControl{
		historyFn: func(topicID string, beforeTurn, limit int) (HistoryResponse, error) {
			if topicID != "topic-dev" {
				return HistoryResponse{}, fmt.Errorf("topic not found: %s", topicID)
			}
			return HistoryResponse{
				Messages: []HistoryMessage{
					{Role: "user", Content: "hello"},
					{Role: "assistant", Content: "hi there", ToolCalls: []HistoryToolCall{{ID: "t-1", Name: "bash", Arguments: "ls"}}},
					{Role: "tool", Content: "file1.txt", ToolCallID: "t-1"},
				},
				TotalTurns: 3,
			}, nil
		},
	}

	req := newEncodedRequest(http.MethodGet, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history", nil)
	w := httptest.NewRecorder()
	HandleHistory(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result HistoryResponse
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 3 {
		t.Errorf("expected 3 messages, got %d", len(result.Messages))
	}
	if result.Messages[0].Role != "user" || result.Messages[0].Content != "hello" {
		t.Errorf("unexpected first message: %+v", result.Messages[0])
	}
	if result.Messages[2].Role != "tool" || result.Messages[2].ToolCallID != "t-1" {
		t.Errorf("unexpected third message: %+v", result.Messages[2])
	}
	if result.TotalTurns != 3 {
		t.Errorf("expected TotalTurns=3, got %d", result.TotalTurns)
	}
}

// --- TC-2: 获取历史消息——分页 ---

func TestHandleHistory_Pagination(t *testing.T) {
	var capturedBefore, capturedLimit int
	ctrl := &mockControl{
		historyFn: func(topicID string, beforeTurn, limit int) (HistoryResponse, error) {
			capturedBefore = beforeTurn
			capturedLimit = limit
			return HistoryResponse{
				Messages: []HistoryMessage{
					{Role: "user", Content: "msg1"},
					{Role: "assistant", Content: "msg2"},
				},
				TotalTurns: 10,
				HasMore:    true,
			}, nil
		},
	}

	req := newEncodedRequest(http.MethodGet, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history?beforeTurn=5&limit=2", nil)
	w := httptest.NewRecorder()
	HandleHistory(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	if capturedBefore != 5 {
		t.Errorf("expected beforeTurn=5, got %d", capturedBefore)
	}
	if capturedLimit != 2 {
		t.Errorf("expected limit=2, got %d", capturedLimit)
	}

	var result HistoryResponse
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result.Messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(result.Messages))
	}
	if !result.HasMore {
		t.Error("expected HasMore=true")
	}

	// Default: no query params = 0, 0 (full history)
	var capturedBefore2, capturedLimit2 int
	ctrl2 := &mockControl{
		historyFn: func(topicID string, beforeTurn, limit int) (HistoryResponse, error) {
			capturedBefore2 = beforeTurn
			capturedLimit2 = limit
			return HistoryResponse{}, nil
		},
	}
	req2 := newEncodedRequest(http.MethodGet, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history", nil)
	w2 := httptest.NewRecorder()
	HandleHistory(ctrl2)(w2, req2)
	if capturedBefore2 != 0 || capturedLimit2 != 0 {
		t.Errorf("expected defaults 0,0; got %d,%d", capturedBefore2, capturedLimit2)
	}
}

// --- TC-3: 获取历史消息——topic 未激活 ---

func TestHandleHistory_TopicNotFound(t *testing.T) {
	ctrl := &mockControl{
		historyFn: func(topicID string, beforeTurn, limit int) (HistoryResponse, error) {
			return HistoryResponse{}, fmt.Errorf("topic not found: %s", topicID)
		},
	}

	req := newEncodedRequest(http.MethodGet, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/non-existent-topic/history", nil)
	w := httptest.NewRecorder()
	HandleHistory(ctrl)(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}

	var errResp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&errResp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(errResp["error"], "topic not found") {
		t.Errorf("expected error containing 'topic not found', got %q", errResp["error"])
	}
}

// --- TC-4: 列出检查点 ---

func TestHandleCheckpoints_ReturnsAll(t *testing.T) {
	ctrl := &mockControl{
		checkpointsFn: func(topicID string) ([]CheckpointMeta, error) {
			return []CheckpointMeta{
				{Turn: 1, Time: 1000, Prompt: "initial setup", Paths: []string{"main.go"}},
				{Turn: 3, Time: 2000, Prompt: "add feature", Paths: []string{"main.go", "utils.go"}},
			}, nil
		},
	}

	req := newEncodedRequest(http.MethodGet, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/checkpoints", nil)
	w := httptest.NewRecorder()
	HandleCheckpoints(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result []CheckpointMeta
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 checkpoints, got %d", len(result))
	}
	if result[0].Turn != 1 || result[0].Prompt != "initial setup" {
		t.Errorf("unexpected first checkpoint: %+v", result[0])
	}
	if len(result[0].Paths) != 1 || result[0].Paths[0] != "main.go" {
		t.Errorf("unexpected paths: %v", result[0].Paths)
	}
}

func TestHandleCheckpoints_Empty(t *testing.T) {
	ctrl := &mockControl{
		checkpointsFn: func(topicID string) ([]CheckpointMeta, error) {
			return []CheckpointMeta{}, nil
		},
	}

	req := newEncodedRequest(http.MethodGet, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/checkpoints", nil)
	w := httptest.NewRecorder()
	HandleCheckpoints(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if strings.TrimSpace(w.Body.String()) != "[]" {
		var result []CheckpointMeta
		if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
			t.Fatal(err)
		}
		if len(result) != 0 {
			t.Errorf("expected empty array, got %d items", len(result))
		}
	}
}

// --- TC-5: 列出分支 ---

func TestHandleBranches_ReturnsAll(t *testing.T) {
	ctrl := &mockControl{
		branchesFn: func(topicID string) ([]BranchInfo, error) {
			return []BranchInfo{
				{ID: "main", Name: "main", ForkTurn: 0, Turns: 5, Preview: "initial session"},
				{ID: "exp1", Name: "experiment", ParentID: "main", ForkTurn: 2, Turns: 3, Preview: "experiment branch"},
			}, nil
		},
	}

	req := newEncodedRequest(http.MethodGet, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/branches", nil)
	w := httptest.NewRecorder()
	HandleBranches(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result []BranchInfo
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 branches, got %d", len(result))
	}
	if result[0].ID != "main" || result[0].Name != "main" {
		t.Errorf("unexpected first branch: %+v", result[0])
	}
	if result[1].ParentID != "main" || result[1].ForkTurn != 2 {
		t.Errorf("unexpected second branch: %+v", result[1])
	}
}

func TestHandleBranches_Empty(t *testing.T) {
	ctrl := &mockControl{
		branchesFn: func(topicID string) ([]BranchInfo, error) {
			return []BranchInfo{}, nil
		},
	}

	req := newEncodedRequest(http.MethodGet, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/branches", nil)
	w := httptest.NewRecorder()
	HandleBranches(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var result []BranchInfo
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if len(result) != 0 {
		t.Errorf("expected empty array")
	}
}

// --- TC-6: 回滚到指定 turn ---

func TestHandleRewind_Success(t *testing.T) {
	var capturedTurn int
	var capturedScope string
	ctrl := &mockControl{
		rewindFn: func(topicID string, turn int, scope string) error {
			capturedTurn = turn
			capturedScope = scope
			return nil
		},
	}

	body := jsonBody(t, RewindRequest{Turn: 2})
	req := newEncodedRequest(http.MethodPost, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/rewind", strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleRewind(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if capturedTurn != 2 {
		t.Errorf("expected turn=2, got %d", capturedTurn)
	}
	if capturedScope != "both" {
		t.Errorf("expected default scope='both', got %q", capturedScope)
	}

	var result map[string]bool
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !result["success"] {
		t.Error("expected success: true")
	}
}

func TestHandleRewind_WithScope(t *testing.T) {
	var capturedScope string
	ctrl := &mockControl{
		rewindFn: func(topicID string, turn int, scope string) error {
			capturedScope = scope
			return nil
		},
	}

	body := jsonBody(t, RewindRequest{Turn: 1, Scope: "code"})
	req := newEncodedRequest(http.MethodPost, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/rewind", strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleRewind(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if capturedScope != "code" {
		t.Errorf("expected scope='code', got %q", capturedScope)
	}
}

func TestHandleRewind_TurnNotFound(t *testing.T) {
	ctrl := &mockControl{
		rewindFn: func(topicID string, turn int, scope string) error {
			return fmt.Errorf("turn not found: %d", turn)
		},
	}

	body := jsonBody(t, RewindRequest{Turn: 999})
	req := newEncodedRequest(http.MethodPost, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/rewind", strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleRewind(ctrl)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleRewind_InvalidScope(t *testing.T) {
	ctrl := &mockControl{}

	body := jsonBody(t, RewindRequest{Turn: 1, Scope: "invalid"})
	req := newEncodedRequest(http.MethodPost, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/rewind", strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleRewind(ctrl)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleRewind_TurnRunning(t *testing.T) {
	ctrl := &mockControl{
		rewindFn: func(topicID string, turn int, scope string) error {
			return fmt.Errorf("turn is running")
		},
	}

	body := jsonBody(t, RewindRequest{Turn: 1})
	req := newEncodedRequest(http.MethodPost, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/rewind", strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleRewind(ctrl)(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", w.Code)
	}
}

func TestHandleRewind_NegativeTurn(t *testing.T) {
	ctrl := &mockControl{}

	body := jsonBody(t, RewindRequest{Turn: -1})
	req := newEncodedRequest(http.MethodPost, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/rewind", strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleRewind(ctrl)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- TC-7: 分叉 ---

func TestHandleFork_Success(t *testing.T) {
	var capturedTurn int
	var capturedName string
	ctrl := &mockControl{
		forkSessionFn: func(topicID string, turn int, name string) (string, error) {
			capturedTurn = turn
			capturedName = name
			return "/sessions/new-fork.jsonl", nil
		},
	}

	body := jsonBody(t, ForkRequest{Turn: 2, Name: "experiment"})
	req := newEncodedRequest(http.MethodPost, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/fork", strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleFork(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if capturedTurn != 2 {
		t.Errorf("expected turn=2, got %d", capturedTurn)
	}
	if capturedName != "experiment" {
		t.Errorf("expected name='experiment', got %q", capturedName)
	}

	var result ForkResponse
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.SessionPath != "/sessions/new-fork.jsonl" {
		t.Errorf("expected sessionPath='/sessions/new-fork.jsonl', got %q", result.SessionPath)
	}
}

func TestHandleFork_TurnNotFound(t *testing.T) {
	ctrl := &mockControl{
		forkSessionFn: func(topicID string, turn int, name string) (string, error) {
			return "", fmt.Errorf("turn not found: %d", turn)
		},
	}

	body := jsonBody(t, ForkRequest{Turn: 999})
	req := newEncodedRequest(http.MethodPost, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/fork", strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleFork(ctrl)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleFork_TurnRunning(t *testing.T) {
	ctrl := &mockControl{
		forkSessionFn: func(topicID string, turn int, name string) (string, error) {
			return "", fmt.Errorf("turn is running")
		},
	}

	body := jsonBody(t, ForkRequest{Turn: 1})
	req := newEncodedRequest(http.MethodPost, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/fork", strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleFork(ctrl)(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", w.Code)
	}
}

func TestHandleFork_NegativeTurn(t *testing.T) {
	ctrl := &mockControl{}

	body := jsonBody(t, ForkRequest{Turn: -1})
	req := newEncodedRequest(http.MethodPost, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/fork", strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleFork(ctrl)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- TC-8: 触发上下文压缩 ---

func TestHandleCompact_Success(t *testing.T) {
	ctrl := &mockControl{
		compactFn: func(topicID string) error {
			return nil
		},
	}

	req := newEncodedRequest(http.MethodPost, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/compact", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	HandleCompact(ctrl)(w, req)

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

func TestHandleCompact_TurnRunning(t *testing.T) {
	ctrl := &mockControl{
		compactFn: func(topicID string) error {
			return fmt.Errorf("turn is running")
		},
	}

	req := newEncodedRequest(http.MethodPost, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/compact", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	HandleCompact(ctrl)(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", w.Code)
	}
}

func TestHandleCompact_TopicNotFound(t *testing.T) {
	ctrl := &mockControl{
		compactFn: func(topicID string) error {
			return fmt.Errorf("topic not found: %s", topicID)
		},
	}

	req := newEncodedRequest(http.MethodPost, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/non-existent/history/compact", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	HandleCompact(ctrl)(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// --- TC-9: 生成摘要 ---

func TestHandleSummarize_From(t *testing.T) {
	var capturedTurn int
	ctrl := &mockControl{
		summarizeFromFn: func(topicID string, turn int) error {
			capturedTurn = turn
			return nil
		},
	}

	body := jsonBody(t, SummarizeRequest{Turn: 3, Direction: "from"})
	req := newEncodedRequest(http.MethodPost, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/summarize", strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleSummarize(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if capturedTurn != 3 {
		t.Errorf("expected turn=3, got %d", capturedTurn)
	}

	var result map[string]bool
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !result["success"] {
		t.Error("expected success: true")
	}
}

func TestHandleSummarize_UpTo(t *testing.T) {
	var capturedTurn int
	ctrl := &mockControl{
		summarizeUpToFn: func(topicID string, turn int) error {
			capturedTurn = turn
			return nil
		},
	}

	body := jsonBody(t, SummarizeRequest{Turn: 3, Direction: "upto"})
	req := newEncodedRequest(http.MethodPost, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/summarize", strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleSummarize(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if capturedTurn != 3 {
		t.Errorf("expected turn=3, got %d", capturedTurn)
	}
}

func TestHandleSummarize_MissingDirection(t *testing.T) {
	ctrl := &mockControl{}

	body := jsonBody(t, SummarizeRequest{Turn: 3})
	req := newEncodedRequest(http.MethodPost, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/summarize", strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleSummarize(ctrl)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleSummarize_InvalidDirection(t *testing.T) {
	ctrl := &mockControl{}

	body := jsonBody(t, SummarizeRequest{Turn: 3, Direction: "invalid"})
	req := newEncodedRequest(http.MethodPost, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/summarize", strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleSummarize(ctrl)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleSummarize_TurnRunning(t *testing.T) {
	ctrl := &mockControl{
		summarizeFromFn: func(topicID string, turn int) error {
			return fmt.Errorf("turn is running")
		},
	}

	body := jsonBody(t, SummarizeRequest{Turn: 3, Direction: "from"})
	req := newEncodedRequest(http.MethodPost, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/summarize", strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleSummarize(ctrl)(w, req)

	if w.Code != http.StatusConflict {
		t.Errorf("expected 409, got %d", w.Code)
	}
}

// --- TC-10: 查询工具调用完整结果 ---

func TestHandleToolResult_Success(t *testing.T) {
	var capturedToolID string
	ctrl := &mockControl{
		toolResultFn: func(topicID, toolID string) (ToolResultResponse, error) {
			capturedToolID = toolID
			return ToolResultResponse{
				ToolID: toolID,
				Args:   "ls -la",
				Output: "file1.txt\nfile2.go",
			}, nil
		},
	}

	req := newEncodedRequest(http.MethodGet, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/tool-result?toolId=t-1", nil)
	w := httptest.NewRecorder()
	HandleToolResult(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if capturedToolID != "t-1" {
		t.Errorf("expected toolID='t-1', got %q", capturedToolID)
	}

	var result ToolResultResponse
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Args != "ls -la" {
		t.Errorf("expected args='ls -la', got %q", result.Args)
	}
	if result.Output != "file1.txt\nfile2.go" {
		t.Errorf("unexpected output: %q", result.Output)
	}
}

func TestHandleToolResult_MissingToolID(t *testing.T) {
	ctrl := &mockControl{}

	req := newEncodedRequest(http.MethodGet, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/tool-result", nil)
	w := httptest.NewRecorder()
	HandleToolResult(ctrl)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleToolResult_NotFound(t *testing.T) {
	ctrl := &mockControl{
		toolResultFn: func(topicID, toolID string) (ToolResultResponse, error) {
			return ToolResultResponse{}, fmt.Errorf("tool not found")
		},
	}

	req := newEncodedRequest(http.MethodGet, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/tool-result?toolId=non-existent", nil)
	w := httptest.NewRecorder()
	HandleToolResult(ctrl)(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// --- Route dispatch integration tests ---

func TestHandleHistory_MissingTopicID(t *testing.T) {
	ctrl := &mockControl{}

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics//history", nil)
	w := httptest.NewRecorder()
	HandleHistory(ctrl)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

func TestHandleInvalidBody_ReturnsBadRequest(t *testing.T) {
	ctrl := &mockControl{}

	// Rewind with invalid body
	req := newEncodedRequest(http.MethodPost, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/rewind", strings.NewReader(`not json`))
	w := httptest.NewRecorder()
	HandleRewind(ctrl)(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid body, got %d", w.Code)
	}

	// Fork with invalid body
	req2 := newEncodedRequest(http.MethodPost, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/fork", strings.NewReader(`not json`))
	w2 := httptest.NewRecorder()
	HandleFork(ctrl)(w2, req2)
	if w2.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid body, got %d", w2.Code)
	}

	// Summarize with invalid body
	req3 := newEncodedRequest(http.MethodPost, "/api/workspaces/E%3A%2Fprojects%2Ffoo/topics/topic-dev/history/summarize", strings.NewReader(`not json`))
	w3 := httptest.NewRecorder()
	HandleSummarize(ctrl)(w3, req3)
	if w3.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid body, got %d", w3.Code)
	}
}
