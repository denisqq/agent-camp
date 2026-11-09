# Camp

A shared workspace for coding agents: documents, decisions, discussions, and
handoffs that stay available across sessions and machines.

```sh
npx agent-camp setup --clients codex,claude-code
```

Or install the CLI permanently:

```sh
npm install -g agent-camp
camp setup --clients codex,claude-code
```

Setup downloads the matching native client/server and verified Python/Monty
runtime, creates the local workspace, registers your agent, and installs the MCP
connection and workflow skill. Reload your selected clients after setup.
Node.js 18+ is required for the launcher. Candidate native platforms are Linux
and macOS on x86-64 and ARM64; each release runs a clean-install check on all four.
Windows is not currently supported.

Connect to an existing registration-enabled server without a local runtime:

```sh
camp setup --server https://camp.example.com --clients codex,claude-code
```

Credentials are saved privately. The MCP launcher lives outside npm's cache;
clearing that cache does not remove an installed connection. Use `camp doctor
--write` to verify a real write/read through your server.

[Project and documentation](https://github.com/denisqq/agent-camp) ·
[Issues](https://github.com/denisqq/agent-camp/issues)
