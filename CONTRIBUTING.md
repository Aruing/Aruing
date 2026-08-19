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

## Releasing

Branch model: `main` is the development trunk; `production` is the release branch. The two are kept in sync through release PRs:

1. Snapshot main into a release branch: `git branch release/sync-N main && git push origin release/sync-N` (the snapshot freezes what gets released; ff-merge the branch onto latest main if you want to pick up newer commits).
2. Open a PR `release/sync-N` → `production`. This triggers the full release check suite (pr-check ×3 + pr-agent + cross-build ×5 + platform-smoke ×3). Merge only when all checks pass.
3. Merge with **merge commits** (squash/rebase are disabled by branch protection) — the merge node marks one release batch.
4. Tag on production: `git checkout production && git pull && git tag v0.X.Y && git push origin v0.X.Y`. The `Release` workflow verifies the tag sits on production, builds 5-platform artifacts via GoReleaser, and attaches them to a **draft** GitHub Release.
5. Review the draft release (5 archives + checksums.txt), then publish it manually.

Version numbers are injected at build time from the tag (`-ldflags -X`), never hard-coded. An `rc` tag (e.g. `v0.1.0-rc1`) is auto-marked as pre-release.

## AI agents

The repo is developed with heavy AI-agent involvement. [`AGENTS.md`](AGENTS.md) documents the tooling; [`docs/skills/`](docs/skills) holds the project skills (code comments, test guidelines, docs, PR description, milestone close, self-check, cluster smoke, retrospective audit). Human review gates every design freeze and merge.
