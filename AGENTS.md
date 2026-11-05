# AGENTS.md — working guide for coding agents

agent-camp is a workspace engine for teams of AI agents: a shared, versioned,
transactional document store behind MCP (`map`/`start`/`run`) and HTTP `/v1`.
Read [README.md](README.md) for positioning and [docs/MODEL.md](docs/MODEL.md)
for the logical model before changing semantics.

## Start here

1. Read [README.md](README.md) for the product and [docs/README.md](docs/README.md)
   for documentation navigation.
2. Read [docs/MODEL.md](docs/MODEL.md) before changing behavior and
   [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for component boundaries.
3. Use Trail-code's `trail_repo_map`, `trail_find_code`, and `trail_trace_calls`
   when its MCP server is available. Build the local source index with
   `just code-index`; see [docs/INDEXING.md](docs/INDEXING.md). Use `rg` for exact
   text, Markdown, and configuration. The index is navigation, not authority.
4. Read the relevant code and tests, preserve the invariants below, and run the
   required checks. See [CONTRIBUTING.md](CONTRIBUTING.md) for the workflow.

## Commands

```bash
go build ./...                     # must pass always
go test -count=1 ./...             # unit + concurrency suites
go test -race -count=1 ./...       # required before commits touching store/sessions/runner
go vet ./...
go build -o /tmp/camp ./cmd/camp   # binary smoke:
CAMP_ROOT=/tmp/campdata /tmp/camp init && CAMP_ROOT=/tmp/campdata /tmp/camp serve
docker build -t agent-camp .       # when touching Dockerfile or runner deps
just <recipe>                      # see justfile
```

## Map

| Path | What lives here |
|---|---|
| `internal/camp` | the engine: store (CAS commits, merkle tree), broker (SDK calls), sessions (run receipts), auth, files, backup, map() |
| `internal/filestore` | local storage mode (directory with conditional writes) |
| `internal/objectstore` | S3 storage mode (any S3-compatible endpoint, self-hosted first) |
| `internal/control` | operational state: tokens, sessions, receipts (SQLite) — never workspace memory |
| `internal/index` | disposable SQLite search projection (FTS5 + literal grep) |
| `internal/runner` | pool of trusted Python adapter processes |
| `runner/camp_runner.py` | the sandbox: Monty process + `camp_sdk` exposed to agent code |
| `internal/api` | HTTP `/v1` + MCP server (map/start/run) |
| `internal/server` | server runtime: engine wiring, serve/mcp/index, bootstrap |
| `camp-cli` | the camp client command line (cobra): setup/register/bridge/doctor/connect, status/up/down |
| `camp-server` | the camp-server command line (cobra): serve/mcp/init/token/backup/setup |
| `installer/` | Go installer: `setup` (onboarding orchestration), `localservice` (child camp-server management), `runtimepack` (verified server runtime packs); `installer/npm` is the npm launcher |
| `cmd/camp`, `cmd/camp-server` | thin entry points into camp-cli and camp-server |
| `plugins/`, `.claude-plugin/` | Claude Code plugin marketplace payload |
| `docs/MODEL.md`, `docs/ARCHITECTURE.md` | logical model; C1–C3 diagrams and the space concept |

| `.mcp.json`, `.cursor/mcp.json`, `.codex/config.toml` | pinned Trail-code MCP settings for source navigation |
| `docs/INDEXING.md`, `docs/DEVELOPMENT.md` | local code index, runtime tests, and development setup |

## Invariants — do not break without explicit user approval

1. **CAS acceptance**: a changeset becomes visible only through the
   conditional `HEAD` write in `internal/camp/store.go` Commit. Never publish
   revisions outside it.
2. **Idempotency**: replaying the same operation id with identical code
   returns the original receipt and never re-executes or double-writes.
   Fingerprint = sdk version + token hash + code + dry-run.
3. **Atomicity**: a run's staging either fully commits as one changeset in one
   space or is discarded (failure, timeout, invalid result, dependency loss).
4. **Ordering**: `orderRevisions` applies path-vacating revisions (delete,
   move-away) before occupants; swaps cycle → explicit conflict. If you touch
   Commit, the changeset tests in `changeset_test.go` must still pass.
4a. **Optimistic concurrency**: a HEAD advance alone never rejects a changeset —
   `checkRebase` must keep fencing exactly these cases: same-record parent
   mismatch, structural change (path/deleted) of a read dependency, effective
   ACL change on any involved path, and shared-view source changes. Disjoint
   write sets rebase over unrelated commits (`concurrent_test.go` locks this).
5. **Access fails closed**: unknown/invalid access metadata means denied, not
   open. Space owner/admin always pass; shared spaces have fixed policy.
6. **Control plane ≠ memory**: tokens/sessions/receipts live in
   `internal/control` SQLite and must never appear in workspace backups
   (`backup.go` walks space roots only).
7. **One format, two modes**: filestore and S3 share the key layout and
   conditional-write contract; anything written into one must be expressible
   in the other.
8. **Sandbox boundary**: agent code only sees `camp_sdk` through Monty host
   callbacks; no host FS/env/network reaches user code. Changes to
   `runner/camp_runner.py` method exposure are security-sensitive.
9. **Search honesty**: results always carry snapshot/watermark/coverage;
   truncation must be reported, never presented as completeness.

## Conventions

- Go: stdlib-first, no new dependencies without need (cobra is the one
  CLI dependency, used by both binaries); errors are the package sentinels
  (`ErrNotFound/ErrConflict/ErrDenied/ErrInvalid`).
- Tests sit next to code; adversarial scenarios (conflict races, revocation,
  poison records) are written as tests, not comments.
- Adding an SDK method = four pieces: broker `case` in `internal/camp`,
  Python method in `runner/camp_runner.py`, entry in `SDKHelp`, and a test
  that exercises it through `Broker.Call`. Keep help and behavior in sync.
- Docs that describe behavior (README, docs/MODEL.md) must be updated in the
  same commit as the behavior change.
- Use Camp names for packages, SDK imports (`camp_sdk`), and environment variables
  (`CAMP_*`). Preserve the historical `brain-curator` ACL subject until a stored
  permissions migration is explicitly approved; see `docs/DEVELOPMENT.md`.
- Commits: `feat|fix|docs|refactor|test: summary` — imperative, one line.

## Before you finish

Run the full check chain (`build`, `test`, `race` if concurrency-adjacent,
`vet`); rebuild the docker image if runner/Dockerfile changed; if you changed
the SDK surface, run a live smoke: `camp init` → `token` → `serve` →
`start`+`run` with `from camp_sdk import camp` writing one document.

## Ask the user first

Releasing (git tag / npm publish), changing the on-disk key layout, weakening
or removing any invariant above, new runtime dependencies.
