# Roadmap

Incremental milestones, each backed by automated tests.

## M0 — Foundations (this checkpoint)
- [x] Repo layout, Makefile, proto, docs
- [ ] Single-node MVP `cloudinit`
- [ ] KV + MQ + obs primitives with stub tests
- [ ] Workload create/list/get/delete via REST

## M1 — Single-node MVP
- [ ] Workload lifecycle (deploy, stop, restart)
- [ ] Service discovery + DNS
- [ ] S3-compatible object storage
- [ ] Volumes
- [ ] Dashboard skeleton

## M2 — Multi-node scheduling
- [ ] Node heartbeats
- [ ] Scheduler bin-packing
- [ ] Leader-elected controller
- [ ] Anti-affinity, constraints, taints

## M3 — Networking & LB
- [ ] CNI bridge plugin
- [ ] Internal DNS server
- [ ] L4 load balancer
- [ ] Ingress controller

## M4 — HA & reliability
- [ ] Graceful drain
- [ ] Rolling deployments
- [ ] Resource quotas
- [ ] Fault-injection tests

## M5 — AI workloads
- [ ] Model registry
- [ ] `cloud run --model ...`
- [ ] OpenAI-compatible `/v1/chat/completions`
- [ ] Autoscaling for inference

## M6 — Hardening
- [ ] Benchmarks
- [ ] Performance tuning
- [ ] Security audit
- [ ] Helm chart + multi-node compose
