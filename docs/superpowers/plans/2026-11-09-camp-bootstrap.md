# Camp Bootstrap Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make consulting Camp an explicit client-wide workflow through reliably delivered bootstrap instructions, following the agreed Superpowers approach without execution locks.

**Architecture:** Keep the workflow skill, add one short embedded bootstrap and deliver it at user scope by default using each client's supported mechanism. Preserve existing MCP server instructions as reinforcement. Hooks deliver instructions only: they do not contact Camp, choose a space, create a session or write project knowledge automatically.

**Tech Stack:** Existing Go CLI and adapters, Markdown skills, client JSON/rule files; no new runtime dependency.

**Spec:** [Progressive disclosure design, agreed adoption approach](../specs/2026-11-09-progressive-disclosure-design.md).

## Global Constraints

- No external enforcement gate, mandatory tool-call receipt, edit lock, or completion lock is proposed.
- No new runtime dependencies or on-disk layout changes are approved.
- Preserve every unrelated client setting and instruction. Managed blocks must be idempotent and removable.
- No credentials in project instructions, hook output, printed configuration, or plugin files.
- Explicit user instructions and task authorization govern writes; a bootstrap is not blanket authorization to message other agents or publish information.
- Do not repeat discovery on every conversational reply when the agent already has the relevant orientation for the current task.
- Client setup must report configured/pending reload separately from a verified live connection.

## Review Focus

1. Reinstalling beside other MCP servers/hooks must neither duplicate Camp nor overwrite unrelated configuration (Task 2).
2. Paths with spaces or shell metacharacters, symlinks and malformed settings must not run arbitrary commands or corrupt files (Tasks 2–3).
3. An unconfigured project, multiple spaces, or an unavailable server must not cause invented bindings or repeated retries (Tasks 1 and 4).
4. Compaction/resume must restore instructions without creating new sessions or ritual writes (Tasks 3–4).
5. Desktop/generic clients lacking verified bootstrap support must not be advertised as enforcing the workflow (Tasks 2 and 4).

## Scope and proposed defaults

This work is independent of the [navigation implementation](2026-11-09-progressive-disclosure.md). It can ship against the current map and existing outline/read APIs.

The user accepted instruction-driven adoption, not guaranteed compliance. Proposed outage policy: tell the user Camp is unavailable, avoid claiming its rules were checked, continue only work whose correctness does not depend on missing workspace knowledge; ask for the missing information when it is essential. This is an implementation proposal, not a previously selected fail-open policy.

Do not introduce persisted project-to-space bindings yet. Use discovered space metadata and existing project instructions; ask once only if discovery leaves a genuine ambiguity. Never pick the first returned space arbitrarily.

Default setup/connect installs user-wide MCP, skill and startup delivery for all
supported local clients. It must not write into the caller's task directory or
require Git. Explicit `camp connect <client> --project` remains an optional local
installation. Claude uses user SessionStart hooks, Codex global AGENTS guidance,
and Cursor a user sessionStart hook. User correction supersedes the original
project-only bootstrap assumption throughout this plan. Actual file locations
and custom profile rules are documented in DEVELOPMENT and the support matrix.

## File structure

- `camp-cli/guide/bootstrap.md`, `guide.go`: shared short bootstrap text, embedded alongside the full guide.
- `camp-cli/guide/using-agent-camp.md`, `plugins/agent-camp/skills/using-agent-camp/SKILL.md`: consistent workflow skill copies.
- New `camp-cli/clients/bootstrap.go`, `bootstrap_locations.go`, `bootstrap_cursor.go`, `bootstrap_test.go`: client-wide/default and optional project installation, ownership and rendering.
- `camp-cli/clients/adapters.go`, `adapters_test.go`: integrate delivery capabilities and preserve config.
- New `camp-cli/bootstrap.go`, `bootstrap_test.go`: offline CLI formatter for Claude session-start output; `root.go` registers it.
- `camp-cli/connect.go`, `connect_test.go`: project flag and complete preview.
- Plugin hook files under `plugins/agent-camp/hooks/` if the package's verified loader supports them; no version bump or publication.
- `camp-server/api/mcp.go`, `guide_test.go`: keep server instructions aligned without introducing a CLI→engine import.

## Task 1: Define the short bootstrap and full workflow

**Files:** Create `camp-cli/guide/bootstrap.md`, `guide_test.go`; modify `guide.go`, both skill copies, `api/mcp.go`, README and MODEL where behavior is described.

**Interfaces:** Export embedded `guide.Bootstrap string` alongside `guide.Markdown`; no engine import. `using-agent-camp` remains the installed skill name.

