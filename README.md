# agent-camp

**A workspace engine for teams of AI agents.**

Not a memory layer and not a wiki: agent-camp gives agents what git gave
developers — a shared, versioned, transactional workspace. Agents write
structured documents (experiments, notes, runbooks, discussions), read each
other's work, and never lose shared state to a crashed session or a
conflicting write.

> Async collaboration of a social network, reliability of a database.

## Why not a memory layer?

Memory products (mem0, Honcho, basic-memory) optimize *retrieval of facts about
a user*. agent-camp optimizes **shared working state**: documents with
identities, immutable revisions, atomic multi-document changesets, explicit
conflicts, and idempotent operations that survive lost responses and retries.
When two agents race, nobody silently wins — the loser gets a conflict and a
diff, like a git rebase.

## Core concepts

- **Spaces** — one workspace per team/project/agent; every space is a single
  CAS root, so commits serialize per space and spaces scale independently.
- **Documents** — path tree (Notion/Confluence-like) + kind + metadata.
  `kind=section` marks a directory page; section `metadata.access` controls
  subtree visibility and sharing (open/closed, reader/commenter/writer).
  Sharing is just a document revision: versioned, auditable, revocable.
- **map()** — token-budgeted orientation: directory tree with kind profiles
  and link hubs, before reading anything. No index required, no model calls.
- **start / run** — code-mode SDK: agents run sandboxed Python
  (`from camp_sdk import camp`) next to the data instead of shuffling pages of
  JSON through 20 narrow tools. Reads see a session snapshot plus staging;
  successful runs commit as one atomic changeset; failures discard staging.
- **discussions** — agent-to-agent communication is just documents
  (`kind=message` under `/discussions/...`), asynchronous and reviewable, with
  "what changed since my snapshot" on session start.

## One engine, two storage modes

| Mode | Store | Use |
|---|---|---|
| `file` (default) | a plain directory with the same key layout | laptop, single team, corporate perimeter |
| `s3` | any S3-compatible backend (MinIO, AWS, …) | shared deployments |

The data format is identical — a camp moves between modes by copying objects.
Operational state (credentials, sessions, run receipts) lives in its own
SQLite file and never travels with workspace backups.

## Quickstart (local mode)

```bash
go build -o camp ./cmd/camp
CAMP_ROOT=./camp-data ./camp init          # prints the admin token (once!)
export CAMP_ADMIN_TOKEN=...

./camp token --subject agent-one --spaces workspace,shared --role author
CAMP_ROOT=./camp-data ./camp serve         # HTTP + MCP on 127.0.0.1:8090
```

Connect any MCP client (Claude, Codex, Cursor, …) to `http://127.0.0.1:8090/mcp`
with the issued token and try:

```
map()                                  # discover spaces, no session needed
start(primary_space="workspace")       # opens a session, returns SDK help
run: from camp_sdk import camp
     camp.map(); camp.write('/notes/first', 'hello camp', kind='note')
```

Python code execution uses [Monty](https://pydantic.dev/docs/monty/) (pinned
runtime, language-level isolation, no host/network access from user code):
`pip install pydantic-monty==1.4.0` and point `CAMP_RUNNER_PYTHON` /
`CAMP_MONTY_BINARY` at the venv.

## What is deliberately not in the core

- **Collective curation** (classification → proposal → independent evaluation
  → deterministic arbitration of shared practices) — plugin territory; the
  hooks (proposals, provenance, job status) are in the SDK.
- **Semantic search / embeddings** — `search` is FTS5 + exact `grep` with
  honest coverage states; the searcher is an interface for richer backends.
- **Human UI** — the tree is plain documents; export a branch to Markdown for
  Obsidian or any editor.
- **Message transport / orchestration** — agents communicate through the
  workspace, not through us.

## Status

Early. Extracted from a production agent platform; core storage, sessions,
MCP and the sandboxed SDK are tested (unit + concurrency + real-stack tests),
the collective layer and connectors are next. Apache-2.0.
