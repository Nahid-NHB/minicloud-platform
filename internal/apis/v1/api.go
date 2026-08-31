// Package apis implements the v1 HTTP API for the Mini Cloud Platform.
//
// The server exposes a small set of resource-oriented routes
// (workloads, services, networks, etc.) using only the standard
// library. Authentication is performed by the identity package's
// Manager, which accepts bearer tokens or session cookies.
package apis

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"

	"github.com/minicloud/platform/internal/identity"
	"github.com/minicloud/platform/internal/state"
)

// Server is the v1 HTTP API.
type Server struct {
	store *state.Store
	idm   *identity.Manager

	mu      sync.Mutex
	started bool
}

// NewServer builds a Server.
func NewServer(store *state.Store, idm *identity.Manager) *Server {
	return &Server{store: store, idm: idm}
}

// Routes returns the configured http.Handler for the API.
func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	// Identity
	mux.HandleFunc("/v1/login", s.handleLogin)
	mux.HandleFunc("/v1/users", s.authRequired(s.handleUsers))
	mux.HandleFunc("/v1/apikeys", s.authRequired(s.handleAPIKeys))
	mux.HandleFunc("/v1/projects", s.authRequired(s.handleProjects))

	// Compute / data plane
	mux.HandleFunc("/v1/workloads", s.authRequired(s.handleWorkloads))
	mux.HandleFunc("/v1/services", s.authRequired(s.handleServices))
	mux.HandleFunc("/v1/deployments", s.authRequired(s.handleDeployments))
	mux.HandleFunc("/v1/networks", s.authRequired(s.handleNetworks))
	mux.HandleFunc("/v1/volumes", s.authRequired(s.handleVolumes))
	mux.HandleFunc("/v1/secrets", s.authRequired(s.handleSecrets))
	mux.HandleFunc("/v1/configmaps", s.authRequired(s.handleConfigMaps))
	mux.HandleFunc("/v1/nodes", s.authRequired(s.handleNodes))

	// Object storage
	mux.HandleFunc("/v1/buckets", s.authRequired(s.handleBuckets))
	mux.HandleFunc("/v1/buckets/", s.authRequired(s.handleObjects))

	// Observability
	mux.HandleFunc("/v1/metrics", s.handleMetrics)
	mux.HandleFunc("/v1/logs", s.authRequired(s.handleLogs))
	mux.HandleFunc("/v1/alerts", s.authRequired(s.handleAlerts))

	// AI / inference
	mux.HandleFunc("/v1/models", s.authRequired(s.handleModels))

	// Health
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	return mux
}

// ---- shared helpers ----

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func (s *Server) authRequired(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := bearer(r)
		if tok == "" {
			http.Error(w, "missing token", http.StatusUnauthorized)
			return
		}
		u, err := s.idm.VerifyToken(context.Background(), tok)
		if err == nil {
			// token authenticated
		} else {
			k, err2 := s.idm.AuthAPIKey(context.Background(), tok)
			if err2 != nil {
				http.Error(w, err.Error(), http.StatusUnauthorized)
				return
			}
			u = &state.User{}
			u.ID = k.ID
		}
		if s.idm.Authorize(u, verbFor(r.Method), resourceFor(r)) != nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeyUser{}, u)
		h(w, r.WithContext(ctx))
	}
}

type ctxKeyUser struct{}

func userFromCtx(ctx context.Context) *state.User {
	if v, ok := ctx.Value(ctxKeyUser{}).(*state.User); ok {
		return v
	}
	return nil
}

func verbFor(method string) string {
	switch method {
	case http.MethodGet:
		return "get"
	case http.MethodPost:
		return "create"
	case http.MethodPut:
		return "update"
	case http.MethodDelete:
		return "delete"
	default:
		return "get"
	}
}

func resourceFor(r *http.Request) string {
	p := r.URL.Path
	parts := strings.Split(strings.Trim(p, "/"), "/")
	if len(parts) < 3 {
		return p
	}
	return strings.TrimPrefix(p, "/v1/")
}

func bearer(r *http.Request) string {
	a := r.Header.Get("Authorization")
	if strings.HasPrefix(a, "Bearer ") {
		return strings.TrimPrefix(a, "Bearer ")
	}
	if c, err := r.Cookie("cloud_session"); err == nil {
		return c.Value
	}
	return ""
}

