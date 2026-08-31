// Package main demonstrates a web-app deployment: a service with a
// public port and a workload pointing at the Nginx image. Use the
// CLI to expose it via the LB.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/minicloud/platform/pkg/sdk/go"
)

func main() {
	c := sdk.New("http://127.0.0.1:8080")
	if _, err := c.Login(context.Background(), "admin@minicloud.local", "admin"); err != nil {
		log.Println("login:", err)
	}
	_ = c.Token
	fmt.Println("webapp example: create a project + workload via the SDK")
}
