// Command cloudctl is the command-line interface for the Mini Cloud
// Platform. It talks to cloudapi over HTTP and accepts subcommands
// for users, projects, workloads, services, networks, volumes,
// secrets, configs, nodes, buckets, objects, models, and chat.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

var (
	apiURL = flag.String("api", "http://127.0.0.1:8080", "API server URL")
	token  = flag.String("token", "", "Bearer token (or set CLOUDCTL_TOKEN)")
)

func main() {
	if t := os.Getenv("CLOUDCTL_TOKEN"); t != "" && *token == "" {
		*token = t
	}
	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(1)
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "user":
		userCmd(rest)
	case "project":
		projectCmd(rest)
	case "workload", "run":
		workloadCmd(rest)
	case "service":
		serviceCmd(rest)
	case "deployment":
		deploymentCmd(rest)
	case "network":
		networkCmd(rest)
	case "volume":
		volumeCmd(rest)
	case "secret":
		secretCmd(rest)
	case "configmap", "cm":
		configmapCmd(rest)
	case "bucket":
		bucketCmd(rest)
	case "node":
		nodeCmd(rest)
	case "model":
		modelCmd(rest)
	case "chat":
		chatCmd(rest)
	case "login":
		loginCmd(rest)
	default:
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `cloudctl — Mini Cloud Platform CLI

Usage:
  cloudctl [-api URL] [-token TOKEN] <command> [args]

Commands:
  login <email> <password>
  user create <email> <password> [--display NAME] [--admin]
  project list | create <id>
  workload list --project_id=P | create --project_id=P --id=I --image=IMG
  service list | create (json) --project_id=P
  deployment list | create (json) --project_id=P
  network list | create (json) --project_id=P
  volume list | create (json) --project_id=P
  secret list | create (json) --project_id=P
  configmap list | create (json) --project_id=P
  bucket list | create <id>
  object put <bucket> <key> <file>
  object get <bucket> <key>
  node list
  model list
  chat --model=M 'hello world'`)
}

func call(method, path string, body any) (int, []byte) {
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, *apiURL+path, rdr)
	if *token != "" {
		req.Header.Set("Authorization", "Bearer "+*token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERR", err)
		return 0, nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func loginCmd(args []string) {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: cloudctl login <email> <password>")
		os.Exit(2)
	}
	body, _ := json.Marshal(map[string]string{"email": args[0], "password": args[1]})
	req, _ := http.NewRequest("POST", *apiURL+"/v1/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ERR", err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		fmt.Fprintln(os.Stderr, "login failed:", string(b))
		os.Exit(1)
	}
	var out map[string]string
	json.NewDecoder(resp.Body).Decode(&out)
	fmt.Println(out["token"])
}

func userCmd(args []string) {
	if len(args) < 1 {
		usage()
		os.Exit(2)
	}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("user create", flag.ExitOnError)
		display := fs.String("display", "", "display name")
		admin := fs.Bool("admin", false, "make admin")
		fs.Parse(args[1:])
		rest := fs.Args()
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "usage: cloudctl user create <email> <password>")
			os.Exit(2)
		}
		status, b := call("POST", "/v1/users", map[string]any{
			"email":        rest[0],
			"password":     rest[1],
			"display_name": *display,
			"admin":        *admin,
		})
		fmt.Println(status, string(b))
	default:
		usage()
	}
}

func projectCmd(args []string) {
	switch args[0] {
	case "list":
		status, b := call("GET", "/v1/projects", nil)
		check(status, b)
		pretty(b)
	case "create":
		if len(args) < 2 {
			usage()
			os.Exit(2)
		}
		body := map[string]any{"id": args[1], "description": args[1]}
		status, b := call("POST", "/v1/projects", body)
		fmt.Println(status, string(b))
	}
}

func workloadCmd(args []string) {
	fs := flag.NewFlagSet("workload", flag.ExitOnError)
	projectID := fs.String("project_id", "", "project id")
	id := fs.String("id", "", "workload id")
	image := fs.String("image", "nginx:latest", "container image")
	replicas := fs.Int("replicas", 1, "replicas")
	cpu := fs.Int64("cpu", 100, "cpu millicores")
	mem := fs.Int64("mem", 128*1024*1024, "memory bytes")
	port := fs.Int("port", 80, "container port")
	model := fs.String("model", "", "inference model name (AI workload)")
	fs.Parse(args)
	rest := fs.Args()
	if len(rest) == 0 {
		usage()
		os.Exit(2)
	}
	switch rest[0] {
	case "list":
		status, b := call("GET", "/v1/workloads?project_id="+*projectID, nil)
		check(status, b)
		pretty(b)
	case "create":
		w := map[string]any{
			"id":               *id,
			"kind":             "ai",
			"image":            *image,
			"replicas":         *replicas,
			"cpu_millicores":   *cpu,
			"memory_bytes":     *mem,
			"port":             *port,
			"ai_model":         *model,
		}
		status, b := call("POST", "/v1/workloads?project_id="+*projectID, w)
		fmt.Println(status, string(b))
	default:
		usage()
	}
}

func serviceCmd(args []string) {
	fs := flag.NewFlagSet("svc", flag.ExitOnError)
	projectID := fs.String("project_id", "", "project id")
	fs.Parse(args)
	rest := fs.Args()
	if len(rest) == 0 {
		usage()
		os.Exit(2)
	}
	switch rest[0] {
	case "list":
		status, b := call("GET", "/v1/services?project_id="+*projectID, nil)
		check(status, b)
		pretty(b)
	case "create":
		body := map[string]any{"id": rest[1], "port": 80, "workload_id": ""}
		status, b := call("POST", "/v1/services?project_id="+*projectID, body)
		fmt.Println(status, string(b))
	}
}

