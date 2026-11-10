# Progressive disclosure: adaptive view checkpoint

Status: Tasks 2–4 implementation evidence, not an agent navigation quality study.
Snapshot handoff is implemented. No model-based document
selection trials have been run, so recall, wrong-branch visits and task quality
are unmeasured. This checkpoint does not claim the overall quality gate passed.

## Deterministic evidence

`camp-server/camp/navigation_view_test.go` covers small/wide branches, document
pages with children, compressed empty chains, direct expansion, partial scans,
metadata bounds, ancestor anchors, and both adaptive and tree views. With 250
siblings, 1 KiB paths and multibyte descriptions, internal output slices fit
4,096 / 12,288 / 32,768-byte budgets and collectively preserve every source card.
The public API now carries encrypted continuation envelopes instead of raw
renderer offsets.

A 100-document fixture with 450 empty path segments per document materializes at
most 301 branch/document nodes at depth 1. This checks representation growth,
not a latency or memory benchmark. Counts are checked independently against
source-path membership, including punctuation and Unicode sibling names.

Broker tests distinguish exactly 3,000 records from 3,001 using a one-record
lookahead. Existing access, revocation, source-failure and object-read tests
remain active. The real Monty/MCP stack exercises old positional map arguments
with the new keyword-only view and byte budget. A local packaged-install smoke
also passed setup, startup hook execution, MCP discovery/start/run, document
write and reconnect after clearing the npm cache. Final build, full tests,
full race suite and vet passed; the final camp race suite took 192.5 seconds
with the ordinary final-changeset fixture. The Docker image also built.

## Large-changeset performance limitation

A 3,000-document fixture created in one changeset exceeded the existing 30-second
sessionless map deadline under concurrent build load. An isolated profiled run
passed in 20.1 seconds total, with about 18.1 seconds in `Broker.Map`. Most CPU
was in existing JSON decoding, `Store.Load`, record resolution and final access
validation, rather than adaptive rendering. `HEAD` includes the most recent
changeset's revision IDs; repeated current-access reads decode this list again.
The full camp race suite containing two such bulk fixtures eventually passed in
448.2 seconds. These are local observations, not controlled comparative results.

The Task 2 boundary fixtures used 3,000/3,001 documents with a single-record
final revision. Task 3 consolidates this coverage into a 3,101-record integration
fixture (asserting the public 3,000-record window) and a small real-store
exact-capacity/one-record-overflow/tail test of the shared window collector. Production deadlines, access checks, store format and
caching were not changed. Large-space throughput, including bulk changesets,
needs separate measurement before introducing any cache or index.


## Cursor validation checkpoint

`navigation_cursor_test.go` covers authenticated encryption, opaque private scan
boundaries, token/query binding, expiry and restart, concurrent key initialization
and read-cache sharing, snapshot stability over HEAD advances, and SDK snapshot,
refresh and staging rejection. It also tests ACL/source revocation, authorized
moves and deletion while an output offset is active. Membership changes require
restart rather than shifting the offset and skipping a source.

A 3,101-record / 121-group fixture uses the default budget, forcing multiple
output pages within a scan window and transition to a later window; represented
routes cover every source. A denied-only 3,000-record window advances without
revealing paths to its visible tail. Large-window tests drive the real broker and
final access/dependency checks without asserting transport wall-clock speed under
the race detector. Separate actual Map and Monty/MCP tests exercise page budgets,
MCP-to-SDK cursor handoff and exhaustive document IDs on bounded fixtures.
Discovery tests cover explicit grants, wildcard registry paging, more than 100
visible spaces, and a denied-only 1,000-ID registry window with a visible tail.
Registry pagination is deliberately live, not an atomic cross-space snapshot.
The long-boundary renderer test verifies an extra empty transition page when
needed, while the storage write counter verifies navigation persists neither
keys nor cursors into workspace objects. No model-quality experiment is claimed.


The initial combined race run reached Go's 10-minute package deadline while
constructing the second redundant Task 2 boundary corpus; it reported no data
race. Those repeated bulk fixtures were replaced by the collector boundary test,
while retaining the full 3,101-record multi-page/multi-window test and the large
denied-window scenario. This changes test preparation, not product limits or
permission checks.

Final Task 3 validation passed: build, full real-Monty unit suite, full race suite
and vet. The final camp package took 23.7 seconds normally and 248.9 seconds under
the race detector after fixture consolidation. The final Docker image and
freshly rebuilt packaged-install/MCP write smoke also passed. These timings are
local test-suite observations, not navigation latency or model-quality benchmarks.

## Snapshot handoff checkpoint

`TestStartExpectedSnapshot` exercises a matching map snapshot, HEAD advancing
between map and start, unknown snapshots, remapping, omission, and session
resume. Mismatches return `ErrConflict` without saving a session; combining a
precondition with resume returns `ErrInvalid`. Existing sessions keep their
original snapshot after HEAD advances. The HTTP/MCP test exercises the same
map → start → conflict → remap → start sequence through both transports. The
real Monty stack executes the returned map, outline and bounded-read examples.

Final handoff validation passed: build, full real-Monty tests, full race suite
and vet (camp package: 19.4 seconds normally, 241.2 seconds with race detection).
A fresh binary smoke passed init → token → serve → MCP map → guarded start →
SDK write → stale-start conflict → remap → start → outline/read, using a
disposable store and SQLite control database. No runner or Docker inputs changed
in this increment. Independent review found no blocking issues; a minor test
gap remains: MCP failures assert `IsError`, while distinct conflict/invalid
contracts are checked by broker error sentinels and HTTP status codes.
