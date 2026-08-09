package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- TC-1: 审批工具调用——正常批准 ---

func TestHandleApprove_Valid(t *testing.T) {
	ctrl := &mockControl{
		approveFn: func(topicID string, req ApproveRequest) error {
			if topicID != "topic-dev" {
				return fmt.Errorf("topic not found")
			}
			if req.ID != "appr-001" {
				return fmt.Errorf("unexpected approval id: %s", req.ID)
			}
			if !req.Allow {
				return fmt.Errorf("expected allow=true")
			}
			return nil
		},
	}

	body := jsonBody(t, ApproveRequest{ID: "appr-001", Allow: true, Session: true})
	req := httptest.NewRequest(http.MethodPost, chatURL("topic-dev", "approve"), strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleApprove(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "approved" {
		t.Errorf("expected status=approved, got %v", resp["status"])
	}
	if resp["approvalId"] != "appr-001" {
		t.Errorf("expected approvalId=appr-001, got %v", resp["approvalId"])
	}
	if resp["allow"] != true {
		t.Errorf("expected allow=true, got %v", resp["allow"])
	}
}

// --- TC-2: 回答多选问题——正常回答 ---

func TestHandleAnswer_Valid(t *testing.T) {
	ctrl := &mockControl{
		answerFn: func(topicID string, req AnswerRequest) error {
			if topicID != "topic-dev" {
				return fmt.Errorf("topic not found")
			}
			if req.ID != "ask-001" {
				return fmt.Errorf("unexpected ask id: %s", req.ID)
			}
			if len(req.Answers) != 1 {
				return fmt.Errorf("expected 1 answer, got %d", len(req.Answers))
			}
			if req.Answers[0].QuestionID != "q1" {
				return fmt.Errorf("unexpected question id: %s", req.Answers[0].QuestionID)
			}
			if len(req.Answers[0].Selected) != 1 || req.Answers[0].Selected[0] != "Option A" {
				return fmt.Errorf("unexpected selected: %v", req.Answers[0].Selected)
			}
			return nil
		},
	}

	body := jsonBody(t, AnswerRequest{
		ID: "ask-001",
		Answers: []AnswerItem{
			{QuestionID: "q1", Selected: []string{"Option A"}},
		},
	})
	req := httptest.NewRequest(http.MethodPost, chatURL("topic-dev", "answer"), strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleAnswer(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "answered" {
		t.Errorf("expected status=answered, got %v", resp["status"])
	}
	if resp["askId"] != "ask-001" {
		t.Errorf("expected askId=ask-001, got %v", resp["askId"])
	}
}

// --- TC-3: 查询待审批状态——有 pending ---

func TestHandlePendingPrompt_WithPending(t *testing.T) {
	ctrl := &mockControl{
		pendingPromptFn: func(topicID string) (bool, error) {
			if topicID != "topic-dev" {
				return false, fmt.Errorf("topic not found")
			}
			return true, nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, chatURL("topic-dev", "pending"), nil)
	w := httptest.NewRecorder()
	HandlePendingPrompt(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp PendingResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if !resp.Pending {
		t.Error("expected pending=true")
	}
}

// --- TC-4: 设置审批模式 ---

func TestHandleSetApprovalMode_Ask(t *testing.T) {
	ctrl := &mockControl{
		setApprovalModeFn: func(topicID, mode string) error {
			if topicID != "topic-dev" {
				return fmt.Errorf("topic not found")
			}
			if mode != "ask" {
				return fmt.Errorf("expected mode=ask, got %s", mode)
			}
			return nil
		},
	}

	body := jsonBody(t, ApprovalModeRequest{Mode: "ask"})
	req := httptest.NewRequest(http.MethodPost, chatURL("topic-dev", "approval-mode"), strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleSetApprovalMode(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", resp["status"])
	}
	if resp["mode"] != "ask" {
		t.Errorf("expected mode=ask, got %v", resp["mode"])
	}
}

// --- TC-5: 重放待审批 prompt ---

func TestHandleReplayPrompts(t *testing.T) {
	replayCalled := false
	ctrl := &mockControl{
		replayPromptsFn: func() {
			replayCalled = true
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/api/approval/replay", nil)
	w := httptest.NewRecorder()
	HandleReplayPrompts(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	if !replayCalled {
		t.Error("expected ReplayPendingPrompts to be called")
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status=ok, got %v", resp["status"])
	}
}

// --- TC-6: 审批——topic 不存在 ---

func TestHandleApprove_TopicNotFound(t *testing.T) {
	ctrl := &mockControl{
		approveFn: func(topicID string, req ApproveRequest) error {
			return fmt.Errorf("topic not found: %s", topicID)
		},
	}

	body := jsonBody(t, ApproveRequest{ID: "appr-001", Allow: true})
	req := httptest.NewRequest(http.MethodPost, chatURL("nonexistent", "approve"), strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleApprove(ctrl)(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// --- TC-7: 审批——缺少 id ---

func TestHandleApprove_MissingID(t *testing.T) {
	ctrl := &mockControl{}

	body := jsonBody(t, ApproveRequest{Allow: true})
	req := httptest.NewRequest(http.MethodPost, chatURL("topic-dev", "approve"), strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleApprove(ctrl)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- TC-8: 回答——topic 不存在 ---

func TestHandleAnswer_TopicNotFound(t *testing.T) {
	ctrl := &mockControl{
		answerFn: func(topicID string, req AnswerRequest) error {
			return fmt.Errorf("topic not found: %s", topicID)
		},
	}

	body := jsonBody(t, AnswerRequest{
		ID: "ask-001",
		Answers: []AnswerItem{
			{QuestionID: "q1", Selected: []string{"A"}},
		},
	})
	req := httptest.NewRequest(http.MethodPost, chatURL("nonexistent", "answer"), strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleAnswer(ctrl)(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// --- TC-9: 回答——缺少 id ---

func TestHandleAnswer_MissingID(t *testing.T) {
	ctrl := &mockControl{}

	body := jsonBody(t, AnswerRequest{
		Answers: []AnswerItem{
			{QuestionID: "q1", Selected: []string{"A"}},
		},
	})
	req := httptest.NewRequest(http.MethodPost, chatURL("topic-dev", "answer"), strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleAnswer(ctrl)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- TC-10: 设置审批模式——无效 mode ---

func TestHandleSetApprovalMode_InvalidMode(t *testing.T) {
	ctrl := &mockControl{}

	body := jsonBody(t, ApprovalModeRequest{Mode: "invalid"})
	req := httptest.NewRequest(http.MethodPost, chatURL("topic-dev", "approval-mode"), strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleSetApprovalMode(ctrl)(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}

// --- TC-11: 审批——拒绝工具调用 ---

func TestHandleApprove_Deny(t *testing.T) {
	ctrl := &mockControl{
		approveFn: func(topicID string, req ApproveRequest) error {
			if topicID != "topic-dev" {
				return fmt.Errorf("topic not found")
			}
			if req.ID != "appr-002" {
				return fmt.Errorf("unexpected approval id: %s", req.ID)
			}
			if req.Allow {
				return fmt.Errorf("expected allow=false")
			}
			return nil
		},
	}

	body := jsonBody(t, ApproveRequest{ID: "appr-002", Allow: false})
	req := httptest.NewRequest(http.MethodPost, chatURL("topic-dev", "approve"), strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleApprove(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp["status"] != "approved" {
		t.Errorf("expected status=approved, got %v", resp["status"])
	}
	if resp["allow"] != false {
		t.Errorf("expected allow=false, got %v", resp["allow"])
	}
}

// --- TC-12: 查询待审批状态——无 pending ---

func TestHandlePendingPrompt_NoPending(t *testing.T) {
	ctrl := &mockControl{
		pendingPromptFn: func(topicID string) (bool, error) {
			return false, nil
		},
	}

	req := httptest.NewRequest(http.MethodGet, chatURL("topic-dev", "pending"), nil)
	w := httptest.NewRecorder()
	HandlePendingPrompt(ctrl)(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var resp PendingResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Pending {
		t.Error("expected pending=false")
	}
}

// --- TC-13: 查询待审批状态——topic 不存在 ---

func TestHandlePendingPrompt_TopicNotFound(t *testing.T) {
	ctrl := &mockControl{
		pendingPromptFn: func(topicID string) (bool, error) {
			return false, fmt.Errorf("topic not found: %s", topicID)
		},
	}

	req := httptest.NewRequest(http.MethodGet, chatURL("nonexistent", "pending"), nil)
	w := httptest.NewRecorder()
	HandlePendingPrompt(ctrl)(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// --- TC-14: 设置审批模式——topic 不存在 ---

func TestHandleSetApprovalMode_TopicNotFound(t *testing.T) {
	ctrl := &mockControl{
		setApprovalModeFn: func(topicID, mode string) error {
			return fmt.Errorf("topic not found: %s", topicID)
		},
	}

	body := jsonBody(t, ApprovalModeRequest{Mode: "ask"})
	req := httptest.NewRequest(http.MethodPost, chatURL("nonexistent", "approval-mode"), strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleSetApprovalMode(ctrl)(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}

// --- TC-15: 重放 prompt——GET 方法不允许 ---

func TestHandleReplayPrompts_WrongMethod(t *testing.T) {
	ctrl := &mockControl{}

	req := httptest.NewRequest(http.MethodGet, "/api/approval/replay", nil)
	w := httptest.NewRecorder()
	HandleReplayPrompts(ctrl)(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

// TC-A3-06 审批意见透传 + omitempty：ApproveRequest.Opinion 经 HandleApprove
// 到达 mock control；意见缺省时 JSON 不含 opinion 字段。
func TestHandleApprove_OpinionPassthrough(t *testing.T) {
	got := ""
	ctrl := &mockControl{
		approveFn: func(topicID string, req ApproveRequest) error {
			got = req.Opinion
			return nil
		},
	}

	body := jsonBody(t, ApproveRequest{ID: "appr-001", Allow: false, Opinion: "验收脚本需覆盖负例"})
	req := httptest.NewRequest(http.MethodPost, chatURL("topic-dev", "approve"), strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleApprove(ctrl)(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if got != "验收脚本需覆盖负例" {
		t.Fatalf("opinion must reach the control layer, got %q", got)
	}
}

func TestApproveRequestOpinionOmitempty(t *testing.T) {
	raw, err := json.Marshal(ApproveRequest{ID: "appr-001", Allow: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "opinion") {
		t.Fatalf("empty opinion must be omitted from the wire JSON, got %s", raw)
	}
	raw2, err := json.Marshal(ApproveRequest{ID: "appr-001", Allow: false, Opinion: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw2), `"opinion":"x"`) {
		t.Fatalf("non-empty opinion must ride the wire JSON, got %s", raw2)
	}
}

// --- TC-8: 审批——approval id 未知/已消费（Sprint 11 HTTP 接口改进）---
// 客户端（E2E 递增试探）必须能区分"审批已消费"与"id 无效"：未知 id 返回 404。

func TestHandleApprove_ApprovalNotFound(t *testing.T) {
	ctrl := &mockControl{
		approveFn: func(topicID string, req ApproveRequest) error {
			return fmt.Errorf("approval %q is not pending", req.ID)
		},
	}

	body := jsonBody(t, ApproveRequest{ID: "ghost-999", Allow: true})
	req := httptest.NewRequest(http.MethodPost, chatURL("topic-dev", "approve"), strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleApprove(ctrl)(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404 for unknown approval id, got %d (body=%s)", w.Code, w.Body.String())
	}
}

// --- TC-9: 审批——内部错误保持 500（不被误判为 not found）---

func TestHandleApprove_InternalError(t *testing.T) {
	ctrl := &mockControl{
		approveFn: func(topicID string, req ApproveRequest) error {
			return fmt.Errorf("boom: decision channel broken")
		},
	}

	body := jsonBody(t, ApproveRequest{ID: "appr-001", Allow: true})
	req := httptest.NewRequest(http.MethodPost, chatURL("topic-dev", "approve"), strings.NewReader(body))
	w := httptest.NewRecorder()
	HandleApprove(ctrl)(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for internal error, got %d (body=%s)", w.Code, w.Body.String())
	}
}
