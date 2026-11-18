# Common tasks. Run `make help` to list them.
BIN := bin/cuwan
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/sgnd/cuwan/internal/cli.Version=$(VERSION)
# -p limits compile parallelism (modernc.org/sqlite is memory-heavy on first build)
GOBUILD := CGO_ENABLED=0 go build -p 2 -trimpath -ldflags "$(LDFLAGS)"

.PHONY: help build install test lint security clean

help: ## Show this help
	@grep -E '^[a-z0-9-]+:.*##' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  %-15s %s\n", $$1, $$2}'

build: ## Build ./bin/cuwan (CGO_ENABLED=0)
	$(GOBUILD) -o $(BIN) ./cmd/cuwan

install: ## Install cuwan into $$GOPATH/bin
	CGO_ENABLED=0 go install -p 2 -ldflags "$(LDFLAGS)" ./cmd/cuwan

test: ## Fast unit tests
	CGO_ENABLED=0 go test -p 2 ./...

lint: ## gofmt check and go vet
	@test -z "$$(gofmt -l cmd internal)" || { gofmt -l cmd internal; echo "run gofmt -w cmd internal"; exit 1; }
	CGO_ENABLED=0 go vet ./...

security: ## Known vulnerabilities in the code and its modules (govulncheck)
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

clean: ## Remove build output
	rm -rf bin/
