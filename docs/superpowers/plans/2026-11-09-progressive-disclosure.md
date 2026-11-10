# Progressive Disclosure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make Camp documents discoverable through a bounded, descriptive, navigable map while preserving direct access to every authorized source.

**Architecture:** Retain the existing document store and broker authorization. Build deterministic navigation cards from explicit metadata and source structure, render adaptive views, and provide opaque continuation over a pinned snapshot. Keep search separate and reuse existing `outline`, `related`, `backlinks`, and line-ranged `read`.

**Tech Stack:** Existing Go server, Python/Monty SDK, MCP and HTTP transports; stdlib only for new logic.

**Spec:** [Progressive disclosure design](../specs/2026-11-09-progressive-disclosure-design.md).

## Global Constraints

- No new runtime dependencies or on-disk layout changes are approved.
- Avoid requiring an LLM call for each `map` request.
- Preserve store CAS, rebase, atomicity, idempotency, sandbox and access invariants.
- Filter cards, counts, links and descriptions by authorization before rendering; recheck current access before returning.
- No task/intent parameter, embeddings, inferred semantic clusters, or hidden relevance ranking in `map`.
- Preserve snapshot semantics and report scan incompleteness separately from output omission.
- No generated navigation cache or new persistent index in this first implementation.

## Review Focus

1. Hidden descendants and shared-source revocation must not leak through cards, counts, cursors or cached reads (Tasks 1–3).
2. A wide tree, long paths, and long Unicode descriptions must remain navigable within a hard response budget (Tasks 2–3).
3. A document can also have children; synthetic directories and single-child chains must not lose the document itself (Task 2).
4. HEAD advances, cursor replay, and refresh must never silently mix snapshots (Tasks 3–4).
5. Existing `map` calls and Python positional arguments must retain a documented compatibility path (Tasks 2 and 4).

## Scope and order

This is the server/navigation plan. [The bootstrap integration plan](2026-11-09-camp-bootstrap.md) is independently shippable and can use today's map. Suggested order: bootstrap, then this plan, then the combined acceptance checks.

Implementation is in progress. The following defaults resolve open implementation details; they are builder decisions, not claims that the user selected each field or numeric limit.

## Proposed contract

- Add `view` (`adaptive` by default, or `tree`), `cursor`, and `budget_bytes` to `MapRequest`; keep `space`, `path`, `depth`. Budget defaults to 12,288 bytes; accepted range 4,096–32,768, measured on serialized result JSON, excluding the transport envelope. Depth remains 1–4, default 2, and limits automatic expansion, not later direct path navigation.
- `tree` retains the existing depth-based node presentation; both views gain continuation and descriptions. Preserve the legacy top-level `root`, `nodes`, `kinds`, `hubs`, `records`, `coverage`, `snapshot` keys. Document adaptive default behavior as a response evolution.
- Add `ancestors`, `cursor`, `scan_complete`, `output_complete`, `counts_scope`, and `index_state`. `index_state` is `authoritative`; no index watermark is invented. `coverage` is `complete` only when both completeness flags are true. Counts are for the bounded scan window, explicitly `counts_scope="scan_window"`, never whole-space estimates.
- Nodes distinguish `entry_type` (`document`, `branch`, `document_branch`). Document cards include stable record ID, revision, path, kind, title, optional description/status, and explicit read/outline/expand actions. Branch cards carry only visible aggregate information and an expand action. Ancestors contain authorized navigation anchors, not summaries of hidden siblings.
- Metadata conventions: optional string fields `title`, `description`, `status`; no validation burden on old records. Ignore wrong types. Fallback title is the first Markdown heading outside fenced code, then the last path segment. Fallback description is absent, not fabricated. Include `description_source` and the record revision; do not promise detection of a semantically stale human-written description.
- Limit display title to 120 Unicode code points, description to 240, status to 64, with an explicit text-truncation flag. Never truncate IDs or paths. Read content only through existing broker resolution. Reuse the already-resolved revision to avoid extra object reads. Do not aggregate prose from descendants.
- Do not embed document bodies, outlines, arbitrary metadata, or link-target titles in map cards. Existing outline and relation methods supply the next disclosure level.
- `start` gains optional `expected_snapshot`: new sessions only; if the newly selected primary snapshot differs, return `ErrConflict` without saving a session. This is an explicit handoff precondition, not a change to commit/rebase semantics. The agent remaps or starts afresh knowingly. Do not claim `start` pins a historic map automatically.

