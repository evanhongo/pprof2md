# pprof2md - Makefile
# Convert pprof profiles to LLM-friendly Markdown

# Project configuration
BINARY_NAME := pprof2md
CMD_DIR := ./cmd/pprof2md
BUILD_DIR := ./build
DIST_DIR := ./dist

# Version and build info
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")
BUILD_TIME := $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

# Go configuration
GO := go
GOOS ?= $(shell go env GOOS)
GOARCH ?= $(shell go env GOARCH)
CGO_ENABLED ?= 0

# Build flags
GO_VERSION := $(shell go version | awk '{print $$3}')
LDFLAGS := -ldflags "-s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildTime=$(BUILD_TIME) -X main.goVersion=$(GO_VERSION)"
BUILD_FLAGS := -trimpath $(LDFLAGS)

# Release platforms (goos/goarch)
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64

# Colors for pretty output
GREEN := \033[0;32m
YELLOW := \033[0;33m
BLUE := \033[0;34m
CYAN := \033[0;36m
NC := \033[0m # No Color

.PHONY: all build clean test test-race fmt fmt-check vet lint install uninstall \
        deps deps-update deps-tidy cross-build release snapshot help dev ci info

# Default target
all: clean build

# Help target
help: ## Show this help message
	@echo "$(CYAN)pprof2md Build System$(NC)"
	@echo "====================="
	@echo ""
	@echo "$(YELLOW)Available targets:$(NC)"
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  $(GREEN)%-20s$(NC) %s\n", $$1, $$2}' $(MAKEFILE_LIST) | sort
	@echo ""
	@echo "$(YELLOW)Examples:$(NC)"
	@echo "  make build              # Build the binary"
	@echo "  make cross-build        # Build for all platforms"
	@echo "  make snapshot           # Dry-run the GoReleaser release"

# Build targets
build: deps ## Build binary
	@echo "$(BLUE)Building pprof2md...$(NC)"
	@mkdir -p $(BUILD_DIR)
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) \
		$(GO) build $(BUILD_FLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) $(CMD_DIR)
	@echo "$(GREEN)✓ Built $(BUILD_DIR)/$(BINARY_NAME)$(NC)"

cross-build: clean deps ## Build for multiple platforms
	@echo "$(BLUE)Building for multiple platforms...$(NC)"
	@for platform in $(PLATFORMS); do \
		os=$${platform%/*}; arch=$${platform#*/}; \
		ext=""; [ "$$os" = "windows" ] && ext=".exe"; \
		echo "$(YELLOW)Building for $$os/$$arch...$(NC)"; \
		mkdir -p $(DIST_DIR)/$$os-$$arch; \
		CGO_ENABLED=$(CGO_ENABLED) GOOS=$$os GOARCH=$$arch \
			$(GO) build $(BUILD_FLAGS) -o $(DIST_DIR)/$$os-$$arch/$(BINARY_NAME)$$ext $(CMD_DIR) || exit 1; \
	done
	@echo "$(GREEN)✓ Cross-platform builds complete in $(DIST_DIR)/$(NC)"

# Dependency management
deps: ## Download and verify dependencies
	@echo "$(BLUE)Downloading dependencies...$(NC)"
	@$(GO) mod download
	@$(GO) mod verify

deps-update: ## Update all dependencies
	@echo "$(BLUE)Updating dependencies...$(NC)"
	@$(GO) get -u ./...
	@$(GO) mod tidy

deps-tidy: ## Clean up dependencies
	@echo "$(BLUE)Tidying dependencies...$(NC)"
	@$(GO) mod tidy

# Testing
test: deps ## Run tests
	@echo "$(BLUE)Running tests...$(NC)"
	@$(GO) test ./...

test-race: deps ## Run tests with race detection
	@echo "$(BLUE)Running tests with race detection...$(NC)"
	@$(GO) test -race ./...

# Code quality
fmt: ## Format code
	@echo "$(BLUE)Formatting code...$(NC)"
	@$(GO) fmt ./...

fmt-check: ## Fail if any Go file is not gofmt-formatted
	@echo "$(BLUE)Checking formatting...$(NC)"
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "$(YELLOW)Unformatted files:$(NC)"; echo "$$unformatted"; exit 1; \
	fi

vet: ## Run go vet
	@echo "$(BLUE)Running go vet...$(NC)"
	@$(GO) vet ./...

lint: ## Run linter (requires golangci-lint)
	@echo "$(BLUE)Running linter...$(NC)"
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "$(YELLOW)golangci-lint not found. Installing...$(NC)"; \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
	fi
	@golangci-lint run

# Installation
install: ## Install binary to $GOBIN
	@echo "$(BLUE)Installing binary...$(NC)"
	@$(GO) install $(BUILD_FLAGS) $(CMD_DIR)
	@echo "$(GREEN)✓ Installed $(BINARY_NAME) to $$($(GO) env GOBIN)$(NC)"

uninstall: ## Remove installed binary
	@echo "$(BLUE)Uninstalling binary...$(NC)"
	@rm -f $(shell go env GOPATH)/bin/$(BINARY_NAME)
	@echo "$(GREEN)✓ Uninstalled binary$(NC)"

# Release management
release: cross-build test ## Create local release archives (CI uses GoReleaser)
	@echo "$(BLUE)Creating release archives...$(NC)"
	@cd $(DIST_DIR) && for dir in */; do \
		platform=$$(basename "$$dir"); \
		echo "$(YELLOW)Creating archive for $$platform...$(NC)"; \
		tar -czf "$(BINARY_NAME)-$(VERSION)-$$platform.tar.gz" -C "$$dir" .; \
	done
	@echo "$(GREEN)✓ Release archives created in $(DIST_DIR)/$(NC)"
	@ls -la $(DIST_DIR)/*.tar.gz

snapshot: ## Dry-run GoReleaser without publishing (requires goreleaser)
	@echo "$(BLUE)Running GoReleaser snapshot...$(NC)"
	@goreleaser release --snapshot --clean

# Cleanup
clean: ## Clean build artifacts
	@echo "$(BLUE)Cleaning build artifacts...$(NC)"
	@rm -rf $(BUILD_DIR) $(DIST_DIR)
	@rm -f $(BINARY_NAME)
	@echo "$(GREEN)✓ Cleaned$(NC)"

# Development workflow
dev: fmt vet test build ## Full development workflow (format, vet, test, build)
	@echo "$(GREEN)✓ Development workflow complete$(NC)"

# CI/CD workflow
ci: deps fmt-check vet lint test build ## CI/CD pipeline
	@echo "$(GREEN)✓ CI pipeline complete$(NC)"

# Show build info
info: ## Show build information
	@echo "$(CYAN)Build Information$(NC)"
	@echo "================="
	@echo "Version:    $(VERSION)"
	@echo "Commit:     $(COMMIT)"
	@echo "Build Time: $(BUILD_TIME)"
	@echo "Go Version: $(shell go version)"
	@echo "Platform:   $(GOOS)/$(GOARCH)"
	@echo "CGO:        $(CGO_ENABLED)"
