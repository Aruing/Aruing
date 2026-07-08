SHELL := /bin/sh

# ---------------- app -----------------

APP := aruing
CMD := ./cmd/aruing
BIN_DIR := bin
BIN := $(BIN_DIR)/$(APP)
ARGS ?= help
QUESTION ?= why is demo-api unreachable in default namespace

.PHONY: run help version diagnose

run:
	go run $(CMD) $(ARGS)

help:
	go run $(CMD) help

version:
	go run $(CMD) version

diagnose:
	go run $(CMD) diagnose "$(QUESTION)"

# ---------------- build -----------------

.PHONY: build test fmt clean

build:
	@mkdir -p "$(BIN_DIR)"
	go build -o "$(BIN)" $(CMD)

test:
	go test ./...

fmt:
	gofmt -w cmd internal

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
