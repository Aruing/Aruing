SHELL := /bin/sh

# ---------------- app -----------------

APP := aruing
CMD := ./cmd/aruing
BIN_DIR := bin
BIN := $(BIN_DIR)/$(APP)
ARGS ?= help
QUESTION ?= why is demo-api unreachable in default namespace
# chat 可选首句；空则进入 stdin 交互
CHAT_MSG ?=
# 真链路 smoke 使用的 env 文件；可用 make run-llm ENV_FILE=.env.ollama 切换
ENV_FILE ?= .env
GO ?= go
GOLANGCI_LINT ?= golangci-lint
GOVULNCHECK ?= govulncheck
GO_PACKAGES := ./...
GO_SOURCE_DIRS := cmd internal

# 在当前 recipe 的 shell 中加载 dotenv（set -a 使赋值自动 export）
# 必须与后续命令写在同一 shell 行串里（用 ; \），否则 Make 换行会开新 shell 丢掉变量
# 文件不存在时静默跳过，便于无密钥时仍走 fake
define load-dotenv
	set -a; \
	if [ -f "$(ENV_FILE)" ]; then . ./$(ENV_FILE); fi; \
	set +a
endef

.PHONY: run run-llm chat print-env help version

run:
	go run $(CMD) $(ARGS)

# 加载 ENV_FILE（默认 .env）后跑一次诊断；无文件或 LLM 三件套不全时与现行为一致（fake）
run-llm:
	$(load-dotenv); \
	go run $(CMD) run $(QUESTION)

# 加载 ENV_FILE 后进入 chat（须 LLM）；CHAT_MSG 非空则单句，否则 stdin 交互
# 例: make chat / make chat CHAT_MSG='hello' / make chat ENV_FILE=.env.ollama
chat:
	$(load-dotenv); \
	go run $(CMD) chat $(CHAT_MSG)

# 确认 dotenv 是否生效；不打印 API key 全文
print-env:
	@$(load-dotenv); \
	echo "ENV_FILE=$(ENV_FILE)"; \
	if [ -f "$(ENV_FILE)" ]; then echo "file: present"; else echo "file: missing (fake path)"; fi; \
	echo "LLM base: $${ARUING_LLM_BASE_URL:-<empty>}"; \
	echo "LLM model: $${ARUING_LLM_MODEL:-<empty>}"; \
	if [ -n "$$ARUING_LLM_API_KEY" ]; then echo "API key set: yes"; else echo "API key set: no"; fi; \
	echo "kubectl path: $${ARUING_KUBECTL_PATH:-<PATH lookup>}"

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

# ---------------- scenarios -----------------
# kind 故障场景台架（不进 make test / check）。
# 验收以 `aruing chat` 为主对象；详见 scenarios/README.md。

.PHONY: scenario-up scenario-down scenario-list scenario-chat scenario-kube

# NAME=crashloop-bad-image
scenario-up:
	bash scripts/scenario-up.sh "$(NAME)"

scenario-down:
	bash scripts/scenario-down.sh "$(NAME)"

scenario-list:
	@bash scripts/scenario-list.sh

# 对场景集群跑 aruing chat：KUBECONFIG 已在同一行 shell 注入，无需手动 export。
# NAME=必填；MSG=可选（填了=单轮诊断，不填=进交互式多轮）。须先 make build 与 scenario-up。
scenario-chat:
	@test -n "$(NAME)" || { echo "NAME= required (known: make scenario-list)"; exit 1; }
	@test -f scenarios/.kube/$(NAME).yaml || { echo "first: make scenario-up NAME=$(NAME)"; exit 1; }
	@test -x ./bin/aruing || { echo "first: make build"; exit 1; }
ifneq ($(strip $(MSG)),)
	KUBECONFIG=$$PWD/scenarios/.kube/$(NAME).yaml ./bin/aruing chat "$(MSG)"
else
	KUBECONFIG=$$PWD/scenarios/.kube/$(NAME).yaml ./bin/aruing chat
endif

# 对场景集群跑任意 kubectl：KUBECONFIG 已注入。NAME=必填，CMD=kubectl 参数。
# 例: make scenario-kube NAME=crashloop-bad-image CMD="get po -A"
scenario-kube:
	@test -n "$(NAME)" || { echo "NAME= required"; exit 1; }
	@test -f scenarios/.kube/$(NAME).yaml || { echo "first: make scenario-up NAME=$(NAME)"; exit 1; }
	KUBECONFIG=$$PWD/scenarios/.kube/$(NAME).yaml kubectl $(CMD)

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
