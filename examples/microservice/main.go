// Package main demonstrates deploying a workload via the SDK and
// verifying it via the REST API. Run `cloudinit` first, then
// `go run ./examples/microservice`.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/minicloud/platform/pkg/sdk/go"
)

func main() {
	c := sdk.New("http://127.0.0.1:8080")
	tok, err := c.Login(context.Background(), "admin@minicloud.local", "admin")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("token:", tok)
	if err := c.CreateProject(context.Background(), "demo", "demo project"); err != nil {
		log.Fatal(err)
	}
	if err := c.CreateWorkload(context.Background(), "demo", map[string]any{
		"id":             "hello",
		"image":          "nginx:latest",
		"replicas":       1,
		"cpu_millicores": 100,
		"memory_bytes":   128 * 1024 * 1024,
	}); err != nil {
		log.Fatal(err)
	}
	fmt.Println("workload created")
}
