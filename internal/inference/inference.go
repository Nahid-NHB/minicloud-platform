// Package inference exposes an OpenAI-compatible HTTP front-end on
// top of the platform's LLM primitive. The endpoint accepts JSON
// payloads identical to /v1/chat/completions and returns the same
// shape (id, object, created, model, choices, usage). Streaming is
// not implemented in this minimal version.
//
// The router picks a backend engine based on the model name in the
// request. The default registry is wired to the TinyEngine so the
// platform works without an external model server.
package inference

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/minicloud/platform/internal/primitives/llm"
)

// Router dispatches incoming chat requests to the right engine.
type Router struct {
	mu       sync.RWMutex
	registry *llm.Registry
}

// NewRouter builds a router.
func NewRouter(reg *llm.Registry) *Router {
	return &Router{registry: reg}
}

// ServeHTTP implements http.Handler for /v1/chat/completions and
// /v1/models.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch {
	case req.URL.Path == "/v1/models" && req.Method == http.MethodGet:
		r.handleModels(w, req)
	case strings.HasSuffix(req.URL.Path, "/chat/completions") && req.Method == http.MethodPost:
		r.handleChat(w, req)
	default:
		http.NotFound(w, req)
	}
}

// ChatMessage is the OpenAI wire format message.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequestBody is the OpenAI-compatible chat-completion request.
type ChatRequestBody struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float64       `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

// Choice is a response choice in OpenAI format.
type Choice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

// Usage mirrors OpenAI's token usage block.
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// ChatResponseBody is the OpenAI-compatible response.
type ChatResponseBody struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Model   string   `json:"model"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// ModelsResponse is the OpenAI-compatible /v1/models response.
type ModelsResponse struct {
	Object string  `json:"object"`
	Data   []Model `json:"data"`
}

// Model describes a single available model.
type Model struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

func (r *Router) handleChat(w http.ResponseWriter, req *http.Request) {
	var body ChatRequestBody
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if body.Model == "" || len(body.Messages) == 0 {
		http.Error(w, "model and messages required", http.StatusBadRequest)
		return
	}
	eng := r.registry.Resolve(body.Model)
	if eng == nil {
		http.Error(w, "model not found", http.StatusNotFound)
		return
	}
	msgs := make([]llm.Message, len(body.Messages))
	for i, m := range body.Messages {
		msgs[i] = llm.Message{Role: m.Role, Content: m.Content}
	}
	llmReq := llm.ChatRequest{
		Model:       body.Model,
		Messages:    msgs,
		Temperature: body.Temperature,
		MaxTokens:   body.MaxTokens,
	}
	if body.Stream {
		r.streamChat(w, req.Context(), eng, llmReq)
		return
	}
	resp, err := eng.Chat(req.Context(), llmReq)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := ChatResponseBody{
		ID:      "chatcmpl-" + req.Header.Get("X-Request-Id"),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   body.Model,
		Choices: []Choice{{
			Index:        0,
			Message:      ChatMessage{Role: resp.Choices[0].Message.Role, Content: resp.Choices[0].Message.Content},
			FinishReason: resp.Choices[0].FinishReason,
		}},
		Usage: Usage{
			PromptTokens:     resp.Usage.PromptTokens,
			CompletionTokens: resp.Usage.CompletionTokens,
			TotalTokens:      resp.Usage.TotalTokens,
		},
	}
	if out.ID == "chatcmpl-" {
		out.ID = fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// streamChat sends tokens as they are produced (server-sent events).
func (r *Router) streamChat(w http.ResponseWriter, ctx context.Context, eng llm.Engine, req llm.ChatRequest) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	tokens, err := eng.Stream(ctx, req)
	if err != nil {
		fmt.Fprintf(w, "event: error\ndata: %q\n\n", err.Error())
		flusher.Flush()
		return
	}
	for tok := range tokens {
		select {
		case <-ctx.Done():
			return
		default:
		}
		payload := map[string]any{
			"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   req.Model,
			"choices": []map[string]any{{
				"index": 0,
				"delta": map[string]string{"content": tok},
			}},
		}
		b, _ := json.Marshal(payload)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flusher.Flush()
	}
	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}

func (r *Router) handleModels(w http.ResponseWriter, req *http.Request) {
	names := r.registry.Names()
	out := ModelsResponse{Object: "list", Data: make([]Model, 0, len(names))}
	for _, n := range names {
		out.Data = append(out.Data, Model{
			ID: n, Object: "model", Created: time.Now().Unix(), OwnedBy: "minicloud",
		})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// errEngineNotFound is returned when a model has no backend.
var errEngineNotFound = errors.New("inference: engine not found")
