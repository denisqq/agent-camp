# Development

## Checks

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
