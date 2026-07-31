# agent-profile — build and quality gates, all inside a container.
#
# Host requirement: docker. Nothing else. No Go, no golangci-lint, no
# goreleaser, no gitleaks — every target that needs a toolchain re-invokes
# itself inside the devtools image (Dockerfile.devtools) via scripts/dev.sh.
#
# This is not just convenience. go.mod requires Go 1.25.8 as a SECURITY floor
# (see CLAUDE.md); a host on an older Go would silently fetch a toolchain or
# build against the two stdlib advisories this program is exposed to. The image
# pins the toolchain and every tool version, so `make verify` means the same
# thing on your machine, a colleague's, and in CI.
#
# The handful of targets that must NOT be containerised are marked "Host-only":
# `smoke` drives the four real agent binaries and touches your real home, and
# `install` puts the binary somewhere on your PATH.

SHELL := /usr/bin/env bash
.SHELLFLAGS := -euo pipefail -c
.DEFAULT_GOAL := help

# Recipes assume sequential execution (notably `verify`, whose gates are
# prerequisites). Refuse -j rather than interleave their output.
.NOTPARALLEL:

BIN     := ap
PREFIX  ?= $(HOME)/.local/bin
IMAGE   ?= agent-profile-devtools:latest

# Computed on the HOST, then forwarded into the container, so the stamp reflects
# your checkout and the container never needs to reason about the git dir.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

# The container is always linux/amd64-ish, but the binary has to run on the
# host. Cross-compile to whatever the host actually is; without this a macOS
# user's `make build` yields a Linux binary and `./ap` reports "cannot execute".
GOOS    ?= $(shell uname -s | tr '[:upper:]' '[:lower:]')
GOARCH  ?= $(shell uname -m | sed -e 's/^x86_64$$/amd64/' -e 's/^aarch64$$/arm64/')

# Stamped into cmd/ap so `ap version` reports something useful in a binary
# somebody else is holding. -s -w strips the symbol table; -trimpath keeps
# build paths out of the binary.
LDFLAGS := -s -w \
	-X main.version=$(VERSION) \
	-X main.commit=$(COMMIT) \
	-X main.date=$(DATE)

# --- execution-context routing (explicit opt-in, no magic by prefix) --------
# in_container re-runs a private target (conventionally _name) inside the
# devtools container unless we are already there. Each public target calls it
# explicitly, so `make help` stays honest and a host-only target is never
# wrapped by accident.
#
# The stamp variables are forwarded as command-line overrides because
# scripts/dev.sh does not carry MAKEFLAGS across the docker boundary: without
# this the inner make would recompute them from the container's point of view.
AP_IN_DEVTOOLS ?= 0
# BIN is forwarded for `smoke`, which builds a linux binary into a path of its
# own rather than clobbering ./ap — on macOS that would leave a ./ap the host
# cannot execute. Everywhere else it is the default and forwarding it is a no-op.
FORWARD = VERSION='$(VERSION)' COMMIT='$(COMMIT)' DATE='$(DATE)' GOOS='$(GOOS)' GOARCH='$(GOARCH)' BIN='$(BIN)'

define in_container
	@if [ "$(AP_IN_DEVTOOLS)" = "1" ]; then \
		$(MAKE) --no-print-directory $(1) $(FORWARD); \
	else \
		./scripts/dev.sh $(MAKE) --no-print-directory $(1) $(FORWARD); \
	fi
endef

# The private in-container halves. Declared phony so a stray file named after
# one of them can never make a gate silently no-op.
.PHONY: _build _snapshot _test _test-verbose _cover _fuzz _fmt _fmt-check _vet \
	_lint _lint-fix _vulncheck _secrets _shellcheck _release-publish _verify

##@ General

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

