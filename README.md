# Aruing

[English](README.md) | [中文](README.zh-CN.md)

**A Kubernetes ops agent that answers with tools and evidence—not vibes.**

Ask in natural language. The agent reasons, calls cluster tools for real evidence, forms hypotheses, and—when you need root cause—runs a forced evidence-adjudication chain so every conclusion is traceable.

## Why an agent (not a chatbot)

| Layer | What it does |
| --- | --- |
| **Always-on baseline** | Observe → think → **call tools** → observe → answer. Grounded in live cluster data for “what’s installed?”, “how is this service configured?”, and similar questions. |
| **Diagnostic specialty** | On root-cause asks, escalate into a formal pipeline: hypothesis → tasks → **Evidence** → **Verdict** (must cite Evidence) → **Report**. No “I think it’s X” without a tool trail. |
| **Multi-turn chat** | Same session follow-ups; prior formal runs can be re-read by `RunID` for deeper explanation without inventing evidence. |

Tools go through a shared **Registry / Dispatcher** (shell-less kubectl backend, policy for auth). Model output never pretends to be Evidence.

## Current stage

**In active development — closing the diagnostic loop.** Targeting a usable closed loop by **end of August 2026**.

Aruing runs end-to-end today, but it's still early:

- **Only simple trials / testing are supported right now** — not production-ready
- **Supporting UX & engineering is still in progress** — terminal input UX, structured logging, and similar polish are not yet built

What works today (version `0.1.0`, in progress):

- `aruing run` / `chat` — LLM required; YAML config and/or `ARUING_*` env (`--config`, see `aruing.example.yaml`)
- `aruing run` — single-shot diagnosis via linear Orchestrator (ambiguity → clarify message + non-zero exit; no resume)
- `aruing chat` — multi-turn Session + Tower; escalate when root cause is needed; resolve clarify suspend/resume; `RunLedger` + `prior_run_details`; after compaction, range rehydrate

Details: [`docs/project-state.md`](docs/project-state.md).

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

## Quick start

```bash
make build              # build cmd/aruing
make test               # all tests
make check              # full CI (test-ci + vet + lint + fmt + tidy + vuln)

./bin/aruing run why is demo-api in default unreachable
./bin/aruing run --format json why is demo-api in default unreachable
./bin/aruing chat hello                                    # multi-turn (LLM required; session id on stderr)
./bin/aruing chat --session sess_xxx check redis again
```

### Reproducible scenarios (kind)

One-shot fault clusters for manual smoke. Verification targets `chat` (see [`scenarios/README.md`](scenarios/README.md)):

```bash
make lab-up   NAME=crashloop-bad-image   # kind cluster + fault manifests
make lab-chat NAME=crashloop-bad-image MSG="why is demo-api in demo not starting"
make lab-down NAME=crashloop-bad-image
```

`lab-chat` / `lab-kube` inject KUBECONFIG for you (no manual export). Not part of `make test` / CI; requires Docker + kind + kubectl locally.

### Configuration & local LLM

`run` / `chat` **require** a complete LLM config (file and/or env). Priority: CLI flags (e.g. `--verbose`) > env (`ARUING_*`) > YAML file > zeros.

```bash
cp aruing.example.yaml playground/config.yaml   # fill llm.*; gitignored under playground/
./bin/aruing run --config playground/config.yaml why is demo-api unreachable
# or: ARUING_CONFIG=... / search playground → $XDG_CONFIG_HOME/aruing → /etc/aruing
```

Make + `.env` still works (repo ignores `.env`; package does not parse the file itself):

```bash
cp .env.example .env    # BaseURL / APIKey / Model
make print-env
make run-llm
make run-llm QUESTION='why is demo-api in default unreachable'
make chat
make chat CHAT_MSG='hello'
```

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
| [`docs/skills/`](docs/skills) | Project skills (docs, tests, comments, PR description, milestone close, self-check, cluster smoke, retrospective) |
| [`AGENTS.md`](AGENTS.md) | AI tooling / skill install |

Longer design notes live in a private `arui-note/aruing/` notebook (maintainer only).
