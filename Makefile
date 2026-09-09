.PHONY: run test test-race build lint fmt clean docker-build docker-run \
        image image-verify stack-up stack-down load deploy transcripts verify help

IMAGE      ?= task-api
REVISION   ?= $(shell git rev-parse HEAD)
VERSION    ?= dev
SIZE_LIMIT := $(shell echo $$((15 * 1024 * 1024)))

help: ## Show available targets
	@grep -hE '^[a-z-]+:.*##' $(MAKEFILE_LIST) | sort | awk -F':.*##' '{printf "  %-14s %s\n", $$1, $$2}'

# --- application ---------------------------------------------------------------------------
run: ## Run the app locally
	go run .

test: ## Run all tests
	go test ./... -v -count=1

test-race: ## Run tests with the race detector
	go test ./... -v -race -count=1

build: ## Build the binary
	CGO_ENABLED=0 go build -o bin/task-api .

lint: ## Static analysis (same checks the CI verify job runs)
	gofmt -l . | tee /dev/stderr | (! read)
	go vet ./...
	golangci-lint run ./...

fmt: ## Format source files
	go fmt ./...

verify: lint test-race build ## Everything the CI verify job does

clean: ## Remove build artifacts
	rm -rf bin/

# --- container -----------------------------------------------------------------------------
docker-build: image ## Alias for image

image: ## Build the container image
	docker build --build-arg VERSION=$(VERSION) --build-arg REVISION=$(REVISION) -t $(IMAGE) .

docker-run: ## Run the container
	docker run --rm -p 8080:8080 $(IMAGE)

image-verify: image ## Assert the Task 1 acceptance criteria against a running container
	@size=$$(docker image inspect $(IMAGE) --format '{{.Size}}'); \
	  echo "image size: $$size bytes (limit $(SIZE_LIMIT))"; \
	  test "$$size" -lt "$(SIZE_LIMIT)" || { echo "FAIL: over 15 MiB"; exit 1; }
	@docker rm -f task-api-verify >/dev/null 2>&1 || true
	@docker run -d --name task-api-verify -p 8080:8080 $(IMAGE) >/dev/null
	@for i in $$(seq 1 30); do \
	   s=$$(docker inspect --format '{{.State.Health.Status}}' task-api-verify); \
	   [ "$$s" = healthy ] && break; sleep 1; done
	@echo "health: $$(docker inspect --format '{{.State.Health.Status}}' task-api-verify)"
	@test "$$(docker inspect --format '{{.State.Health.Status}}' task-api-verify)" = healthy \
	   || { echo "FAIL: never became healthy"; docker rm -f task-api-verify; exit 1; }
	@uid=$$(docker top task-api-verify | awk 'NR==2{print $$1}'); \
	  echo "uid: $$uid"; \
	  test -n "$$uid" || { echo "FAIL: could not read process uid"; docker rm -f task-api-verify; exit 1; }; \
	  test "$$uid" != 0 && test "$$uid" != root \
	   || { echo "FAIL: running as root"; docker rm -f task-api-verify; exit 1; }
	@echo "healthz: $$(curl -fsS localhost:8080/healthz)"
	@docker rm -f task-api-verify >/dev/null
	@echo "PASS: under 15 MiB, reports healthy, non-root"

# --- observability stack -------------------------------------------------------------------
stack-up: ## Start app + Prometheus + Grafana (Grafana on :3000, Prometheus on :9090)
	REVISION=$(REVISION) VERSION=$(VERSION) docker compose up -d --build

stack-down: ## Stop the stack and delete its data
	docker compose down -v --remove-orphans

load: ## Generate deterministic business traffic for the dashboard
	./scripts/loadgen.sh

# --- delivery ------------------------------------------------------------------------------
deploy: ## Deploy an image by digest (TASK_API_IMAGE=... make deploy)
	./deploy/deploy.sh

transcripts: ## Re-export the AI conversation records
	python3 scripts/export-ai-transcript.py
