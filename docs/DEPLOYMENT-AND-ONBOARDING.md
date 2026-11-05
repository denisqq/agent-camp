# Deployment and agent onboarding

Status: agreed product scope, 2026-11-05; implementation pending. Commands
introduced here describe interfaces to implement and are not available in the
current CLI. Current working installation instructions remain in the
[README](../README.md). The staged implementation is described in the
[implementation plan](IMPLEMENTATION-PLAN.md).

## Goal

A developer installs Camp, selects their agent clients, and opens an agent that
already knows when and how to use the workspace. Local and remotely hosted
installations share the same workspace and onboarding contract. Camp supplies a
runnable component and its client integrations.

Installation succeeds only when a real document write and read work. Agent
onboarding succeeds when ordinary task prompts lead the agent to consult and
update relevant workspace documents without being told to use Camp.

## The Hermes reference

Hermes is the reference for the installation experience: acquire dependencies,
provide one recognizable setup entry point, verify a working first interaction,
and offer focused commands to revisit settings. Its installer and quickstart
make the path from installation to actual use explicit. See the official
[installation
guide](https://hermes-agent.nousresearch.com/docs/getting-started/installation)
and
[quickstart](https://hermes-agent.nousresearch.com/docs/getting-started/quickstart).

For Camp, the useful properties are sensible defaults, progress by named stages,
short questions, repeatable setup, a clear completion screen, and a diagnosis
command. Camp does not need model-provider selection: the developer's existing
agent supplies the model.

## Approach

Provide a native CLI with managed runtime installation, a versioned Docker
image, and small Compose examples. Setup configures Camp itself: storage,
listener, workspace, credentials, and agent connections.

The operator supplies the surrounding infrastructure: a machine or container
platform, networking, DNS, TLS termination, and any external storage service.
Helm charts, reverse-proxy installation, certificate management, and remote
provisioning over SSH are outside the current scope. Camp can run behind the
operator's existing infrastructure without managing it.

The local default is one Camp server per OS user, with a starter `workspace`
space of kind `team`, writable by all their selected agent clients. Setup grants
that access automatically. The reserved `shared` kind has a different fixed
policy and is not the starter collaborative workspace. Additional project spaces
and remote profiles are explicit choices. Client integration is user-scoped by
default; teams can opt into project-scoped settings. Credentials are never
stored in repository configuration.

## Configuration through setup

Storage, listener address, and data paths live in a persistent Camp
configuration file. `camp setup` reads and writes that configuration; users do
not need to assemble environment variables to configure Camp. Reopening a setup
section shows current values as defaults.

The local quick path selects directory storage automatically and does not ask
about S3. `camp setup storage` offers local storage or S3 for users who need it.
For S3, ask for endpoint, bucket, region, and an available credential source,
validate the connection, and save the settings. If the host already supplies a
working credential source, do not ask for access keys again.

Use a JSON configuration file under the OS user's configuration directory for
local installations, and an explicit mounted configuration file for deployed
servers. JSON allows the Go CLI to use its standard library. The same settings
model is used by the native process and the container image. Compose examples
show how to mount the configuration and persist data. For example, the proposed
non-secret storage section is:

```json
{
  "storage": {
    "mode": "s3",
    "endpoint": "https://s3.example.internal",
    "bucket": "camp",
    "region": "us-east-1",
    "credentials_ref": "primary-storage"
  }
}
```

Setup stores credentials separately in private local storage or an
operator-provided secret file; the configuration refers to them. These are
internal storage locations, not extra setup steps for the user. The loader
resolves defaults, configuration, explicit environment overrides, and CLI
overrides in that order. Environment variables remain an advanced automation
interface, not the normal onboarding path. Configuration errors identify the
setting and the setup command that can repair it, without printing secrets.

Save validated configuration atomically, preserving the previous working values
on failure. An existing populated workspace needs an explicit data-migration
flow when changing backends; changing a setting must not silently present an
empty workspace as if its existing documents disappeared.

## Access without credential administration

For a personal installation, the user chooses the agents to connect. Setup
handles identity creation, token issuance, storage, and connection settings as
one operation. There is no account-registration screen, role picker, manual
`camp token` step, or requirement to export a token before opening an agent. All
selected agents can work in the starter workspace. Adding another local client
gives it the same workspace access automatically under the owner's setup.

The backend retains its existing authentication and permission checks. The
current engine already enforces token scopes and space/subtree access; it is not
an anonymous, universally open store. A simple personal experience comes from
automatically provisioning the intended access, without changing those rules.
Unknown or unprovisioned clients still do not gain access.

For a personal VM, the owner starts Camp on the machine using the binary or
container image. Client setup connects to the supplied reachable address and
registers an agent under the server's configured registration policy. It saves
the returned identity, token, and connection automatically. Camp does not log
into the VM or configure the network route to it.

Local and company installations use the same user-to-agent relationship and
registration operation. A local user is prepared automatically; a future company
identity provider will establish the user through external login. Provider
integrations are outside the current implementation scope.

## Users, agents, and authentication sources

Use a small common model:

| Entity | Responsibility |
|---|---|
| User | Stable internal ID, display name, creation time, and active/disabled state. Represents the person owning agents. |
| Agent | Stable internal ID, owning user ID, display name, optional client type, and active/revoked state. |
| Agent credential | Revocable token bound to one agent, with its allowed spaces, scopes, and expiry. Store the token hash server-side. |
| Identity binding (future) | Maps a verified external identity to an internal user. Provider integrations are not implemented in this milestone. |

A user can own several agents, including multiple agents using the same client.
An agent belongs to one user. Client type and display name are labels, not the
identity or an ownership proof. Keep agent ownership fixed in the initial model
so historical attribution remains understandable. Workspace revisions continue
to identify the acting agent; its user is resolved through the agent record. No
workspace key-layout change is required for these control-plane records.

### One registration operation

The common flow is: establish the internal user, register an agent for that
user, issue the agent credential, and configure its connection. The registration
service consumes an established user context; a caller cannot pick an arbitrary
`owner_user_id` in its request. Authentication sources establish identity, while
Camp's registration and access logic decide what that user and agent may do.
Ownership alone grants neither administrator privileges nor access to every
space. Existing space policy and explicitly provisioned grants still apply.

The user's identity is needed to manage/register agents. Once registered, an
agent uses its own saved Camp credential for normal workspace operations.
Changing or rotating that credential does not change the agent's ID. Repeated
setup reuses the existing user and agent records. Disabling an agent denies its
credentials; disabling a user must deny registration and credentials for all of
that user's agents. Preserve authored documents and their stable attribution.

### Local user now

Local setup creates and persists an internal user automatically for this Camp
profile. It may use the OS account name as a display-name default, but generates
its own stable user ID. Renaming an OS account or registering another agent does
not create a new Camp user. This needs no signup form or external account.

A personal VM also uses Camp-local users. The server-side setup creates its
owner, and subsequent agent registration is associated with an established local
user through the configured registration policy. A client-supplied OS username
is not proof that it owns an existing server-side user. Optional open
registration can create a new local user and its first agent, but cannot claim
another user's identity. Saved private credentials allow that user to register
further agents.

### Five laptops connected to one personal VM

Five independent client installations create five Camp-local users and five
agents on the same server. This holds even when one physical person owns all
five laptops. Each installation persists its own user and agent IDs and uses
them again on reconnect; local OS logins are display-name defaults only.

| Client installation | Camp user | Registered agent |
|---|---|---|
| Laptop 1 | user-1 | agent-1 |
| Laptop 2 | user-2 | agent-2 |
| Laptop 3 | user-3 | agent-3 |
| Laptop 4 | user-4 | agent-4 |
| Laptop 5 | user-5 | agent-5 |

The server's personal registration policy grants those agents access to the same
starter workspace. Two agents registered from the same saved user profile
instead share that user and retain separate agent IDs. Matching OS names,
email-like labels, or client types never merge users. Moving to an existing
identity requires its credentials; account merging is outside this milestone.

### External authentication later

Leave a narrow boundary that resolves an authenticated identity to an internal
user. Initially provide only the local source. A future OIDC adapter validates
the provider's authentication result, resolves its identity binding, and passes
the resulting internal user to the same registration operation.

For OIDC, plan a binding keyed by the configured provider and its verified
issuer/subject pair. Names and email addresses can change and are not account
linking keys. OIDC defines the subject within its issuer; see the [identity
claims
specification](https://openid.net/specs/openid-connect-core-1_0.html#IDToken).
Binding another source to an existing local user requires an explicit authorized
account-linking operation, not an automatic email match.

This milestone defines the boundary and ownership model. It does not add OIDC
libraries, provider configuration screens, login redirects, token exchange,
group synchronization, or corporate user provisioning. Those can be added later
without changing how Camp registers agents or identifies their work.

### SQLite for the current scope

Keep users, agents, credentials, their ownership relationships, and existing
sessions/receipts in the control SQLite database. Future identity bindings can
live there too. Use ordinary relational constraints and transactions for user,
agent, and credential creation; there is no need for a separate identity service
or another database in this milestone.

SQLite and one active Camp server are an accepted operating model. Horizontal
scaling and a shared control database are not prerequisites for company use or
external login. Keep the current workspace document storage contract (directory
or S3) and its backup boundary: identity/control records do not enter workspace
backups. This storage decision concerns the control plane, not a migration of
documents into SQLite.

## Local installation

Proposed first-run command:

```bash
npx --yes agent-camp setup
```

Equivalent persistent CLI installation:

```bash
npm install -g agent-camp
camp setup
```

Both invoke the same setup workflow. The npm package must support launching
without relying exclusively on npm lifecycle scripts. A native installer can
publish the same launcher for machines without Node.js. Its download location
will be documented when release artifacts exist.

The first-run flow asks:

1. Create a local Camp, or connect to an existing server? Local is the default.
2. Which detected agent clients should be connected? Show the actual client
   names and let the user select them together.
3. Start Camp automatically at login? Explain the consequence of either choice.

The workspace and data location use defaults; advanced settings remain optional.
No manual token copy, Python installation, port choice, or shell profile edit is
required on the normal path.

Setup performs these stages:

1. Check the supported OS/architecture, installation destination, and permissions.
2. Acquire a versioned Go CLI and a matching server runtime pack. The pack
   includes Python, the adapter, and the locked Monty dependencies.
3. Initialize an instance in the user's application-data directory, independently
   of the shell's current directory. Create its local user and starter workspace
   once, reusing their stable IDs on subsequent setup runs.
4. Keep the administrator credential in private local operator storage. Create
   agents owned by that local user, issue their scoped credentials, and automatically
   grant each client writer access to the starter workspace. No token or role
   prompts appear in the personal setup flow.
5. Start one local server bound to loopback. Record its address and profile.
   Use a per-instance lock to avoid competing servers or setup processes.
6. Merge client settings, install the Camp workflow skill, and add a small
   startup instruction through that client's supported mechanism.
7. Exercise discovery, session creation, a write/read/delete cycle under an
   installation-check path, and credential access. Explain that cleanup leaves
   normal version history; do not claim the check made no writes.
8. Report server readiness and configuration status for each client. If a client
   requires reload, say so. Its tools are not "loaded" until the client has
   actually connected.

A proposed completion screen:

```text
Camp is running on this computer.
Workspace: workspace
Codex: configured — open a new session
Claude Code: configured — reconnect MCP
Workspace check: write and read passed

Your agents will use Camp for shared context, decisions, and handoffs.
Check this installation later: camp doctor
```

Rerunning setup reuses the instance, workspace, identities, and user choices. It
updates Camp-owned settings without duplicating them or overwriting other MCP
servers. `camp setup agents` revisits client integration; `camp doctor` checks
the installation. `camp status`, `camp up`, and `camp down` expose the service
state without requiring knowledge of its process manager.

If login startup is declined, start the service for the current session and
explain when it will stop. A lightweight client bridge can restart the local
service on demand. With login startup enabled, use the host's user-service
mechanism; do not require a system administrator for the laptop path.

## Docker and VM deployment

Ship a published, versioned Docker image containing Camp and its complete
Python/Monty runtime. The operator can pull it and run it without cloning this
repository or building dependencies. Provide short `docker run` instructions and
Compose examples for local-directory storage and an existing S3 endpoint.

The examples configure only the Camp service, its port, configuration mount, and
persistent data volume. They do not install a proxy, acquire certificates,
create a bucket, or provision a machine. The listener address is a Camp setting;
port publishing and external routing belong to the operator's deployment.

The proposed server-only setup configures Camp without looking for desktop agent
clients. In the supplied Compose example, the flow is:

```bash
docker compose pull
docker compose run --rm camp setup server
docker compose up -d
```

The setup container and the running service use the same mounted configuration
and data. Setup asks about storage only when needed, initializes the workspace
and internal credentials, and saves the result. Rerunning it reuses existing
state. Operators can also supply a configuration file directly for unattended
startup. The regular service runs Camp in the foreground; Docker or the chosen
process supervisor manages its lifecycle.

For a binary installation on a VM, install the CLI and runtime, run `camp setup
server`, then `camp serve`. Operators can place that process under their
existing service manager. The same configuration and runtime behavior apply to
both distribution formats.

Document the component's operational contract: listening port, configuration
path, required persistent paths, process exit behavior, and health checks.
Workspace objects and control SQLite need durable storage; the search index is
rebuildable. Replacing the container must preserve that state and the receipt
returned by a retried operation.

Provide component update and backup instructions. Workspace backup and
control-plane backup are separate, coordinated operator procedures. Workspace
exports continue to exclude credentials, sessions, and receipts.

## Running in other environments

An organization can run the same image in its own Kubernetes deployment or
another container platform. Camp supplies the image and operating requirements;
the organization owns the manifests and infrastructure configuration.

The current server uses local SQLite for tokens, sessions, and execution state.
Document one active server process per instance, even when workspace objects are
in S3. An S3 backend alone does not make independent server replicas share their
control state. SQLite and this single-server deployment are sufficient for the
current scope; no shared-database or scaling work is planned.

The current `/health` endpoint reports that HTTP is alive. A component readiness
check should also verify that storage and the runner are usable. Expose the
result for the operator's process manager; configuring platform probes belongs
to that deployment.

## Agent registration and connection

For a personal VM, the basic operation is registering an agent with an existing
Camp server. The client needs the CLI and integrations, not a local Python/Monty
runtime or workspace server.

Proposed explicit command:

```bash
camp register --server https://camp.example.com --name codex-laptop
```

The regular setup flow performs the same operation for the selected clients:

```bash
npx --yes agent-camp setup --server https://camp.example.com
```

Registration takes an established internal user context, the agent's display
name, and optional client information. It creates an agent owned by that user
and returns its stable ID, token, and default workspace. Client setup saves
those values privately, installs the MCP connection and workflow skill, and
verifies access. The local user record and token are prepared automatically,
without a signup form or manual credential copying. Registering an agent and
wiring an agent client are separate internal steps behind the same setup
experience.

A minimal agent record contains its ID, owning user ID, display name, client
type when supplied, registration time, and active/revoked status. Track last
authenticated activity if useful, but do not present it as proof that a process
is currently online. Names and client types are self-reported labels; the token
authenticates the server-issued identity. Store identity and credential records
in the control plane. Use the stable identity as the author of workspace changes
and comments, so work can be attributed to the same agent across sessions.

Each selected client gets its own identity and token. Normal setup with an
existing valid token reuses that identity. A display name alone never allows a
caller to claim an existing identity or retrieve its token. Reusing a name
without credentials creates a distinct identity, which remains distinguishable
by ID. An interrupted first registration must not recover credentials by name;
setup reports the incomplete attempt and can register a new identity. Revocation
invalidates the existing credential without deleting its authored documents.

### Personal-server registration policy

Support an optional `open` self-registration mode for a personal server. The
owner enables it in server setup or configuration. Any caller able to reach that
registration endpoint can then create a local user and its first agent and
receive the configured starter-workspace access. Further agents attach to that
user only through its established registration context. This is an explicit
server policy, not a claim that knowing an agent name establishes trust.

The server decides the granted workspace, role, and scopes. A registration
request cannot choose administrator privileges, arbitrary spaces, or another
agent's ID. Document operations still require the issued token and pass the
existing access checks. Local setup can provision agents directly under the
owner's authority without enabling remote self-registration.

The registration mode is a server configuration choice: `disabled` by default,
or `open` when explicitly enabled by the owner during server setup. In `open`
mode, all reachable callers can create their own local user and join the
configured starter workspace; the operator chooses where the endpoint is
reachable. A restricted server can require owner provisioning through the same
client flow. Future external login supplies the same user context. These new
registration features are implemented in a later change; existing document
authorization continues to fail closed.

### Connection details

Client configuration contains the address and identity reference; credentials
stay in private local storage. The CLI reads them automatically, including when
the client starts outside a terminal. Existing scoped credentials can also be
supplied through a hidden prompt or standard input. Administrator credentials
are not distributed to registered agents.

The server is already running at an address supplied by the user. HTTPS uses the
operator's existing TLS endpoint. Camp does not provision the remote machine,
network, or proxy.

Named profiles distinguish local and company servers. Workspace bindings are
explicit; matching project names must not silently redirect a task to another
server. Rebinding a project changes the connection, not the location of existing
documents. Moving workspace data is a separate operation.

## Client integration

Each supported client gets the same Camp contract through an adapter:

| Layer | Purpose |
|---|---|
| MCP connection | Make `map`, `start`, and `run` available. |
| Startup instruction | Tell the agent that this project uses Camp and when to consult it. |
| Workflow skill | Teach progressive reads, document updates, discussions, conflicts, and retries. |
| Server instructions and SDK help | Supply the current contract and executable examples. |

Where practical, point the client at a proposed `camp bridge` stdio adapter. It
reads its credential from private local profile storage and connects to the
profile's HTTP server. Local mode can ensure the local service is running;
remote mode never starts a local server or silently falls back to one. The
bridge also avoids requiring GUI-launched clients to inherit shell tokens. The
existing `camp mcp` command runs the engine itself; it is not this bridge.

Support native HTTP MCP as an alternative when the client has suitable
credential storage. All transports preserve the same identity and workspace
permissions. Store an absolute launcher path so the client can connect even if
its environment has a different PATH from the terminal.

Claude Code can load a short reminder through a session-start hook; its hook
output supports adding context. See the official [hook
reference](https://code.claude.com/docs/en/hooks#add-context-for-claude). For
Codex and clients without an equivalent verified hook, use their supported
persistent instructions and skill locations. Do not assume one hook format works
in every client. Project instruction updates are bounded, clearly owned sections
that preserve existing content; global setup must not alter unrelated projects.

Skill metadata should describe ordinary user goals rather than require the word
"Camp". Codex can choose skills implicitly when the task matches the
description; this is model selection behavior, not a guarantee that every task
triggers it. See [OpenAI's skills
guidance](https://learn.chatgpt.com/docs/build-skills#how-chatgpt-and-codex-use-skills).
The same metadata-first loading principle applies to [Claude Code
skills](https://code.claude.com/docs/en/skills).

Adapters have a tested support matrix. Detection of an installed client is not
proof of a supported integration. Report configured, connected, and behaviorally
verified states separately. If the host requires project trust or MCP approval,
use that host's normal flow and report the remaining action precisely.

## Agent behavior

At the start of meaningful project work, the agent should discover accessible
spaces, locate the relevant project, and read its short overview, current rules,
and relevant decisions or handoffs. It should not ask the user for an internal
space ID that discovery can provide. Startup context is bounded; it does not
load the whole workspace or run a wide search for every message.

The workflow skill should trigger for continuing project work, planning a shared
task, reviewing a document, recording a durable decision, or handing work to
another agent. Routine factual answers and unrelated conversation should not
create workspace activity.

| Situation | Expected behavior |
|---|---|
| Continue a feature | Read relevant context and unfinished work before starting. |
| Choose an approach | Read existing decisions; save the resulting rationale when useful. |
| Review a document | Read the document and comments; add findings as discussion messages. |
| Finish a meaningful task | Update the existing task/handoff with outcome, checks, and remaining work. |
| Another agent needs input | Comment on the relevant document with the intended recipient. |
| Conflict | Refresh and reconsider the overlapping change; use a new operation ID for new code. |
| Lost response | Retry identical code with the original operation ID. |
| Unrelated small question | Answer without a Camp round trip. |

Save outcomes, rationale, constraints, links to artifacts, useful procedures,
and handoffs. Do not copy full chats, private reasoning, credentials, generated
build output, or entire repositories into the workspace. Update existing
documents when they represent the same work; do not create a new page for every
turn.

Setup grants an agreed scope for project documentation. Automatic workspace
updates stay within that scope and the user's current instructions. Reading a
Camp document does not authorize an external action or override host policies.

An empty workspace has a brief welcome/orientation document and an overview
explaining where future project documents can live. It needs no artificial
history or sample decisions. Creating additional task/project structure follows
the user's actual work. New templates are ordinary documents, not a required
storage schema.

Camp is asynchronous. A discussion comment is not proof that another agent was
notified or read it. The current SDK does not expose a general unread-feed or
"changed since" method; do not promise one through a skill. Read discussion
catalogs and relevant histories using existing methods. A real changes cursor or
inbox is a separate product increment if volume requires it.

## Recovery and maintenance

| Condition | Expected outcome |
|---|---|
| Setup is run twice | Reuse the local user, agents, state, grants, client entries, and choices. |
| Download fails | Leave the previous working installation intact; show a retry command. |
| Other MCP servers are configured | Preserve them and merge only Camp-owned entries. |
| Local service is stopped | Start it on demand or give one actionable start command. |
| Remote server is unavailable | Report the failure once; continue independent work without claiming synchronization. |
| Credential expires or is revoked | Report that registration needs attention; do not fall back to administrator access. |
| Client has not reloaded | Report configured, pending reload; do not report an active connection. |
| Update changes server/runner versions | Verify the matched runtime before replacing the working installation. |
| User disconnects a client | Remove only its Camp integration and revoke its credential if requested. |
| User uninstalls Camp | Keep workspace data unless deletion is explicitly requested. |

`camp doctor` reports binary/runtime versions, profile, server reachability,
workspace access, client configuration, and a real runner check. Its normal
checks are read-only or sandbox-only. An explicit write check creates an
identified test document with normal revision history and explains the cleanup.
Diagnostics redact secrets. A machine-readable mode supports setup by agents
without menus or interpretation of terminal output.

## Gaps in the current repository

| Area | Current behavior | Required change |
|---|---|---|
| npm launcher | Downloads only a Go archive; Node `x64` is used where release names use `amd64`; launch failures can exit zero. | Correct platform mapping, failures, extraction, integrity checks, and first-use acquisition. |
| Configuration | Storage and server settings are supplied through environment variables. | Persistent configuration managed and validated by setup; environment overrides for automation. |
| Runtime | Python/Monty paths must be prepared separately and be absolute. | Managed versioned server runtime, independent of the repository checkout. |
| Setup | Separate `init`, `token`, `serve`, and `connect` commands. | Idempotent setup that prepares the full working path. |
| Client settings | Claude Code/Cursor files are overwritten; Codex credentials rely on environment setup. | Merge adapters, private credential resolution, supported skill locations, and startup guidance. |
| Agent instructions | Existing skill largely explains how to call Camp once it is selected. | Clear triggers for ordinary project work, bounded startup guidance, and behavior evaluations. |
| User/agent ownership | Principals identify actors, without a dedicated user-to-agent ownership model. | Camp-local users owning agents, stored in SQLite; a narrow authentication-source boundary for later providers. |
| Remote onboarding | Administrator issues tokens manually. | Register agents for an established user, save tokens automatically, and configure clients under an explicit admission policy. |
| Containers | Compose builds from source and needs environment configuration. | Published versioned images, simple Compose examples, and server-only Camp setup. |
| Operations | Basic HTTP health endpoint and local SQLite control state. | Document persistence and single-process requirements; expose component readiness. |
| Distribution | Go archives for Linux/macOS; no full server bundles. | Test every supported CLI/runtime pair before advertising platform support. |

Linux and macOS on x86-64 and ARM64 are candidate first-release targets. Verify
Monty and the packaged Python on each platform; Go cross-compilation alone is
not acceptance. Native Windows requires separate process-management and runner
work; WSL2 can follow the Linux path. Do not advertise native Windows until that
full path passes.

## Acceptance scenarios

1. Fresh supported laptop with no Python/uv: one setup command leads to a working
   workspace, connected supported clients after any required reload, and a real
   write/read check. No source checkout is required.
2. Same setup repeated: no duplicate identities, workspaces, or MCP entries; other
   servers and user instructions remain intact.
3. Two selected clients: distinct credentials, access to the same intended space,
   and a successful document handoff through a comment.
4. New agent session with a plain project task and no mention of Camp: relevant
   context is consulted; a useful outcome is saved within authorized scope.
5. Unrelated question: no unnecessary workspace calls or writes.
6. Restart under the operator's process manager: workspace/control state survive
   and clients reconnect.
7. Container replacement with the same persistent data: a retried operation
   returns its original receipt.
8. Revocation: the disconnected client's credential fails; another authorized
   agent still works. Stale context never bypasses a fresh access check.
9. Interrupted installer or update: rerun resumes safely; the existing data and
   previous executable remain usable.
10. Remote profile without local Python: registration and agent access work without
    downloading a server runtime to the developer's machine.
11. Personal setup: no token, role, or account-registration prompts; all selected
    clients can write to the starter workspace, while unprovisioned clients are
    denied. Adding another client preserves existing connections.
12. S3 setup: settings survive a restart without shell exports, existing host
    credentials are reused when available, and a failed validation preserves the
    previously working configuration.
13. Supplied Compose example: pull the image, configure Camp, and start it with
    persistent state. No source build, proxy setup, or infrastructure provisioning
    is required from Camp.

14. Registered agent reconnects: saved credentials preserve its ID and authorship;
    another client using the same display name cannot assume that identity.
15. Personal-server registration, if explicitly enabled: a new agent receives
    only the server-configured workspace grant. Requests for administrator access
    or unrelated spaces are rejected. Disabled registration creates no identity.

16. One local user registers multiple agents: all resolve to the same owning user,
    while each agent retains its own ID, credential, and revision attribution.
17. Repeated setup and OS display-name changes preserve the internal user's ID.
    Supplying another person's display name or user ID does not transfer ownership.
18. Disabling a user denies that user's agents and new registrations; disabling
    one agent leaves the user's other active agents usable.
19. Registration consumes the same established-user context regardless of its
    source. Test the boundary with a fake source; no external provider, network
    login, or additional database is required for the milestone.

20. Five fresh laptop profiles connect to one VM: create five users and five
    agents with shared starter-workspace access. Reconnect all five and verify
    their IDs are unchanged. Matching display names never merge identities.

Evaluate agent behavior with pinned client/model versions, relevant prompts,
negative controls, and several fresh sessions. Measure triggering, useful reads,
authorized writes, and unnecessary calls separately. A hook firing is not proof
that the model followed the workflow. Never label that behavior guaranteed.

## Suggested delivery order

1. Define and implement the local user/agent ownership model in SQLite, then
   complete CLI/runtime packaging, local setup, safe configuration merge,
   diagnostics, and end-to-end installation tests.
2. Deliver the agent workflow and client startup integrations with behavioral
   evaluations. The first milestone is a fresh laptop whose agents can perform a
   useful shared task without being taught Camp in the conversation.
3. Publish versioned container artifacts with server-only setup and small
   Compose examples using the same configuration contract.
4. Deliver remote profiles and agent registration for an existing server, with
   owner-configured admission policy.

External identity providers, infrastructure automation, and high availability
are outside this delivery plan. SQLite remains the control-plane database.

This document proposes behavior. It does not publish packages or images, install
services, change access invariants, or introduce a new runtime dependency.
