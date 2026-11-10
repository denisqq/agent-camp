# Development

## Checks

Two binaries ship from this repository: `camp` (client: onboarding,
bridges, local service management) and `camp-server` (server runtime for
process managers and containers). Both are cobra CLIs over the shared
internal packages.

Use the Go version from [go.mod](../go.mod). The full check chain is:

```bash
go build ./...
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
```

`just check` runs build, tests, and vet. `just race` runs the race detector.
Race checks are required for changes touching storage, sessions, or the runner.

## Real Python runner

Install the existing locked dependencies and enable the integration tests:

```bash
uv sync --project runner --frozen
export CAMP_TEST_PYTHON="$PWD/runner/.venv/bin/python"
go test -count=1 ./internal/runner ./internal/api
```

The runner suite exercises SDK calls, sandbox restrictions, timeouts, output
limits, and process recovery. The MCP suite runs returned `start` examples,
reads and writes documents, checks retries and concurrent sessions, and verifies
rollback with self-contained test data. Neither requires external seed files.
Tests skip the real runtime when `CAMP_TEST_PYTHON` is unset.

To include S3 contract and MCP stack checks, set `CAMP_TEST_S3_ENDPOINT` to a
reachable disposable MinIO instance configured with access key `camp-local-test`
and secret key `camp-local-test-password`. Tests create buckets named
`camp-contract` and `camp-stack-<id>`; use a test deployment.

After runner or Docker dependency changes:

```bash
docker build -t agent-camp:dev .
```

The full container path (setup server → serve → register → write/read →
replace container with the same volumes) is exercised by:

```bash
scripts/smoke-container.sh
```

Published-image Compose examples live in `examples/compose/` (directory
storage and S3); publishing the versioned images themselves is a release
operation requiring approval.

