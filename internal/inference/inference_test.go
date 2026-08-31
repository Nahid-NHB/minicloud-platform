package inference

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/minicloud/platform/internal/primitives/llm"
)

func setup(t *testing.T) *Router {
	t.Helper()
	r := llm.NewRegistry()
	r.Register(llm.Model{Name: "echo"}, llm.NewTinyEngine())
	return NewRouter(r)
}

func TestChatCompletions(t *testing.T) {
	r := setup(t)
	body, _ := json.Marshal(ChatRequestBody{
		Model:    "echo",
		Messages: []ChatMessage{{Role: "user", Content: "hello"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var resp ChatResponseBody
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.Model != "echo" || len(resp.Choices) == 0 {
		t.Fatalf("bad response: %+v", resp)
	}
	if resp.Choices[0].Message.Content == "" || resp.Choices[0].Message.Role != "assistant" {
		t.Fatalf("unexpected content: %+v", resp.Choices[0])
	}
}

func TestListModels(t *testing.T) {
	r := setup(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var resp ModelsResponse
	json.NewDecoder(w.Body).Decode(&resp)
	if len(resp.Data) == 0 {
		t.Fatalf("expected models, got %+v", resp)
	}
}

func TestMissingModel(t *testing.T) {
	r := setup(t)
	body, _ := json.Marshal(ChatRequestBody{
		Model:    "missing",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	// Should still succeed because registry.Resolve falls back to TinyEngine.
	if w.Code != 200 {
		t.Fatalf("expected fallback, got code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestStream(t *testing.T) {
	r := setup(t)
	body, _ := json.Marshal(ChatRequestBody{
		Model:    "echo",
		Messages: []ChatMessage{{Role: "user", Content: "hi"}},
		Stream:   true,
		MaxTokens: 5,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if !strings.Contains(w.Body.String(), "data: ") {
		t.Fatalf("expected SSE, got %s", w.Body.String())
	}
}
