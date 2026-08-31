// Package integration contains end-to-end tests that run against a
// fully-wired single-node platform: controller, scheduler, node
// agent, fake runtime, and the REST API. Each test brings up its own
// copy under t.TempDir so suites can be run in parallel.
package integration

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
	"github.com/minicloud/platform/internal/controller"
	"github.com/minicloud/platform/internal/dashboard"
	"github.com/minicloud/platform/internal/identity"
	"github.com/minicloud/platform/internal/inference"
	"github.com/minicloud/platform/internal/primitives/db"
	"github.com/minicloud/platform/internal/primitives/llm"
	"github.com/minicloud/platform/internal/primitives/runtime/fake"
	"github.com/minicloud/platform/internal/scheduler"
	"github.com/minicloud/platform/internal/state"
	"github.com/minicloud/platform/internal/util/leader"
)

// platform wires up the entire single-node control plane in-process.
type platform struct {
	store    *state.Store
	idm      *identity.Manager
	srv      *httptest.Server
	stop     func()
	token    string
	project  string
	rt       *fake.Fake
	rec      *controller.Controller
}

func newPlatform(t *testing.T) *platform {
	t.Helper()
	dir := t.TempDir()
	dbDir := filepath.Join(dir, "kv")
	kv, _, err := db.Open(context.Background(), db.Config{NodeID: "n1", DataDir: dbDir, Listen: "127.0.0.1:0", Bootstrap: true})
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewStore(kv)
	idm := identity.New(store, []byte("test"))
	if _, err := idm.BootstrapAdmin(context.Background(), "admin@x.com", "pw", "Admin"); err != nil {
		t.Fatal(err)
	}
	tok, _, _, err := idm.Login(context.Background(), "admin@x.com", "pw")
	if err != nil {
		t.Fatal(err)
	}
	rt := fake.New()
	sched := scheduler.New(store)
	elec := leader.NewElection(leader.Config{NodeID: "n1", Group: "ctrl", KV: kv, TTL: 5 * time.Second, Heartbeat: 500 * time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	go elec.Run(ctx)
	rec := controller.New(store, sched, nil, elec)
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				rec.Run(ctx)
			}
		}
	}()
	mux := http.NewServeMux()
	api := apis.NewServer(store, idm)
	reg := llm.NewRegistry()
	reg.Register(llm.Model{Name: "echo"}, llm.NewTinyEngine())
	router := inference.NewRouter(reg)
	mux.Handle("/v1/models", router)
	mux.Handle("/v1/chat/completions", router)
	mux.Handle("/dashboard/", http.StripPrefix("/dashboard/", dashboard.Handler()))
	mux.Handle("/", api.Routes())
	srv := httptest.NewServer(mux)
	return &platform{
		store: store, idm: idm, srv: srv,
		stop: cancel, token: tok, project: "p",
		rt: rt, rec: rec,
	}
}

func (p *platform) close() { p.srv.Close(); p.stop() }

func (p *platform) createProject(t *testing.T) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"id": p.project, "description": "test"})
	req, _ := http.NewRequest("POST", p.srv.URL+"/v1/projects", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func (p *platform) createWorkload(t *testing.T, id, image string, replicas int) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"id":             id,
		"image":          image,
		"replicas":       replicas,
		"cpu_millicores": 100,
		"memory_bytes":   1 << 20,
	})
	req, _ := http.NewRequest("POST", p.srv.URL+"/v1/workloads?project_id="+p.project, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusConflict {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("create workload code=%d body=%s", resp.StatusCode, string(b))
	}
}

func TestProjectAndWorkloadEndToEnd(t *testing.T) {
	p := newPlatform(t)
	defer p.close()
	p.createProject(t)
	// Register a node first so the scheduler has somewhere to place.
	p.store.UpsertNode(context.Background(), &state.Node{
		Base:           state.Base{ID: "n1", Name: "n1"},
		Address:        "127.0.0.1",
		Status:         "Ready",
		CPUAllocatable: 8000,
		MemAllocatable: 16 << 30,
	})
	p.createWorkload(t, "w1", "nginx:latest", 1)
	// Give the controller a moment to write a placement.
	time.Sleep(3500 * time.Millisecond)
	ws, err := p.store.ListWorkloads(context.Background(), p.project)
	if err != nil {
		t.Fatal(err)
	}
	if len(ws) != 1 {
		t.Fatalf("expected 1 workload, got %d", len(ws))
	}
	ps, _ := p.store.GetPlacements(context.Background(), ws[0].ID)
	if len(ps) == 0 {
		t.Fatalf("no placements yet")
	}
}

func TestLoginAndAuth(t *testing.T) {
	p := newPlatform(t)
	defer p.close()
	req, _ := http.NewRequest("GET", p.srv.URL+"/v1/projects", nil)
	resp, _ := http.DefaultClient.Do(req)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestInferenceEndToEnd(t *testing.T) {
	p := newPlatform(t)
	defer p.close()
	body, _ := json.Marshal(map[string]any{
		"model":    "echo",
		"messages": []map[string]string{{"role": "user", "content": "hi"}},
	})
	resp, err := http.Post(p.srv.URL+"/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("code=%d", resp.StatusCode)
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Choices) == 0 {
		t.Fatal("no choices")
	}
}

func TestDashboardServed(t *testing.T) {
	p := newPlatform(t)
	defer p.close()
	resp, err := http.Get(p.srv.URL + "/dashboard/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("dashboard code=%d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(b, []byte("Mini Cloud Platform")) {
		t.Fatalf("dashboard missing title")
	}
}
