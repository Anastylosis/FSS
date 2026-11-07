# FSS — common dev targets.
#
# Run `make help` for the full list.

GO       ?= go
PKGS     := ./...
# `go test -timeout` applies per test binary, i.e. per package — not to the run
# as a whole. Packages differ enormously in how many live scrapes they pack in:
# gamma has 45 RunLiveScrape calls, dirtyflix 16. Worst case per call is ~183s
# (a 90s context, a 3s pause, then a 90s retry), so a couple of slow sites in
# gamma can blow a 5m budget — and Go panics the whole binary on timeout,
# losing every result in the package rather than just the slow one.
#
# Measured across a full run: median package 2.9s, only 3 over 150s. This bound
# exists to stop a runaway package, not to pace healthy ones, so it is set well
# clear of the worst observed case.
SMOKE_TIMEOUT ?= 20m
GOLINT   ?= golangci-lint

# Use bash for all recipes (need PIPESTATUS, [[ ]], etc. in the smoke target).
SHELL := /bin/bash
# Stricter shell behaviour for every recipe:
#   -u             : error on unset variables (catches typos like $$pas vs $$pass)
#   -o pipefail    : a failing command in a pipe makes the pipe fail
#   -c             : run argument as a command (required when overriding SHELLFLAGS)
# `-e` is intentionally omitted: the `smoke` target uses `;`-chained commands so
# the summary still prints when tests fail, which `set -e` would abort.
.SHELLFLAGS := -u -o pipefail -c

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help.
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z0-9_-]+:.*##/ {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}' $(MAKEFILE_LIST)

.PHONY: build
build: ## Build the fss binary into ./fss.
	$(GO) build -o fss .

.PHONY: test
test: ## Run unit tests with race detector (no integration tag).
	$(GO) test -race -count=1 $(PKGS)

.PHONY: i18n-extract
i18n-extract: ## Regenerate the i18n locale template and pseudo catalogs from the command tree.
	FSS_I18N_WRITE=1 $(GO) test -count=1 -run TestI18nTemplateInSync ./cmd/

.PHONY: smoke
smoke: ## Run integration smoke tests against live sites + Stash. Manual only — never in CI.
	@echo "==> Integration smoke tests (live HTTP, not for CI)"
	@echo "==> Tests with placeholder URLs will SKIP. Stash tests skip if not reachable."
	@# Two invocations, not one package list: the Stash tests live in ./cmd
	@# alongside that package's unit tests, so they need -run TestLive to
	@# select them. The scrapers must NOT carry that filter — a few name their
	@# entry point TestIntegration, and a global -run would drop them silently.
	@rm -f /tmp/fss-smoke.fail; \
	{ $(GO) test -tags=integration -timeout=$(SMOKE_TIMEOUT) -v ./internal/scrapers/... \
	    || echo x >> /tmp/fss-smoke.fail; \
	  $(GO) test -tags=integration -timeout=$(SMOKE_TIMEOUT) -run TestLive -v ./cmd/... \
	    || echo x >> /tmp/fss-smoke.fail; \
	} 2>&1 | tee /tmp/fss-smoke.log; \
	rc=0; [ -s /tmp/fss-smoke.fail ] && rc=1; \
	echo ""; \
	echo "========================================"; \
	echo "  SMOKE TEST SUMMARY"; \
	echo "========================================"; \
	pass=$$(grep -c '^--- PASS' /tmp/fss-smoke.log || true); \
	fails=$$(grep -c '^--- FAIL' /tmp/fss-smoke.log || true); \
	skip=$$(grep -c '^--- SKIP' /tmp/fss-smoke.log || true); \
	echo "  PASS: $$pass  FAIL: $$fails  SKIP: $$skip"; \
	echo ""; \
	if [ "$$fails" -gt 0 ]; then \
		echo "  Failed tests:"; \
		grep '^--- FAIL' /tmp/fss-smoke.log | sed 's/^--- FAIL: /    ✗ /' | sed 's/ (.*//' ; \
		echo ""; \
		echo "  Failed packages:"; \
		grep '^FAIL	' /tmp/fss-smoke.log | sed 's/^FAIL/    ✗/' ; \
		echo ""; \
	fi; \
	echo "========================================"; \
	rm -f /tmp/fss-smoke.log /tmp/fss-smoke.fail; \
	exit $$rc

.PHONY: cover
cover: ## Unit-test coverage, matching what CI reports.
	$(GO) test -count=1 -coverpkg=./... -coverprofile=coverage.out -covermode=atomic $(PKGS)
	@$(MAKE) -s compact-coverage PROFILE=coverage.out
	@$(GO) tool cover -func=coverage.out | tail -1

.PHONY: smoke-cover
smoke-cover: ## Coverage including integration tests. Manual only — same live-HTTP caveat as smoke.
	@echo "==> Unit + integration coverage (live HTTP, not for CI)"
	@echo "==> Cloudflare blocks datacentre IPs, so run this from a normal connection."
	$(GO) test -tags=integration -timeout=$(SMOKE_TIMEOUT) -coverpkg=./... \
		-coverprofile=coverage.smoke.out -covermode=atomic $(PKGS) || true
	@$(MAKE) -s compact-coverage PROFILE=coverage.smoke.out
	@echo ""
	@$(GO) tool cover -func=coverage.smoke.out | tail -1
	@echo "==> Compare with 'make cover' to see what the integration tests add."

.PHONY: compact-coverage
compact-coverage: ## Internal: collapse -coverpkg duplicate blocks in $(PROFILE).
	@awk 'NR==1 { print; next } { n[$$1] = $$2; c[$$1] += $$3 } END { for (k in n) print k, n[k], c[k] }' \
		$(PROFILE) > $(PROFILE).merged && mv $(PROFILE).merged $(PROFILE)

.PHONY: smoke-one
smoke-one: ## Run smoke for one scraper. Usage: make smoke-one SCRAPER=manyvids
	@if [ -z "$(SCRAPER)" ]; then echo "usage: make smoke-one SCRAPER=<name>"; exit 1; fi
	$(GO) test -tags=integration -timeout=$(SMOKE_TIMEOUT) -v ./internal/scrapers/$(SCRAPER)/...

.PHONY: smoke-stash
smoke-stash: ## Run Stash integration tests. Set FSS_STASH_URL / FSS_STASH_API_KEY if needed.
	$(GO) test -tags=integration -timeout=$(SMOKE_TIMEOUT) -run TestLive -v ./cmd/...

.PHONY: vet
vet: ## go vet on all packages (including integration-tagged).
	$(GO) vet $(PKGS)
	$(GO) vet -tags=integration $(PKGS)

.PHONY: lint
lint: vet ## Run go vet + golangci-lint.
	$(GOLINT) run --timeout=5m

.PHONY: tidy
tidy: ## go mod tidy.
	$(GO) mod tidy

.PHONY: clean
clean: ## Remove built binary and test artifacts.
	rm -f fss fss.exe coverage.out coverage.smoke.out test-output.txt

.PHONY: docker
docker: ## Build the docker image as fss:dev with version metadata from git.
	docker build \
	  --build-arg VERSION=$$(git describe --tags --always --dirty) \
	  --build-arg COMMIT=$$(git rev-parse --short HEAD) \
	  --build-arg DATE=$$(date -u +%Y-%m-%dT%H:%M:%SZ) \
	  -t fss:dev .
