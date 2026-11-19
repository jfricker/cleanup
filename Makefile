SHELL := /bin/bash
BIN   := cleanup
PKGS  := ./...
export CGO_ENABLED := 0

.DEFAULT_GOAL := help

.PHONY: help build lint test check bench golden clean

help: ## Show available targets
	@awk 'BEGIN{FS=":.*## "} /^[a-zA-Z_-]+:.*## /{printf "  %-8s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Build the static binary ./cleanup
	go build -trimpath -o $(BIN) ./cmd/cleanup

lint: ## Fast checks: gofmt, go vet, staticcheck
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi
	go vet $(PKGS)
	go tool staticcheck $(PKGS)

test: ## Run tests with the race detector
	CGO_ENABLED=1 go test -race -count=1 $(PKGS)

check: lint test ## Full quality gate: lint, race tests, govulncheck, tidy
	go tool govulncheck $(PKGS)
	@test -z "$$(go mod tidy -diff)" || { echo "go.mod/go.sum not tidy"; exit 1; }

bench: ## Run the NFR-1 scan benchmark (slow; not part of check)
	go test -run '^$$' -bench . -benchtime 1x ./internal/scan/

golden: ## Regenerate golden test files
	go test ./... -update

clean: ## Remove build output
	go clean
	$(RM) $(BIN)
