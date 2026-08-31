// Command cloudscheduler is a thin wrapper that exposes the scheduler
// as a service. The scheduler is otherwise invoked directly by the
// cloudcontroller process. This binary is useful for distributed
// deployments where scheduler and controller run as separate
// processes.
package main

import (
	"context"
	"flag"
	"log"

	db "github.com/minicloud/platform/internal/primitives/db"
	"github.com/minicloud/platform/internal/scheduler"
	"github.com/minicloud/platform/internal/state"
)

func main() {
	var (
		dbDir  = flag.String("db", "./data/state", "DB data dir")
		nodeID = flag.String("node-id", "sched-1", "Node ID")
	)
	flag.Parse()
	ctx := context.Background()
	kv, _, err := db.Open(ctx, db.Config{NodeID: *nodeID, DataDir: *dbDir, Listen: "127.0.0.1:0", Bootstrap: true})
	if err != nil {
		log.Fatal(err)
	}
	store := state.NewStore(kv)
	s := scheduler.New(store)
	_ = s
	log.Printf("cloudscheduler ready on %s", *nodeID)
	<-ctx.Done()
}
