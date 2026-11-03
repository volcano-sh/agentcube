# GC candidate pagination

Research date: 2026-11-03. Tracking issue: [#454](https://github.com/volcano-sh/agentcube/issues/454).

## Problem and upstream work

GC previously requested only the first 100 inactive sessions, then applied each
session's idle timeout. A hundred old sessions with long idle timeouts could
therefore indefinitely hide a later session whose shorter timeout had elapsed.
Increasing the limit only moves the failure threshold. This is the reproduction
reported in [issue #454](https://github.com/volcano-sh/agentcube/issues/454).

[Issue #491](https://github.com/volcano-sh/agentcube/issues/491) describes a related
but distinct problem: missing session records leave orphaned members in the
expiry and activity indexes. At the research date, [PR #495](https://github.com/volcano-sh/agentcube/pull/495)
was open and unmerged, with head `b144f7add8bce06da465a239d9e483748d59be62`.
Its patch removes orphaned members after an atomic existence recheck; it does
not paginate live sessions and does not fix #454's reproduction. The store test
in that patch expects its first listing to clean orphans and return nothing,
and its second listing to return the real session. The PR description's claim
about collection in one GC cycle should not be confused with one store listing.

Searching PR references to #454 returned only #495; a broader search for GC,
pagination, and starvation found no newer dedicated fix. This establishes the
research result, not the absence of unlinked work. The issues and PR status may
change after this note was written.

## Pagination alternatives

Redis-compatible sorted sets order by score, breaking ties lexicographically by
member. Score ranges can be inclusive or exclusive. Large offsets require
traversing the skipped members and can add linear work.
See the [Valkey ZRANGEBYSCORE reference](https://valkey.io/commands/zrangebyscore/).

The following consequences are design analysis based on those semantics:

| Approach | Consequence |
| --- | --- |
| Larger fixed limit | Still permits starvation above the new limit. |
| Increasing offsets | Deletions shift ranks and can skip later members; concurrent activity changes make subtraction-based corrections unreliable. |
| Score-only continuation | An exclusive bound skips unvisited members with the same score; an inclusive bound repeats them. Whole-second activity scores make ties common. |
| Score and member continuation | Can express the desired ordering, but requires additional machinery because the existing score-range command has no compound continuation token. |
| ZSCAN continuation | Avoids rank offsets and score tie handling, at the cost of scanning active members too. |

`ZSCAN` returns an opaque cursor and member/score pairs; its complete traversal
is linear in the collection size. [Valkey ZSCAN reference](https://valkey.io/commands/zscan/)

The SCAN family guarantees that a completed traversal visits members present
throughout the traversal. It may return duplicates or an empty page before
completion. `COUNT` is a work hint, not a strict page-size limit; compact sorted
sets can return all members at once. Completion is guaranteed only when the
collection remains bounded in size. [Redis SCAN documentation](https://redis.io/docs/latest/commands/scan/)

## Selected design

Add `ScanInactiveSandboxIDs` and `GetSandboxWithLastActivity` to the store
interface, with matching Redis and Valkey implementations. Preserve the existing
list methods and storage layout.

`ScanInactiveSandboxIDs` uses `ZSCAN` on the activity index, filters scanned
members by the inactivity cutoff, and returns candidate IDs and the next opaque
cursor. The caller must continue when no IDs qualify but the cursor is nonzero.
An empty candidate page is not evidence that traversal has finished.

The garbage collector retains its cursor and pending candidate IDs across runs.
This is necessary because repeatedly restarting a traversal at zero after its
time budget expires can recreate starvation. A partially processed page retains
its unprocessed IDs even when the server cursor has already reached zero. Once
that page is drained and the cursor is zero, the sweep is complete; a later run
starts the next sweep. These are in-memory progress markers, so process restart
starts a new sweep.

Before evaluating a pending ID for deletion, `GetSandboxWithLastActivity`
refreshes the session record and its current activity score. Retaining complete
session objects across runs would allow an old activity observation to survive
a later heartbeat. The existing JSON getter cannot supply this refresh because
[`LastActivityAt` is transient and excluded from JSON](../../pkg/common/types/sandbox.go).
Fresh reads narrow that race; they do not make the subsequent Kubernetes delete
atomic with activity updates.

Keep expiry processing first and cap it at one minute within the existing
two-minute total GC budget. Inactive traversal receives the remaining budget.
This prevents a large inactive scan from newly depriving expiry collection of
all execution time. Check cancellation between requests and candidates, and
pass the appropriate context to store and Kubernetes operations. A context
deadline signals cancellation; it does not forcibly interrupt arbitrary work.
[Go context documentation](https://pkg.go.dev/context#WithTimeout)

Ordinary per-session failures must not hold the pending queue at one ID forever.
Advance past those attempts and allow later sweeps to retry. Preserve pending
work when the run ends before an attempt can complete. Deletion remains
idempotent, and repeated members within a run should be deduplicated.

## Scope and tradeoffs

- This fixes traversal behind long-timeout sessions without changing session
  keys, sorted-set scores, persisted JSON, configuration, or requiring migration.
- `ZSCAN` visits active members as well as inactive ones. Filter before loading
  JSON, and refresh only candidate IDs. The count hint does not impose a strict
  memory bound on one returned page.
- Retained progress gives eventual coverage when sweeps can complete and the
  process remains running. It does not promise a fixed collection latency under
  unbounded index growth, repeated restarts, or persistently failing operations.
- Orphan index cleanup remains the separate concern addressed by #491/#495.
  Pagination can pass orphaned entries without removing them.
- Existing expiry-list limits remain unchanged. This work does not claim to
  solve all possible expiry-list starvation scenarios.
- Cluster support is not introduced. A [maintainer review on #495](https://github.com/volcano-sh/agentcube/pull/495#discussion_r4412334220)
  identifies standalone or master/slave Redis/Valkey as the current supported
  deployment and asks that cross-slot Lua limitations be documented for future
  cluster support.

## Verification

The regression `TestGarbageCollector_once_candidateLimitStarvation` was added
before production changes. With the fake store enforcing the 100-item limit,
it failed on the original GC: expected deletion of `eligible`, actual deletions
were nil. The same regression passes after the change.

Completed checks:

| Check | Result |
| --- | --- |
| `go test -mod=readonly ./pkg/...` | Passed across all packages. |
| `go test -mod=readonly -race ./pkg/...` | Passed across all packages. |
| `go test -mod=readonly -race ./pkg/store ./pkg/workloadmanager -count=1` | Passed after final test-helper and failure-path additions. |
| `go vet -mod=readonly ./...` | Passed. |
| `go build -mod=readonly ./cmd/...` | Passed. |
| `make fmt-check` | Passed. |
| `make lint` | Passed using the repository's pinned linter. |
| Native Redis 7.2.7 GC integration with `-race` | Passed. |
| Native Valkey 8.0.2 GC integration with `-race` | Passed. |

The separately installed golangci-lint 2.12.2 rejected the existing configuration
during planning. The actual `make lint` target uses the repository's pinned
1.64.1 binary and succeeds; no lint configuration changes were required.

Coverage includes 100 ineligible sessions ahead of an eligible session, tied
scores, filtered empty pages, oversized pages, duplicate IDs, missing records,
JSON/read errors, Kubernetes and store deletion errors, expiry/inactive overlap,
independent listing failures, cancellation during reads/scans/deletion, resumed
pending IDs with refreshed activity, and reserving time for inactive work.
Existing timeout defaults, creation-time fallback, and SandboxClaim behavior
remain covered.

Miniredis 2.35.0 implements `ZSCAN` using offsets internally, so its unit tests
cannot prove native traversal under deletion. For that reason, native server
binaries were built from [Redis 7.2.7 source](https://download.redis.io/releases/redis-7.2.7.tar.gz)
and [Valkey 8.0.2 source](https://github.com/valkey-io/valkey/releases/tag/8.0.2)
under `/tmp/agentcube-454-servers`. No system packages were installed.

The opt-in native integration test starts and stops a disposable localhost
server itself, with persistence disabled. It verifies the exact 101-session
reproduction, preserves all 100 long-timeout sessions, then deletes all 512
eligible sessions sharing one activity score while traversing a native hash
encoded index. Kubernetes delete calls use the fake dynamic client.

Reproduce each provider in its own test process (the store uses a singleton):

```bash
AGENTCUBE_GC_TEST_PROVIDER=redis \
AGENTCUBE_GC_TEST_SERVER=/tmp/agentcube-454-servers/redis-7.2.7/src/redis-server \
go test -mod=readonly -race ./pkg/workloadmanager -run '^TestGarbageCollectorNativeStore$' -count=1

AGENTCUBE_GC_TEST_PROVIDER=valkey \
AGENTCUBE_GC_TEST_SERVER=/tmp/agentcube-454-servers/valkey-8.0.2/src/valkey-server \
go test -mod=readonly -race ./pkg/workloadmanager -run '^TestGarbageCollectorNativeStore$' -count=1
```

The native test skips when its executable is not supplied. Full Kubernetes E2E
was not run: Docker's daemon is unavailable in this environment. Results above
verify the GC through the real store implementations, not a deployed cluster.

## Changed files

The following files implement and verify this fix.

| File | Change |
| --- | --- |
| `pkg/store/interface.go` | Add scan and activity-refresh methods; preserve list methods. |
| `pkg/store/gc_scan.go` | Shared member/score filtering for native scan responses. |
| `pkg/store/store_redis.go` | Redis scan and fresh activity lookup. |
| `pkg/store/store_valkey.go` | Valkey scan and fresh activity lookup. |
| `pkg/store/gc_scan_test.go` | Shared conformance tests through both client implementations. |
| `pkg/workloadmanager/garbage_collection.go` | Resume traversal across runs, refresh candidates, bound expiry phase. |
| `pkg/workloadmanager/garbage_collection_test.go` | Reproduction, pagination, cancellation, fairness, and failure regressions. |
| `pkg/workloadmanager/garbage_collection_integration_test.go` | Opt-in native server integration tests. |
| `pkg/workloadmanager/handlers_test.go` | Update store fake for the extended interface. |
| `pkg/router/session_manager_test.go` | Update store fake for the extended interface. |
| `pkg/common/types/sandbox.go` | Update the activity field comment for the new getter. |
| `docs/research/gc-candidate-pagination.md` | Research, design, verification, and handoff. |
