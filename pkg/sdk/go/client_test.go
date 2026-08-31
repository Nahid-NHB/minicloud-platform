package sdk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLoginAndWorkload(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/login" && r.Method == http.MethodPost:
			json.NewEncoder(w).Encode(map[string]string{"token": "abc"})
		case r.URL.Path == "/v1/projects" && r.Method == http.MethodPost:
			w.WriteHeader(201)
		case r.URL.Path == "/v1/workloads" && r.Method == http.MethodPost:
			w.WriteHeader(201)
		case r.URL.Path == "/v1/workloads" && r.Method == http.MethodGet:
			w.Write([]byte(`[{"id":"w1"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := New(srv.URL)
	tok, err := c.Login(context.Background(), "a@b", "pw")
	if err != nil || tok != "abc" {
		t.Fatalf("login: err=%v tok=%s", err, tok)
	}
	if err := c.CreateProject(context.Background(), "p1", ""); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateWorkload(context.Background(), "p1", map[string]any{"id": "w1", "image": "nginx"}); err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	if err := c.ListWorkloads(context.Background(), "p1", &out); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0]["id"] != "w1" {
		t.Fatalf("got %+v", out)
	}
}
