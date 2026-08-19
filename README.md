# Aruing

[English](README.md) | [中文](README.zh-CN.md)

[![CI](https://github.com/Aruing/aruing/actions/workflows/pr-check.yml/badge.svg)](https://github.com/Aruing/aruing/actions/workflows/pr-check.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)
[![Go Version](https://img.shields.io/badge/go-1.26-00ADD8.svg)](go.mod)

**A Kubernetes ops agent that answers with tools and evidence—not vibes.**

Ask in natural language. The agent reasons, calls cluster tools for real evidence, forms hypotheses, and—when you need root cause—runs a forced evidence-adjudication chain so every conclusion is traceable.

## Why an agent (not a chatbot)

| Layer | What it does |
| --- | --- |
| **Always-on baseline** | Observe → think → **call tools** → observe → answer. Grounded in live cluster data for “what’s installed?”, “how is this service configured?”, and similar questions. |
| **Diagnostic specialty** | On root-cause asks, escalate into a formal pipeline: hypothesis → tasks → **Evidence** → **Verdict** (must cite Evidence) → **Report**. No “I think it’s X” without a tool trail. |
| **Multi-turn chat** | Same session follow-ups; prior formal runs can be re-read by `RunID` for deeper explanation without inventing evidence. |

Tools go through a shared **Registry / Dispatcher** (shell-less kubectl backend, policy for auth). Model output never pretends to be Evidence.

![Aruing inline chat diagnosing a crashloop pod](docs/assets/example.png)

## Current stage

**`0.1.0` — usable diagnostic assistant, released for evaluation.** Runs end-to-end against a real cluster with a real LLM: interactive terminal chat (inline or fullscreen), theme customization, evidence navigation with time-window slicing, and clarify-suspend/resume on ambiguous asks.

Not yet built (planned 0.2+): disk persistence (sessions are in-memory and lost on exit), streaming responses, write tools with approval, multi-cluster, Web UI. See [`docs/project-state.md`](docs/project-state.md) for the live roadmap.

⚠️ Only simple trials / testing are supported right now — not production-ready.

## Core data flow

```
Run → Query → Target → Hypothesis → Task → Evidence → Verdict → Report
```

- **Run** — one diagnosis unit  
- **Query / Node / Edge** — unverified clues from the question  
- **Target** — objects confirmed in the real cluster  
- **Hypothesis** — candidate causes awaiting evidence  
- **Evidence** — records from actual tool execution (only trusted fact source)  
- **Verdict** — only from Evidence  
- **Report** — cites Verdict + Evidence; does not invent  

## Requirements

| Requirement | Notes |
| --- | --- |
| Go 1.26+ | build only |
| kubectl | cluster access; path auto-detected or set `tools.kubectl_path` |
| LLM (OpenAI-compatible) | any base URL + model; required by `run` / `chat` |
| Docker + kind | optional — only for the reproducible fault scenarios |

## Install

One-liners (installs the latest release to `~/.aruing/bin`, checksum-verified):

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/Aruing/Aruing/main/scripts/install.sh | bash
```

```powershell
# Windows (PowerShell) — experimental: core commands verified; interactive `chat` TUI not yet validated on real Windows terminals
irm https://raw.githubusercontent.com/Aruing/Aruing/main/scripts/install.ps1 | iex
```

If `~/.aruing/bin` is not on your `PATH`, the installer prints the exact line to add. From source: `go install github.com/Aruing/Aruing/cmd/aruing@latest` (once v0.1.0 is tagged) or `make build`.

## Quick start

```bash
make build              # build cmd/aruing
make test               # all tests
make check              # full CI (test-ci + vet + lint + fmt + tidy + vuln)

cp aruing.example.yaml playground/config.yaml   # fill llm.* (gitignored)
./bin/aruing run --config playground/config.yaml why is demo-api in default unreachable
./bin/aruing chat --config playground/config.yaml hello   # interactive TUI (inline mode)
```

`run` / `chat` **require** a complete LLM config. Priority: CLI flags (e.g. `--verbose`, `--ui`) > env (`ARUING_*`) > YAML file > zeros. Config search: `--config` / `ARUING_CONFIG` → `playground/config.yaml` → `$XDG_CONFIG_HOME/aruing` → `/etc/aruing`.

Other examples:

```bash
./bin/aruing run --format json why is demo-api in default unreachable
./bin/aruing chat --session sess_xxx check redis again   # resume a session
./bin/aruing chat --ui app                               # fullscreen mode
```

### Terminal UI

`aruing chat` ships two modes (config `tui.mode` or `--ui`):

- **inline** (default) — scrollback-style chat in your terminal, markdown rendered via glamour, soft newlines (shift+enter)
- **app** — fullscreen bubbletea interface

Themes: built-in `dark` / `light` / `auto` (config `tui.theme`). For full customization, copy [`tui.example.yaml`](tui.example.yaml) and point `tui.theme_file` at it — declare only the style entries you want to override; the rest falls back to the built-in base.

### Reproducible scenarios (kind)

One-shot fault clusters for manual smoke. Verification targets `chat` (see [`scenarios/README.md`](scenarios/README.md)):

```bash
make lab-list                                     # known scenarios + cluster state
make lab-up   NAME=crashloop-bad-image            # kind cluster + fault manifests
make lab-chat NAME=crashloop-bad-image MSG="why is demo-api in demo not starting"
make lab-down NAME=crashloop-bad-image
```

Four scenarios ship today: `crashloop-bad-image`, `svc-wrong-selector`, `same-name-multi-ns` (incl. a multi-turn clarify-suspend case), `log-time-window` (evidence time-window slicing). `lab-chat` / `lab-kube` inject KUBECONFIG for you (no manual export). Not part of `make test` / CI; requires Docker + kind + kubectl locally.

### Configuration & local LLM

Full reference: [`aruing.example.yaml`](aruing.example.yaml) (annotated: llm / tools / tui / debug). Env fallback: [`.env.example`](.env.example) with `make run-llm` / `make print-env` (Make sources `.env`; the binary itself does not parse it).

## Constraints (short)

- Flat entities linked by `RunID`—no nested `Run`  
- Clues are not Targets until the environment confirms them  
- Model output ≠ Evidence; Verdicts must cite Evidence  
- No enumerating user ops / resource types; no artificial N-item amputations of normal capability (over budget → compact, don’t silently drop)  
- Tools are not inherently R/O; policy gates execution. Read tools registered now; write tools later with approval  
- `run` → Orchestrator; `chat` → Session.Turn + Tower; same Dispatcher  

Full list: [`docs/architecture.md`](docs/architecture.md#硬约束) (incl. #15–#20).

## Docs

| Path | Content |
| --- | --- |
| [`docs/architecture.md`](docs/architecture.md) | Architecture facts: modules, data model, trust boundary, hard constraints |
| [`docs/project-state.md`](docs/project-state.md) | Stage, work units, next step |
| [`docs/README.md`](docs/README.md) | What lives in docs/ vs the private notebook |
| [`scenarios/README.md`](scenarios/README.md) | Kind fault scenarios: usage, cases protocol, verification |
| [`aruing.example.yaml`](aruing.example.yaml) / [`tui.example.yaml`](tui.example.yaml) | Annotated config / theme references |
| [`docs/skills/`](docs/skills) | Project skills (docs, tests, comments, PR description, milestone close, self-check, cluster smoke, retrospective) |
| [`AGENTS.md`](AGENTS.md) | AI tooling / skill install |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | How to contribute: setup, PR rules, verification |

Longer design notes live in a private `arui-note/aruing/` notebook (maintainer only).
