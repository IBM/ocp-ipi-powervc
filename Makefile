# Copyright 2026 IBM Corp
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Variables
BINARY_NAME := ocp-ipi-powervc
MODULE_NAME := example/user/PowerVC-Tool
GO := go
GOFLAGS := 
LDFLAGS := 
ARCH := $(shell uname -m)
OS := $(shell uname -s | tr '[:upper:]' '[:lower:]')

# Version information from git
GIT_VERSION := $(shell git describe --always --long --dirty 2>/dev/null || echo "unknown")
GIT_RELEASE := $(shell git describe --tags --abbrev=0 2>/dev/null || echo "v0.0.0")
BUILD_DATE := $(shell date -u '+%Y-%m-%d_%H:%M:%S')

# Build flags
LDFLAGS += -X main.version=$(GIT_VERSION)
LDFLAGS += -X main.release=$(GIT_RELEASE)

# Output binary name
OUTPUT_BINARY := $(BINARY_NAME)-$(OS)-$(ARCH)

# Test flags
TEST_FLAGS := -v
TEST_TIMEOUT := 15m
COVERAGE_FILE := coverage.out

# Directories
BUILD_DIR := build
DIST_DIR := dist

.PHONY: all
all: clean deps build test

.PHONY: help
help: ## Display this help message
	@echo "OpenShift IPI PowerVC Tool - Makefile targets:"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "Variables:"
	@echo "  BINARY_NAME    = $(BINARY_NAME)"
	@echo "  OUTPUT_BINARY  = $(OUTPUT_BINARY)"
	@echo "  GIT_VERSION    = $(GIT_VERSION)"
	@echo "  GIT_RELEASE    = $(GIT_RELEASE)"
	@echo "  ARCH           = $(ARCH)"
	@echo "  OS             = $(OS)"

.PHONY: init
init: ## Initialize Go module (use only for fresh setup)
	@echo "Initializing Go module..."
	@rm -f go.mod go.sum
	$(GO) mod init $(MODULE_NAME)
	$(GO) mod tidy
	@echo "Module initialized successfully"

.PHONY: deps
deps: ## Download and verify dependencies
	@echo "Downloading dependencies..."
	$(GO) mod download
	$(GO) mod verify
	@echo "Dependencies ready"

.PHONY: tidy
tidy: ## Tidy Go modules
	@echo "Tidying Go modules..."
	$(GO) mod tidy
	@echo "Modules tidied"

.PHONY: build
build: ## Build the binary
	@echo "Building $(OUTPUT_BINARY)..."
	$(GO) build $(GOFLAGS) -ldflags="$(LDFLAGS)" -o $(OUTPUT_BINARY) *.go
	@echo "Build complete: $(OUTPUT_BINARY)"

.PHONY: build-all
build-all: ## Build for all supported platforms
	@echo "Building for all platforms..."
	@mkdir -p $(DIST_DIR)
	GOOS=linux GOARCH=amd64 $(GO) build -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-linux-amd64 *.go
	GOOS=linux GOARCH=arm64 $(GO) build -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-linux-arm64 *.go
	GOOS=linux GOARCH=ppc64le $(GO) build -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-linux-ppc64le *.go
	GOOS=darwin GOARCH=amd64 $(GO) build -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-amd64 *.go
	GOOS=darwin GOARCH=arm64 $(GO) build -ldflags="$(LDFLAGS)" -o $(DIST_DIR)/$(BINARY_NAME)-darwin-arm64 *.go
	@echo "All platform builds complete in $(DIST_DIR)/"

.PHONY: install
install: build ## Install the binary to GOPATH/bin
	@echo "Installing $(OUTPUT_BINARY) to $(GOPATH)/bin/$(BINARY_NAME)..."
	@mkdir -p $(GOPATH)/bin
	@cp $(OUTPUT_BINARY) $(GOPATH)/bin/$(BINARY_NAME)
	@echo "Installation complete"

.PHONY: init-jobhistory
init-jobhistory: ## Initialize JobHistory Go module and download dependencies
	@echo "Initializing JobHistory module..."
	@cd JobHistory && $(GO) mod download
	@cd JobHistory && $(GO) mod tidy
	@echo "JobHistory module initialized"

.PHONY: build-jobhistory
build-jobhistory: ## Build the JobHistory tool
	@echo "Building JobHistory..."
	@cd JobHistory && $(GO) build -ldflags="$(LDFLAGS)" $(GOFLAGS) -o JobHistory *.go
	@echo "JobHistory build complete: JobHistory/JobHistory"

