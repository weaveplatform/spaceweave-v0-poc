SHELL := /bin/bash
.DEFAULT_GOAL := help

# Built outside any parent go.work: local runs match CI.
export GOWORK := off

GO ?= go
GOLANGCI_LINT ?= golangci-lint
MODULE := $(shell GOWORK=off $(GO) list -m)
COVER_DIR := cover
BIN_DIR := bin
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo devel)
COMMIT ?= $(shell git rev-parse HEAD 2>/dev/null)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X $(MODULE)/internal/platform/buildinfo.version=$(VERSION) -X $(MODULE)/internal/platform/buildinfo.commit=$(COMMIT) -X $(MODULE)/internal/platform/buildinfo.buildDate=$(BUILD_DATE)
PLATFORMS ?= linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64
UNIT_PKGS = $(shell GOWORK=off $(GO) list ./... | grep -v /test/acceptance)

## help: list targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## //' | column -t -s ':'

## init: rename the module after creating a repository from the template (make init NAME=<repo>)
init:
	@test -n "$(NAME)" || { echo "usage: make init NAME=<repository name>"; exit 1; }
	@grep -rl --exclude-dir=.git weaveplatform-template . | xargs sed -i.bak 's/weaveplatform-template/$(NAME)/g'
	@find . -name '*.bak' -not -path './.git/*' -delete
	$(GO) mod tidy
	@echo "renamed to github.com/weaveplatform/$(NAME); review README.md and CODEOWNERS"

## fmt: apply the formatters configured in .golangci.yml
fmt:
	$(GOLANGCI_LINT) fmt --config .golangci.yml ./...

## lint: golangci-lint over the whole module
lint:
	$(GOLANGCI_LINT) run --config .golangci.yml --new=false --fix=false ./...

## vet: go vet
vet:
	$(GO) vet ./...

## test: unit tests (race, shuffle); coverage to cover/unit
# The timeout is per package. Under race and coverage, internal/floorplans/processing/autotrace's
# image tracing takes 5–8 minutes on Windows CI runners, whose speed varies
# about twofold between runs, so go test's default 10 minutes failed at
# random there.
test:
	@rm -rf $(COVER_DIR)/unit && mkdir -p $(COVER_DIR)/unit
	$(GO) test -race -shuffle=on -count=1 -timeout 25m -cover -coverpkg=$(MODULE)/... $(UNIT_PKGS) -args -test.gocoverdir=$(CURDIR)/$(COVER_DIR)/unit

## acceptance: run Go acceptance tests and strict Gherkin features against both binaries (needs node); coverage to cover/acceptance
acceptance:
	@rm -rf $(COVER_DIR)/acceptance && mkdir -p $(COVER_DIR)/acceptance
	SPACEWEAVE_COVERDIR=$(CURDIR)/$(COVER_DIR)/acceptance $(GO) test -count=1 ./test/acceptance/...

## eval: the automatic tracer's accuracy on plans drawn from the hand traces, and the golden plan hashes (no -race)
eval:
	set -o pipefail; $(GO) test -count=1 -run 'Accuracy|Golden' -v ./internal/floorplans/processing/autotrace | grep -v '^=== RUN'

## cover: merge every cover/* directory and enforce .testcoverage.yml (>=95% total, >=90% per package)
cover:
	@dirs=$$(find $(COVER_DIR) -mindepth 1 -maxdepth 1 -type d ! -name '.merged' | paste -sd, -); \
	if [ -z "$$dirs" ]; then echo "no coverage data; run make test first"; exit 1; fi; \
	rm -rf $(COVER_DIR)/.merged && mkdir -p $(COVER_DIR)/.merged && \
	$(GO) tool covdata merge -i=$$dirs -o=$(COVER_DIR)/.merged && \
	$(GO) tool covdata textfmt -i=$(COVER_DIR)/.merged -o=$(COVER_DIR)/coverage.out && \
	$(GO) tool covdata percent -i=$(COVER_DIR)/.merged
	$(GO) tool go-test-coverage --config=.testcoverage.yml

## vuln: govulncheck (version pinned in go.mod's tool block, kept current with everything else)
vuln:
	$(GO) tool govulncheck ./...

## build: cross-compile every cmd/* binary for each release platform into bin/ (CGO disabled)
build:
	@mkdir -p $(BIN_DIR)
	@for d in $$(find cmd -mindepth 1 -maxdepth 1 -type d); do \
		name=$${d#cmd/}; \
		for p in $(PLATFORMS); do \
			os=$${p%/*}; arch=$${p#*/}; ext=; [ $$os = windows ] && ext=.exe; \
			echo "$$name $$os/$$arch"; \
			CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags "$(LDFLAGS)" \
				-o $(BIN_DIR)/$$name-$$os-$$arch$$ext ./$$d || exit 1; \
		done; \
	done

## gate: everything CI runs, in order
gate: verify-generate vet lint test eval acceptance cover vuln build

.PHONY: help init fmt lint vet test acceptance eval cover vuln build gate

## quality: report-only CAD package and raster extraction trials (private CAD needs SPACEWEAVE_DWG_DIR)
quality:
	SPACEWEAVE_QUALITY_TRIALS=1 $(GO) test -count=1 -run 'TestQualityTrials|TestRasterTrials' -v ./internal/floorplans/processing/autotrace ./internal/floorplans/processing/autotrace/cadfront

.PHONY: quality

# Pins match the verified OpenAPI 3.0.3 toolchain; tools run in their own module graph.
OAPI_CODEGEN := github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.8.2
SPECS := api operator

## bundle: validate and bundle the product and operator REST contracts
bundle:
	$(GO) run ./tools/openapi-bundle --src api/src --out api

## generate: generate strict Chi servers and public Go clients from the bundles
generate: bundle
	@for surface in $(SPECS); do \
		$(GO) run $(OAPI_CODEGEN) -config api/oapi-server-$$surface.yaml api/$$surface.openapi.yaml || exit 1; \
		$(GO) run $(OAPI_CODEGEN) -config api/oapi-client-$$surface.yaml api/$$surface.openapi.yaml || exit 1; \
	done

## verify-generate: fail if contract bundles or generated clients/servers are stale
verify-generate:
	$(GO) run ./tools/verify-generate

.PHONY: bundle generate verify-generate
