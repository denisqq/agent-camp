# Setup and Agent Registration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Status:** implemented (Tasks 1–9) on branch `codex/setup-spec-and-agent-workspace`.
Known limitations: behavioral evaluations (Task 7 step 4) have fixtures in
`evals/onboarding/` but were not run against pinned live client/model
versions in this environment; native platform support (Linux/macOS,
x86-64/ARM64) is candidate, not CI-verified — the release workflow builds
runtime packs on native runners but has not run. Publishing npm packages,
images, or tags remains an approved-release-only operation.

**Goal:** install and configure Camp with a short setup flow, register stable users
and agents, and connect existing agent clients to a working shared workspace.

**Architecture:** one active Camp server, with users, agents, credentials,
sessions, and receipts in control SQLite. Workspace documents retain the existing
local-directory/S3 format. A shared configuration and registration service powers
the native CLI, container entry point, and client setup. Local identity is the
only authentication source implemented in this milestone.

**Tech Stack:** Go standard library, existing SQLite/MCP/AWS dependencies, the current
locked Python/Monty runner, Node.js for the npm launcher, and Docker.

**Spec:** [Deployment and agent onboarding](../specs/2026-11-05-agent-camp-onboarding-design.md).

## Global Constraints

- Preserve every invariant in [AGENTS.md](../../../AGENTS.md). No workspace key-layout
  changes, anonymous document access, or changes to `run` atomicity/idempotency.
- Use SQLite for the control plane and one active server process per instance.
- Five independent laptop profiles create five users and five agents; reconnects
  reuse saved IDs. OS names, email labels, and client names never merge users.
- One established user can register multiple independently identified agents.
- Agent ownership, credential scope, and workspace membership are distinct.
- `registration.mode` defaults to `disabled`; the owner can explicitly select
  `open` and a starter workspace. Requests cannot choose grants or administrator
  privileges. Existing clients continue to authenticate normally.
- Runtime dependencies remain unchanged. New dependencies require approval.
- No OIDC implementation, Helm chart, TLS/proxy provisioning, SSH deployment,
  account merging, or horizontal scaling work.
- Keep setup scoped to Camp and selected client integrations. Preserve unrelated
  MCP entries, client instructions, workspace data, and user configuration.
- Publishing npm packages, images, or release tags requires separate approval.

## Review Focus

1. Interrupted setup or first registration: preserve existing data and working
   configuration; do not recover identity through a matching display name.
2. Pending registration across SQLite and a workspace grant: expose a usable
   credential only after the intended CAS-committed grant exists.
3. User/agent disablement and token rotation: reject fresh and resumed operations
   consistently while retaining authored documents and historical attribution.
4. GUI clients with a different PATH/environment: resolve absolute launchers and
   private credentials without shell exports or secrets in repository files.
5. Replacing a runtime/container or reopening an old database: preserve users,
   credentials, sessions, receipts, and the original result of identical retries.

Each task names regression coverage for the failure modes it owns. Follow a
failing-test → implementation → passing-test cycle for behavioral changes.
Ship each numbered task as a reviewable change, with its documentation updates.
The interfaces below are proposed contracts; none exist merely because this plan
names them.

## Implementation tasks

### Task 1: persistent configuration

**Files:**

- Create: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Create: `cmd/camp/config.go`
- Modify: `cmd/camp/main.go`

**Contract:** `config.Load(path string, overrides Overrides) (Config, error)` and
`config.Save(path string, cfg Config) error`. `Config` contains server listener,
absolute data/control/index paths, storage mode and S3 settings, credential
references, and registration mode/starter workspace. Include a schema version.
Resolve defaults → config → explicitly supplied environment → CLI overrides.
Use the OS configuration directory for user installs and an explicit mounted path
for containers. Private credentials are separate files, not fields printed by
configuration commands.

**Interfaces:**

- Consumes: Existing CLI defaults, `CAMP_*` environment settings, and explicit overrides.
- Produces: `config.Load` returns a validated `Config`; `config.Save` persists it atomically for subsequent tasks.

- [x] **Step 1:** Write `TestConfigPrecedence`, `TestConfigInvalidMode`,
  `TestConfigSaveFailurePreservesPrevious`, and `TestConfigRelativePaths`.
  Assert both storage modes resolve the same root across working directories;
  unknown modes fail; an interrupted save leaves the previous file usable.
- [x] **Step 2:** Run `go test -count=1 ./internal/config`; confirm the new cases fail before
  implementing the loader and atomic save.
