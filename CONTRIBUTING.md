# Contributing

Read [AGENTS.md](AGENTS.md) before changing code. It contains the repository map,
required checks, and storage, access, and execution invariants. Read
[the logical model](docs/MODEL.md) before changing workspace behavior.

## Workflow

1. Find the relevant package and its tests. The optional
   [Trail-code index](docs/INDEXING.md) helps trace callers and dependencies.
2. Keep changes focused. Add regression coverage for behavior changes and update
   the documentation that describes them.
3. Run `just check`, or the equivalent build, test, and vet commands from
   `AGENTS.md`. Run `just race` for store, sessions, or runner changes.
4. For runner changes, rebuild Docker and run the real Monty tests described in
   [development](docs/DEVELOPMENT.md). For SDK changes, also run a live
   `init` → `token` → `serve` → `start` → `run` write.
5. Review the diff for generated files, credentials, and unrelated changes.

Use commit subjects in the form `feat|fix|docs|refactor|test: summary`.
New runtime dependencies, storage format changes, invariant changes, and releases
require maintainer approval.

## Agent instructions

Keep repository instructions in `AGENTS.md`; `CLAUDE.md` and `GEMINI.md` point to
it. Keep the public workspace skill in
`plugins/agent-camp/skills/using-agent-camp/SKILL.md` consistent with the copy
installed by `camp connect` in `cmd/camp/connect.go`.

Trail-code is development tooling. Its generated `graft/` cache stays out of Git
and is rebuilt by each checkout. Avoid duplicating the same rules across agent
configuration files.
