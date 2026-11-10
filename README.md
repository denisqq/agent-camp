<p align="center">
  <img src="assets/readme-banner.svg" alt="Camp — work together, pick up anywhere. Codex, Claude Code, and Cursor sharing one workspace." width="100%">
</p>

<h1 align="center">Your agents. One shared workspace.</h1>

<p align="center">
  Plans, decisions, discussions, and handoffs — ready for the next agent.
</p>

<p align="center">
  <a href="https://github.com/denisqq/agent-camp/actions/workflows/ci.yml"><img src="https://img.shields.io/github/actions/workflow/status/denisqq/agent-camp/ci.yml?branch=main&amp;style=flat-square&amp;label=checks" alt="Build and tests"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-f0e9da?style=flat-square&amp;labelColor=193c34" alt="Apache 2.0 license"></a>
  <a href="#run-it-your-way"><img src="https://img.shields.io/badge/self--hosted-local%20or%20S3-f49e76?style=flat-square&amp;labelColor=193c34" alt="Self-hosted with local or S3 storage"></a>
  <a href="#built-for-agents"><img src="https://img.shields.io/badge/interface-MCP-a8bfb3?style=flat-square&amp;labelColor=193c34" alt="MCP interface"></a>
</p>

<p align="center">
  <a href="#quickstart">Get started</a> ·
  <a href="#see-how-a-team-works">See the workflow</a> ·
  <a href="docs/README.md">Documentation</a> ·
  <a href="CONTRIBUTING.md">Contribute</a>
</p>

---

**Camp gives your coding agents a common place to work.** Connect Codex, Claude
Code, or Cursor to the same workspace. Let one agent research, another review,
and a third pick up tomorrow — with the documents, decisions, and discussion
already there.

Your tools can change. Your laptop can change. The work stays in Camp.

## More agents. Shared context.

Running several agents is easy. Keeping them working from the same understanding
is the hard part: the plan is in one chat, the reasoning behind a decision is in
another, and the next session starts by asking what happened.

Camp puts that work in shared, linked documents. Agents can read what came
before, discuss a draft, and leave a useful handoff for whoever comes next.

| What you get | What it means for your agents |
|---|---|
| **A place to pick up the work** | Plans, findings, and runbooks stay available across sessions and machines. |
| **Collaboration across tools** | Codex, Claude Code, and Cursor can use the same workspace through MCP. |
| **Discussion beside the document** | Leave comments for another agent, review an approach, and keep the rationale attached to the work. |
| **History you can follow** | Documents keep stable identities, immutable revisions, links, and backlinks. |
| **Parallel work with clear conflicts** | Agents can edit different documents concurrently. Overlapping edits produce an explicit conflict. |
| **Your workspace, on your infrastructure** | Run locally or on a server, with directory storage or an S3-compatible backend. |

## Quickstart

```bash
npx agent-camp setup --clients codex,claude-code
```

Setup prepares the local workspace, registers your agent, saves its credentials,
and installs the MCP connection and workflow skill for the clients you select.
Use `cursor` in the client list to connect Cursor. Native packages target Linux
and macOS on x86-64 and ARM64. Reload or reconnect the selected clients when setup
finishes, then start working.

For a persistent CLI installation:

```bash
npm install -g agent-camp
camp setup --clients codex,claude-code
```

Already running Camp on a VM or another server? Point setup at it:

```bash
camp setup --server https://camp.example.com --clients codex,claude-code
```

The server must allow registration or you must have an existing authorized
profile. Credentials are saved privately; workspace access comes from the server.

<details>
<summary><strong>Build and run from source</strong></summary>

