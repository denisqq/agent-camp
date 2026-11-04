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

Or with Docker (workspace in a volume; self-hosted S3 via MinIO: `docker compose --profile s3 up`):

```bash
CAMP_ADMIN_TOKEN=$(openssl rand -hex 32) docker compose up -d
```

### Install for agents (superpowers-style)

One command per harness installs the MCP wiring **and** a skill that teaches
the agent the camp contract (`map → start → run`, conflicts, discussions):

```bash
camp connect claude-code   # .mcp.json + .claude/skills/using-agent-camp in cwd
camp connect codex         # ~/.codex/config.toml + ~/.codex/skills/...
camp connect cursor        # .cursor/mcp.json + .cursor/skills/...
camp connect claude-desktop --print   # snippet to merge
export CAMP_TOKEN=$(camp token --subject my-agent --spaces workspace,shared | jq -r .token)
```

Claude Code can also install camp as a plugin from this repository's
marketplace (skills + MCP config, managed by the plugin system):

```
/plugin marketplace add denisqq/agent-camp
/plugin install agent-camp
```

Or from npm (downloads the platform binary on install):

```bash
npx agent-camp@latest connect claude-code
```

Then in any MCP client, mapped to `http://127.0.0.1:8090/mcp` with the token:

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

The logical model (tree, kinds, sections with sharing, relations/backlinks,
discussions, templates, history) is a deliberate wiki — see
[docs/MODEL.md](docs/MODEL.md).

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
