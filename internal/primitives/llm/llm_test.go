package llm

import (
	"context"
	"strings"
	"testing"
)

func TestTinyEngineChat(t *testing.T) {
	e := NewTinyEngine()
	resp, err := e.Chat(context.Background(), ChatRequest{
		Model:    "test",
		Messages: []Message{{Role: "user", Content: "hello"}},
		MaxTokens: 16,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Model != "test" {
		t.Fatalf("model=%s", resp.Model)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices=%d", len(resp.Choices))
	}
	if resp.Usage.TotalTokens == 0 {
		t.Fatalf("usage=0")
	}
	if strings.TrimSpace(resp.Choices[0].Message.Content) == "" {
		t.Fatalf("empty content")
	}
}

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	r.Register(Model{Name: "foo", Backend: "tiny"}, NewTinyEngine())
	r.Register(Model{Name: "bar", Backend: "tiny"}, NewTinyEngine())
	if len(r.List()) != 2 {
		t.Fatalf("list=%d", len(r.List()))
	}
	if r.Resolve("missing") == nil {
		t.Fatalf("default engine missing")
	}
}
