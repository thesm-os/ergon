.PHONY: help bootstrap install fmt license vet lint lint-md tidy check-tidy \
        test test-race test-coverage build clean check

BLUE   := $(shell printf "\033[0;36m")
GREEN  := $(shell printf "\033[0;32m")
RED    := $(shell printf "\033[0;31m")
YELLOW := $(shell printf "\033[0;33m")
NC     := $(shell printf "\033[0m")

GO     := go
FLAGS  ?=

BIN_DIR      := bin
COVERAGE_DIR := .ergon/coverage

VERSION    ?= dev
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME ?= $(shell date -u '+%Y-%m-%dT%H:%M:%SZ')
LDFLAGS    := -ldflags="-X go.thesmos.sh/ergon/internal/version.buildVersion=$(VERSION) -X go.thesmos.sh/ergon/internal/version.buildCommit=$(COMMIT) -X go.thesmos.sh/ergon/internal/version.buildDate=$(BUILD_TIME)"

GO_FILES := $(shell find . -type f -name '*.go' \
	! -path './.git/*' \
	! -name '*.gen.go' \
	! -name '*.gen_test.go')

help:
	@echo "$(BLUE)ergon — make targets$(NC)"
	@echo ""
	@echo "$(GREEN)Setup:$(NC)"
	@echo "  bootstrap          Install development tools"
	@echo "  install            Download and verify Go dependencies"
	@echo ""
	@echo "$(GREEN)Development:$(NC)"
	@echo "  fmt                Format Go (gofumpt + gci)"
	@echo "  license            Apply license headers"
	@echo "  vet                Run go vet"
	@echo "  lint               Full lint suite (fmt + vet + golangci-lint + markdownlint + license verify)"
	@echo "  lint-md            Lint Markdown only"
	@echo "  tidy               Run go mod tidy"
	@echo "  check-tidy         Fail if go mod tidy produces changes"
	@echo ""
	@echo "$(GREEN)Testing:$(NC)"
	@echo "  test               Run tests with coverage"
	@echo "  test-race          Run tests with race detector"
	@echo "  test-coverage      Generate HTML coverage report"
	@echo ""
	@echo "$(GREEN)Building:$(NC)"
	@echo "  build              Build the ergon binary"
	@echo "  clean              Remove build artifacts"
	@echo ""
	@echo "$(GREEN)Quality gates:$(NC)"
	@echo "  check              Full pre-merge gate (lint + test)"

.DEFAULT_GOAL := help

bootstrap:
	@echo "$(BLUE)Installing development tools...$(NC)"
	$(GO) install mvdan.cc/gofumpt@latest
	$(GO) install github.com/daixiang0/gci@latest
	$(GO) install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	$(GO) install golang.org/x/vuln/cmd/govulncheck@latest
	$(GO) install github.com/palantir/go-license@latest
	@command -v markdownlint-cli2 >/dev/null 2>&1 || { \
		command -v npm >/dev/null 2>&1 && npm install -g markdownlint-cli2 || \
		echo "$(YELLOW)Install markdownlint-cli2: brew install markdownlint-cli2 (or npm install -g markdownlint-cli2)$(NC)"; \
	}
	@echo "$(GREEN)Done. Run 'pre-commit install --hook-type pre-commit --hook-type pre-push --hook-type commit-msg'$(NC)"

install:
	@echo "$(BLUE)Installing dependencies...$(NC)"
	$(GO) mod download
	$(GO) mod verify
	@echo "$(GREEN)Done$(NC)"

fmt: license
	@echo "$(BLUE)Formatting Go...$(NC)"
	gofumpt -l -w -extra .
	gci write --section standard --section default --section "prefix(go.thesmos.sh/ergon)" --custom-order --skip-generated .
	@echo "$(BLUE)Formatting Markdown...$(NC)"
	markdownlint-cli2 --fix "**/*.md" "#vendor" "#dist" "#node_modules" "#docs/superpowers" 2>/dev/null || true
	@echo "$(GREEN)Done$(NC)"

license:
	@echo "$(BLUE)Applying license headers...$(NC)"
	@go-license --config=.go-license.yml $(GO_FILES)
	@echo "$(GREEN)Done$(NC)"

vet:
	$(GO) vet ./...

lint: fmt vet lint-md
	@echo "$(BLUE)Running golangci-lint...$(NC)"
	golangci-lint run --timeout=5m ./...
	@echo "$(BLUE)Verifying license headers...$(NC)"
	@go-license --config=.go-license.yml --verify $(GO_FILES)
	@echo "$(GREEN)Lint passed$(NC)"

lint-md:
	@echo "$(BLUE)Linting Markdown...$(NC)"
	markdownlint-cli2 "**/*.md" "#vendor" "#dist" "#node_modules" "#docs/superpowers"

test:
	@echo "$(BLUE)Running tests...$(NC)"
	@mkdir -p $(COVERAGE_DIR)
	$(GO) test -coverprofile=$(COVERAGE_DIR)/coverage.out -covermode=atomic $(FLAGS) ./...
	@echo "$(GREEN)Tests passed$(NC)"

test-race:
	@echo "$(BLUE)Running tests with race detector...$(NC)"
	$(GO) test -race $(FLAGS) ./...
	@echo "$(GREEN)No races detected$(NC)"

test-coverage: test
	@echo "$(BLUE)Generating coverage report...$(NC)"
	$(GO) tool cover -html=$(COVERAGE_DIR)/coverage.out -o $(COVERAGE_DIR)/coverage.html
	@echo "$(GREEN)Report: $(COVERAGE_DIR)/coverage.html$(NC)"

tidy:
	$(GO) mod tidy

check-tidy: tidy
	@if ! git diff --quiet -- go.mod go.sum 2>/dev/null; then \
		echo "$(RED)go mod tidy produced changes. Run 'make tidy' and commit.$(NC)"; \
		git diff --stat -- go.mod go.sum; \
		exit 1; \
	fi

# Build whatever cmd/ packages exist. Phase 1 introduces cmd/ergon
# and folds cmd/release into internal/release.
build:
	@echo "$(BLUE)Building...$(NC)"
	@mkdir -p $(BIN_DIR)
	@for d in cmd/*; do \
		[ -d "$$d" ] || continue; \
		name=$$(basename $$d); \
		echo "  $$name"; \
		$(GO) build $(LDFLAGS) -o $(BIN_DIR)/$$name ./$$d || exit 1; \
	done
	@echo "$(GREEN)Done$(NC)"

clean:
	@echo "$(BLUE)Cleaning...$(NC)"
	rm -rf $(BIN_DIR) .ergon/ dist/
	$(GO) clean -cache -testcache
	@echo "$(GREEN)Clean$(NC)"

check: check-tidy lint test
	@echo "$(GREEN)All checks passed$(NC)"
