---
sidebar_position: 6
---

# Contributing Guide

Thank you for your interest in improving AgentCube! This guide outlines how to get started, the workflow we follow, and the expectations for contributors. The structure closely follows the [Volcano](https://github.com/volcano-sh/volcano/blob/master/contribute.md) contributor guide, with adjustments for the AgentCube project.

## How to Get Involved

- Join the conversation on GitHub:
  - [Issues](https://github.com/volcano-sh/agentcube/issues) for bug reports and feature requests
  - [Discussions](https://github.com/volcano-sh/agentcube/discussions) for design questions and ideas
- Review the [Code of Conduct](https://github.com/volcano-sh/community/blob/master/CODE_OF_CONDUCT.md) before participating

---

## Contribution Workflow

### 1. Pick or Propose Work

- Search open issues labeled `good first issue` or `help wanted`
- If you have a new idea, open an issue describing the motivation, proposal, and alternatives
- For large changes, propose an enhancement in Discussions first to get feedback

### 2. Set Up Your Environment

Install the required tools:

```bash
# Required
go version  # Must be the version in go.mod
make --version
docker --version  # or podman

# Recommended for local testing
kind version  # Kubernetes in Docker
kubectl version --client
```

Clone the repository:

```bash
git clone https://github.com/volcano-sh/agentcube.git
cd agentcube
```

Verify the setup:

```bash
make lint    # Run linters
make test    # Run unit tests
make build   # Build binaries
```

### 3. Create a Working Branch

```bash
# From the repo root
git checkout main
git pull origin main
git checkout -b feat/<short-description>
```

Branch naming conventions:

- `feat/<description>` — New features
- `fix/<description>` — Bug fixes
- `docs/<description>` — Documentation changes
- `refactor/<description>` — Refactoring

### 4. Develop with Tests

- Follow Go best practices and respect existing module structure under `pkg/` and `cmd/`
- Maintain backwards compatibility for user-facing APIs (CRDs, CLI, HTTP schemas)
- Add unit tests in the relevant `*_test.go` files or integration tests under `test/`
- Run `make lint` and `make test` before submitting your changes

```bash
# Run a specific package's tests
go test -v ./pkg/workloadmanager/...

# Run with race detector
go test -race ./...

# Check test coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### 5. Commit Conventions

Use clear, descriptive commit messages in the format `component: summary`:

```
router: fix session ID not returned in response header
workloadmanager: add garbage collection for expired sandboxes
docs: update getting-started with Redis setup steps
```

- Reference issues with `Fixes #<issue-number>` or `Refs #<issue-number>` when applicable
- Sign off your commits if required by your employer/company policies

```bash
git commit -m "router: fix nil pointer when session not found

Fixes #123"
```

### 6. Keep Your Branch Up to Date

Rebase frequently on `main` to reduce merge conflicts:

```bash
git fetch origin
git rebase origin/main
```

Resolve conflicts locally and rerun tests after rebasing.

### 7. Open a Pull Request

- Push your branch and create a PR against `main`
- Fill in the PR template, covering:
  - Problem statement
  - Summary of changes
  - Testing performed
  - Screenshots/logs if relevant
- Link related issues or discussions for context
- Request review from maintainers or area owners (see the `OWNERS` directories)

### 8. Code Review

To make it easier for your PR to receive reviews, consider that reviewers will need you to:

- Follow [good coding guidelines](https://go.dev/wiki/CodeReviewComments)
- Write [good commit messages](https://chris.beams.io/posts/git-commit/)
- Break large changes into a logical series of smaller patches which individually make easily understandable changes, and in aggregate solve a broader issue
- Label PRs with appropriate reviewers: read the messages the bot sends you to guide you through the PR process

### 9. Address Review Feedback

- Be responsive to comments and iterate quickly
- Reply to each comment once addressed
- Squash commits if asked by reviewers to keep history clean

### 10. Celebrate the Merge 🎉

Once the PR is approved and checks are green, the maintainer will merge it. Your contribution becomes part of the AgentCube history!

---

## Coding Standards

- **Go formatting**: Run `make fmt` to automatically format code with `gofmt`
- **Logging**: Maintain consistent log semantics via shared logging packages under `pkg/`
- **API compatibility**: Keep public API changes backward compatible; update CRDs and generated clients when fields change (`make generate`)
- **Documentation**: Document new features under `docs/` and update READMEs/examples when behavior changes

---

## Testing Guidelines

- Unit tests are mandatory for new functionality and bug fixes
- Use table-driven tests where appropriate for clarity
- For concurrency-sensitive code, add race detector checks (`go test -race ./...`)
- Integration tests should target scenarios under `test/` or `example/`

See the [Testing Guide](./testing.md) for detailed instructions.

---

## Documentation Expectations

- Update relevant docs under `docs/`
- Provide getting-started examples if introducing new CRDs or CLI commands
- Refresh charts/examples under `example/` when changing deployments
- Include release notes summary for significant changes (tag `release-note`) in PRs

---

## Tooling Reference

| Command                    | Description                              |
| -------------------------- | ---------------------------------------- |
| `make lint`                | Runs `golangci-lint`                     |
| `make test`                | Runs all unit tests                      |
| `make e2e`                 | Runs the full E2E test suite             |
| `make build`               | Builds the Workload Manager binary       |
| `make build-agentd`        | Builds the AgentD binary                 |
| `make build-router`        | Builds the Router binary                 |
| `make build-all`           | Builds all component binaries to `bin/`  |
| `make docker-build`        | Builds the Workload Manager Docker image |
| `make docker-build-router` | Builds the Router Docker image           |
| `make docker-build-picod`  | Builds the PicoD Docker image            |
| `make generate`            | Regenerates CRDs and DeepCopy methods    |
| `make gen-client`          | Regenerates client-go code               |
| `make gen-all`             | Regenerates all codegen artifacts        |
| `make fmt`                 | Formats Go code with `gofmt`             |

---

## Governance and Ownership

- Maintainers are listed in the top-level [`OWNERS`](https://github.com/volcano-sh/agentcube/blob/main/OWNERS) file and `OWNERS` files in subdirectories
- Subsystem owners review and approve changes in their areas
- Major design decisions go through design docs in `docs/design/`

---

## AI Guidance

Using AI tools to help write your PR is acceptable, but as the author, you are responsible for understanding every change. Do not leave the first review of AI-generated changes to the reviewers — verify the changes (code review, testing, etc.) before submitting your PR.

Reviewers may ask questions about your AI-assisted code, and if you cannot explain why a change was made, the PR will be closed. When responding to review comments, please do so without relying on AI tools. If you used AI tools in preparing your PR, please disclose this in the "Special notes for your reviewer" section.

---

## Security Reporting

For sensitive security issues, email `volcano-security@googlegroups.com` **instead of** filing a public GitHub issue. Provide:

- Steps to reproduce
- Affected components and versions
- Impact assessment

---

## License

By contributing, you agree that your contributions will be licensed under the Apache 2.0 License. See [LICENSE](https://github.com/volcano-sh/agentcube/blob/main/LICENSE) for details.
