# Architecture

The Mini Cloud Platform is a layered system where every higher layer is built
on a small set of internal primitives. This document describes the layers,
their interfaces, and the failure modes each must handle.

## Layered model

```
+---------------------------------------------------------------+
|                    API surface                                |
|    REST (grpc-gateway) + gRPC + OpenAI-compatible inference    |
+---------------------------------------------------------------+
|  Identity | Auth | Quotas | RBAC | Audit                       |
+---------------------------------------------------------------+
|  Controller (reconciler)  | Scheduler  | Autoscaler           |
+---------------------------------------------------------------+
|  Networking (CNI, DNS, LB) | Storage (Obj+PV) | Secrets       |
+---------------------------------------------------------------+
|  Node agents (heartbeat, CRI, drain, exec, logs)               |
+---------------------------------------------------------------+
|  Primitives:                                                  |
|   • Raft KV     • MQ    • Observability    • Runtime    • LLM |
+---------------------------------------------------------------+
```

### Primitives

Each primitive lives under `internal/primitives/<name>` and is its own Go
module with a stable interface so the rest of the platform can compile
against a stub during unit testing and against a real implementation at
runtime.

- **db** — strongly-consistent Raft KV with watch streams and CAS.
- **mq** — at-least-once pub/sub with consumer groups and DLQ.
- **obs** — metrics (Prometheus exposition), logs (structured), traces
  (OTLP-compatible), and alerting rules.
- **runtime** — a CRI-like container lifecycle interface with two
  implementations: `runc` (process-level) and `containerd` shim.
- **llm** — model registry + inference engine with an OpenAI-compatible
  HTTP front-end.

### Identity

`internal/identity` issues short-lived JWTs and long-lived API keys. RBAC
roles bind a set of resource types and verbs (`workloads:get`, `*:admin`).
Each project has quotas on CPU, memory, storage, and object-store size.

### Controller

A leader-elected reconciler watches desired-state resources and drives the
actual cluster toward them. It uses optimistic concurrency (CAS) on the
KV store, retries with backoff, and emits metrics for every reconcile.

### Scheduler

Filters and scores nodes:

1. **Filter** — drops nodes without enough free CPU/RAM, in the wrong
   region, or failing affinity rules.
2. **Score** — ranks by least-allocated, GPU fit, and spread preferences.

The scheduler uses the `Lease` primitive to reserve a placement and
releases it on heartbeat loss.

### Networking

- **CNI plugin** — creates a bridge per project, attaches veth pairs to
  containers, programs iptables/eBPF for egress NAT.
- **DNS** — watches service records, serves A/SRV records from a tiny
  authoritative server, supports forwarders for external names.
- **LB** — round-robin / least-conn L4 proxy with health checks.

### Storage

- **Object store** — S3 API for buckets and objects, multipart upload,
  pre-signed URLs. Backed by the KV store + content-addressed chunks.
- **Volumes** — block devices exposed to a workload via a CSI-lite
  driver. Snapshot and restore supported.

### Node agent

Reports heartbeats (CPU, RAM, disk, GPU, network). Manages the local
container runtime. Implements graceful drain by stopping new placements,
then cordoning, then evicting workloads.

### Failure handling

| Failure                  | Detection       | Recovery                                 |
|--------------------------|-----------------|------------------------------------------|
| Node crash               | heartbeat miss  | reschedule workloads, replicate leases  |
| Network partition        | split-brain     | Raft term enforces single leader         |
| Bad workload image       | runtime error   | exponential backoff, mark workload failed|
| Storage corruption       | checksum        | rebuild from replica                     |
| Lost leader              | election timer  | new term, fresh election                 |

## OpenAI-compatible inference

The platform schedules model-serving workloads as ordinary workloads, but
with a model registry sidecar. Requests hit `/v1/chat/completions` on the
API server, are routed by the platform to a healthy replica, and forwarded
to the local llama.cpp / vLLM-compatible backend.

See `docs/ai.md` for details.

## Diagrams

See `docs/diagrams/`. All diagrams are rendered from the source Mermaid
files in that directory.
