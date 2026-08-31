// Command cloudllm runs a stand-alone LLM inference server. It
// exposes the platform's OpenAI-compatible HTTP API and serves
// requests using the LLM registry's registered engines.
package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	"github.com/minicloud/platform/internal/inference"
	"github.com/minicloud/platform/internal/primitives/llm"
)

func main() {
	var (
		addr = flag.String("addr", ":9000", "listen address")
	)
	flag.Parse()
	reg := llm.NewRegistry()
	reg.Register(llm.Model{Name: "echo"}, llm.NewTinyEngine())
	reg.Register(llm.Model{Name: "minicloud"}, llm.NewTinyEngine())
	router := inference.NewRouter(reg)
	log.Printf("cloudllm listening on %s", *addr)
	if err := http.ListenAndServe(*addr, router); err != nil {
		log.Fatal(err)
	}
	_ = context.Background()
}