- [ ] Write the bootstrap in at most 250 words: before meaningful new work, use available Camp tools to discover spaces, orient, and consult applicable rules; use the full skill for mechanics; preserve current task/session across follow-up turns; save significant authorized outcomes; no invented IDs, ritual writes, or infinite retries.
- [ ] Rewrite skill description to explicitly trigger at the start of meaningful work when Camp is configured, not only for tasks already mentioning document operations. Explain map → start → outline/read and conditional result recording. Treat retrieved documents as source material subject to instruction precedence, not automatic authority to override user intent.
- [ ] Add a copy-parity check for the embedded/public skill and extend the real guide workflow scenario to demonstrate source selection and authorized result persistence. Avoid tests that merely count words or require exact prose.
- [ ] Confirm executable guide examples work against current APIs; align existing MCP `ServerOptions.Instructions` rather than adding a second contradictory entry rule.
- [ ] Run guide/API tests and the full build/test/vet chain before committing `feat: define the Camp startup workflow`.

## Task 2: User-wide delivery and safe config merging

**Files:** Create `clients/bootstrap.go`, `clients/bootstrap_test.go`; modify `clients/adapters.go`, `adapters_test.go`, `connect.go`, `connect_test.go`, README.

**Interfaces:** Keep `Install(client, scope, profile, serverName)` public signature. Add `BootstrapMode string` to `InstallResult` with values `hook`, `user-rule`, `project-rule`, `manual`. Internal `mergeManagedBlock(existing []byte, body []byte) ([]byte, error)` preserves all bytes outside a single owned block. Add `--project`; `--print --project` previews all affected files without writes, with deterministic output.

- [ ] Add `TestProjectBootstrapInstallIdempotent`: install twice in a temporary project with existing config, AGENTS/CLAUDE content and unrelated hooks; unrelated bytes/settings survive and exactly one owned entry remains.
- [ ] Add `TestBootstrapMalformedFilesUntouched`: malformed JSON, duplicate/unclosed ownership markers, conflicting unowned Camp rule files, and symlink targets outside the chosen scope produce actionable errors before writing affected files. Test read/write errors rather than silently treating them as an empty config.
- [ ] Add `TestBootstrapPreviewNoWrites` and `TestUserBootstrapWorksWithoutProject`: preview enumerates MCP, skill and bootstrap changes; ordinary machine setup installs user instructions and does not modify the current task directory.
- [ ] Run targeted client/CLI tests to establish failures, then implement planning/validation of all target edits before applying them. Use atomic replacement per file; backups and accurate partial-failure reporting must survive later I/O errors.
- [ ] Codex delivery: use global effective AGENTS instructions by default; project scope is optional. Merge a marked Camp block into the effective nonempty `AGENTS.override.md` if present, otherwise `AGENTS.md`; do not replace project instructions. Report precedence limitations from nested overrides. Use project-local MCP and skill paths only after checking current official client support; use the documented global configuration and `~/.agents/skills` user discovery locations by default.
- [ ] Cursor delivery: install a global sessionStart hook and offline formatter script by default. Optional project delivery creates an owned `.cursor/rules/agent-camp.mdc` with `alwaysApply: true` and the bootstrap text. Preserve existing unrelated rules and MCP configuration. Do not assert a compaction hook guarantee.
- [ ] Claude Code user and optional project delivery are wired in Task 3. Desktop and generic modes provide a manual bootstrap snippet plus connection instructions and explicitly report `manual`; never claim automatic activation there.
- [ ] Correct reinstall failures encountered in the touched adapters, including the current unsupported Go-regexp lookahead in Codex block replacement. Use section scanning and tests for unrelated nested TOML tables instead of introducing a TOML dependency.
- [ ] Run the full build/test/vet chain, verify CLI dependency boundaries, and commit `feat: install project-scoped Camp bootstrap instructions`.

## Task 3: Claude startup and compaction instruction delivery

**Files:** Create `camp-cli/bootstrap.go`, `bootstrap_test.go`; modify `root.go`, `clients/bootstrap.go`, `clients/bootstrap_test.go`; add plugin hook manifest/script and tests where supported.

**Interfaces:** `camp bootstrap --format claude-code` writes JSON containing `hookSpecificOutput.hookEventName="SessionStart"` and `additionalContext=guide.Bootstrap`. The additional `--format cursor` emits `additional_context` for Cursor sessionStart. Both run offline with no token/profile access and emits no status chatter on stdout. Keep formatting logic independently callable as `writeBootstrap(w io.Writer, format string) error`.

