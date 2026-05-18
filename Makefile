# iplayer-arr Makefile
#
# Targets mirror the Gitea Actions workflow so contributors can reproduce CI
# locally before pushing. See .gitea/workflows/ci.yml for the canonical
# pipeline and docs/testing.md for the wider regression-safety framework.

.PHONY: help ci smoke diag test build fmt

help: ## Show this help.
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-10s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

ci: ## Run the same unit checks as the Gitea `unit` job.
	cd frontend && npm ci && npm run build
	go vet ./...
	go test ./... -race

smoke: ## Build, start, diag, stop -- the full isolated smoke cycle.
	bash scripts/smoke-test.sh build
	bash scripts/smoke-test.sh up
	bash scripts/smoke-test.sh diag
	bash scripts/smoke-test.sh down

diag: ## Run the diag suite against an already-running smoke container.
	bash scripts/smoke-test.sh diag

test: ## Run Go tests (no race detector).
	go test ./...

build: ## Build the iplayer-arr binary (CGO disabled, matches Dockerfile).
	CGO_ENABLED=0 go build -o iplayer-arr ./cmd/iplayer-arr/

fmt: ## Run gofmt on the tree (writes changes).
	gofmt -w .
