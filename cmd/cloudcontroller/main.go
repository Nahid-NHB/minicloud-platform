// Command cloudcontroller runs the reconciliation loop. It watches
// projects, runs the scheduler, and writes desired-state placements
// to the KV. Multiple controllers can run together; leader election
// ensures only one is active at a time.
package main

import (
	"context"
	"flag"
	"log"
	"time"

	db "github.com/minicloud/platform/internal/primitives/db"
	"github.com/minicloud/platform/internal/controller"
	"github.com/minicloud/platform/internal/scheduler"
	"github.com/minicloud/platform/internal/state"
	"github.com/minicloud/platform/internal/util/leader"
)

func main() {
	var (
		dbDir  = flag.String("db", "./data/state", "DB data dir")
		nodeID = flag.String("node-id", "ctrl-1", "Node ID")
		tick   = flag.Duration("tick", 5*time.Second, "reconcile tick")
	)
	flag.Parse()

	ctx := context.Background()
	kv, _, err := db.Open(ctx, db.Config{NodeID: *nodeID, DataDir: *dbDir, Listen: "127.0.0.1:0", Bootstrap: true})
	if err != nil {
		log.Fatal(err)
	}
	store := state.NewStore(kv)
	sched := scheduler.New(store)
	elec := leader.NewElection(leader.Config{NodeID: *nodeID, Group: "controller", KV: kv, TTL: 5 * time.Second, Heartbeat: 1500 * time.Millisecond})
	go elec.Run(ctx)
	rec := controller.New(store, sched, nil, elec)
	log.Printf("cloudcontroller started on %s; tick=%s", *nodeID, *tick)
	t := time.NewTicker(*tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			rec.Run(ctx)
		}
	}
}