.PHONY: doctor
doctor: ## Check the host has what it needs and the image has what it claims.
	@echo "== agent-profile doctor =="
	@docker info >/dev/null 2>&1 && echo "OK   docker daemon reachable" || { echo "FAIL docker daemon unreachable"; exit 1; }
	@docker image inspect $(IMAGE) >/dev/null 2>&1 && echo "OK   $(IMAGE) present" || echo "INFO $(IMAGE) absent (built on first use)"
	@echo "INFO host stamp: version=$(VERSION) commit=$(COMMIT) target=$(GOOS)/$(GOARCH)"
	@./scripts/dev.sh bash -c 'for t in go gofmt golangci-lint govulncheck goreleaser gitleaks shellcheck git; do \
	    command -v $$t >/dev/null 2>&1 && echo "OK   (container) $$t" || { echo "FAIL (container) $$t MISSING"; exit 1; }; \
	  done; echo "OK   (container) $$(go version)"'
	@docker image inspect $(SMOKE_IMAGE) >/dev/null 2>&1 \
	  && ./scripts/dev.sh true >/dev/null 2>&1 \
	  && docker run --rm --user "$$(id -u):$$(id -g)" -e HOME=/home/smoke $(SMOKE_IMAGE) sh -c \
	     'for a in claude codex opencode pi; do command -v $$a >/dev/null 2>&1 \
	        && echo "OK   (smoke image) $$a $$($$a --version 2>/dev/null | head -1)" \
	        || echo "FAIL (smoke image) $$a MISSING"; done' \
	  || echo "INFO $(SMOKE_IMAGE) absent — built on first `make smoke`"
	@[ -n "$$ANTHROPIC_API_KEY" ] && echo "OK   ANTHROPIC_API_KEY set — smoke's clone block will run" \
	  || echo "INFO ANTHROPIC_API_KEY unset — smoke's clone block will skip (the only check any key buys)"

.PHONY: shell
shell: ## Interactive shell inside the devtools container.
	./scripts/dev.sh bash

.PHONY: devtools-image
devtools-image: ## Rebuild the devtools image from scratch.
	AP_DEVTOOLS_REBUILD=1 ./scripts/dev.sh true

##@ Build

.PHONY: build
build: ## Build ./ap for this host, with version stamping.
	$(call in_container,_build)
_build:
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) \
		go build -trimpath -ldflags '$(LDFLAGS)' -o $(BIN) ./cmd/ap
	@./$(BIN) version 2>/dev/null || echo "built $(BIN) for $(GOOS)/$(GOARCH) (not runnable here)"

.PHONY: install
install: build ## Host-only — copy ./ap into $(PREFIX).
	@mkdir -p $(PREFIX)
	install -m 0755 $(BIN) $(PREFIX)/$(BIN)
	@echo "installed $(PREFIX)/$(BIN) — ensure $(PREFIX) is on your PATH"

.PHONY: snapshot
snapshot: ## Cross-build release archives locally, no publishing.
	$(call in_container,_snapshot)
_snapshot:
	goreleaser release --snapshot --clean

##@ Release

# Cutting a release is the one irreversible thing this repo does, so every gate
# runs BEFORE the tag exists. A failure anywhere leaves origin with no orphan tag
# and the fix is just another `make release` — no tag to delete, no release to
# withdraw. That is why the tag push is the very last step, and why the macOS half
# of the gate is checked through CI (require-green-ci) rather than after tagging.
.PHONY: release
release: ## Host-only — cut a release: gates, tag, push. Usage: make release VERSION=v0.1.0
	@echo '$(VERSION)' | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.]+)?$$' \
		|| { echo "ERROR: pass a semver tag, e.g. make release VERSION=v0.1.0 (got '$(VERSION)')" >&2; exit 1; }
	@branch=$$(git rev-parse --abbrev-ref HEAD); \
		test "$$branch" = main || { echo "ERROR: must be on main (current: $$branch)" >&2; exit 1; }
	@git diff --quiet || { echo "ERROR: working tree is dirty — commit or stash first" >&2; exit 1; }
	@git diff --cached --quiet || { echo "ERROR: index has staged changes" >&2; exit 1; }
	@if git rev-parse -q --verify 'refs/tags/$(VERSION)' >/dev/null; then \
		echo "ERROR: tag $(VERSION) already exists" >&2; exit 1; fi
	@git fetch --quiet origin 2>/dev/null || true
	@if git rev-parse -q --verify refs/remotes/origin/main >/dev/null; then \
		test "$$(git rev-parse HEAD)" = "$$(git rev-parse origin/main)" \
			|| { echo "ERROR: local main differs from origin/main — pull or push first" >&2; exit 1; }; fi
	$(MAKE) verify
	$(MAKE) secrets
	$(MAKE) snapshot
	@$(MAKE) --no-print-directory require-green-ci
	@echo
	@echo "gates green — tagging $(VERSION) and pushing"
	git tag -a $(VERSION) -m "$(VERSION)"
	@if git rev-parse -q --verify refs/remotes/origin/main >/dev/null; then \
		git push origin main; else git push -u origin main; fi
	git push origin $(VERSION)
	@echo
	@echo "release.yml is now building $(VERSION). Watch it with:"
	@echo "  gh run watch \$$(gh run list --workflow Release --limit 1 --json databaseId --jq '.[0].databaseId')"