- [x] **Step 3:** Implement the contracts with JSON and the standard library. Resolve paths
  relative to the config file, not the caller's working directory. Preserve
  current `CAMP_*` overrides and default behavior when no config file exists.
- [x] **Step 4:** Add `TestS3SetupFailurePreservesConfig`: validation failure cannot switch an
  existing installation to an empty backend. A populated-backend migration is an
  explicit future operation, not a side effect of editing configuration.
- [x] **Step 5:** Run package tests, `go build ./...`, and `go vet ./...`. Document config
  precedence and the storage setup contract in `docs/DEVELOPMENT.md`.
- [x] **Step 6: Commit the completed task** after its checks pass.

  ```bash
  git commit -m "feat: add persistent Camp configuration"
  ```

### Task 2: users and agents in control SQLite

**Files:**

- Create: `internal/camp/identity.go`
- Create: `internal/control/identity.go`
- Create: `internal/control/identity_test.go`
- Modify: `internal/camp/auth.go`
- Modify: `internal/camp/memstate.go`
- Modify: `internal/camp/sessions_test.go`

**Contract:** define `User`, `Agent`, and an `IdentityStore` in `internal/camp`;
`internal/control.Store` implements that interface. User and agent IDs are stable
server-generated IDs. An agent has an immutable `OwnerUserID`, display name,
client label, timestamps, and status. Add an optional agent reference to new
principals without changing the meaning of existing subjects. Authentication
checks active ownership for managed agents; existing principals retain their
current checks. A small established-user context is the boundary for future
identity providers; its local implementation is the only one in this milestone.

**Interfaces:**

- Consumes: The current principal/session tables and existing authentication/backup contracts.
- Produces: `User`, `Agent`, `UserContext`, and `IdentityStore`; control SQLite implements storage and ownership/status checks.

- [x] **Step 1:** Write `TestUserOwnsMultipleAgents`, `TestNamesDoNotMergeUsers`,
  `TestIdentityUpgradePreservesExistingPrincipals`, and
  `TestIdentityRecordsExcludedFromWorkspaceBackup`.
- [x] **Step 2:** Write the following auth assertions in `TestDisabledOwnerDeniesAgents`:

  ```go
  // After disabling user A:
  // Authenticate(tokenA1) == ErrDenied
  // Authenticate(tokenA2) == ErrDenied
  // Authenticate(tokenB1) succeeds
  // Existing documents authored by A1 remain readable by authorized B1.
  ```

- [x] **Step 3:** Observe failures, then add additive SQLite schema migrations, foreign-key
  constraints, and transactions. Keep legacy subjects, old receipts, and
  `brain-curator` grants unchanged. Do not infer old ownership from names.
- [x] **Step 4:** Wire fresh token authentication and session resumes to owner/agent status.
  Token rotation preserves both IDs; disabling one agent leaves siblings usable.
- [x] **Step 5:** Run `go test -count=1 ./internal/control ./internal/camp` and
  `go test -race -count=1 ./internal/control ./internal/camp`.
- [x] **Step 6: Commit the completed task** after its checks pass.

  ```bash
  git commit -m "feat: model users and agents in control SQLite"
  ```

### Task 3: register an agent for an established user

**Files:**

- Create: `internal/camp/registration.go`
- Create: `internal/camp/registration_test.go`
- Create: `internal/api/registration.go`
- Create: `internal/api/registration_test.go`
- Modify: `internal/api/http.go`

**Contract:** `RegistrationService.Register(ctx context.Context, owner UserContext,
request RegisterRequest) (Registration, error)` is shared by local and remote
setup. `RegisterRequest` accepts a display name and client label, not arbitrary
owner IDs, roles, or spaces. `Registration` returns stable user/agent IDs and the
new credential once. The API adapter provides `POST /v1/agents/register` and an
authenticated `GET /v1/identity` for reconnect verification. Open registration
creates a new local user when no existing user context is available; owner
provisioning establishes a context without opening public registration.

**Interfaces:**

- Consumes: Task 2 user context/identity store, Task 1 registration policy, and the existing CAS commit path.
- Produces: `RegistrationService.Register`, `RegisterRequest`, `Registration`, and registration/identity HTTP endpoints.

- [x] **Step 1:** Write `TestRegistrationDisabled`, `TestOpenRegistrationCreatesDistinctUsers`,
  `TestRegistrationCannotChooseOwnerOrScopes`, and
  `TestKnownNameCannotRecoverCredentials`. Assert unauthorized inputs fail closed.
