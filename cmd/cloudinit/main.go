// Command cloudinit bootstraps a single-node Mini Cloud Platform for
// local development. It starts a single embedded database, runs the
// controller loop, and launches the cloudapi HTTP server on the
// requested address.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/minicloud/platform/internal/apis/v1"
	"github.com/minicloud/platform/internal/controller"
	"github.com/minicloud/platform/internal/dashboard"
	"github.com/minicloud/platform/internal/identity"
	"github.com/minicloud/platform/internal/inference"
	"github.com/minicloud/platform/internal/primitives/db"
	"github.com/minicloud/platform/internal/primitives/llm"
	"github.com/minicloud/platform/internal/scheduler"
	"github.com/minicloud/platform/internal/state"
	"github.com/minicloud/platform/internal/storage/object"
	"github.com/minicloud/platform/internal/util/leader"
)

func main() {
	var (
		addr    = flag.String("addr", ":8080", "API listen address")
		dbDir   = flag.String("db", "./data/state", "DB data dir")
		objDir  = flag.String("objects", "./data/objects", "object store root")
		admin   = flag.String("admin-email", "admin@minicloud.local", "bootstrap admin email")
		adminPw = flag.String("admin-password", "admin", "bootstrap admin password")
	)
	flag.Parse()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	kv, _, err := db.Open(ctx, db.Config{NodeID: "init-1", DataDir: *dbDir, Listen: "127.0.0.1:0", Bootstrap: true})
	if err != nil {
		log.Fatal(err)
	}
	store := state.NewStore(kv)
	idm := identity.New(store, []byte("devkey"))
	if _, err := idm.BootstrapAdmin(ctx, *admin, *adminPw, "Bootstrap Admin"); err != nil {
		log.Printf("admin bootstrap: %v", err)
	}
	_, _ = object.New(object.Config{Store: store, RootDir: *objDir, HMACKey: []byte("devkey")})
	sched := scheduler.New(store)
	elec := leader.NewElection(leader.Config{NodeID: "init-1", Group: "controller", KV: kv, TTL: 5 * time.Second, Heartbeat: 1500 * time.Millisecond})
	go elec.Run(ctx)
	rec := controller.New(store, sched, nil, elec)
	go func() {
		t := time.NewTicker(2 * time.Second)
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
	reg := llm.NewRegistry()
	reg.Register(llm.Model{Name: "echo"}, llm.NewTinyEngine())
	reg.Register(llm.Model{Name: "minicloud"}, llm.NewTinyEngine())
	mux := http.NewServeMux()
	srv := apis.NewServer(store, idm)
	router := inference.NewRouter(reg)
	mux.Handle("/v1/models", router)
	mux.Handle("/v1/chat/completions", router)
	mux.Handle("/dashboard/", http.StripPrefix("/dashboard/", dashboard.Handler()))
	mux.Handle("/", srv.Routes())
	log.Printf("cloudinit listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
