.PHONY: help tests lint docker docker-push docker-push-test test-local lint-local clean

# Variables
DOCKER_REGISTRY := us-central1-docker.pkg.dev/bluecore-ops/ops
IMAGE_NAME := buildkite-gcp-autoscaler
GIT_SHORT_HASH := $(shell git rev-parse --short HEAD)
IMAGE_TAG := $(GIT_SHORT_HASH)
FULL_IMAGE := $(DOCKER_REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)

# Docker build options
DOCKER_BUILD_OPTS := --ssh default --platform linux/amd64
DOCKER_BUILDKIT := 1
export DOCKER_BUILDKIT

help: ## Show this help message
	@echo 'Usage: make [target]'
	@echo ''
	@echo 'Available targets:'
	@awk 'BEGIN {FS = ":.*?## "} /^[a-zA-Z_-]+:.*?## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

# CI targets (used by Buildkite)
tests: ## Run tests in Docker
	@echo "Running tests..."
	docker build $(DOCKER_BUILD_OPTS) --target tests -t $(IMAGE_NAME):tests .
	@echo "✓ Tests passed"

lint: ## Run linter in Docker
	@echo "Running linter..."
	docker build $(DOCKER_BUILD_OPTS) --target lint -t $(IMAGE_NAME):lint .
	@echo "✓ Linting passed"

docker: ## Build and tag Docker image
	@echo "Building Docker image..."
	docker build $(DOCKER_BUILD_OPTS) -t $(IMAGE_NAME):latest -t $(FULL_IMAGE) .
	@echo "✓ Docker image built: $(FULL_IMAGE)"

docker-push: docker ## Build and push Docker image to registry
	@echo "Pushing Docker image to registry..."
	docker push $(FULL_IMAGE)
	@echo "✓ Image pushed: $(FULL_IMAGE)"

docker-push-test: ## Build and push to test registry (rabia-test-repo)
	@echo "Building and pushing to test registry..."
	$(eval TEST_IMAGE := us-central1-docker.pkg.dev/bluecore-ops/rabia-test-repo/$(IMAGE_NAME):$(IMAGE_TAG))
	docker build $(DOCKER_BUILD_OPTS) -t $(TEST_IMAGE) .
	docker push $(TEST_IMAGE)
	@echo "✓ Image pushed: $(TEST_IMAGE)"

# Local development targets (without Docker)
test-local: ## Run tests locally (fast, requires Go installed)
	go test -v ./... -cover

lint-local: ## Run linter locally (fast, requires Go installed)
	go fmt ./...
	go vet ./...

clean: ## Clean up Docker images
	@echo "Cleaning up..."
	docker rmi -f $(IMAGE_NAME):tests $(IMAGE_NAME):lint $(IMAGE_NAME):latest $(FULL_IMAGE) 2>/dev/null || true
	@echo "✓ Cleanup complete"
