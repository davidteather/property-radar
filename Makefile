.PHONY: fmt vet lint test test-short build check ci mocks gen-proto clean-proto gen-code check-generated db-up db-down binaries bump registry-validate registry-publish

PART ?= patch

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './internal/*/mocks/*')

vet:
	go vet ./...

lint:
	golangci-lint run ./...

check: fmt vet lint test

# CI staleness gate; runs on a committed tree (git diff fails on any dirt).
ci: check check-generated

# -race: the store's advisory-lock and drain paths are exercised concurrently.
test:
	go test -race ./...

test-short:
	go test -short ./...

build:
	go build ./...

binaries:
	mkdir -p bin
	go build -o bin/ ./cmd/...

mocks:
	go tool mockery

# No protos in v0. Targets exist so gen-code is stable when they arrive.
gen-proto:
	@echo "gen-proto: no proto contracts yet"

clean-proto:
	@echo "clean-proto: no proto contracts yet"

gen-code: mocks gen-proto

check-generated:
	$(MAKE) gen-code
	git diff --exit-code -- .

# make bump PART=patch|minor|major: rewrites every version string (.bumpversion.cfg
# lists them), commits, and tags v<new>. Needs bump2version (pip install bump2version).
bump:
	bumpversion $(PART)

# Official MCP Registry listing (server.json). Publish after the public repo exists:
# mcp-publisher login github, then make registry-publish.
registry-validate:
	mcp-publisher validate

registry-publish: registry-validate
	mcp-publisher publish

db-up:
	docker compose up -d db

db-down:
	docker compose down
