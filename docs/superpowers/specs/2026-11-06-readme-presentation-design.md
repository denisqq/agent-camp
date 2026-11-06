# README presentation design

**Date:** 2026-11-06.
**Scope:** README copy, presentation, and an original repository banner.
**Direction:** requested Hermes-style product presentation; the user confirmed
that the README should lead with the target release installation command while
command/distribution work continues on this branch.

## Reader and purpose

A developer using multiple agents should understand what Camp contributes in
one screen, recognize a useful collaboration scenario, and find setup without
reading the storage architecture. The opening explains shared work across tools,
machines, and sessions through concrete documents, discussions, and handoffs.

## Presentation

Use an original vector banner with forest-green ink, warm paper, and a restrained
orange accent. Connected agent terminals and a common document express the
product visually. Keep the illustration conceptual, not a fabricated screenshot.
Hermes informs the editorial hierarchy: banner, concise promise, useful feature
rows, quick install, practical examples, and links to deeper documentation.

The README stays in English. Write concrete benefits, use a small number of
purposeful badges and links, and give the page whitespace. Avoid invented
benchmarks, customer claims, model rankings, and copied Hermes text/artwork.

## Content order

1. Banner, product promise, and navigation.
2. Short collaborative-work story and benefit table.
3. Target `npx agent-camp setup` command with explicit client selection.
4. An example research → review → handoff workflow and SDK snippet.
5. Agent identity and the three-tool MCP interaction.
6. Local/server operation, native/container options, and existing storage choices.
7. Reliability, documentation, contribution links, and project status.

Keep manual administrator/token examples and internal repository indexing out of
the main onboarding path. Source builds remain available in a collapsible block,
using both current binary entry points and absolute runner paths. Preserve the
in-progress binary layout changes already present in README and other files.

## Verification

Check local links, section anchors, image paths, and SVG validity. Render the
banner and README at desktop/mobile widths and inspect visual hierarchy and
clipping. Verify documented CLI spellings against source/help. Run the checks
required by AGENTS.md; report existing failures without modifying unrelated work.
No runtime behavior, dependency, storage format, or release action is in scope.
