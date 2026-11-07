GO ?= go
COMPOSE ?= docker compose
CODEGEN = GOWORK=off $(GO) tool -modfile=tools/go.mod
VACUUM = GOWORK=off $(GO) -C tools/vacuum tool vacuum
CLI_ARGS = --spec api/openapi.cli.json --name oiectl --module github.com/araihu/oie/cli --out cli

.PHONY: generate lint build build-cli run check test test-race check-generated dev-up dev-down test-xmpp test-engine-xmpp

generate:
	$(CODEGEN) sqlc generate
	$(CODEGEN) oapi-codegen --config api/codegen.yaml api/openapi.yaml
	$(CODEGEN) oapi-codegen --config api/ui-preview.codegen.yaml api/ui-preview.openapi.yaml
	$(CODEGEN) templ generate
	$(VACUUM) bundle --composed ../../api/openapi.yaml ../../api/openapi.bundle.yaml
	$(VACUUM) apply-overlay --fail-on-warnings ../../api/openapi.bundle.yaml ../../api/cli.overlay.yaml ../../api/openapi.cli.yaml
	$(VACUUM) bundle --composed --format json ../../api/openapi.cli.yaml ../../api/openapi.cli.json
	@if [ -f cli/.oapi-cli-gen.json ]; then $(CODEGEN) oapi-cli-gen generate $(CLI_ARGS) --update; else $(CODEGEN) oapi-cli-gen generate $(CLI_ARGS); fi
	GOWORK=off $(GO) -C cli mod tidy

lint:
	$(VACUUM) lint --ext-refs --no-banner --no-update-check -d --fail-severity warn ../../api/openapi.yaml
	$(VACUUM) lint --ext-refs --no-banner --no-update-check -d --fail-severity warn ../../api/ui-preview.openapi.yaml

build:
	GOWORK=off $(GO) build -trimpath -o bin/oie ./cmd/oie

build-cli:
	GOWORK=off $(GO) -C cli build -trimpath -o ../bin/oiectl ./cmd/oiectl

run:
	GOWORK=off $(GO) run ./cmd/oie

test:
	GOWORK=off $(GO) test ./...
	GOWORK=off $(GO) -C cli test ./...

test-race:
	GOWORK=off $(GO) test -race ./...

check-generated:
	sh scripts/check-generated.sh

check: lint check-generated
	@test -z "$$(gofmt -l cmd internal api/spec.go tests tools/cmd)" || { echo 'Go formatting drift'; exit 1; }
	GOWORK=off $(GO) vet ./...
	GOWORK=off $(GO) -C cli vet ./...
	GOWORK=off $(GO) -C tools vet ./cmd/...
	$(MAKE) test
	$(COMPOSE) config --quiet

dev-up:
	$(COMPOSE) up --build -d --wait

dev-down:
	$(COMPOSE) down

test-xmpp:
	$(COMPOSE) run --rm --no-deps -T xmpp-check < scripts/test-xmpp.py

# The scratch test container shares Prosody's network, including remote daemons.
test-engine-xmpp:
	GOWORK=off CGO_ENABLED=0 GOOS=linux $(GO) test -c -o bin/oie-xmpp-test ./tests/xmpp
	$(COMPOSE) build engine-check
	$(COMPOSE) run --rm --no-deps -T engine-check

.PHONY: mock-api
mock-api:
	GOWORK=off $(GO) run ./cmd/oie-mock

.PHONY: build-dev-bridge dev-bridge
build-dev-bridge:
	GOWORK=off $(GO) -C tools build -trimpath -o ../bin/oie-xmpp-bridge ./cmd/xmpp-bridge

dev-bridge: build-dev-bridge
	bin/oie-xmpp-bridge

.PHONY: test-live-browser
test-live-browser: build
	sh scripts/test-live-browser.sh
