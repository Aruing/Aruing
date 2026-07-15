# Pull Request CI Design

## Goal

Add a GitHub Actions quality gate that checks every active pull request and
re-checks `main` after merges or direct pushes. The same commands must be
available locally through the Makefile.

## Triggering

The workflow runs for pull request `opened`, `synchronize`, `reopened`, and
`ready_for_review` events, and for pushes to `main`. A concurrency group keyed
by workflow and Git ref cancels obsolete runs after a pull request receives a
new commit.

## Components

- `.github/workflows/pr-check.yml` contains one workflow with independent jobs
  for build/tests, lint, and dependency security.
- `.golangci.yml` defines the repository's golangci-lint v2 rules.
- `Makefile` exposes local targets for CI tests, format checking, vetting,
  linting, vulnerability scanning, and module tidiness.

The workflow reads the Go version from `go.mod`, pins GitHub Actions to stable
major versions, and grants only `contents: read` permission.

## Checks

The build/test job verifies module tidiness, builds the CLI, and runs all tests
with the race detector, randomized order, and disabled test-result caching. The
lint job checks gofmt output, runs `go vet`, and runs golangci-lint without
modifying source files. The security job runs `govulncheck` against all Go
packages.

All commands fail with a non-zero exit code. Jobs run independently so a pull
request reports all available failures instead of stopping after the first
category. The workflow does not publish binaries, alter repository contents,
or require repository secrets.

## Verification

Before delivery, run every new Makefile target locally, validate the
golangci-lint configuration, validate the workflow syntax, and confirm the
working tree contains only the intended CI, lint, Makefile, and design changes.
