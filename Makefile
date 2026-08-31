SHELL := /bin/bash
GO ?= go

CMDS := cloudctl cloudapi cloudcontroller cloudscheduler cloudnode clouddns cloudlb cloudstorage cloudllm cloudinit
DIST := bin

.PHONY: all build build-all lint test test-unit test-integration test-e2e \
        test-fault bench proto fmt vet up down clean tidy init dev

all: build

init:
	$(GO) mod download
	@for m in internal/primitives/* proto/cloud/v1; do test -f $$m/go.mod && (cd $$m && $(GO) mod download) || true; done

tidy:
	$(GO) mod tidy
	@for m in internal/primitives/* proto/cloud/v1 cmd/* pkg/sdk/go test/integration test/e2e; do test -f $$m/go.mod && (cd $$m && $(GO) mod tidy); done

fmt:
	$(GO) fmt ./...
	@for m in internal/primitives/* proto/cloud/v1 cmd/* pkg/sdk/go test/integration test/e2e; do test -f $$m/go.mod && (cd $$m && $(GO) fmt ./...); done

vet:
	$(GO) vet ./...
	@for m in internal/primitives/* proto/cloud/v1 cmd/* pkg/sdk/go test/integration test/e2e; do test -f $$m/go.mod && (cd $$m && $(GO) vet ./...); done

proto:
	@command -v protoc >/dev/null || { echo "protoc not installed"; exit 1; }
	@command -v protoc-gen-go >/dev/null || $(GO) install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	@command -v protoc-gen-go-grpc >/dev/null || $(GO) install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	@command -v protoc-gen-grpc-gateway >/dev/null || $(GO) install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest
	protoc -I proto \
	  --go_out=proto/cloud/v1 --go_opt=paths=source_relative \
	  --go-grpc_out=proto/cloud/v1 --go-grpc_opt=paths=source_relative \
	  --grpc-gateway_out=proto/cloud/v1 --grpc-gateway_opt=paths=source_relative \
	  proto/cloud/v1/cloud.proto

build: proto
	mkdir -p $(DIST)
	@for c in $(CMDS); do \
	  echo "  building $$c"; \
	  $(GO) build -o $(DIST)/$$c ./cmd/$$c || exit 1; \
	done

build-all: build

test: test-unit

test-unit:
	$(GO) test -race -count=1 ./...
	@for m in internal/primitives/* proto/cloud/v1 cmd/* pkg/sdk/go test/integration; do test -f $$m/go.mod && (cd $$m && $(GO) test -race -count=1 ./...); done

test-integration:
	cd test/integration && $(GO) test -race -count=1 ./...

test-e2e:
	cd test/e2e && $(GO) test -race -count=1 -tags=e2e ./...

test-fault:
	cd test/fault && $(GO) test -race -count=1 -tags=fault ./...

bench:
	cd test/benchmark && $(GO) test -bench=. -benchmem ./...

up:
	docker compose -f deploy/docker/docker-compose.yml up -d
	@echo "Cluster booting; tail logs with:  docker compose -f deploy/docker/docker-compose.yml logs -f"

down:
	docker compose -f deploy/docker/docker-compose.yml down

dev:
	./bin/cloudinit --data ./data --addr :8443 --single

clean:
	rm -rf $(DIST) data/ logs/ tmp/