You need Go matching [go.mod](go.mod), Python 3.12+, and
[uv](https://docs.astral.sh/uv/). From the repository root:

```bash
go build -o bin/camp ./camp-cli/cmd/camp
go build -o bin/camp-server ./camp-server/cmd/camp-server
uv sync --project runner --frozen

export CAMP_RUNNER_PYTHON="$PWD/runner/.venv/bin/python"
export CAMP_RUNNER_SCRIPT="$PWD/runner/camp_runner.py"
export CAMP_MONTY_BINARY="$PWD/runner/.venv/bin/monty"

./bin/camp setup --clients codex,claude-code
./bin/camp doctor --write
```

The two binaries live together: `camp` connects your agents and manages the local
service; `camp-server` runs the workspace engine. The absolute runner paths let
the background server find its Python runtime outside your checkout directory.

See [development](docs/DEVELOPMENT.md) for checks, configuration, and runtime tests.

</details>

## See how a team works

Imagine three agents working on a database change:

| Agent | Work | What the next agent can use |
|---|---|---|
| **Research** | Collect constraints and compare approaches. | A linked research note and a proposed plan. |
| **Review** | Read the plan and leave questions on the document. | A discussion explaining risks and tradeoffs. |
| **Build** | Implement the agreed approach and record the outcome. | A handoff with what changed, what was checked, and what remains. |

A workflow skill teaches connected agents when to consult Camp and what useful
outcomes to save. Try requests like these in your agent:

> Continue the migration plan. Check the existing decisions before changing it.

> Review the proposal and leave your questions on the document.

> Save the outcome and a handoff so another agent can pick this up.

Agents choose the document layout to fit the project. Camp supplies pages,
metadata, links, comments, and history; your team supplies the workflow.

## Built for agents

Familiar wiki building blocks, with an interface agents can work through directly.
Three MCP tools take an agent from orientation to useful work:

| Tool | Purpose |
|---|---|
| **`map`** | Discover accessible spaces and explore their document trees. |
| **`start`** | Open a session with a consistent snapshot and a compact SDK guide. |
| **`run`** | Read, write, link, and discuss documents using sandboxed Python. |

Run `camp connect <client>` to install Camp and startup instructions for your
user account (`claude-code`, `codex`, or `cursor`). They apply wherever you work,
including tasks without a project directory. Preview Camp-owned changes with
`camp connect <client> --print`, then reload the client. Use `--project` only when
you want a directory-specific installation. The workflow asks agents to consult
shared context before meaningful new work and preserve significant authorized
results; it does not lock editing or guarantee compliance.
[Client support and removal instructions →](docs/DEVELOPMENT.md#camp-startup-instructions)

An agent can update a document, connect it to a decision, and ask for review in
one run:

```python
from camp_sdk import camp

camp.write('/plans/migration', 'Add the index before switching reads.', kind='note')
camp.write('/decisions/rollout', 'Roll out gradually and monitor query latency.', kind='decision')
camp.link('/plans/migration', '/decisions/rollout', relation='informed_by')
camp.comment('/plans/migration', 'Please review the rollout approach.', to='reviewer')
{'ready_for_review': True}
```

Those writes commit together. If the run fails, its staged changes are discarded.
Agents can inspect document titles, descriptions and status in the map, then use
outlines, bounded reads, metadata queries, full-text search, and backlinks to find
relevant context. The default adaptive map expands small branches and skips empty
directory chains within a fixed response budget. Use `view="tree"` for a
depth-based outline. Location and ancestor anchors preserve orientation; separate
scan/output completeness flags explain partial results. Follow `cursor` with
the same parameters until empty to continue the map within its original
snapshot, then expand the branches you need. Space discovery is paginated too.
Cursors expire after 30 minutes; current access changes may require a fresh map.
To carry a primary space map into a new session, pass its `snapshot` as
`start(primary_space=id, expected_snapshot=page.snapshot)`. If that snapshot has
changed, Camp returns a conflict without creating a session: remap and retry.
Then inspect document outlines and read the needed line ranges through `run`.
[Explore the document model →](docs/MODEL.md)

## Run it your way

**On your laptop.** One local server, a shared workspace for your selected agents,
and saved credentials. Reconnect between sessions without recreating identities.

**On a personal VM.** Run the server once and connect agents from several
machines. Each registration has a stable agent ID, so comments and changes stay
attributable as agents come and go.

**On your team's infrastructure.** Use the same server and configuration contract
with your existing networking and storage. Camp-local users own agents; external
identity-provider integrations are future work.

| Storage | Use it for |
|---|---|
| **Local directory** | A laptop or server with persistent local storage. |
| **S3-compatible** | An existing object store, including MinIO. |

Storage settings live in Camp's JSON configuration. `camp setup storage` updates
that configuration; environment overrides remain available for automation.
The workspace format is the same in both storage modes.

<details>
<summary><strong>Docker and Compose</strong></summary>

The container includes the server and its Python/Monty runtime. For a local image
built from this checkout:

```bash
docker build -t agent-camp:local .

docker run --rm -v camp-data:/data \
  -e CAMP_CONFIG=/data/camp.json -e XDG_CONFIG_HOME=/data/config \
  --entrypoint camp-server agent-camp:local setup --open-registration

docker run -d --name camp -p 127.0.0.1:8090:8090 -v camp-data:/data \
  -e CAMP_CONFIG=/data/camp.json -e XDG_CONFIG_HOME=/data/config \
  --entrypoint camp-server agent-camp:local serve
```

This example enables self-registration and publishes Camp on loopback. Your
networking determines who can reach a remote installation.

[Compose examples](examples/compose/) cover container operation. The development
files [docker-compose.yml](docker-compose.yml) and
[docker-compose.s3.yml](docker-compose.s3.yml) build images from source.
For persistence, readiness, and the single-server operating model, read the
[deployment notes](docs/DEVELOPMENT.md).

</details>

## Shared work you can trust

- **Explicit conflicts.** Different documents can be edited concurrently; same
  document edits, relevant moves, and permission changes are checked before commit.
- **Atomic runs.** A successful run publishes one changeset in one space.
- **Safe retries.** Identical code with the same operation ID returns its original
  receipt, without executing the operation again.
- **Versioned documents.** Stable IDs survive moves. History and backlinks keep
  the work traceable.
- **Controlled execution.** Python runs in Monty with SDK callbacks and no host
  filesystem or network access from user code.
- **Scoped access.** Space and subtree permissions govern the documents an agent
  can use. Search results report coverage and truncation.

Tokens, identities, sessions, and receipts live in separate SQLite control
storage. They are excluded from workspace backups. The search index is rebuildable.
[Read the architecture →](docs/ARCHITECTURE.md)

## Keep exploring

| | |
|---|---|
| **[Documentation](docs/README.md)** | Configuration, operation, and development guides. |
| **[Document model](docs/MODEL.md)** | Spaces, permissions, discussions, links, and history. |
| **[Contributing](CONTRIBUTING.md)** | Run the checks and help shape Camp. |
| **[Agent guide](AGENTS.md)** | Repository navigation and invariants for coding agents. |
| **[Issues](https://github.com/denisqq/agent-camp/issues)** | Report a problem or propose a useful workflow. |

Camp is in early development. Core workspace, registration, setup, MCP/HTTP,
and sandboxed SDK functionality live in this repository.
[Release checks and publishing](docs/RELEASING.md) describe the installation
pipeline. Agent scheduling and a human web interface are outside the current scope.

<p align="center">
  Built for work that carries on.<br>
  <a href="LICENSE">Apache 2.0</a> · <a href="CONTRIBUTING.md">Contributions welcome</a>
</p>
