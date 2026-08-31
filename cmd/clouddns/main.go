// Command clouddns runs the in-cluster DNS server. It serves A
// records for service/workload names registered through the platform
// state store.
package main

import (
	"context"
	"flag"
	"log"

	db "github.com/minicloud/platform/internal/primitives/db"
	"github.com/minicloud/platform/internal/dns"
	"github.com/minicloud/platform/internal/state"
)

func main() {
	var (
		dbDir  = flag.String("db", "./data/state", "DB data dir")
		nodeID = flag.String("node-id", "dns-1", "Node ID")
		addr   = flag.String("addr", ":5353", "DNS UDP listen address")
		domain = flag.String("domain", "cloud.local", "zone")
	)
	flag.Parse()
	ctx := context.Background()
	kv, _, err := db.Open(ctx, db.Config{NodeID: *nodeID, DataDir: *dbDir, Listen: "127.0.0.1:0", Bootstrap: true})
	if err != nil {
		log.Fatal(err)
	}
	store := state.NewStore(kv)
	srv := dns.NewServer(dns.Config{Store: store, Listen: *addr, Zone: *domain})
	log.Printf("clouddns listening on %s for zone %s", *addr, *domain)
	if err := srv.Run(ctx); err != nil {
		log.Fatal(err)
	}
}
