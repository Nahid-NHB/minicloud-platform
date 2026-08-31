package apis

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	db "github.com/minicloud/platform/internal/primitives/db"
	"github.com/minicloud/platform/internal/identity"
	"github.com/minicloud/platform/internal/state"
)

func newAPI(t *testing.T) (*Server, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "kv")
	kv, _, err := db.Open(context.Background(), db.Config{NodeID: "n", DataDir: dir, Listen: "127.0.0.1:0", Bootstrap: true})
	if err != nil {
		t.Fatal(err)
	}
	st := state.NewStore(kv)
	idm := identity.New(st, []byte("test"))
	_, err = idm.BootstrapAdmin(context.Background(), "admin@x.com", "pw", "Admin")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(st, idm)
	tok, _, _, err := idm.Login(context.Background(), "admin@x.com", "pw")
	if err != nil {
		t.Fatal(err)
	}
	return srv, tok
}

func do(t *testing.T, srv *Server, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, bytes.NewReader([]byte(body)))
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	srv.Routes().ServeHTTP(w, r)
	return w
}

func TestLoginAndCreateProject(t *testing.T) {
	srv, tok := newAPI(t)
	w := do(t, srv, http.MethodPost, "/v1/projects", tok, `{"id":"p","description":"hi"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	w = do(t, srv, http.MethodGet, "/v1/projects", tok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var ps []*state.Project
	json.NewDecoder(w.Body).Decode(&ps)
	if len(ps) != 1 || ps[0].ID != "p" {
		t.Fatalf("got %+v", ps)
	}
}

func TestUnauthenticated(t *testing.T) {
	srv, _ := newAPI(t)
	w := do(t, srv, http.MethodGet, "/v1/projects", "", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
}

func TestWorkloadCRUD(t *testing.T) {
	srv, tok := newAPI(t)
	do(t, srv, http.MethodPost, "/v1/projects", tok, `{"id":"p"}`)
	w := do(t, srv, http.MethodPost, "/v1/workloads?project_id=p", tok, `{"id":"w1","image":"nginx","replicas":1,"cpu_millicores":100,"memory_bytes":1000}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	w = do(t, srv, http.MethodGet, "/v1/workloads?project_id=p", tok, "")
	if w.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	var ws []*state.Workload
	json.NewDecoder(w.Body).Decode(&ws)
	if len(ws) != 1 || ws[0].Image != "nginx" {
		t.Fatalf("got %+v", ws)
	}
}