## File structure

- `camp-server/camp/map.go`: external request, validation, discovery, final access checks.
- New `camp-server/camp/navigation.go`: card extraction and authorization-aware collection; move `spaceMap` here from `documents.go`.
- New `camp-server/camp/navigation_view.go`: pure deterministic grouping/rendering and response budgeting.
- New `camp-server/camp/navigation_cursor.go`: continuation payload validation and encryption helpers.
- `camp-server/camp/broker.go`, `camp-server/camp/read_cache.go`: broker-lifetime cursor key integration without weakening read-cache isolation.
- `camp-server/camp/sessions.go`: expected-snapshot precondition and help examples.
- `camp-server/camp/documents.go`, `runner/camp_runner.py`, `camp-server/api/mcp.go`, `http.go`: SDK help and transport surface.
- Tests alongside these packages; behavior docs in README and MODEL.

## Task 1: Descriptive cards from authorized records

**Files:** Create `navigation.go`, `navigation_test.go`; modify `documents.go`; extend `map_test.go` and `docs/MODEL.md`.

**Interfaces:** Internal `navigationCard(rec Record, rev Revision) MapNode`; `MapNode` is the typed JSON card described above. Preserve `spaceMap(ctx, e, space, args) (any, error)` as the broker entry point. Pure heading extraction must not introduce a Markdown dependency.

- [ ] Add `TestNavigationCardMetadataFallback`: explicit title wins; absent/wrong-type fields fall back safely; headings inside backtick/tilde fences are skipped; long Unicode values are valid UTF-8 and marked truncated; IDs/paths are unchanged.
- [ ] Add `TestNavigationCardsRespectAccess`: private descriptions, invalid ACL metadata and shared-source failures never enter output; storage failures propagate rather than becoming empty success.
- [ ] Run `go test -count=1 ./camp-server/camp -run 'TestNavigationCard'`; confirm failure against the missing functionality.
- [ ] Extract map collection into `navigation.go`, preserving existing authorization and final dependency checks; add card fields from authorized revisions. Keep unrelated document methods untouched.
- [ ] Document metadata conventions and provenance limits in MODEL in the same change. Run the targeted tests and existing `TestMap` suite; check object-read regression with the existing 60-record fixture.
- [ ] Run the full check chain before committing `feat: expose descriptive document navigation cards`.

## Task 2: Adaptive views without loss of routes

**Files:** Create `navigation_view.go`, `navigation_view_test.go`; modify `navigation.go`, `map.go`, `map_test.go`, README and MODEL.

**Interfaces:** `renderNavigation(input NavigationInput, opts NavigationOptions) (NavigationPage, error)` is pure. Input includes authorized cards, root, snapshot and scan-window completeness; options include view, depth, budget, and output offset. Output includes response nodes and the next output offset.

- [x] Add `TestAdaptiveNavigationFrontier`: documents that also contain descendants retain both read and expand actions; synthetic branches have no invented descriptions; a single-child chain skips intermediate empty nodes but records its path; depth cap and direct expansion work.
- [x] Add `TestNavigationBudgetAndCoverage`: root with 250 siblings, 1 KiB paths, repeated titles and multibyte descriptions fits every allowed byte budget; every omitted item has a continuation route; invalid budgets/depth/view are rejected. Empty accessible space is a complete empty result.
- [x] Run the targeted tests and confirm failure before implementation.
- [x] Implement deterministic expansion: initially group by the first path segment below the focus; preserve exact-root document separately. Expand only confirmed-complete branches with at most eight direct children, within depth and byte budget. Visit expansion candidates by relative depth then path. Do not split based on popularity. Never assume an unfinished scan window contains a complete branch.
- [x] Always retain the current-location anchor. Add authorized ancestors nearest-first within a reserved maximum of one quarter of the budget; expose an upward action for ancestors not shown. Optional hubs are subordinate to navigable cards and remain labeled connectivity hints. Paginate cards instead of silently dropping them. Measure the actual serialized envelope, not an estimated token count; Task 3 must additionally reserve the actual serialized cursor.
- [x] Bound auxiliary kind dictionaries to 32 displayed kinds with explicit truncation; counts and full kind names remain accessible through catalog/record reads. If required card fields cannot fit the minimum budget, return an explicit invalid-budget error with the required size; never loop on a page that cannot make progress. Cursor pagination is completed in Task 3; pure-renderer tests here assert the output offset and coverage of subsequent rendered slices.
- [x] Retain `view="tree"` compatibility and legacy fields, add explicit count/completeness semantics, update behavior docs, and pass the targeted and existing map suites.
- [x] Run the full check chain before committing `feat: add adaptive bounded workspace maps`.