type ctxKeyIdent struct{}

var _ = ctxKeyIdent{}

// ---- Identity handlers ----

type loginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body loginReq
	if err := readJSON(r, &body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	tok, _, _, err := s.idm.Login(context.Background(), body.Email, body.Password)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": tok})
}

type userReq struct {
	Email       string `json:"email"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
	Admin       bool   `json:"admin"`
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		users, err := s.store.ListUsers(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, users)
	case http.MethodPost:
		var body userReq
		if err := readJSON(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.Email == "" || body.Password == "" {
			http.Error(w, "email/password required", http.StatusBadRequest)
			return
		}
		u, err := s.idm.CreateUser(r.Context(), body.Email, body.Password, body.DisplayName, body.Admin)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusCreated, u)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type apiKeyReq struct {
	UserID    string `json:"user_id"`
	ProjectID string `json:"project_id"`
	Name      string `json:"name"`
}

func (s *Server) handleAPIKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		keys, _ := s.store.ListAPIKeys(r.Context())
		writeJSON(w, http.StatusOK, keys)
	case http.MethodPost:
		var body apiKeyReq
		if err := readJSON(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		k, secret, err := s.idm.IssueAPIKey(r.Context(), body.ProjectID, body.Name, 0)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"id": k.ID, "secret": secret})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type projectReq struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	QuotaCPU    int64  `json:"quota_cpu_millicores"`
	QuotaMem    int64  `json:"quota_mem_bytes"`
	QuotaObj    int64  `json:"quota_object_bytes"`
}

func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		ps, _ := s.store.ListProjects(r.Context())
		writeJSON(w, http.StatusOK, ps)
	case http.MethodPost:
		var body projectReq
		if err := readJSON(r, &body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if body.ID == "" {
			http.Error(w, "id required", http.StatusBadRequest)
			return
		}
		p := &state.Project{
			Base:        state.Base{ID: body.ID, Name: body.ID},
			Description: body.Description,
			QuotaCPU:    body.QuotaCPU,
			QuotaMem:    body.QuotaMem,
			QuotaObj:    body.QuotaObj,
		}
		if err := s.store.CreateProject(r.Context(), p); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, http.StatusCreated, p)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ---- Resource handlers (typed wrappers) ----

func (s *Server) handleWorkloads(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	if projectID == "" {
		http.Error(w, "project_id required", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		ws, err := s.store.ListWorkloads(r.Context(), projectID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, ws)
	case http.MethodPost:
		var w0 state.Workload
		if err := readJSON(r, &w0); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w0.ProjectID = projectID
		if w0.ID == "" {
			http.Error(w, "id required", http.StatusBadRequest)
			return
		}
		w0.Name = w0.ID
		if err := s.store.CreateWorkload(r.Context(), &w0); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, http.StatusCreated, &w0)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleServices(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	if projectID == "" {
		http.Error(w, "project_id required", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		xs, _ := s.store.ListServices(r.Context(), projectID)
		writeJSON(w, http.StatusOK, xs)
	case http.MethodPost:
		var svc state.Service
		if err := readJSON(r, &svc); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		svc.ProjectID = projectID
		if svc.ID == "" {
			http.Error(w, "id required", http.StatusBadRequest)
			return
		}
		svc.Name = svc.ID
		if err := s.store.CreateService(r.Context(), &svc); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, http.StatusCreated, &svc)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleDeployments(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	if projectID == "" {
		http.Error(w, "project_id required", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		xs, _ := s.store.ListDeployments(r.Context(), projectID)
		writeJSON(w, http.StatusOK, xs)
	case http.MethodPost:
		var d state.Deployment
		if err := readJSON(r, &d); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		d.ProjectID = projectID
		if d.ID == "" {
			http.Error(w, "id required", http.StatusBadRequest)
			return
		}
		d.Name = d.ID
		if err := s.store.CreateDeployment(r.Context(), &d); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, http.StatusCreated, &d)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleNetworks(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	if projectID == "" {
		http.Error(w, "project_id required", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		xs, _ := s.store.ListNetworks(r.Context(), projectID)
		writeJSON(w, http.StatusOK, xs)
	case http.MethodPost:
		var n state.Network
		if err := readJSON(r, &n); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		n.ProjectID = projectID
		if n.ID == "" {
			http.Error(w, "id required", http.StatusBadRequest)
			return
		}
		n.Name = n.ID
		if err := s.store.CreateNetwork(r.Context(), &n); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, http.StatusCreated, &n)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleVolumes(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	if projectID == "" {
		http.Error(w, "project_id required", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		xs, _ := s.store.ListVolumes(r.Context(), projectID)
		writeJSON(w, http.StatusOK, xs)
	case http.MethodPost:
		var v state.Volume
		if err := readJSON(r, &v); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		v.ProjectID = projectID
		if v.ID == "" {
			http.Error(w, "id required", http.StatusBadRequest)
			return
		}
		v.Name = v.ID
		if err := s.store.CreateVolume(r.Context(), &v); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, http.StatusCreated, &v)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleSecrets(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	if projectID == "" {
		http.Error(w, "project_id required", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		xs, _ := s.store.ListSecrets(r.Context(), projectID)
		writeJSON(w, http.StatusOK, xs)
	case http.MethodPost:
		var sec state.Secret
		if err := readJSON(r, &sec); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		sec.ProjectID = projectID
		if sec.ID == "" {
			http.Error(w, "id required", http.StatusBadRequest)
			return
		}
		sec.Name = sec.ID
		if err := s.store.CreateSecret(r.Context(), &sec); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, http.StatusCreated, &sec)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleConfigMaps(w http.ResponseWriter, r *http.Request) {
	projectID := r.URL.Query().Get("project_id")
	if projectID == "" {
		http.Error(w, "project_id required", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodGet:
		xs, _ := s.store.ListConfigMaps(r.Context(), projectID)
		writeJSON(w, http.StatusOK, xs)
	case http.MethodPost:
		var cm state.ConfigMap
		if err := readJSON(r, &cm); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		cm.ProjectID = projectID
		if cm.ID == "" {
			http.Error(w, "id required", http.StatusBadRequest)
			return
		}
		cm.Name = cm.ID
		if err := s.store.CreateConfigMap(r.Context(), &cm); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, http.StatusCreated, &cm)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		xs, _ := s.store.ListNodes(r.Context())
		writeJSON(w, http.StatusOK, xs)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleBuckets(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		xs, _ := s.store.ListBuckets(r.Context())
		writeJSON(w, http.StatusOK, xs)
	case http.MethodPost:
		var b state.Bucket
		if err := readJSON(r, &b); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if b.ID == "" {
			http.Error(w, "id required", http.StatusBadRequest)
			return
		}
		b.Name = b.ID
		if err := s.store.CreateBucket(r.Context(), &b); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, http.StatusCreated, &b)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// handleObjects handles /v1/buckets/{bucket}/{key...} PUT/GET/DELETE.
func (s *Server) handleObjects(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/v1/buckets/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) < 2 {
		http.Error(w, "bucket/key required", http.StatusBadRequest)
		return
	}
	bucket, key := parts[0], parts[1]
	switch r.Method {
	case http.MethodGet:
		obj, err := s.store.GetObject(r.Context(), bucket, key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		writeJSON(w, http.StatusOK, obj)
	case http.MethodDelete:
		if err := s.store.DeleteObject(r.Context(), bucket, key); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	_, _ = w.Write([]byte("# metrics are exposed by /v1/metrics/exposition\n"))
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_, _ = w.Write([]byte("[]"))
}

func (s *Server) handleAlerts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		xs, _ := s.store.ListAlerts(r.Context())
		writeJSON(w, http.StatusOK, xs)
	case http.MethodPost:
		var a state.Alert
		if err := readJSON(r, &a); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if a.ID == "" {
			http.Error(w, "id required", http.StatusBadRequest)
			return
		}
		a.Name = a.ID
		if err := s.store.CreateAlert(r.Context(), &a); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		writeJSON(w, http.StatusCreated, &a)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	xs, _ := s.store.ListModels(r.Context())
	writeJSON(w, http.StatusOK, xs)
}

// ensure imports stay referenced.
var _ identity.Manager
