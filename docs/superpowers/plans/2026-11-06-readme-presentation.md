# README Presentation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** give Camp a clear, visually distinctive product README that leads to setup.
**Architecture:** GitHub-compatible Markdown/HTML plus an original local SVG asset.
**Tech Stack:** Markdown, GitHub-supported HTML, SVG; no runtime dependencies.
**Spec:** [README presentation design](../specs/2026-11-06-readme-presentation-design.md).

## Global Constraints

- Preserve unrelated in-progress CLI/server/installer changes.
- Use English, concrete benefits, and the user-approved target setup command.
- Create original artwork; use Hermes as a layout/editorial reference.
- Keep existing build paths and distinguish clients from the server executable.
- Avoid fabricated metrics, testimonials, screenshots, and compatibility promises.

## Review Focus

- Narrow GitHub content widths and mobile legibility.
- Existing section anchors used elsewhere in the repository.
- CLI flags, binary names, and source build entry points.
- Source setup runner paths, which must be absolute.
- Asset rendering with GitHub's restricted HTML/SVG support.

## Tasks

### Task 1: create the banner and rewrite the page

**Files:** create `assets/readme-banner.svg`; modify `README.md`.
**Interfaces:** README embeds the local SVG; links point to current repository docs.

- [x] Create an original banner expressing several agents sharing one workspace.
- [x] Rewrite README around the product promise, target install, collaboration
  examples, operation, reliability, and documentation.
- [x] Keep source-build instructions available below the primary setup path.
- [x] Validate SVG/XML, repository links/anchors, fenced blocks, and GitHub Markdown rendering.
- [ ] Inspect desktop/mobile visual renders (blocked by automatic approval review usage limit).
- [x] Run the repository's required build/test/vet checks and record limitations.
- [ ] Live source setup smoke (requires network sockets unavailable in this sandbox).
- [x] Review the diff to confirm unrelated changes remain intact.

## Verification record

- `go build ./...`: passed using `/usr/local/go/bin/go`.
- `go vet ./...`: passed using the same toolchain.
- `CAMP_TEST_PYTHON=…/runner/.venv/bin/python go test -count=1 ./...`:
  unsuccessful because API and runtimepack HTTP tests cannot open sockets
  (`socket: operation not permitted`). The remaining test packages passed.
- README relative links, navigation anchors, balanced code fences, SVG XML, and
  `git diff --check`: passed. GitHub's Markdown rendering API accepted the README.
- Target npm commands remain intentional: publication and distribution work
  follows separately, as agreed with the user.
- Browser preview unavailable: automatic approval review could not complete
  because of the account usage limit; the temporary preview server was not started.
- Both source binary entry points built successfully; `camp setup --help` and
  `camp-server setup --help` confirm the documented flags and subcommands.
- No runtime, dependency, storage layout, or release changes were made.

## Independent review

Read-only review found no Important or Critical issues. CLI spellings, source
entry points, absolute runner paths, Docker flags, links, and SVG safety were
checked against the current checkout.

Deferred minor: small banner labels become decorative at narrow mobile widths.
The product promise and client names are repeated in accessible HTML/alt text;
a separate mobile artwork variant remains a possible presentation refinement.
