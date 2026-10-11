GOLANGCI_LINT ?= $(CURDIR)/.cache/bin/golangci-lint
VERSION ?= dev

.PHONY: build run install tools hooks setup fmt fmt-check lint vet test tidy-check check vuln screenshots docs-tools docs docs-serve integration cross-check

build:
	CGO_ENABLED=0 go build -trimpath -ldflags '-X main.version=$(VERSION)' -o bin/cairn ./cmd/cairn

run:
	go run ./cmd/cairn

install:
	CGO_ENABLED=0 go install -trimpath -ldflags '-X main.version=$(VERSION)' ./cmd/cairn

tools:
	./scripts/install-tools.sh

hooks:
	git config --local core.hooksPath .githooks

setup: tools hooks

fmt:
	$(GOLANGCI_LINT) fmt ./...

fmt-check:
	$(GOLANGCI_LINT) fmt --diff ./...

lint:
	$(GOLANGCI_LINT) run ./...

vet:
	go vet ./...

test:
	go test -race ./...

tidy-check:
	go mod tidy -diff

check: fmt-check lint test tidy-check build

vuln:
	go run golang.org/x/vuln/cmd/govulncheck@v1.8.0 ./...

screenshots: build
	npm ci --prefix scripts/screenshots
	npm --prefix scripts/screenshots run capture

docs-tools:
	@if command -v uv >/dev/null 2>&1; then \
		UV_CACHE_DIR="$(CURDIR)/.cache/uv" uv venv --python python3 --allow-existing .cache/docs-env && \
		UV_CACHE_DIR="$(CURDIR)/.cache/uv" uv pip install --python .cache/docs-env/bin/python --require-hashes -r scripts/docs/requirements.txt; \
	else \
		python3 -m venv .cache/docs-env && \
		.cache/docs-env/bin/python -m pip install --disable-pip-version-check --no-cache-dir --require-hashes -r scripts/docs/requirements.txt; \
	fi

docs:
	.cache/docs-env/bin/python scripts/docs/build.py

docs-serve: docs
	python3 -m http.server --bind 127.0.0.1 --directory site 8000

integration: build
	python3 scripts/integration.py bin/cairn

cross-check:
	mkdir -p .cache/cross
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o .cache/cross/cairn-linux-amd64 ./cmd/cairn
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o .cache/cross/cairn-linux-arm64 ./cmd/cairn
