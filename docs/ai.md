# AI workloads

AI workloads are first-class. A user runs:

```bash
cloudctl run --model llama3.2-1b --replicas 2 --cpu 2 --memory 4Gi chatbot
```

This creates a `Workload` of kind `MODEL`. The platform:

1. Pulls the model artifacts (or downloads them on first run).
2. Schedules the workload onto one or more nodes with the requested
   resources plus a small sidecar that registers the model with the
   inference router.
3. Exposes the workload via the OpenAI-compatible inference API:

```bash
curl -X POST $ENDPOINT/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"chatbot","messages":[{"role":"user","content":"hi"}]}'
```

The router picks a healthy replica, applies rate limits, and forwards the
request. Responses include `x-model-name`, `x-replica`, and standard
OpenAI fields.

## Supported backends

- `llama.cpp` HTTP server (compatible with `llama-server`).
- `vLLM` OpenAI server.
- Custom Go inference engine shipped in this repo under
  `internal/primitives/llm` for tiny toy models (smoke tests).

## Autoscaling

The platform scales model-serving workloads on requests-in-flight per
replica and on GPU/CPU utilization. See `internal/autoscale`.
