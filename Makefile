SHELL := /bin/sh

# ---------------- app -----------------

APP := aruing
CMD := ./cmd/aruing
BIN_DIR := bin
BIN := $(BIN_DIR)/$(APP)
ARGS ?= help
QUESTION ?= why is demo-api unreachable in default namespace
GO ?= go
GOLANGCI_LINT ?= golangci-lint
GOVULNCHECK ?= govulncheck
GO_PACKAGES := ./...
GO_SOURCE_DIRS := cmd internal

.PHONY: run help version

run:
	go run $(CMD) $(ARGS)

help:
	go run $(CMD) help

version:
	go run $(CMD) version

# ---------------- build -----------------

.PHONY: build test test-ci fmt fmt-check vet lint lint-fix tidy-check vuln check clean

build:
	@mkdir -p "$(BIN_DIR)"
	$(GO) build -o "$(BIN)" $(CMD)

test:
	$(GO) test $(GO_PACKAGES)

test-ci:
	$(GO) test -race -shuffle=on -count=1 $(GO_PACKAGES)

fmt:
	gofmt -w $(GO_SOURCE_DIRS)

fmt-check:
	@files="$$(gofmt -l $(GO_SOURCE_DIRS))"; \
	if [ -n "$$files" ]; then \
		echo "$$files"; \
		exit 1; \
	fi

vet:
	$(GO) vet $(GO_PACKAGES)

lint:
	$(GOLANGCI_LINT) run $(GO_PACKAGES)

lint-fix:
	$(GOLANGCI_LINT) run --fix $(GO_PACKAGES)

tidy-check:
	$(GO) mod tidy
	git diff --exit-code -- go.mod go.sum
	@files="$$(git ls-files --others --exclude-standard -- go.mod go.sum)"; \
	if [ -n "$$files" ]; then \
		echo "$$files"; \
		exit 1; \
	fi

vuln:
	$(GOVULNCHECK) $(GO_PACKAGES)

check: tidy-check build test-ci fmt-check vet lint vuln

clean:
	rm -rf "$(BIN_DIR)"

# ---------------- skills -----------------

AGENTS_DIR := .agents
AGENTS_SKILLS_DIR := $(AGENTS_DIR)/skills
AGENT_LEGACY_DIR := agent
ARUING_SKILLS_DIR := docs/skills

.PHONY: install-skills install-aruing-skills reset-skills install-all-skills

install-skills:
	npx --yes skills add https://github.com/samber/cc-skills-golang --all
	rm -rf "$(AGENT_LEGACY_DIR)"

install-aruing-skills:
	@mkdir -p "$(AGENTS_SKILLS_DIR)"
	@find "$(AGENTS_SKILLS_DIR)" -maxdepth 1 -type d -name 'aruing-*' -exec rm -rf {} +
	@for skill in "$(ARUING_SKILLS_DIR)"/aruing-*; do \
		[ -d "$$skill" ] || continue; \
		name=$$(basename "$$skill"); \
		cp -R "$$skill" "$(AGENTS_SKILLS_DIR)/"; \
		echo "installed $$name"; \
	done

reset-skills:
	rm -rf "$(AGENTS_DIR)"
	$(MAKE) install-all-skills

install-all-skills: install-skills install-aruing-skills
