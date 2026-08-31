// Command cloudstorage runs the platform's S3-compatible object
// store as a stand-alone HTTP service. The actual payload bytes live
// in --root-dir; metadata is held in the KV store.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	db "github.com/minicloud/platform/internal/primitives/db"
	"github.com/minicloud/platform/internal/primitives/obs"
	"github.com/minicloud/platform/internal/state"
	"github.com/minicloud/platform/internal/storage/object"
)

func main() {
	var (
		addr    = flag.String("addr", ":9100", "listen address")
		rootDir = flag.String("root", "./data/objects", "root dir for objects")
		dbDir   = flag.String("db", "./data/state", "DB data dir")
		nodeID  = flag.String("node-id", "store-1", "Node ID")
	)
	flag.Parse()
	ctx := context.Background()
	kv, _, err := db.Open(ctx, db.Config{NodeID: *nodeID, DataDir: *dbDir, Listen: "127.0.0.1:0", Bootstrap: true})
	if err != nil {
		log.Fatal(err)
	}
	store := state.NewStore(kv)
	_ = obs.NewMetrics()
	st, err := object.New(object.Config{Store: store, RootDir: *rootDir})
	if err != nil {
		log.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("cloudstorage ok\n"))
	})
	_ = st
	log.Printf("cloudstorage listening on %s", *addr)
	http.ListenAndServe(*addr, mux)
}