## Task 3: Snapshot-bound continuation, including large spaces

**Files:** Create `navigation_cursor.go`, `navigation_cursor_test.go`; modify `broker.go`, `navigation.go`, `map.go`, `map_test.go`, and cache-wrapper tests where necessary.

**Interfaces:** `MapCursor` stores version, principal token hash, space, path, view, depth, budget, snapshot commit, scan-window start, output offset and expiry. Internal `sealMapCursor(key []byte, c MapCursor) (string, error)` and `openMapCursor(key []byte, token string) (MapCursor, error)` use authenticated encryption from Go stdlib. The key is generated once per broker lifetime, shared by its read-cache copies, never serialized into workspace storage. Initialize safely once, including struct-literal broker construction; propagate entropy errors.

- [ ] Add `TestMapContinuationExhaustive`: more than 3,000 records and more than 100 groups; follow all pages; union of represented routes reaches all authorized records, with strict progress and no permanently stranded tail. Repeated branch anchors across scan windows are allowed and documented.
- [ ] Add `TestMapCursorBinding`: tampering, different token/principal, space, path, view, depth or budget is rejected. Private raw scan paths are not exposed by decoding the external token. Expiry (30 minutes) and broker restart produce an explicit restart-required invalid-cursor error.
- [ ] Add `TestMapCursorSnapshotAndRevocation`: unrelated commits do not change a continued map; current revocation prevents old data from being returned; deleted/moved records follow existing immutable-snapshot read semantics and current authorization. SDK refresh with an old cursor fails explicitly.
- [ ] Run the targeted tests and confirm failure.
- [ ] Scan in bounded windows of at most 3,000 records using `Store.List`. Render a window into potentially multiple output pages; while paging that window, retain its original start and advance only the output offset. Once exhausted, advance the scan boundary and reset offset. Re-scanning a window is an intentional initial tradeoff; do not add persistent caching.
- [ ] Load the cursor snapshot using `Store.At` for sessionless continuation after current principal validation; keep existing current access and dependency rechecks. Inside SDK execution require the cursor snapshot to equal the session snapshot. Reject map continuation with nonempty staging to avoid changing page membership between calls.
- [ ] Ensure denied-only windows can return no cards with an advancing opaque cursor. Counts concern accessible records only. Cursor carries no raw client-controlled tree roots; snapshot and all query bindings are validated. Use one-record lookahead to distinguish exactly 3,000 records from a larger collection.
- [ ] Add equivalent continuation to space discovery for more than 100 authorized spaces. Bind discovery tokens to the principal and parameters; document discovery as live registry pagination, with no atomic cross-space snapshot claim. Require space/view-only arguments to be absent during discovery.
- [ ] Pass full map/cursor tests and race checks. Before committing `feat: preserve navigation coverage with opaque continuation`, verify no cursor/key appears in backups and no store key layout changed.

## Task 4: Transport, SDK and snapshot handoff

**Files:** Modify `map.go`, `sessions.go`, `documents.go`, `runner/camp_runner.py`, `api/mcp.go`, `api/http.go`; extend `map_test.go`, `sessions_test.go`, `api/http_test.go`, `api/stack_test.go`, `api/guide_test.go`, `runner/runner_test.go`; update README and MODEL.

**Interfaces:** Extend Python `map(self, path="/", depth=2, space="", *, view="adaptive", cursor="", budget_bytes=12288)` without changing existing positional meanings. Add `ExpectedSnapshot string` with JSON `expected_snapshot,omitempty` to `StartRequest`. HTTP map query parsing and MCP input schema expose the same validated parameters.