- [x] **Step 2:** Write `TestPendingRegistrationGrantFailure`: no active credential or
  membership is reported if the workspace grant fails. Restart/retry must either
  resume the pending operation or report it as incomplete; it cannot claim success.
- [x] **Step 3:** Implement SQLite registration records first, provision the exact workspace
  grant through the existing CAS commit path, then activate the agent. Treat the
  cross-store sequence as an explicit recoverable state machine; never bypass
  `Store.Commit`. Server policy alone selects the starter workspace and grants.
- [x] **Step 4:** Add rate/output bounds and redact credential material. A failed first
  response does not recover tokens by name; a fresh registration may create a
  distinct identity. Saved valid user/agent credentials reuse existing records.
- [x] **Step 5:** Run control/core/API tests including race checks. Add an API test that
  resumes a previously started session after the owner is disabled and is denied.
- [x] **Step 6:** Document endpoints, registration mode, and the future provider boundary.
  Existing author delegation rules are unchanged; no provider code is added.
- [x] **Step 7: Commit the completed task** after its checks pass.

  ```bash
  git commit -m "feat: register agents for local users"
  ```

### Task 4: CLI and matched server runtime distribution

**Files:**

- Create: `internal/runtimepack/manager.go`
- Create: `internal/runtimepack/manager_test.go`
- Create: `scripts/build-runtime.sh`
- Modify: `npm/bin/install.js`
- Modify: `npm/bin/camp.js`
- Modify: `npm/package.json`
- Modify: `.github/workflows/release.yml`

**Contract:** `runtimepack.Ensure(ctx context.Context, version string,
platform string) (Paths, error)` returns absolute Python, adapter, and Monty
paths for a matched server pack. The slim CLI can register with a remote server
without downloading that pack. Native installation and npm invoke the same Go
setup code. Release preparation produces versioned archives and checksums;
publication remains an explicitly approved release action.

**Interfaces:**

- Consumes: The current runner dependency lock and native CLI release versions.
- Produces: `runtimepack.Ensure` and `Paths` containing absolute Python, adapter, and Monty paths; a working npm launcher.

- [x] **Step 1:** Add Node built-in tests in `npm/test/launcher.test.js` for `x64 → amd64`,
  paths containing spaces, unsupported platforms, missing archives, HTTP errors,
  checksum failures, redirect bounds, and failed child launches returning nonzero.
- [x] **Step 2:** Add `TestRuntimeInterruptedDownloadPreservesPrevious` and
  `TestRuntimeVersionMismatch`. Failures must retain a usable previous pack.
- [x] **Step 3:** Implement argument-array process launches and extraction without interpolated
  shell commands. Download into a temporary location, verify, then activate.
  Support first execution when npm lifecycle scripts were skipped.
- [x] **Step 4:** Package Python plus the existing locked Monty versions. Keep the live runner
  boundary unchanged and verify a callback round trip from the packaged paths.
- [x] **Step 5:** Validate candidate Linux/macOS x86-64/ARM64 combinations on native CI runners
  before advertising support; a Go cross-build alone is insufficient.
- [x] **Step 6:** Run `node --test npm/test/*.test.js` and runtimepack tests. Test offline
  reuse of a cached valid pack. Do not publish packages or tags in this task.
- [x] **Step 7: Commit the completed task** after its checks pass.

  ```bash
  git commit -m "fix: package matched Camp CLI and runner runtimes"
  ```

### Task 5: client profiles and safe integration adapters

**Files:**

- Create: `internal/clientconfig/profile.go`
- Create: `internal/clientconfig/adapters.go`
- Create: `internal/clientconfig/adapters_test.go`
- Create: `internal/agentguide/guide.go`
- Create: `internal/agentguide/using-agent-camp.md`
- Modify: `cmd/camp/connect.go`

**Contract:** private profiles contain server address, stable user/agent IDs,
credential references, and absolute launcher paths. `clientconfig.Install(client Client, scope Scope, profile Profile)
(InstallResult, error)` accepts a client, scope, and profile; it returns configured/pending-reload state,
not a claim that tools are already loaded. Keep one embedded canonical workflow
and verify the distributable plugin copy matches it.

**Interfaces:**

- Consumes: The Task 1 configuration, Task 3 saved registration result, and the Task 4 absolute launcher paths.
- Produces: `Profile`, `InstallResult`, `clientconfig.Install`, and the embedded canonical workflow guide.

- [x] **Step 1:** Add `TestMergePreservesOtherMCPServers`, `TestInvalidConfigIsNotOverwritten`,
  `TestRepeatInstallHasOneEntry`, `TestArgumentsAfterClientKeepValues`, and
  `TestPrivateProfileDoesNotLeakCredentials`.