.PHONY: install-jobhistory
install-jobhistory: build-jobhistory ## Install JobHistory to GOPATH/bin
	@echo "Installing JobHistory to $(GOPATH)/bin/JobHistory..."
	@mkdir -p $(GOPATH)/bin
	@cp JobHistory/JobHistory $(GOPATH)/bin/JobHistory
	@echo "JobHistory installation complete"

.PHONY: test
test: ## Run all tests
	@echo "Running tests..."
	$(GO) test $(TEST_FLAGS) -timeout $(TEST_TIMEOUT) ./...
	@echo "Tests complete"

.PHONY: test-short
test-short: ## Run short tests only
	@echo "Running short tests..."
	$(GO) test -short $(TEST_FLAGS) ./...
	@echo "Short tests complete"

.PHONY: test-coverage
test-coverage: ## Run tests with coverage
	@echo "Running tests with coverage..."
	$(GO) test $(TEST_FLAGS) -timeout $(TEST_TIMEOUT) -coverprofile=$(COVERAGE_FILE) -covermode=atomic ./...
	@echo "Coverage report generated: $(COVERAGE_FILE)"

.PHONY: coverage-html
coverage-html: test-coverage ## Generate HTML coverage report
	@echo "Generating HTML coverage report..."
	$(GO) tool cover -html=$(COVERAGE_FILE) -o coverage.html
	@echo "HTML coverage report: coverage.html"

.PHONY: test-race
test-race: ## Run tests with race detector
	@echo "Running tests with race detector..."
	$(GO) test $(TEST_FLAGS) -race -timeout $(TEST_TIMEOUT) ./...
	@echo "Race tests complete"

.PHONY: bench
bench: ## Run benchmarks
	@echo "Running benchmarks..."
	$(GO) test -bench=. -benchmem ./...
	@echo "Benchmarks complete"

.PHONY: fmt
fmt: ## Format Go code
	@echo "Formatting code..."
	$(GO) fmt ./...
	@echo "Code formatted"

.PHONY: vet
vet: ## Run go vet
	@echo "Running go vet..."
	$(GO) vet ./...
	@echo "Vet complete"

.PHONY: lint
lint: ## Run golangci-lint (requires golangci-lint installed)
	@echo "Running golangci-lint..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not found. Install it from https://golangci-lint.run/"; \
		exit 1; \
	fi
	@echo "Lint complete"

.PHONY: check
check: fmt vet test ## Run format, vet, and tests

.PHONY: clean
clean: ## Clean build artifacts
	@echo "Cleaning build artifacts..."
	@rm -f $(OUTPUT_BINARY)
	@rm -f $(BINARY_NAME)-*
	@rm -f ocp-ipi-powervc-*
	@rm -f $(COVERAGE_FILE) coverage.html
	@rm -rf $(BUILD_DIR) $(DIST_DIR)
	@rm -f ocp-ipi-powervc-test
	@rm -f JobHistory/JobHistory
	@echo "Clean complete"

.PHONY: clean-all
clean-all: clean ## Clean everything including dependencies
	@echo "Cleaning all artifacts and cache..."
	$(GO) clean -cache -testcache -modcache
	@echo "All clean"

.PHONY: version
version: ## Display version information
	@echo "Version:     $(GIT_VERSION)"
	@echo "Release:     $(GIT_RELEASE)"
	@echo "Build Date:  $(BUILD_DATE)"
	@echo "Go Version:  $(shell $(GO) version)"
	@echo "Platform:    $(OS)/$(ARCH)"

.PHONY: run-check-alive
run-check-alive: build ## Run check-alive command (requires -serverIP flag)
	@if [ -z "$(SERVER_IP)" ]; then \
		echo "Error: SERVER_IP not set. Usage: make run-check-alive SERVER_IP=192.168.1.100"; \
		exit 1; \
	fi
	./$(OUTPUT_BINARY) check-alive -serverIP=$(SERVER_IP)

.PHONY: run-help
run-help: build ## Run with help flag
	./$(OUTPUT_BINARY) --help

.PHONY: run-version
run-version: build ## Run with version flag
	./$(OUTPUT_BINARY) --version

.PHONY: docker-build
docker-build: ## Build Docker image (requires Dockerfile)
	@if [ -f Dockerfile ]; then \
		docker build -t $(BINARY_NAME):$(GIT_RELEASE) .; \
	else \
		echo "Dockerfile not found"; \
		exit 1; \
	fi

.PHONY: release
release: clean deps test build-all ## Prepare a release (clean, test, build all platforms)
	@echo "Release preparation complete"
	@echo "Binaries available in $(DIST_DIR)/"
	@ls -lh $(DIST_DIR)/

.PHONY: dev
dev: clean deps build test ## Quick development cycle (clean, deps, build, test)
	@echo "Development build complete"

# Default target
.DEFAULT_GOAL := help

# Made with Bob