- [ ] Add transport parity tests: omitted defaults, explicit tree view, bad numbers, cursor continuation, space discovery and permission denial have matching behavior through broker, HTTP and MCP. Confirm legacy SDK positional examples still run.
- [ ] Add `TestStartExpectedSnapshot`: matching commit succeeds; mismatched commit saves no session; omission retains current behavior; resume plus expected snapshot is rejected as an invalid combination rather than ignored.
- [ ] Run targeted tests to confirm missing behavior; then wire the shared implementation, Python wrapper, schema descriptions, and `SDKHelp`.
- [ ] Update `start.help` examples to use map cards → existing outline → bounded read, and explain remapping after a handoff conflict. Keep the three MCP tools; do not add one tool for each disclosure level.
- [ ] Run real Monty tests using `CAMP_TEST_PYTHON="$PWD/runner/.venv/bin/python" go test -count=1 ./camp-server/runner ./camp-server/api`; skipped runtime tests do not count as validation.
- [ ] Run the full check chain, Docker build, and live SDK smoke before committing `feat: expose progressive navigation across Camp transports`.

## Task 5: Navigation quality and release evidence

**Files:** Create `camp-server/camp/navigation_eval_test.go`, `docs/evaluations/progressive-disclosure.md`; update behavior documentation as necessary.

- [ ] Build deterministic fixtures for wide/deep trees, unusual documents, missing descriptions, obsolete decisions, cross-branch evidence, and restricted subtrees. Assert exhaustive reachability and no disclosure independently of an LLM. Do not confuse lower output size with better discovery.
- [ ] Define twelve source-selection tasks with required document IDs, including two multi-document tasks. Record a baseline from the pre-change commit and the new map using the same agent/version, prompt, corpus and budget; run three trials per task. Keep model execution optional and record unavailable credentials as unmeasured, never passing.
- [ ] Record document recall, unnecessary reads, wrong-branch visits, calls, tokens, latency, and final answer quality. Proposed quality gate: macro required-document recall is no worse than baseline, with at least a 20% reduction in median navigation calls or input tokens. Report uncertainty and regressions; do not tune only to one task or claim significance from this small sample.
- [ ] Confirm automatic tests pass: `go build ./...`, `go test -count=1 ./...`, `go test -race -count=1 ./...`, `go vet ./...`.
- [ ] Build both binaries to `/tmp`, run a disposable `camp-server init` → token → serve → MCP discovery/start/run write/read smoke, using current CLI `--help` for exact flags. Build `docker build -t agent-camp:dev .` because runner code changes. Inspect `go list -deps ./camp-cli/...` and `go list -deps ./camp-server/...` for forbidden cross-imports.
- [ ] Commit evaluation fixtures and measured evidence as `test: verify progressive navigation coverage and quality`. Do not tag, publish or deploy.

## Completion and deferred work

Done means bounded navigation is reachable and honest, all transports agree, safety regressions pass, and measured agent results are recorded or explicitly marked unmeasured. Automatic summarization, semantic clustering, persistent navigation indexes and a custom agent runtime remain out of scope. Large-space performance is measured before adding any cache or index.

## Implementation checkpoint

Task 1's descriptive-card slice is implemented in `navigation.go` and
`navigation_card.go`, with broker-level tests in `navigation_test.go` and existing
map regressions. Cards use authorized resolved revisions, including own staged
metadata for existing records. README, MODEL, MCP descriptions and SDK help
explain the new fields. Existing map size and object-read limits remain.

Task 2's adaptive view is implemented in `navigation_view.go`, with byte-bounded
pages, authorized ancestor anchors and explicit scan/output completeness. The
internal renderer exposes output offsets for Task 3; these are not public
continuation tokens. Compact branch storage scales with records and requested
depth rather than every empty path segment. Kind dictionaries are bounded by
both count and bytes, and optional dictionaries/ancestors yield to a first source
card. Python keyword-only `view` and `budget_bytes` options were brought forward
from Task 4 so both MCP and SDK callers can select the compatibility tree view.
[Validation and performance observations](../../evaluations/progressive-disclosure.md)
record what has been checked and what remains unmeasured.

Continuation, snapshot handoff and agent quality evaluation (Tasks 3–5) remain
unimplemented. No exhaustive map coverage or completion of this
entire plan is claimed. Use `ls` continuation or narrower maps until continuation
ships. PR #4 covers descriptive cards and client-wide bootstrap. The adaptive view is
a subsequent change; the remaining navigation tasks stay explicitly pending.