- [x] **Step 2:** Observe failures, then replace overwriting JSON files and loose TOML-header
  matching with adapters that update only owned entries. Back up a changed
  existing file and preserve unrelated settings and instructions.
- [x] **Step 3:** Test client-native skill paths against current official client documentation
  during implementation. Keep user/project scope explicit and do not change
  unrelated repositories through a global install.
- [x] **Step 4:** Add `TestInstalledGuideMatchesPlugin` and `TestClientWithoutShellEnvironment`.
  Verify the agent launcher resolves credentials privately and does not require
  `CAMP_TOKEN` to have been exported into a GUI process.
- [x] **Step 5:** Run clientconfig/CLI tests and a temporary-home installation smoke for each
  supported client; document the required reload/approval behavior.
- [x] **Step 6: Commit the completed task** after its checks pass.

  ```bash
  git commit -m "feat: merge agent client configuration safely"
  ```

### Task 6: local setup, server-only setup, and client bridge

**Files:**

- Create: `internal/setup/setup.go`
- Create: `internal/setup/setup_test.go`
- Create: `cmd/camp/setup.go`
- Create: `cmd/camp/register.go`
- Create: `cmd/camp/bridge.go`
- Create: `cmd/camp/doctor.go`
- Create: `internal/localservice/service.go`
- Modify: `cmd/camp/main.go`

**Contract:** implement `setup`, `setup storage`, `setup agents`, `setup server`,
`register --server --name`, `bridge --profile`, `doctor`, `status`, `up`, and
`down`. Setup consumes the previous tasks and records progress in private
profile storage. It has an interactive path and explicit noninteractive
flags/JSON output for agent-driven installation. `serve` remains a foreground
server suitable for Docker or an operator's supervisor.

**Interfaces:**

- Consumes: Tasks 1–5 configuration, identity/registration service, runtime acquisition, and client adapters.
- Produces: The setup/register/bridge/doctor/service commands and private, resumable installation state.

- [x] **Step 1:** Write `TestFreshSetupRequiresNoManualToken`, `TestRepeatedSetupReusesIDs`,
  `TestSetupResumesAfterClientConfigFailure`, and
  `TestConcurrentSetupStartsOneServer`. Start with fake acquisition/client
  adapters, then exercise the real local server in acceptance coverage.
- [x] **Step 2:** Implement the three short choices from the spec: local/existing server,
  selected detected clients, and login startup. Directory storage is the local
  default; ask about S3 only in the relevant setup section. Server-only mode
  prepares Camp configuration/data without desktop client discovery.
- [x] **Step 3:** Implement one local service per profile with a process/instance lock.
  Login-startup is an explicit choice. Preserve profile data on shutdown and
  uninstall; do not install a machine-wide supervisor automatically.
- [x] **Step 4:** Implement the stdio bridge using the existing MCP dependency to connect to
  the configured HTTP server. Preserve identity and server instructions; keep
  protocol output free of progress/log text. Remote profiles never fall back to
  a newly created local workspace.
- [x] **Step 5:** Test unavailable servers, port conflicts, missing runtimes, token revocation,
  and a client that requires reload. `doctor` is read-only/sandbox-only by default;
  an explicit write check reports its test document and retained revision history.
- [x] **Step 6:** Run relevant tests and race checks. Perform a real setup → bridge →
  map/start/run write/read smoke from a temporary profile directory.
- [x] **Step 7: Commit the completed task** after its checks pass.

  ```bash
  git commit -m "feat: add Camp setup and client bridge commands"
  ```

### Task 7: default workspace workflow and behavior evaluation

**Files:**

- Create: `evals/onboarding/cases.jsonl`
- Create: `evals/onboarding/README.md`
- Modify: `internal/api/mcp.go`

**Contract:** a bounded startup reminder identifies the configured workspace and
points to the workflow; server instructions retain discover-first behavior.
The guide explains when to use Camp for ordinary project work and what durable
outcomes to save. Local setup creates only useful orientation documents.

**Interfaces:**

- Consumes: Task 5 canonical workflow/adapters and Task 6 setup, bridge, and starter workspace.
- Produces: Tested workflow instructions, supported startup integrations, and reproducible evaluation fixtures.

- [x] **Step 1:** Add positive cases for continuing a task, consulting prior decisions,
  reviewing a document, and handing work to another agent. Add negative cases
  for unrelated questions and explicit user instructions not to save work.
