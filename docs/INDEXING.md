# Repository indexing

[Trail-code](https://github.com/trailhq/Trail-Code), formerly Graft, builds a local
map of source files, symbols, and call relationships. Use it to find code and
check what depends on a function before changing it. It is development tooling,
separate from Camp's document search and runtime.

## Build the index

Install Node.js 22.3+ (or a newer LTS release) and npm, then run from the repository
root:

```bash
just code-index
just code-map
just code-index-check
```

Without `just`, the equivalent commands are:

```bash
DO_NOT_TRACK=1 npx --yes @trailhq/trail-code@0.28.1 build
DO_NOT_TRACK=1 npx --yes @trailhq/trail-code@0.28.1 map
DO_NOT_TRACK=1 npx --yes @trailhq/trail-code@0.28.1 build --check
```

The first invocation downloads the pinned package. Structural indexing needs no
model API key. Go, Python, and JavaScript sources are indexed, including tests;
Git's ignore rules exclude virtual environments, data, and generated files.
Markdown documents and configuration files should still be read directly.

The generated `graft/` directory is a local cache excluded from Git and Docker's
build context. Each checkout builds its own index. `build --check` exits with a
failure when the index is missing or stale; it does not rebuild it. Queries
normally refresh changed source files automatically. Run a full build after
large changes or a branch switch if needed.

## Agent connections

The repository includes the same pinned Trail-code MCP server in:

| Client | Configuration |
|---|---|
| Claude Code | [`.mcp.json`](../.mcp.json) |
| Cursor | [`.cursor/mcp.json`](../.cursor/mcp.json) |
| Codex | [`.codex/config.toml`](../.codex/config.toml) |

Open the repository as the client's project and enable its project MCP server.
Codex must trust the project to load project configuration. Restart or reconnect
the client after adding settings; tools are available once the client loads the
server. These settings do not change any user-level configuration.

The MCP server exposes `trail_repo_map`, `trail_find_code`, `trail_file_api`,
`trail_trace_calls`, `trail_find_all`, and `trail_check_freshness`.
Start with the map, find the relevant symbols, and trace callers before edits.
Use `rg` for exact text, configuration, and files outside the source index.

You can also query from a terminal:

```bash
DO_NOT_TRACK=1 npx --yes @trailhq/trail-code@0.28.1 ask "How does a run commit its changes?"
DO_NOT_TRACK=1 npx --yes @trailhq/trail-code@0.28.1 callers CheckSources
```

Project commands and MCP settings disable anonymous usage reporting with
`DO_NOT_TRACK=1`. The setup uses structural indexing only; it does not configure
LLM summaries, automatic learning capture, or agent hooks. Keep durable project
rules in [AGENTS.md](../AGENTS.md).

When updating Trail-code, change the version together in the three MCP configs,
`justfile`, and this guide, then rebuild and query the index.

## Camp connection settings

`camp connect` merges its named MCP entry while preserving Trail and other
servers. Use `camp connect <client> --project --print` to preview affected
file before installing project startup instructions; the preview contains a
`files` array with paths and Camp-owned additions. Existing unrelated contents
are omitted to keep private settings out of the preview. Use a separate checkout or directory when testing installation.
