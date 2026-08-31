# Mini Cloud Platform

A production-grade, AWS-like cloud platform written in Go for deploying and
operating containerized workloads on a cluster of Linux machines. It provides
infrastructure primitives — compute, networking, storage, identity, AI
inference, and observability — through a unified control plane.

```
+--------- Cloud API (REST + gRPC) ---------+
|  projects | workloads | networks | storage |
|  secrets  | models    | users    | keys    |
+------------------------------------------+
       |                |                |
+--Controller--+  +--Scheduler--+  +--Inference API--+
| reconcile    |  | bin-packing |  | OpenAI-compat   |
+--------------+  +-------------+  +-----------------+
       |                |                |
   +---+----------------+----------------+---+
   |              Cluster State (Raft KV)    |
   +------------------------------------------+
       |                |                |
+-------+--------+  +---+-----+  +------+----------+
| Node Agent(s)  |  | Object  |  | Observability   |
| runc/containerd|  | Storage |  | metrics+logs    |
+----------------+  +---------+  +-----------------+
```

## Status

This is an incremental build-up from a single-node MVP to a full multi-node
production-grade platform. See `docs/roadmap.md` for the milestone plan and
`docs/architecture.md` for the full design.

## Components

| Service                | Path                            | Responsibility                                                  |
|------------------------|---------------------------------|-----------------------------------------------------------------|
| `cloudapi`             | `cmd/cloudapi`                  | Public REST + gRPC control plane                                |
| `cloudcontroller`      | `cmd/cloudcontroller`           | Desired-state reconciliation (leader-elected)                   |
| `cloudscheduler`       | `cmd/cloudscheduler`            | Resource-aware workload placement                               |
| `cloudnode`            | `cmd/cloudnode`                 | Per-host agent: heartbeat, container lifecycle, drain           |
| `clouddns`             | `cmd/clouddns`                  | Internal service-discovery DNS                                  |
| `cloudlb`              | `cmd/cloudlb`                   | L4 load balancer for services                                   |
| `cloudstorage`         | `cmd/cloudstorage`              | S3-compatible object store                                      |
| `cloudllm`             | `cmd/cloudllm`                  | LLM inference engine (OpenAI-compatible API)                   |
| `cloudinit`            | `cmd/cloudinit`                 | Bootstrap a single-node cluster (MVP)                           |
| `cloudctl`             | `cmd/cloudctl`                  | Operator CLI                                                    |

## Quickstart (single-node MVP)

```bash
# 1. Bootstrap a one-node cluster locally
./bin/cloudinit --data ./data --addr :8443

# 2. Create a workload
./bin/cloudctl workload create --image nginx:alpine --cpu 1 --memory 256Mi --replicas 1 web

# 3. Run an AI workload (auto-served via OpenAI-compatible API)
./bin/cloudctl run --model llama3.2-1b --replicas 1 --cpu 2 --memory 4Gi chatbot

# 4. Chat with it
curl -s http://localhost:8443/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"chatbot","messages":[{"role":"user","content":"hello"}]}'
```

See `docs/cli.md` and `docs/api.md` for the full surface.

## Development

```bash
go work use ./internal/primitives/db ./internal/primitives/runtime ./...
make build      # produce ./bin/* binaries
make test       # unit tests
make e2e        # end-to-end + fault-injection tests
make up         # docker-compose multi-node dev cluster
```

## License

Apache 2.0 — see `LICENSE`.
