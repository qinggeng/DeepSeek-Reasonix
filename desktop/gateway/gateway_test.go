package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayRoutesRequest(t *testing.T) {
	g := New(7777)
	var called bool
	g.Handle("GET", "/api/test", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	w := httptest.NewRecorder()
	g.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if !called {
		t.Error("handler was not called")
	}
}

func TestGatewayRejectsWrongMethod(t *testing.T) {
	g := New(7777)
	g.Handle("POST", "/api/test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	w := httptest.NewRecorder()
	g.mux.ServeHTTP(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Code)
	}
}

func TestGatewayReturnsJSON(t *testing.T) {
	g := New(7777)
	g.Handle("GET", "/api/test", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]string{"msg": "ok"})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	w := httptest.NewRecorder()
	g.mux.ServeHTTP(w, req)

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %s", ct)
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["msg"] != "ok" {
		t.Errorf("expected msg=ok, got %v", body)
	}
}

func TestGatewayWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteError(w, http.StatusBadRequest, "bad input")

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}

	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["error"] != "bad input" {
		t.Errorf("expected error=bad input, got %v", body)
	}
}

func TestGatewayDecodeBody(t *testing.T) {
	body := `{"key":"value"}`
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))

	var dst struct {
		Key string `json:"key"`
	}
	if err := DecodeBody(req, &dst); err != nil {
		t.Fatal(err)
	}
	if dst.Key != "value" {
		t.Errorf("expected value, got %s", dst.Key)
	}
}

func TestGatewayHandleAndQuery(t *testing.T) {
	g := New(7777)
	g.Handle("GET", "/api/workspaces", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, []string{"a", "b"})
	})

	req := httptest.NewRequest(http.MethodGet, "/api/workspaces", nil)
	w := httptest.NewRecorder()
	g.mux.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}

	var arr []string
	if err := json.NewDecoder(w.Body).Decode(&arr); err != nil {
		t.Fatal(err)
	}
	if len(arr) != 2 || arr[0] != "a" || arr[1] != "b" {
		t.Errorf("unexpected body: %v", arr)
	}
}

func TestGatewayNotFound(t *testing.T) {
	g := New(7777)
	req := httptest.NewRequest(http.MethodGet, "/api/nonexistent", nil)
	w := httptest.NewRecorder()
	g.mux.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", w.Code)
	}
}
