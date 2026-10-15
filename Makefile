.PHONY: help tidy run build build-client build-admin run-client run-admin vet test docker-up docker-down fmt openapi

GO ?= go

# Build in pure-Go mode so no C toolchain is required (CGO is on by default on
# macOS and pulls in net's C resolver, which fails when system headers like
# stdlib.h aren't installed). This matches the Dockerfile's CGO_ENABLED=0.
export CGO_ENABLED = 0

help: ## Show available targets
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'

tidy: ## Resolve and tidy module dependencies
	$(GO) mod tidy

build: ## Build the server binary into ./bin
	@mkdir -p bin
	$(GO) build -o bin/dechat ./cmd/server

build-client: ## Build the console client binary into ./bin
	@mkdir -p bin
	$(GO) build -o bin/dechat-client ./cmd/client

build-admin: ## Build the admin client binary into ./bin
	@mkdir -p bin
	$(GO) build -o bin/dechat-admin ./cmd/admin

openapi: ## Dump the OpenAPI spec to ./openapi.json and ./openapi.yaml
	$(GO) run ./cmd/openapi

run-client: ## Run the console client locally (reads DECHAT_URL / environment)
	$(GO) run ./cmd/client

run-admin: ## Run the admin client locally (reads DECHAT_URL / environment)
	$(GO) run ./cmd/admin

run: ## Run the server locally (reads .env / environment)
	$(GO) run ./cmd/server

vet: ## Run go vet
	$(GO) vet ./...

test: ## Run tests
	$(GO) test ./...

fmt: ## Format the code
	$(GO) fmt ./...

docker-up: ## Build and start the full stack (db + app + web/Caddy) via docker compose
	docker compose up --build

docker-down: ## Stop and remove containers
	docker compose down