For a live write through the server, follow the [quickstart](../README.md#quickstart).
The runner executable paths must be absolute.

## Persistent configuration

Camp resolves settings from a JSON configuration file: `$CAMP_CONFIG`, then
`./camp.json` if present, then the OS user configuration directory
(`~/.config/camp/camp.json` on Linux). When no file exists, the historical
defaults rooted at the working directory apply, so existing `CAMP_*`
workflows are unchanged.

Precedence for each setting: defaults → configuration file → `CAMP_*`
environment variables → CLI overrides. Paths inside the file resolve against
the file's directory, not the caller's working directory. `camp config`
prints the resolved configuration without secrets.

`config.Save` validates before writing and replaces the file atomically
(temporary sibling plus rename), so an interrupted or failing save leaves
the previous working configuration intact. The file never holds secret
material: S3 credentials live in private files referenced by
`storage.credentials_ref`. Changing the storage backend of a populated
workspace is an explicit future migration operation; a failed validation
cannot silently switch an installation to an empty backend.

See the [onboarding design](superpowers/specs/2026-11-05-agent-camp-onboarding-design.md)
for the storage setup contract this serves.

## Agent registration

Control SQLite now also stores Camp-local users and their agents
(`internal/camp/identity.go`, `internal/control/identity.go`). A credential
may carry `agent_id`; authentication then requires that agent and its owner
to be active. Legacy principals (including `brain-curator` grants) are
unaffected.

`POST /v1/agents/register` registers an agent from a display name (and an
optional client label only — the request cannot choose an owner, role,
space, or scopes). With a valid agent credential it registers under that
credential's user; without one it works only when
`registration.mode = "open"` and then creates a new Camp-local user. A
presented-but-invalid credential fails closed. `GET /v1/identity` verifies
a saved credential and reports its user and agent. The server — through
`registration.starter_workspace` — selects the granted workspace (writer
access) via the normal CAS commit path; the agent row stays `pending` and
no credential is issued until that grant commits.

The registration service is the boundary a future identity provider plugs
into: it consumes an established `UserContext` and never an arbitrary
owner ID. No provider code exists yet.

## Sharing a team space

Creating a space with `kind: "team"` does not grant every agent access. Its owner
can write; other agents need a matching space or section grant, as well as a
token listing the space and required scopes.

As an administrator, call `GET /v1/admin/spaces/workspace` to read existing
space-level grants. Open a session with `POST /v1/sessions/start` and body
`{"primary_space":"workspace"}`. Use the returned `snapshot` as the expected
commit when updating access:

```http
PUT /v1/admin/spaces/workspace/access
Authorization: Bearer <admin-token>
Content-Type: application/json

{
  "expected_commit": "<snapshot-from-start>",
  "access": {
    "share": [{"subject": "agent-two", "role": "writer"}]
  }
}
```

The access update replaces the space-level grants. Include any existing grants
you intend to keep. A stale `expected_commit` produces a conflict; fetch the
current state before retrying. Issue `agent-two` its own token with `workspace`
in its space list. See the [document model](MODEL.md) for section overrides.

## Naming compatibility

The public CLI, SDK, environment variables, and packages use Camp. The internal
curation subject `brain-curator` keeps its historical name because persisted ACLs
may grant that exact subject access. Renaming it requires an explicit migration
of stored grants; it is not a public SDK name or an alias for `camp_sdk`.

## Camp startup instructions

After setting up a Camp profile, run `camp connect <client>` (`claude-code`,
`codex`, or `cursor`). The default installation is user-wide and does not depend
on the current directory or a Git repository. `camp setup` uses the same default.
Use `--print` for a JSON preview of destination paths and Camp-owned additions;
preview does not write files, contact Camp, or print unrelated private settings.
Reload the client to load the integration.

| Client | Default connection | Default startup delivery |
|---|---|---|
| Claude Code | `~/.claude.json` | `~/.claude/settings.json` SessionStart hook and `~/.claude/skills/using-agent-camp` |
| Codex | `~/.codex/config.toml` | Effective nonempty `~/.codex/AGENTS.override.md` or `AGENTS.md`, plus `~/.agents/skills/using-agent-camp` |
| Cursor | `~/.cursor/mcp.json` | `~/.cursor/hooks.json` sessionStart hook, owned script under `~/.cursor/hooks/`, and `~/.cursor/skills/using-agent-camp` |

Absolute `CLAUDE_CONFIG_DIR` and `CODEX_HOME` values select custom client profiles.
For Claude, the MCP file, settings, and skill use the custom directory. For
Codex, configuration and global instructions use `CODEX_HOME`; user skills stay
under `~/.agents/skills`, the client's documented user discovery location.

The bootstrap requires initial orientation and bounded reading, and preserves
significant results only when authorized. The agent chooses a space from the
task, user context and discovery. No project or space ID is required in advance.
An optional project association is extra context. Follow-up replies reuse the
current task; unavailable service is reported, and independent work can continue.

`camp connect <client> --project` remains available for a directory-specific
installation. It adds local configuration plus a Claude hook, Codex instruction
block, or Cursor always-applied rule. Project trust and normal instruction
precedence apply. It does not remove or overwrite existing user integration.

To remove startup delivery, remove only the managed block between
`<!-- camp:bootstrap:start -->` and `<!-- camp:bootstrap:end -->` in the effective
Codex instruction file, the Claude SessionStart handler whose `statusMessage` is
`Camp workflow` (or the earlier `Camp project workflow`), or the Cursor
`sessionStart` entry invoking the owned `agent-camp-bootstrap` script. Remove
that owned script too if no longer referenced. For a project Cursor install,
remove its owned `.cursor/rules/agent-camp.mdc`. Preserve unrelated instructions
and hooks. Installation output lists the exact paths for the selected scope.

To remove the connection too, remove only its named MCP entry/table and the
`using-agent-camp` skill directory. Existing files are backed up as `.camp-backup`
before replacement; backups may contain private settings and should not be
committed. Remove them when no longer needed. Existing directory-specific Camp
entries from earlier installs remain in place and can override user entries;
review them explicitly if switching profiles.

See the [validation and support matrix](evaluations/camp-bootstrap.md). User-level
Cursor hooks apply to local clients; cloud agents do not load your local home
directory, so use project or managed configuration there.
