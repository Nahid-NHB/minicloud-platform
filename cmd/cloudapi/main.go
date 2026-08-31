// Command cloudapi is the unified control-plane API server for the
// Mini Cloud Platform. It exposes the REST API under /v1/* and the
// OpenAI-compatible inference API under /v1/* alongside it.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/minicloud/platform/internal/apis/v1"
	"github.com/minicloud/platform/internal/identity"
	"github.com/minicloud/platform/internal/inference"
	"github.com/minicloud/platform/internal/primitives/db"
	"github.com/minicloud/platform/internal/primitives/llm"
	"github.com/minicloud/platform/internal/state"
	"github.com/minicloud/platform/internal/storage/object"
)

func main() {
	var (
		addr     = flag.String("addr", ":8080", "HTTP listen address")
		dbDir    = flag.String("db", "./data/state", "DB data directory")
		nodeID   = flag.String("node-id", "api-1", "Node ID")
		admin    = flag.String("admin-email", "admin@minicloud.local", "Bootstrap admin email")
		adminPw  = flag.String("admin-password", "admin", "Bootstrap admin password")
		hmacKey  = flag.String("hmac", "dev-hmac-key", "HMAC key for presigned URLs")
	)
	flag.Parse()

	if err := os.MkdirAll(*dbDir, 0o755); err != nil {
		log.Fatal(err)
	}
	ctx := context.Background()
	kv, _, err := db.Open(ctx, db.Config{NodeID: *nodeID, DataDir: *dbDir, Listen: "127.0.0.1:0", Bootstrap: true})
	if err != nil {
		log.Fatal(err)
	}
	store := state.NewStore(kv)
	idm := identity.New(store, []byte(*hmacKey))
	if _, err := idm.BootstrapAdmin(ctx, *admin, *adminPw, "Bootstrap Admin"); err != nil {
		log.Printf("bootstrap admin: %v", err)
	}

	if _, err := object.New(object.Config{Store: store, RootDir: "./data/objects", HMACKey: []byte(*hmacKey)}); err != nil {
		log.Fatal(err)
	}

	reg := llm.NewRegistry()
	reg.Register(llm.Model{Name: "echo"}, llm.NewTinyEngine())
	reg.Register(llm.Model{Name: "minicloud"}, llm.NewTinyEngine())
	router := inference.NewRouter(reg)

	mux := http.NewServeMux()
	srv := apis.NewServer(store, idm)
	mux.Handle("/v1/", srv.Routes())
	mux.Handle("/v1/models", router)
	mux.Handle("/v1/chat/completions", router)
	mux.Handle("/", srv.Routes())

	log.Printf("cloudapi listening on %s", *addr)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}
