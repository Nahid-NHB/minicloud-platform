// Command cloudnode is the per-host node agent. It runs the local
// container runtime, sends heartbeats to the control plane, and
// applies the placements assigned to it.
package main

import (
	"context"
	"flag"
	"log"
	"time"

	db "github.com/minicloud/platform/internal/primitives/db"
	"github.com/minicloud/platform/internal/nodeagent"
	"github.com/minicloud/platform/internal/primitives/runtime/fake"
	"github.com/minicloud/platform/internal/state"
)

func main() {
	var (
		dbDir  = flag.String("db", "./data/state", "DB data dir")
		nodeID = flag.String("node-id", "node-1", "Node ID")
		addr   = flag.String("addr", "127.0.0.1", "Node address")
	)
	flag.Parse()

	ctx := context.Background()
	kv, _, err := db.Open(ctx, db.Config{NodeID: *nodeID, DataDir: *dbDir, Listen: "127.0.0.1:0", Bootstrap: true})
	if err != nil {
		log.Fatal(err)
	}
	store := state.NewStore(kv)
	rt := fake.New()
	a := &nodeagent.Agent{
		NodeID:  *nodeID,
		Address: *addr,
		Store:   store,
		Runtime: rt,
		C:       &nodeagent.StaticCollector{},
	}
	log.Printf("cloudnode %s running on %s", *nodeID, *addr)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(nodeagent.HeartbeatInterval):
			a.Run(ctx)
		}
	}
}