# `make verify` covers Linux only, because the devtools container is Linux. macOS
# is the other supported platform, and a macOS-only defect has already shipped
# this way: the Clone containment check compared a resolved path against an
# unresolved one, which only diverges where /var is a symlink to /private/var.
#
# CI has already run on this commit — `release` refuses unless HEAD matches
# origin/main — so require that run to be green rather than discovering a macOS
# failure after the tag is public. That keeps the invariant: every gate passes
# before the tag exists.
.PHONY: require-green-ci
require-green-ci:
	@if ! command -v gh >/dev/null 2>&1; then \
		echo "WARNING: gh not installed, so the macOS half of CI cannot be checked here."; \
		echo "         make verify only covers Linux. If macOS fails after the tag is"; \
		echo "         pushed you will have an orphan tag to delete. Continuing."; \
		exit 0; \
	fi; \
	head=$$(git rev-parse HEAD); \
	res=$$(gh run list --workflow CI --limit 20 \
		--json headSha,conclusion,status --jq \
		".[] | select(.headSha==\"$$head\") | \"\(.status) \(.conclusion)\"" 2>/dev/null | head -1); \
	case "$$res" in \
		"completed success") echo "OK   CI green on $$head (ubuntu + macos)" ;; \
		"") echo "ERROR: no CI run found for HEAD ($$head). Push and let CI finish first." >&2; exit 1 ;; \
		completed*) echo "ERROR: CI on HEAD concluded '$$res' — fix it before releasing." >&2; exit 1 ;; \
		*) echo "CI on HEAD is still running ($$res). Wait for it, then re-run:" >&2; \
		   echo "  gh run watch \$$(gh run list --workflow CI --limit 1 --json databaseId --jq '.[0].databaseId')" >&2; \
		   exit 1 ;; \
	esac

.PHONY: release-publish
release-publish: ## Internal — goreleaser publish for the tag at HEAD. release.yml calls this.
	$(call in_container,_release-publish)
_release-publish:
	@test -n "$${GITHUB_TOKEN:-}" || { echo "GITHUB_TOKEN is not set"; exit 1; }
	goreleaser release --clean

##@ Test

.PHONY: test
test: ## Run all tests with race detection and shuffling.
	$(call in_container,_test)
_test:
	go test -race -shuffle=on -count=1 -coverprofile coverage.out ./...

.PHONY: test-verbose
test-verbose: ## Run all tests, listing every test name.
	$(call in_container,_test-verbose)
_test-verbose:
	go test -v -race -shuffle=on -count=1 ./...

.PHONY: cover
cover: ## Report coverage per function.
	$(call in_container,_cover)
_cover: _test
	go tool cover -func coverage.out | tail -20

.PHONY: fuzz
fuzz: ## Fuzz the two path-validation surfaces, 30s each (the traversal-bug lesson).
	$(call in_container,_fuzz)
_fuzz:
	go test -run '^$$' -fuzz FuzzValidName -fuzztime 30s ./internal/profile/
	go test -run '^$$' -fuzz FuzzParseVariantRef -fuzztime 30s ./internal/profile/

# The agents run in their own image, not on your machine. That image is where the
# four real binaries live, so this target needs none of them installed on the
# host and never writes to your home — which is also what makes it runnable in
# CI, where the release gate could not reach it before.
#
# The binary it drives is built for linux, into a path of its own: ./ap is built
# for the HOST, and on macOS that binary cannot run in the container at all.
SMOKE_IMAGE ?= agent-profile-smoke:latest
SMOKE_ARCH  ?= $(shell uname -m | sed -e 's/^x86_64$$/amd64/' -e 's/^aarch64$$/arm64/')

