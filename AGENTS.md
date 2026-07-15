# AGENTS.md

## Agent Setup

- Install all skills with `make install-all-skills`.
- Reset local skill state with `make reset-skills`.
- Third-party Go skills are installed from `samber/cc-skills-golang` using the latest available version.
- Project-specific skills live under `docs/skills/aruing-*`.
- Do not commit `.agents/`, `agent/`, or `skills-lock.json`.

## Project Skills

- Keep project-specific skill names prefixed with `aruing-`.
- After editing `docs/skills/`, run `make install-aruing-skills` to sync them into `.agents/skills`.
