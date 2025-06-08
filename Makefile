PREFIX?=$(shell pwd)

NAME := nsg-data-importer
PKG := gitlab.com/crusoeenergy/neteng/${NAME}

BUILDDIR := ${PREFIX}/dist
# Set any default go build tags
BUILDTAGS :=

GOLANGCI_VERSION = v1.62.2
GO_ACC_VERSION = latest
GOTESTSUM_VERSION = latest
GOCOVER_VERSION = latest
WORKFLOWCHECK_VERSION = latest

GO_LDFLAGS = -ldflags="-w -s -extldflags '-static'"

.PHONY: dev
dev: test build-deps precommit lint ## Runs a build-deps, test, lint

.PHONY: ci
ci: test-ci build-deps lint-ci ## Runs test, build-deps, lint

.PHONY: build-deps
build-deps: ## Install build dependencies
	@echo "==> $@"
	@go install github.com/golangci/golangci-lint/cmd/golangci-lint@${GOLANGCI_VERSION}

.PHONY: get-aliaslint
get-aliaslint:
	@echo "==> $@"
	@go mod download gitlab.com/crusoeenergy/tools@${TOOLS_VERSION}
	@cd `go env GOMODCACHE`/gitlab.com/crusoeenergy/tools@${TOOLS_VERSION}; go build -o ${PREFIX}/aliaslint.so -buildmode=plugin aliaslint/plugin/aliaslint.go

.PHONY: precommit
precommit: get-aliaslint ## runs various formatters that will be checked by linter (but can/should be automatic in your editor)
	@echo "==> $@"
	@go mod tidy
	@golangci-lint run --fix ./...

.PHONY: test
test: ## Runs the go tests.
	@echo "==> $@"
	@go test -tags "$(BUILDTAGS)" -cover -race -v ./...

.PHONY: test-ci
test-ci: ## Runs the go tests with additional options for a CI environment
	@echo "==> $@"
	@go mod tidy
	@git diff --exit-code go.mod go.sum # fail if go.mod is not tidy
	@go install github.com/ory/go-acc@${GO_ACC_VERSION}
	@go install gotest.tools/gotestsum@${GOTESTSUM_VERSION}
	@go install github.com/boumenot/gocover-cobertura@${GOCOVER_VERSION}
	@gotestsum --junitfile tests.xml --raw-command -- go-acc -o coverage.out ./... -- -json -tags "$(BUILDTAGS)" -race -v
	@go tool cover -func=coverage.out
	@gocover-cobertura < coverage.out > coverage.xml

.PHONY: lint
lint: get-aliaslint ## Verifies `golangci-lint` passes
	@echo "==> $@"
	@golangci-lint version
	@golangci-lint run ./...

.PHONY: lint-ci
lint-ci: get-aliaslint ## Verifies `golangci-lint` passes and outputs in CI-friendly format
	@echo "==> $@"
	@golangci-lint version
	@golangci-lint run ./... --out-format code-climate > golangci-lint.json

.PHONY: build
build: ## Builds the executable and places it in the build dir
	@go build -o ${BUILDDIR}/${NAME} ./cmd/nsg-data-importer/...

.PHONY: install
install: ## Builds and installs the executable on PATH
	@go install ${PKG}

.PHONY: cross
cross: ## Builds the cross compiled executable for use within a container
	@GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o ${BUILDDIR}/${NAME} ${GO_LDFLAGS} ./cmd/nsg-data-importer/...

.PHONY: run
run:
	@go run ./cmd/nsg-data-importer/... -vm_url "http://localhost:8428"

.PHONY: help
help:
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-30s\033[0m %s\n", $$1, $$2}'