func deploymentCmd(args []string) {
	fs := flag.NewFlagSet("dep", flag.ExitOnError)
	projectID := fs.String("project_id", "", "project id")
	fs.Parse(args)
	rest := fs.Args()
	if len(rest) < 1 {
		usage()
		os.Exit(2)
	}
	switch rest[0] {
	case "list":
		status, b := call("GET", "/v1/deployments?project_id="+*projectID, nil)
		check(status, b)
		pretty(b)
	case "create":
		body := map[string]any{"id": rest[1]}
		status, b := call("POST", "/v1/deployments?project_id="+*projectID, body)
		fmt.Println(status, string(b))
	}
}

func networkCmd(args []string) {
	fs := flag.NewFlagSet("net", flag.ExitOnError)
	projectID := fs.String("project_id", "", "project id")
	fs.Parse(args)
	rest := fs.Args()
	if len(rest) < 1 {
		usage()
		os.Exit(2)
	}
	switch rest[0] {
	case "list":
		status, b := call("GET", "/v1/networks?project_id="+*projectID, nil)
		check(status, b)
		pretty(b)
	case "create":
		body := map[string]any{"id": rest[1], "cidr": "10.0.0.0/24"}
		status, b := call("POST", "/v1/networks?project_id="+*projectID, body)
		fmt.Println(status, string(b))
	}
}

func volumeCmd(args []string) {
	fs := flag.NewFlagSet("vol", flag.ExitOnError)
	projectID := fs.String("project_id", "", "project id")
	fs.Parse(args)
	rest := fs.Args()
	if len(rest) < 1 {
		usage()
		os.Exit(2)
	}
	switch rest[0] {
	case "list":
		status, b := call("GET", "/v1/volumes?project_id="+*projectID, nil)
		check(status, b)
		pretty(b)
	case "create":
		body := map[string]any{"id": rest[1], "size_bytes": 1 << 30}
		status, b := call("POST", "/v1/volumes?project_id="+*projectID, body)
		fmt.Println(status, string(b))
	}
}

func secretCmd(args []string) {
	fs := flag.NewFlagSet("sec", flag.ExitOnError)
	projectID := fs.String("project_id", "", "project id")
	fs.Parse(args)
	rest := fs.Args()
	if len(rest) < 1 {
		usage()
		os.Exit(2)
	}
	switch rest[0] {
	case "list":
		status, b := call("GET", "/v1/secrets?project_id="+*projectID, nil)
		check(status, b)
		pretty(b)
	case "create":
		body := map[string]any{"id": rest[1]}
		status, b := call("POST", "/v1/secrets?project_id="+*projectID, body)
		fmt.Println(status, string(b))
	}
}

func configmapCmd(args []string) {
	fs := flag.NewFlagSet("cm", flag.ExitOnError)
	projectID := fs.String("project_id", "", "project id")
	fs.Parse(args)
	rest := fs.Args()
	if len(rest) < 1 {
		usage()
		os.Exit(2)
	}
	switch rest[0] {
	case "list":
		status, b := call("GET", "/v1/configmaps?project_id="+*projectID, nil)
		check(status, b)
		pretty(b)
	case "create":
		body := map[string]any{"id": rest[1], "data": map[string]string{}}
		status, b := call("POST", "/v1/configmaps?project_id="+*projectID, body)
		fmt.Println(status, string(b))
	}
}

func bucketCmd(args []string) {
	switch args[0] {
	case "list":
		status, b := call("GET", "/v1/buckets", nil)
		check(status, b)
		pretty(b)
	case "create":
		body := map[string]any{"id": args[1]}
		status, b := call("POST", "/v1/buckets", body)
		fmt.Println(status, string(b))
	}
}

func nodeCmd(args []string) {
	if args[0] == "list" {
		status, b := call("GET", "/v1/nodes", nil)
		check(status, b)
		pretty(b)
	}
}

func modelCmd(args []string) {
	if args[0] == "list" {
		req, _ := http.NewRequest("GET", *apiURL+"/v1/models", nil)
		if *token != "" {
			req.Header.Set("Authorization", "Bearer "+*token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		var out struct {
			Data []map[string]any `json:"data"`
		}
		json.Unmarshal(b, &out)
		for _, m := range out.Data {
			fmt.Println("-", m["id"])
		}
	}
}

func chatCmd(args []string) {
	fs := flag.NewFlagSet("chat", flag.ExitOnError)
	model := fs.String("model", "echo", "model name")
	fs.Parse(args)
	rest := fs.Args()
	prompt := strings.Join(rest, " ")
	if prompt == "" {
		usage()
		os.Exit(2)
	}
	body, _ := json.Marshal(map[string]any{
		"model":    *model,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
	})
	req, _ := http.NewRequest("POST", *apiURL+"/v1/chat/completions", bytes.NewReader(body))
	if *token != "" {
		req.Header.Set("Authorization", "Bearer "+*token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		fmt.Println(string(b))
		return
	}
	for _, c := range out.Choices {
		fmt.Println(c.Message.Content)
	}
	_ = time.Now()
}

func check(status int, body []byte) {
	if status != http.StatusOK && status != http.StatusCreated {
		fmt.Fprintln(os.Stderr, status, string(body))
		os.Exit(1)
	}
}

func pretty(b []byte) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		fmt.Println(string(b))
		return
	}
	out, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(out))
}
