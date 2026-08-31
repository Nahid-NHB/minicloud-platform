// Package llm provides a small LLM inference engine that produces
// "completions" from a chat-style prompt without requiring an external
// model binary. The engine is intentionally tiny so the rest of the
// platform can be tested, benchmarked, and demoed end-to-end.
//
// A production deployment replaces this with llama.cpp or vLLM by
// implementing the same Engine interface and pointing the inference
// router at the upstream server via the `backend` field of ModelSpec.
package llm

import (
	"context"
	"errors"
	"hash/fnv"
	"math/rand/v2"
	"strings"
	"sync"
	"time"
)

// Message is a single chat turn.
type Message struct {
	Role    string
	Content string
}

// ChatRequest is a chat-completion request.
type ChatRequest struct {
	Model       string
	Messages    []Message
	Temperature float64
	MaxTokens   int
	Stream      bool
}

// Choice is a single completion alternative.
type Choice struct {
	Index        int
	Message      Message
	FinishReason string
}

// Response is the chat-completion response.
type Response struct {
	ID      string
	Model   string
	Created time.Time
	Choices []Choice
	Usage   Usage
}

// Usage tracks tokens.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// Engine is the inference backend.
type Engine interface {
	Chat(ctx context.Context, req ChatRequest) (*Response, error)
}

// ---- TinyEngine: deterministic, fast, no external deps ----

// TinyEngine is a small but useful engine. It tokenizes the prompt by
// splitting on whitespace, then generates one token at a time by
// hashing the previous token. The output is reproducible for a given
// model+seed pair which makes the inference API easy to test.
type TinyEngine struct {
	mu    sync.Mutex
	seed  uint64
	vocab []string
}

// NewTinyEngine builds a tiny engine with a small synthetic vocabulary
// so smoke tests, the dashboard, and the OpenAI-compat layer all work
// without a real model.
func NewTinyEngine() *TinyEngine {
	return &TinyEngine{
		seed: uint64(time.Now().UnixNano()),
		vocab: []string{
			"the", "a", "an", "of", "to", "and", "in", "on", "for", "with",
			"cloud", "model", "workload", "node", "service", "deployment",
			"hello", "world", "this", "that", "is", "are", "be", "or", "not",
			"yes", "no", "ok", "ack", "running", "scheduled", "ready", "complete",
			"data", "compute", "storage", "network", "policy", "role", "key",
			"happy", "thanks", "welcome",
		},
	}
}

// Chat produces a response.
func (e *TinyEngine) Chat(ctx context.Context, req ChatRequest) (*Response, error) {
	if req.Model == "" {
		return nil, errors.New("llm: model required")
	}
	last := req.Messages[len(req.Messages)-1]
	prompt := last.Content
	if len(req.Messages) > 1 {
		var b strings.Builder
		for _, m := range req.Messages {
			b.WriteString(m.Role)
			b.WriteString(":")
			b.WriteString(m.Content)
			b.WriteString("\n")
		}
		prompt = b.String()
	}
	promptTokens := len(strings.Fields(prompt))
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 64
	}

	r := newRand(req.Model + ":" + prompt)
	temperature := req.Temperature
	if temperature <= 0 {
		temperature = 1
	}

	out := strings.Builder{}
	for i := 0; i < maxTokens; i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		// Sample a token from vocab deterministically.
		idx := int(r.Uint64()) % len(e.vocab)
		out.WriteString(e.vocab[idx])
		out.WriteString(" ")
		if shouldStop(out.String()) {
			break
		}
	}
	finish := "stop"
	if int(out.Len()) < maxTokens*5 {
		finish = "length"
	}
	resp := &Response{
		ID:      "chatcmpl-" + randID(),
		Model:   req.Model,
		Created: time.Now().UTC(),
		Choices: []Choice{{
			Index:        0,
			Message:      Message{Role: "assistant", Content: strings.TrimSpace(out.String())},
			FinishReason: finish,
		}},
	}
	completionTokens := len(strings.Fields(resp.Choices[0].Message.Content))
	resp.Usage = Usage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      promptTokens + completionTokens,
	}
	_ = temperature
	return resp, nil
}

func shouldStop(s string) bool {
	return strings.Contains(s, "ack") || strings.Contains(s, "complete") || strings.Contains(s, "ready") || strings.Contains(s, "thanks")
}

func newRand(seed string) *rand.Rand {
	h := fnv.New64a()
	_, _ = h.Write([]byte(seed))
	return rand.New(rand.NewPCG(h.Sum64(), h.Sum64()))
}

func randID() string {
	b := make([]byte, 12)
	for i := range b {
		b[i] = byte('a' + rand.IntN(26))
	}
	return string(b)
}

// Compile-time guard.
var _ Engine = (*TinyEngine)(nil)