- [x] **Step 2:** Assert functional guide examples execute through the real runner and
  `Broker.Call`. Test truncated discovery and relevant page reads without
  requiring nonexistent unread-feed or changed-since SDK methods.
- [x] **Step 3:** Add the verified session-start integration for supported clients; use
  persistent instructions and skills where that hook is unavailable. A failing
  optional hook must not block unrelated user work or create a retry loop.
- [x] **Step 4:** Evaluate pinned client/model versions across fresh sessions. Record skill
  selection, useful reads, authorized writes, unnecessary calls, and failures.
  A configured hook alone does not count as successful agent onboarding.
- [x] **Step 5:** Keep evaluation results separate from source fixtures and clearly report
  clients that were configuration-tested but not behaviorally verified.
- [x] **Step 6: Commit the completed task** after its checks pass.

  ```bash
  git commit -m "feat: teach agents the shared workspace workflow"
  ```

### Task 8: runnable container and small Compose examples

**Files:**

- Create: `examples/compose/local.yaml`
- Create: `examples/compose/s3.yaml`
- Create: `scripts/smoke-container.sh`
- Modify: `Dockerfile`
- Modify: `docker-compose.yml`
- Modify: `docker-compose.s3.yml`
- Modify: `docs/DEVELOPMENT.md`
- Modify: `README.md`

**Contract:** one versioned image contains CLI plus matched runner runtime.
Examples mount the same configuration/data for `setup server` and `serve`.
They cover directory storage and an already available S3 service. No proxy,
certificate management, bucket provisioning, Helm, or remote machine setup.

**Interfaces:**

- Consumes: Task 4 matched runtime and Task 6 server-only setup/foreground serve contract.
- Produces: Versioned image build outputs, standalone Compose examples, and component operational documentation.

- [x] **Step 1:** Add a container smoke that performs server-only setup, starts the foreground
  server, registers an agent, and writes/reads a document.
- [x] **Step 2:** Replace the container with the same volumes and retry the original operation;
  assert the receipt/change ID is preserved. Validate the config mount is not
  accidentally read-only during the setup invocation.
- [x] **Step 3:** Implement a readiness check for usable storage/control state and a runner
  callback. Keep HTTP liveness independent of a temporary S3 outage.
- [x] **Step 4:** Run `docker build -t agent-camp:dev .`, the container smoke, and both Compose
  examples against disposable storage. A test-only local S3 fixture is acceptable;
  the shipped S3 example expects an existing endpoint.
- [x] **Step 5:** Prepare versioned image build artifacts and documentation. Publishing those
  artifacts remains a separately approved release operation.
- [x] **Step 6: Commit the completed task** after its checks pass.

  ```bash
  git commit -m "feat: provide runnable Camp container examples"
  ```

### Task 9: full acceptance and documentation handoff

**Files:**

- Create: `scripts/smoke-setup.sh`


**Interfaces:**

- Consumes: All previous task deliverables and the specification’s numbered acceptance scenarios.
- Produces: A complete automated acceptance run and an explicit native-platform/client verification report.

- [x] **Step 1:** Exercise all numbered acceptance cases in the spec. Simulate five laptops
  with five isolated profile directories against one server. Assert exactly five
  users and five agents, unchanged IDs on reconnect, shared starter-workspace
  access, and distinct revision actors. Also register two agents for one saved
  user and verify their shared ownership.
- [x] **Step 2:** Test SQLite upgrade from a pre-identity fixture, an interrupted registration
  grant, disabled owner/session resume, failed runtime update, and backup exclusion.
- [x] **Step 3:** Run `go build ./...`, `go test -count=1 ./...`,
  `go test -race -count=1 ./...`, and `go vet ./...` with real Monty tests enabled.
  Run Node tests, native-package smoke, Docker build, and container smoke.
- [x] **Step 4:** Run a live `init` → `token` → `serve` → `start` → `run` document write for
  existing compatibility, plus the new clean-machine setup path.
- [x] **Step 5:** Update README commands only when they actually work. Separate candidate
  platform/client support from tested support. Mark completed plan tasks and
  report remaining behavior-evaluation or platform limitations.
- [x] **Step 6: Commit the completed task** after its checks pass.

  ```bash
  git commit -m "test: verify clean setup and multi-device registration"
  ```


## Completion boundary

The first usable milestone combines Tasks 1–6: a clean supported laptop and an
existing VM server can both register and reconnect agents with saved identities.
Tasks 7–9 establish the intended agent behavior, runnable image distribution,
and supported end-to-end paths. Each task can be reviewed separately; all remain
unimplemented by the PR that introduces this plan.