.PHONY: smoke
smoke: ## Drive the four real agent binaries inside their own container.
	@$(MAKE) --no-print-directory build GOOS=linux GOARCH=$(SMOKE_ARCH) BIN=.gocache/smoke/ap
	AP_SMOKE_IMAGE=$(SMOKE_IMAGE) ./scripts/smoke.sh

.PHONY: smoke-image
smoke-image: ## Rebuild the smoke image (the four agents, unpinned on purpose).
	docker build --pull --no-cache -t $(SMOKE_IMAGE) -f Dockerfile.smoke .

# Not wrapped by in_container: the script re-enters the container itself, having
# first pointed HOME at a home it builds and throws away. Wrapping it would
# nest, and dev.sh would then be arguing with itself about HOME.
#
# `sandbox` is not a substitute for `smoke`, and a green one does not stand in
# for the other. smoke asks whether the real binaries honour the registry, which
# needs them installed and logged in; sandbox asks whether ap keeps its hands off
# a home that already has things in it, which needs neither and so can run here.
.PHONY: sandbox
sandbox: ## Run the home-safety checks against a throwaway home in the container.
	./scripts/sandbox.sh

##@ Quality

.PHONY: fmt
fmt: ## Format the code.
	$(call in_container,_fmt)
_fmt:
	gofmt -w ./cmd ./internal

.PHONY: fmt-check
fmt-check: ## Fail if any Go file is not gofmt-clean. Does not mutate.
	$(call in_container,_fmt-check)
_fmt-check:
	@out=$$(gofmt -l ./cmd ./internal); \
	if [ -n "$$out" ]; then echo "Not gofmt-clean:"; echo "$$out"; exit 1; fi; \
	echo "OK gofmt-clean"

.PHONY: shellcheck
shellcheck: ## Lint the shell scripts, install.sh included.
	$(call in_container,_shellcheck)
_shellcheck:
	shellcheck install.sh scripts/*.sh

.PHONY: vet
vet: ## Run go vet.
	$(call in_container,_vet)
_vet:
	go vet ./...

.PHONY: lint
lint: ## Run golangci-lint (includes gosec).
	$(call in_container,_lint)
_lint:
	golangci-lint run

.PHONY: lint-fix
lint-fix: ## Run golangci-lint with --fix.
	$(call in_container,_lint-fix)
_lint-fix:
	golangci-lint run --fix

.PHONY: vulncheck
vulncheck: ## Check dependencies and stdlib for known vulnerabilities.
	$(call in_container,_vulncheck)
_vulncheck:
	govulncheck ./...

.PHONY: secrets
secrets: ## Scan the full git history for secrets.
	$(call in_container,_secrets)
_secrets:
	gitleaks git --no-banner --redact --verbose .

.PHONY: verify
verify: ## Everything CI runs, in one container hop.
	$(call in_container,_verify)
_verify: _fmt-check _shellcheck _vet _lint _test _vulncheck
	@echo
	@echo "verify OK — and before pushing a public change, also: make secrets smoke"

##@ Housekeeping

.PHONY: clean
clean: ## Remove build artifacts. Leaves ./.gocache alone.
	rm -rf $(BIN) dist coverage.out

.PHONY: clean-cache
clean-cache: ## Remove ./.gocache. Go's modcache is read-only, so unlock it first.
	@if [ ! -e .gocache ]; then echo "no ./.gocache — nothing to clean"; exit 0; fi
	@chmod -R u+w .gocache 2>/dev/null || true
	rm -rf .gocache
	@echo "removed ./.gocache (re-created on the next containerised target)"

.PHONY: clear
clear: clean clean-cache ## clean + clean-cache.

.PHONY: hooks
hooks: ## Install the pre-push hook (runs verify).
	@mkdir -p .git/hooks
	@printf '#!/bin/sh\nexec make verify\n' > .git/hooks/pre-push
	@chmod +x .git/hooks/pre-push
	@echo "installed .git/hooks/pre-push -> make verify"
