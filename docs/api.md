# API

The Mini Cloud Platform exposes a unified control plane over both REST and
gRPC, generated from `proto/cloud/v1/cloud.proto` via grpc-gateway. The
OpenAI-compatible inference API lives at `/v1/chat/completions`,
`/v1/completions`, and `/v1/models`.

## Authentication

```http
Authorization: Bearer <jwt>
# or
X-Api-Key: <api-key>
```

Tokens are issued by `POST /v1/auth/login`. API keys are scoped to a
project and can carry an expiry.

## Resource surface (excerpt)

| Resource        | Methods                                          |
|-----------------|--------------------------------------------------|
| Users           | list, create, get, update, delete, login         |
| Projects        | list, create, get, update, delete                |
| API keys        | list, create, revoke                             |
| Roles           | list, bind, unbind                               |
| Workloads       | list, create, get, update, delete, scale, restart|
| Deployments     | list, create, get, update, delete, rollback      |
| Services        | list, create, get, update, delete                |
| Networks        | list, create, get, delete, attach, detach        |
| Volumes         | list, create, get, delete, snapshot, restore     |
| Buckets         | list, create, delete + S3 object API            |
| Secrets         | list, create, get, delete                        |
| ConfigMaps      | list, create, get, delete                        |
| Models          | list, register, get, delete                      |
| Nodes           | list, get, cordon, drain                         |
| Metrics         | `/metrics`, `/v1/metrics/query`                  |
| Logs            | `/v1/logs?workload=...`                          |
| Alerts          | list, create, update, delete                     |

## Examples

```bash
# Create a workload
curl -X POST localhost:8443/v1/workloads \
  -H 'Authorization: Bearer ...' \
  -d '{
        "name": "web",
        "image": "nginx:alpine",
        "replicas": 2,
        "cpu_millicores": 500,
        "memory_bytes": 268435456,
        "ports": [{"container_port": 80}]
      }'

# Run an AI workload
curl -X POST localhost:8443/v1/workloads \
  -H 'Authorization: Bearer ...' \
  -d '{
        "name": "chatbot",
        "kind": "MODEL",
        "model": {"name": "llama3.2-1b", "replicas": 1},
        "cpu_millicores": 2000,
        "memory_bytes": 4294967296
      }'

# Chat with it
curl -X POST localhost:8443/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"chatbot","messages":[{"role":"user","content":"hi"}]}'
```

See `proto/cloud/v1/cloud.proto` for the full schema.
