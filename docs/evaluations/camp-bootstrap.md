# Camp startup workflow validation

The integration delivers an instruction to consult Camp before meaningful new
work, including tasks without a project directory. This is the agreed Superpowers-style workflow, not an execution
lock or a guarantee of model compliance.

## Delivery support

| Client/install | Mechanism | Scope and limitations |
|---|---|---|
| Claude Code, default setup/connect | User SessionStart hook in `~/.claude/settings.json`, MCP in `~/.claude.json` | Startup, resume, clear, compact; works independently of the current directory. Absolute `CLAUDE_CONFIG_DIR` relocates MCP, settings and skill. |
| Codex, default setup/connect | Effective nonempty user AGENTS.override.md or AGENTS.md in `~/.codex`; MCP in `~/.codex/config.toml`; skill in `~/.agents/skills` | Global guidance; project overrides retain their precedence. Absolute `CODEX_HOME` relocates config and instructions, not user skills. |
| Cursor, default setup/connect | User `~/.cursor/hooks.json` sessionStart hook and an owned offline launcher script | New conversation context, not a blocking gate. User hooks are local; cloud agents do not load local home configuration. |
| Explicit `connect --project` | Local MCP/skill plus Claude hook, Codex instruction block, or Cursor always-applied rule | Optional directory-specific integration, not a prerequisite for using Camp. Does not remove user integration. |
| Claude Desktop / generic | Manual instructions | Automatic activation is not claimed. |
| Existing Claude plugin | Workflow skill | CLI connection installs startup delivery; plugin hook delivery remains pending. |

## Automated evidence

Tests exercise the offline JSON formatter, exact launcher argument delivery with
spaces/apostrophes/dollar signs/backticks, idempotent install, preservation of
unrelated settings and hooks, no-write previews of Camp-owned additions, Codex table updates,
malformed-file rejection before writes, override precedence and symlink safety.
Additional tests pin default user scope, no writes to an unrelated task directory,
custom client profiles, global hook preservation, Cursor hook execution from
another directory, and repair of executable permissions on its owned script.
The real Monty guide scenario exercises discovery, rules lookup, bounded reads,
outline, authorized outcome persistence, and idempotent retries.

Verified in this checkout:

- Full Go test suite with the repository Python/Monty runtime: PASS.
- Client Go race suite (`go test -race -count=1 ./camp-cli/...`): PASS for the user-scope correction. The earlier full-project race suite also passed before this correction.
- `go build ./...` and `go vet ./...`: PASS.
- Disposable local `camp setup` → `camp doctor --write` → `camp down`: PASS.
- Client/server production dependency boundaries: PASS.
- Fresh code review findings (preview credential disclosure, whitespace TOML
  duplicate tables, empty override shadowing) reproduced RED and fixed GREEN.

Installed clients report Claude Code 2.1.239 and Codex CLI 0.164.0. Actual client
instruction loading and model behavior have not been exercised; Cursor is
unavailable here. Claude uses the documented executable/args hook form. Older
client compatibility needs actual loading checks.

Codex edits refuse multiline TOML strings, quoted MCP table names, and
inline/array MCP configuration rather than guess. Apply the previewed Camp
stanza manually in those cases.

## Behavioral trials

Actual model compliance for new tasks, follow-up replies, ambiguous spaces,
unavailable service, compaction, and user opt-out has not been measured. No
percentage or universal client-loading claim follows from passing Go tests.

## References

- [Claude hooks](https://code.claude.com/docs/en/hooks)
- [Claude user MCP scope](https://code.claude.com/docs/en/mcp#user-scope)
- [Codex global instructions](https://developers.openai.com/codex/guides/agents-md)
- [Cursor user hooks](https://cursor.com/docs/hooks)
- [Cursor global MCP](https://cursor.com/docs/mcp#configuration-locations)
- [Codex skill locations](https://developers.openai.com/codex/skills)
- [Codex project configuration](https://developers.openai.com/codex/config-basic)
- [Cursor project rules](https://cursor.com/docs/rules)

## User-scope correction

The user clarified that Camp is a general working environment, not necessarily a
coding project. Default setup/connect now install user-wide startup delivery;
`--project` is optional. A disposable local Claude Code configuration probe
confirmed that user MCP config moves to `CLAUDE_CONFIG_DIR/.claude.json` when the
variable is set. No real user configuration was modified by the probe or tests.

The existing project-level regression tests explicitly select project scope.
The setup regression runs from an unrelated plain directory and asserts it stays
empty while global MCP and startup hooks are installed in a temporary user home.

Latest correction verification: full Go tests with real Monty, build, vet and
client race tests passed. A disposable `setup --clients claude-code,codex,cursor`
followed by `doctor --write` passed. From a plain task directory, all three CLI
previews stayed user-scoped; installed Claude Code `mcp get` and Codex `mcp list`
read the global connection, and the generated Cursor hook emitted valid startup
context. The task directory stayed empty. The server was stopped after the run.
These checks confirm config parsing and hook execution, not LLM compliance.

Fresh review found an additional Codex merge case: inline/dotted server entries
under `[mcp_servers]` could be duplicated into invalid TOML. Three regression
forms failed before the fix; unsupported representations are now refused before
any writes, with instructions to merge manually.
