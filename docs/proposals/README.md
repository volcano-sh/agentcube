# AgentCube Proposals

This directory is the entry point for AgentCube design proposals.

Proposal documents are used for major design decisions, new APIs, new
controllers, user-facing behavior changes, and cross-component architecture
changes that need review before implementation.

This README intentionally does **not** maintain a full index of proposals stored
under `docs/proposals/`. New proposals should be discoverable from the directory
tree itself, so adding a new proposal does not require an additional README
update. The table below only indexes legacy design documents that still live
under `docs/design/`.

## Proposal Layout

New proposals should use a dedicated directory directly under `docs/proposals/`.
Do not require area-based subdirectories at this stage.

```text
docs/proposals/
  README.md
  proposal-template.md
  <proposal-name>/
    README.md
    images/
```

Use short, descriptive, lowercase directory names. Prefer hyphen-separated
names, for example:

- `docs/proposals/sandbox-pool-control-plane/README.md`
- `docs/proposals/e2b-compatible-sdk-facade/README.md`

Keep the proposal content in `README.md`. Store supporting images, manifests,
benchmarks, or examples in the same proposal directory so links remain stable as
the proposal grows.

## Proposal Template

Start new proposals from [proposal-template.md](proposal-template.md).

The template keeps proposal metadata and review expectations consistent:

- title
- authors
- reviewers
- approvers
- creation date
- last updated date
- status
- tracking issue (optional)
- summary
- motivation
- goals and non-goals
- proposal
- user stories (optional)
- design details
- risks and mitigations
- test plan
- alternatives
- implementation plan (optional)

## Legacy Design Documents

AgentCube already has several design documents under `docs/design/`. They are
listed here to make the historical proposal surface easier to discover. These
legacy documents are not moved by this index, so existing links remain stable.

| Proposal | Existing location | Area |
| --- | --- | --- |
| AgentCube Design Proposal | [docs/design/agentcube-proposal.md](../design/agentcube-proposal.md) | Overall architecture |
| Sandbox Template for Agent and CodeInterpreter Runtimes | [docs/design/runtime-template-proposal.md](../design/runtime-template-proposal.md) | Runtime template API |
| PicoD Design Document | [docs/design/picod-proposal.md](../design/picod-proposal.md) | Sandbox daemon API |
| PicoD Plain Authentication Design | [docs/design/PicoD-Plain-Authentication-Design.md](../design/PicoD-Plain-Authentication-Design.md) | PicoD authentication |
| Router Submodule Design Document | [docs/design/router-proposal.md](../design/router-proposal.md) | Router |
| AgentCube Authentication and Authorization Design | [docs/design/auth-proposal.md](../design/auth-proposal.md) | AuthN/AuthZ |
| Keycloak Integration Design | [docs/design/keycloak-proposal.md](../design/keycloak-proposal.md) | External identity provider |
| AgentCube CLI Design | [docs/design/AgentRun-CLI-Design.md](../design/AgentRun-CLI-Design.md) | CLI |

## Status Values

Use one of these values in proposal front matter:

| Status | Meaning |
| --- | --- |
| `draft` | The proposal is being written or discussed. |
| `provisional` | The direction is accepted enough to guide implementation, but details may still change. |
| `implemented` | The proposal has been implemented. |
| `deferred` | The proposal is valid but not currently planned. |
| `rejected` | The proposal was considered and rejected. |
| `obsolete` | The proposal has been superseded by a newer design. |

## Review Guidance

Before implementing a major design change:

1. Add or update a proposal.
2. Link the related issue or PR if one exists.
3. Keep implementation-specific details separate from the design summary.
4. Include a test plan that covers the main risk areas.
5. Keep rejected alternatives in the proposal so future contributors can
   understand the tradeoffs.

Do not use proposals as release notes or user guides. Once a proposal is
implemented, update user-facing documentation under the appropriate docs area.