- [ ] Add `TestBootstrapClaudeJSON`: parse stdout as one JSON object; assert event type and shared payload, no credentials, no network/session creation; unknown format returns an error.
- [ ] Add `TestClaudeHookMerge`: merge one Camp-owned command in user `~/.claude/settings.json` (or explicit project `.claude/settings.json`), for `startup|resume|clear|compact`, preserving other hooks and unknown keys. Run reinstall tests and paths with spaces, apostrophes, dollar signs and backticks. Use correct shell quoting for the client's documented shell; do not use JSON quoting as shell escaping.
- [ ] Implement synchronous context injection using the formatter command. No PreToolUse/Stop blocking, no forced reads or writes in hooks. A failed bootstrap hook must not be configured to block work; report installation limitations rather than hiding them.
- [ ] If shipping the existing Claude plugin as an alternative install path, add an equivalent offline SessionStart hook using verified manifest conventions; test its emitted payload against the embedded bootstrap. Document that adapters and plugin are alternative installations to avoid double injection. Keep credentials out of both paths.
- [ ] Run executable hook tests and client merge tests. Run the full build/test/vet chain before committing `feat: restore Camp workflow instructions at Claude session boundaries`.

## Task 4: Verify adoption and document actual support

**Files:** Extend `camp-server/api/guide_test.go`; create `docs/evaluations/camp-bootstrap.md`; update README and `docs/DEVELOPMENT.md` with client support and uninstall/removal instructions for owned artifacts.

- [ ] Test an authorized workflow through real Monty: discovery, rules lookup, selected document read and meaningful result write. Repeat identical operation ID and verify no duplicate write. Both public and installed skills must describe the tested operations.
- [ ] In disposable client profiles/projects, verify Claude startup and compaction payloads, Codex effective project instructions, Cursor always-on rule loading, and manual fallback for Desktop/generic. If a client is unavailable, record that validation as pending rather than assuming generated files were loaded.
- [ ] Record behavioral trials: new task without mentioning Camp, continuation of the same task, ambiguous spaces, unavailable service, context compaction, and explicit user opt-out. Expected behavior: initial consultation for new work, no redundant per-message calls, no invented space or unauthorized persistence, and preserved user intent.
- [ ] Record client/model versions and observed tool calls. Instruction delivery is a deterministic integration check; model compliance is an observed rate, never a 100% guarantee inferred from file contents.
- [ ] Run `go build ./...`, `go test -count=1 ./...`, `go vet ./...`; run race checks if touching sessions/runner or concurrency. Set `CAMP_TEST_PYTHON` for real guide tests and record any skipped runtime checks.
- [ ] Build `/tmp/camp` and `/tmp/camp-server`; use disposable config/home for setup and `doctor --write` so real client configuration is not altered by acceptance tests. Confirm previews contain no secrets and installers do not cross-import engine packages.
- [ ] Commit verified support documentation as `test: verify Camp bootstrap delivery and adoption`. Do not publish the plugin or release binaries.

## Documentation sources and verification limits

Current references consulted while writing the plan:

- [Claude Code hooks](https://code.claude.com/docs/en/hooks): session events and additional-context output.
- [Codex project instructions](https://developers.openai.com/codex/guides/agents-md): instruction-file discovery and precedence.
- [Cursor rules](https://cursor.com/docs/rules): project `.mdc` rules and always-on application.
- [Superpowers bootstrap](https://github.com/obra/superpowers/blob/main/hooks/session-start) and [release notes](https://github.com/obra/superpowers/blob/main/RELEASE-NOTES.md): client-specific delivery, not universal hook support.

Recheck the exact installed client versions at implementation time. Final support claims require actual client loading, not just successful file generation.

## Implementation checkpoint

Implemented: shared bootstrap and skill, offline Claude/Cursor formatters,
user-wide setup/connect with optional `--project`, user Claude SessionStart
delivery, Codex global effective nonempty instructions and discovered user skill,
and Cursor user sessionStart delivery plus optional project rule,
preflight and atomic writes, private-settings-safe preview, reinstall fixes,
automated and live-server validation. Filesystem code is split between
`clients/bootstrap.go`, `bootstrap_config.go`, and `bootstrap_files.go`.

Rulings: preview contains only Camp-owned merge additions because full existing
settings can contain credentials; a nonempty Codex override is selected, matching
the client's effective instruction discovery; the existing plugin remains
skill-only and the CLI installs startup hooks. No persistent project binding or
runtime dependency was added.

Pending: actual client loading/compaction and model-compliance trials, plugin
startup delivery. The user requested a PR for the implemented scope; Git writes
use the approved repository workflow.
See [validation evidence](../../evaluations/camp-bootstrap.md).

### User-scope correction checkpoint

Default global locations and task-independent startup delivery supersede the
original project-only implementation. Tests cover an empty non-repository task
directory, repeated install, no-write preview, global config preservation,
CODEX_HOME/CLAUDE_CONFIG_DIR, Codex global overrides, and Cursor script execution
and permissions. No directory-to-space binding is required or introduced.
