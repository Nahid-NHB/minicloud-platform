// Package main demonstrates an AI workload: a model-backed workload
// that the platform's inference router can serve from any node.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

func main() {
	body, _ := json.Marshal(map[string]any{
		"model":    "echo",
		"messages": []map[string]string{{"role": "user", "content": "hello from example"}},
	})
	resp, err := http.Post("http://127.0.0.1:8080/v1/chat/completions", "application/json", bytes.NewReader(body))
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		log.Fatalf("status=%d", resp.StatusCode)
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	choices := out["choices"].([]any)
	if len(choices) == 0 {
		log.Fatal("no choices")
	}
	c := choices[0].(map[string]any)["message"].(map[string]any)["content"].(string)
	fmt.Println(c)
}
