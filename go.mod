module github.com/minicloud/platform

go 1.26

require (
	github.com/minicloud/platform/internal/primitives/db v0.0.0
	github.com/minicloud/platform/internal/primitives/llm v0.0.0-00010101000000-000000000000
	github.com/minicloud/platform/internal/primitives/obs v0.0.0-00010101000000-000000000000
	github.com/minicloud/platform/internal/primitives/runtime v0.0.0-00010101000000-000000000000
	golang.org/x/crypto v0.55.0
)

replace (
	github.com/minicloud/platform/internal/primitives/db => ./internal/primitives/db
	github.com/minicloud/platform/internal/primitives/llm => ./internal/primitives/llm
	github.com/minicloud/platform/internal/primitives/mq => ./internal/primitives/mq
	github.com/minicloud/platform/internal/primitives/obs => ./internal/primitives/obs
	github.com/minicloud/platform/internal/primitives/runtime => ./internal/primitives/runtime
	github.com/minicloud/platform/proto/cloud => ./proto/cloud/v1
)
