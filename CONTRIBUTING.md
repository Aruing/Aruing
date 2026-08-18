# Contributing

Thanks for your interest in contributing! Aruing is in active early development (0.x) — issues and PRs are welcome.

## Ground rules

- Read [`docs/architecture.md`](docs/architecture.md#硬约束) first — the numbered **hard constraints (#1–#20)** are non-negotiable. PRs that violate them will be rejected.
- Evidence discipline is the core of this project: model output never pretends to be `Evidence`; `Verdict` must cite `Evidence`. Design your change around this trust boundary.

## Development setup

```bash
git clone https://github.com/Aruing/aruing && cd aruing
make build && make test    # build + tests
make check                # full CI gate (tests, vet, lint, fmt, tidy, vuln)
```

Requirements: Go 1.26+, kubectl, and an LLM config for manual smoke (see [`aruing.example.yaml`](aruing.example.yaml)).

## Pull requests

1. Branch from `main` (`feat/...`, `fix/...`, `docs/...`).
2. Keep one PR = one concern. Run `make lint fmt-check` before opening a PR (CI will fail otherwise).
3. PRs follow a structured description (type / scope / architecture impact / breaking changes / docs-sync checklist). If you use an AI agent with the repo's skills installed, this is generated automatically; otherwise follow the headings of recent merged PRs.
4. Docs and code land in the **same PR** when a change affects architecture or project state (see `docs/architecture.md`, `docs/project-state.md`).
5. New tools go through `Registry` + `Policy` — never a private execution path (constraint #16/#17).

## Verification

- Unit tests must stay green: `make test`.
- Changes touching `internal/tools`, `internal/agent`, `internal/tui`, or orchestration should be smoke-tested against a real cluster via the kind scenarios ([`scenarios/README.md`](scenarios/README.md)): `make smoke-all`.
- No auto-scoring/golden tests for LLM output — scenario verification is checklist-based (`expect.md`).

## AI agents

The repo is developed with heavy AI-agent involvement. [`AGENTS.md`](AGENTS.md) documents the tooling; [`docs/skills/`](docs/skills) holds the project skills (code comments, test guidelines, docs, PR description, milestone close, self-check, cluster smoke, retrospective audit). Human review gates every design freeze and merge.
