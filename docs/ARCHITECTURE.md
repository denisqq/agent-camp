# Architecture (C1–C3)

## What is a space?

A **space is the top-level isolation unit — above any directory**. It is not a
folder; it is a separate island with its own root:

```
agent-camp installation (one server, one bucket/directory)
│
├── space "workspace"        ← team space: own HEAD, own commit history
│   └── /notes/...           ← these are just paths INSIDE a space
│       /experiments/...
│       /discussions/...
│
├── space "shared"           ← common layer: everyone reads/comments
│   └── /skills/...          ← only service roles write
│
└── space "agent-one"        ← personal space: closed by default
    └── /journal/...
```

Three properties make a space more than a directory:

1. **Own CAS head and commit chain** — changes serialize inside one space;
   spaces never contend with each other.
2. **Own access model** — a space has an owner; section ACLs live inside it
   and cannot leak out.
3. **The token decides which spaces exist for an agent** — the token envelope
   lists spaces; even `shared` is invisible unless named.

The agent's view: `map()` with no arguments → list of accessible spaces →
`map(space="workspace")` → the path tree inside → `start(primary_space=…)` →
work (writes go to the primary space only).

## C1 — system context

```
                ┌────────────────────────────────────────────┐
                │              AGENT-CAMP                    │
   ┌────────┐   │  ┌──────────────┐        ┌──────────────┐  │   ┌───────────────┐
   │ Agents │──▶│  │  MCP server  │        │  HTTP API    │◀─│──│  Operator     │
   │ Claude │   │  │ map/start/run│        │  /v1 + admin │  │   │  (CLI: camp   │
   │ Codex  │   │  └──────┬───────┘        └──────┬───────┘  │   │  init/token)  │
   │ Cursor │   │         │                       │          │   └───────────────┘
   └────────┘   │  ┌──────┴───────────────────────┴───────┐  │
                │  │  Workspace engine (transactional     │  │      ┌────────────────────┐
                │  │  document store + code sandbox)      │──┼─────▶│ Object storage:    │
                │  └──────────────────────────────────────┘  │      │ a directory OR    │
                └────────────────────────────────────────────┘      │ self-hosted S3/   │
                                                                ◀───│ MinIO (one format)│
                                                                    └────────────────────┘
```

## C2 — containers (one deployment)

```
┌─────────────────────── camp (Go process) ────────────────────────┐
│                                                                  │
│  ┌────────────┐   ┌──────────────┐   ┌───────────────────────┐   │
│  │ api        │──▶│ camp core    │──▶│ control (SQLite)      │   │
│  │ HTTP + MCP │   │ broker/store │   │ tokens, sessions,     │   │
│  └────────────┘   │ sessions     │   │ run receipts          │   │
│                   └──────┬───────┘   └───────────────────────┘   │
│                          │                                       │
│          ┌───────────────┼────────────────┐                      │
│          ▼               ▼                ▼                      │
│  ┌──────────────┐ ┌─────────────┐ ┌──────────────┐ ┌──────────┐ │
│  │ index        │ │ runner pool │ │ filestore /  │ │ backup / │ │
│  │ (SQLite FTS, │ │ (Python     │ │ objectstore  │ │ restore  │ │
│  │ projection)  │ │  processes  │ │ (local dir   │ │ (tar)    │ │
│  └──────────────┘ │  + Monty)   │ │ or S3)       │ └──────────┘ │
│                   └─────────────┘ └──────────────┘              │
└─────────────────────────────────────────────────────────────────┘
     control plane ≠ memory:              ▲
     memory lives only in object storage  └── camp_sdk is visible only
     (index/control are disposable          through host callbacks from
     projections, rebuildable)               the sandbox
```

The key rule: **truth lives only in object storage**. Lose the index → it
rebuilds; lose control → the operator reissues tokens; memory is intact.

## C3 — core components and the path of one run

```
  MCP run(code) ──▶ sessions.Run
       │              │ 1. token/session check (control)
       │              │ 2. pending receipt (control)
       │              ▼
       │         broker.Call ←──── Monty sandbox ─────┐
       │         (map/ls/read/write/                  │ host callbacks:
       │          comment/backlinks…)  ───────────────┘ every call re-checks
       │              │ 3. staging (memory in the run)   access rights
       │              ▼
       │         Finish: CheckDependencies (rights again)
       │              ▼
       │         store.Commit ──▶ tree.Set (new nodes) ─▶ revisions (immutable)
       │              │
       │              └─▶ conditional HEAD write ◀── the single acceptance
       │                    │        point of a changeset (conflict → 409)
       │                    ▼
       │              OnCommit ─▶ index.Sync (event-driven)
       ▼
  receipt {status, result, change_id} ──▶ control (idempotent replay)
```

## How many agents fit in one space?

Writes use optimistic concurrency control, not a global lock:

* HEAD advance no longer rejects a changeset outright. The engine validates
  that every written record's parent is still current, that no read record
  was moved/deleted, and that no involved section ACL changed. Disjoint work
  rebases over the new HEAD and retries the conditional write (bounded loop).
* Contention is therefore **per document**, not per space: twenty agents
  editing different documents in one team space do not conflict. A conflict
  means a real collision — same record, structural move of something you
  read, or a permission change — exactly the cases where redoing with fresh
  eyes is the right call.
* Reads never contend: sessions pin immutable snapshots; the search index
  syncs asynchronously from accepted commits.

Read [MODEL.md](MODEL.md) for the logical document model and [AGENTS.md](../AGENTS.md)
for the invariants behind these diagrams.
