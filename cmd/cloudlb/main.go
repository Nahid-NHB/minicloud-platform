// Command cloudlb runs an L4 load balancer in front of a service.
// It watches placements from the state store and forwards TCP
// traffic to the backing workloads using round-robin selection.
package main

import (
	"context"
	"flag"
	"log"
	"time"

	db "github.com/minicloud/platform/internal/primitives/db"
	"github.com/minicloud/platform/internal/lb"
	"github.com/minicloud/platform/internal/state"
)

func main() {
	var (
		dbDir   = flag.String("db", "./data/state", "DB data dir")
		nodeID  = flag.String("node-id", "lb-1", "Node ID")
		listen  = flag.String("listen", ":8080", "LB listen address")
		service = flag.String("service", "", "Service ID to load balance")
	)
	flag.Parse()
	ctx := context.Background()
	kv, _, err := db.Open(ctx, db.Config{NodeID: *nodeID, DataDir: *dbDir, Listen: "127.0.0.1:0", Bootstrap: true})
	if err != nil {
		log.Fatal(err)
	}
	store := state.NewStore(kv)
	b := lb.NewBalancer(lb.Config{Store: store, Service: *service, Strategy: lb.StrategyRoundRobin})
	go b.Run(ctx)
	log.Printf("cloudlb listening on %s for service %s", *listen, *service)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Hour):
		}
	}
}
