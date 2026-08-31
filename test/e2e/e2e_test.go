// Package e2e runs the platform end-to-end against a real CLI
// invocation. It stands up the cloudinit server in-process, then
// calls the same REST endpoints the cloudctl CLI would hit.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/minicloud/platform/internal/apis/v1"
	"github.com/minicloud/platform/internal/identity"
	"github.com/minicloud/platform/internal/inference"
	"github.com/minicloud/platform/internal/primitives/db"
	"github.com/minicloud/platform/internal/primitives/llm"
	"github.com/minicloud/platform/internal/state"
)

func bootServer(t *testing.T) (string, *state.Store, func()) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "kv")
	kv, _, err := db.Open(context.Background(), db.Config{NodeID: "n1", DataDir: dir, Listen: "127.0.0.1:0", Bootstrap: true})
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(kv)
	idm := identity.New(store, []byte("test"))
	if _, err := idm.BootstrapAdmin(context.Background(), "admin@x.com", "pw", "Admin"); err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	api := apis.NewServer(store, idm)
	reg := llm.NewRegistry()
	reg.Register(llm.Model{Name: "echo"}, llm.NewTinyEngine())
	router := inference.NewRouter(reg)
	mux.Handle("/v1/models", router)
	mux.Handle("/v1/chat/completions", router)
	mux.Handle("/", api.Routes())
	srv := httptest.NewServer(mux)
	return srv.URL, store, srv.Close
}

func postJSON(t *testing.T, url, token string, body any) (int, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func getJSON(t *testing.T, url, token string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, out
}

func TestLogin(t *testing.T) {
	base, _, stop := bootServer(t)
	defer stop()
	body := map[string]string{"email": "admin@x.com", "password": "pw"}
	b, _ := json.Marshal(body)
	resp, err := http.Post(base+"/v1/login", "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("login: %d", resp.StatusCode)
	}
	var out map[string]string
	json.NewDecoder(resp.Body).Decode(&out)
	if out["token"] == "" {
		t.Fatal("no token")
	}
}

func TestFullLifecycle(t *testing.T) {
	base, store, stop := bootServer(t)
	defer stop()

	// Login
	b, _ := json.Marshal(map[string]string{"email": "admin@x.com", "password": "pw"})
	resp, _ := http.Post(base+"/v1/login", "application/json", bytes.NewReader(b))
	var lr struct{ Token string }
	json.NewDecoder(resp.Body).Decode(&lr)
	resp.Body.Close()
	tok := lr.Token

	// Create project + workload
	status, _ := postJSON(t, base+"/v1/projects", tok, map[string]any{"id": "p1"})
	if status != 201 {
		t.Fatalf("project: %d", status)
	}
	status, _ = postJSON(t, base+"/v1/workloads?project_id=p1", tok, map[string]any{
		"id": "w1", "image": "nginx", "replicas": 1, "cpu_millicores": 100, "memory_bytes": 1 << 20,
	})
	if status != 201 {
		t.Fatalf("workload: %d", status)
	}
	// Wait briefly for any async effect, then list.
	time.Sleep(200 * time.Millisecond)
	status, b2 := getJSON(t, base+"/v1/workloads?project_id=p1", tok)
	if status != 200 {
		t.Fatalf("list: %d %s", status, string(b2))
	}
	var ws []*state.Workload
	json.Unmarshal(b2, &ws)
	if len(ws) != 1 {
		t.Fatalf("got %d workloads", len(ws))
	}
	_ = store
}

func TestChatCompletions(t *testing.T) {
	base, _, stop := bootServer(t)
	defer stop()
	body, _ := json.Marshal(map[string]any{
		"model":    "echo",
		"messages": []map[string]string{{"role": "user", "content": "ping"}},
	})
	resp, err := http.Post(base+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("chat code=%d", resp.StatusCode)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Choices) == 0 {
		t.Fatal("no choices")
	}
}
