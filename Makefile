SHELL := /bin/sh

# ---------------- app -----------------

APP := aruing
CMD := ./cmd/aruing
BIN_DIR := bin
BIN := $(BIN_DIR)/$(APP)
ARGS ?= help
QUESTION ?= why is demo-api unreachable in default namespace
# chat 可选首句；空则进入交互循环
CHAT_MSG ?=
# chat 交互界面：inline（默认留痕）| app（全屏）；透传 --ui
UI ?=
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
	go run $(CMD) chat $(if $(UI),--ui $(UI),) $(CHAT_MSG)

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
	$(MAKE) build
	./"$(BIN)" version

# ---------------- build -----------------

# 版本信息在链接期注入（-X 只能改写包级 var，不能改写 const）
# 无 git 环境（源码 tarball）时凭 shell 兑底为 dev/none，不阻断构建
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(DATE)

.PHONY: build test test-ci fmt fmt-check vet lint lint-fix tidy-check vuln check clean

build:
	@mkdir -p "$(BIN_DIR)"
	$(GO) build -ldflags "$(LDFLAGS)" -o "$(BIN)" $(CMD)

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

# 机械判分 bench：生成器 × 方法 × 预算 × 位置 × 种子遍历 → CSV + 矩阵快照到 bench/results/
# 零 LLM 零集群；出图用 scripts/bench-plot.py（本地 venv，不进 CI）
# 例: make bench / make bench MATRIX=bench/my-matrix.yaml OUT=/tmp/b.csv
MATRIX ?=
OUT ?=

.PHONY: bench

bench:
	$(GO) run $(CMD) bench $(if $(MATRIX),--matrix $(MATRIX),) $(if $(OUT),--out $(OUT),)

# 创新点一实验矩阵（真 LLM + kind，先授权后跑）：场景 × 方法(B1/B2/B4/Ours) × K × 重复
# → eval 记录 + judge 汇总 + CSV 到 eval/results/；子集与输出目录经 env 覆盖
# 例: make eval-sweep DRYRUN=1 / make eval-sweep METHODS="ours b1-serial" OUT=/tmp/sweep
eval-sweep:
	bash scripts/eval-sweep.sh

# ---------------- probe sweep (0.1.3 step 4) ----------------
# 探针实验矩阵驱动器：场景 × 记忆方法 × 轮数 × 重复 → 会话记录 + judge --probe + CSV
# 例: make probe-sweep DRYRUN=1 / make probe-sweep METHODS="ours" ROUNDS="20" OUT=/tmp/probe
probe-sweep:
	bash scripts/probe-sweep.sh

# 全量真集群 smoke（全部场景，严格校验）：up → chat → down，单场景失败不中断，末尾汇总。
# 依赖 Docker/kind/kubectl + bin/aruing + LLM 配置（playground/config.yaml 或 ARUING_CONFIG）。
smoke-all:
	bash scripts/smoke-all.sh

# 一键全量自检 = 静态链 + 真集群 smoke。
self-check: check smoke-all

clean:
	rm -rf "$(BIN_DIR)"

# ---------------- scenarios (lab-*) -----------------
# kind 故障场景台架（不进 make test / check）。
# 验收以 `aruing chat` 为主对象；详见 scenarios/README.md。
# 用户命令前缀 lab-*；脚本实现仍在 scripts/scenario-*.sh（内部）。

.PHONY: lab-up lab-down lab-list lab-chat lab-kube

# NAME=crashloop-bad-image
lab-up:
	bash scripts/scenario-up.sh "$(NAME)"

lab-down:
	bash scripts/scenario-down.sh "$(NAME)"

lab-list:
	@bash scripts/scenario-list.sh

# 对场景集群跑 aruing chat：KUBECONFIG 已在同一行 shell 注入，无需手动 export。
# NAME=必填；MSG=可选（填了=单轮诊断，不填=进交互式多轮）。须先 make build 与 lab-up。
lab-chat:
	@test -n "$(NAME)" || { echo "NAME= required (known: make lab-list)"; exit 1; }
	@test -f scenarios/.kube/$(NAME).yaml || { echo "first: make lab-up NAME=$(NAME)"; exit 1; }
	@test -x ./bin/aruing || { echo "first: make build"; exit 1; }
ifneq ($(strip $(MSG)),)
	KUBECONFIG=$$PWD/scenarios/.kube/$(NAME).yaml ./bin/aruing chat "$(MSG)"
else
	KUBECONFIG=$$PWD/scenarios/.kube/$(NAME).yaml ./bin/aruing chat
endif

# 对场景集群跑任意 kubectl：KUBECONFIG 已注入。NAME=必填，CMD=kubectl 参数。
# 例: make lab-kube NAME=crashloop-bad-image CMD="get po -A"
lab-kube:
	@test -n "$(NAME)" || { echo "NAME= required"; exit 1; }
	@test -f scenarios/.kube/$(NAME).yaml || { echo "first: make lab-up NAME=$(NAME)"; exit 1; }
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
