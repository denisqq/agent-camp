# Progressive disclosure for Camp navigation

Status: the user accepted the overall navigation direction and the
Superpowers-style adoption approach below. Detailed API and implementation
choices remain open. This document records the agreed direction; implementation
has started with client-wide bootstrap delivery and descriptive navigation cards.

## Intent and confirmed requirements

An agent entering an unfamiliar space should understand what knowledge exists
and select the documents it needs with sufficient accuracy, without loading the
whole workspace into context.

The user explicitly rejected turning `map` into a conventional RAG pipeline.
The intended direction is navigable knowledge: the agent sees the available
structure, chooses where to go, inspects sources, and can recover from a wrong
turn. A ranked selection of passages must not substitute for the workspace map.

The user asked for an idea and algorithm first, then scientific references and
comparable implementations, specifically modern research and Trail-code.
The user subsequently accepted the algorithm at a high level. Implementation has started; adaptive views and continuation remain pending.

## Current implementation

`camp-server/camp/map.go` provides sessionless discovery and delegates space
navigation to `Broker.spaceMap` in `camp-server/camp/documents.go`.

- Discovery lists accessible spaces and root action hints.
- A space map accepts a path and depth (1–4, default 2).
- It scans up to 3,000 records, checks visibility, aggregates path counts and
  kinds, and reports up to ten hubs by incoming relation count.
- Nodes are sorted by path and capped at 100. The JSON response is capped at
  12 KiB by removing trailing nodes, then hubs.
- The response carries a snapshot and coverage indication.

This describes layout, but gives little help distinguishing the purpose of
documents. Uniform depth does not adapt to the shape of the tree. Output
truncation favors earlier paths and does not provide a continuation cursor.
Incoming link count indicates connectivity, not relevance, validity, or authority.

## Approaches discussed

1. **Descriptive outline:** add descriptions to sections and documents. Simple,
   inspectable, and predictable; navigation quality depends on those descriptions.
2. **Task-conditioned map:** accept an intent and rank documents or excerpts.
   Initially proposed, then withdrawn from the proposed core after the user's
   objection to RAG. Search may remain a separate tool.
3. **Structural navigation with progressive disclosure:** expose compact views,
   document outlines, explicit links, and direct source reads. The agent selects
   its own route. The user accepted this overall direction; the exact algorithm
   and API remain to be specified.

## Research and comparable implementations

### Modern work

- [Corpus2Skill — Don't Retrieve, Navigate (April 2026, v2)](https://arxiv.org/html/2604.14572v2):
  compiles documents into a hierarchy using embeddings, clustering, and LLM
  summaries. At serving time the agent browses branches and reads documents by
  ID, with backtracking. Its results favor structurally coherent topical corpora;
  navigation is not universally superior on heterogeneous collections. Useful
  precedent for an exposed navigable corpus, but its automatic clustering and
  summarization pipeline is not proposed as a Camp dependency.
- [Is Progressive Disclosure All You Need for Long-Context Agents? (July 2026)](https://arxiv.org/html/2607.17598v1):
  compares raw reading, flat disclosure, hierarchical disclosure, and hybrid
  retrieval. Flat disclosure helps at larger corpus sizes in the tested setups;
  additional hierarchy does not improve results and sometimes hurts. The
  variants also differ in placement of descriptions in context, so this is not
  an isolated proof that tree depth causes failure. It motivates testing short
  routes and avoiding mandatory traversal of every directory level.
- [Knowledge-as-Skill (September 2026)](https://arxiv.org/abs/2609.25991):
  discovery, directory navigation indexes, and documents with topic, type,
  provenance, and lifecycle metadata. Close to Camp's needs. Evaluation is
  preliminary; its comparison with Corpus2Skill is explicitly not controlled.
- [Agent Skills specification](https://agentskills.io/specification): names and
  descriptions are exposed first, instructions are loaded on activation, and
  resources are read on demand. Useful operational precedent, not evidence that
  a flat catalog of every workspace document will scale.
- [PageIndex](https://github.com/VectifyAI/PageIndex): builds document trees and
  uses LLM reasoning to retrieve from them. Explicitly a vectorless RAG system;
  lack of vectors alone does not satisfy the desired navigation contract.
- [Aider repository map](https://aider.chat/docs/repomap.html): compact symbol
  definitions with graph-based selection under a context budget. Useful for
  representation, but ranked omission should not be the only way to browse Camp.

These research papers are evidence to guide experiments, not guarantees of Camp
performance. No independent reproduction was performed during this discussion.

### Earlier theoretical foundations

- [Furnas, Effective View Navigation, CHI 1997](https://interactivity.ucsd.edu/articles/In_Process/CHI97-EVN.2.pdf):
  small views, short routes, and discoverable paths. A hidden target needs a
  visible indication of which route leads toward it (navigation residue).
- [Furnas, Generalized Fisheye Views, CHI 1986](https://www.researchgate.net/publication/229100011_Generalized_Fisheye_Views):
  local detail with distant landmarks. Useful for maintaining orientation around
  a selected branch, without task-conditioned relevance scoring.
- [Pirolli and Card, Information Foraging, 1999](https://www.researchgate.net/profile/Stuart-Card-2/publication/229101074_Information_Foraging/links/09e41508383bc5a245000000/Information-Foraging.pdf):
  cues help a navigator estimate the value and cost of following a route.

These concern human navigation. Applying them to LLM agents is a design
hypothesis requiring evaluation, not a demonstrated equivalence.

## Trail-code: verified behavior in version 0.28.1

The pinned version in this repository was inspected in its installed package,
including `dist/graph/map.js`, `dist/graph/relations.js`, `dist/ask/ask.js`, and
`dist/ask/graphrank.js`. See [the project](https://github.com/trailhq/Trail-Code)
and [Camp's indexing guide](../../INDEXING.md).

### Index

Tree-sitter extracts files and symbols with source positions. Structural edges
include calls, references, imports, implements, and extends. The base index needs
no LLM; optional `--deep` enrichment is separate. Camp's development setup uses
the structural index, without configuring generated summaries.

### Map

`trail_repo_map` is a deterministic structural summary:

1. Group nodes by directory, respecting project scopes.
2. If one top-level group contains more than 60% of files within the scope,
   split that group one path segment deeper.
3. Count files and symbols; select three hubs per group by incoming structural
   edge count.
4. Sort groups by symbol count, then path; show up to 16 groups per scope by
   default.
5. Show up to 12 global symbol hubs, and report omitted directory counts.

This map does not use semantic clustering, query understanding, or PageRank.
Its size is primarily bounded by item caps, not a strict token-budget optimizer.

### Search and subsequent exploration

`trail_find_code` is distinct from the map. Structural queries resolve a symbol
and traverse edges. Other queries use lexical matching over names, paths, and
content, including IDF weighting and BM25 for definition bodies. Graph ranking
can supplement lexical evidence through personalized PageRank. File-diversity
selection helps prevent one file's symbols from monopolizing the result.
Results include source positions and bounded excerpts.

Progressive disclosure emerges from the tool set: map, file API/skeleton,
relationship traversal, search, and source reading. The map alone is not the
entire navigation algorithm.

Code has mechanically extractable signatures and relationships. Documents need
additional descriptions and lifecycle cues; citation count cannot distinguish a
current decision from an obsolete but frequently linked discussion.

## Proposed Camp direction

Combine Trail's separation of views with self-descriptive documents and short
routes to sources. These are proposals, not finalized contracts:

- `map` presents structure; it does not need an agent-task or intent parameter.
- Section cards explain what can be found under them.
- Document cards distinguish purpose, kind, and known status; outlines expose
  headings without loading full bodies.
- Explicit typed links provide routes across directory boundaries.
- The agent chooses documents and reads their content separately.
- Search remains independently available when the agent needs it.

### Candidate disclosure algorithm

1. Resolve the requested scope, snapshot, and accessible records.
2. Show the current location, its ancestors, and compact surrounding landmarks.
3. Represent branches with short cards describing their contents.
4. Expand small branches when their contents fit. Compress uninformative
   single-child chains and permit direct jumps to deeper documents.
5. Keep large branches folded with explicit expansion or continuation actions.
6. Allocate remaining response space to structural detail using stable rules,
   not inferred relevance to an unstated task.
7. Report omitted output and incomplete scan/index coverage separately. Every
   available document must remain reachable through explicit continuation.

This is a sketch, not a finished budget-allocation algorithm. It does not claim
that shortest paths, optimal compression, or accurate agent choices are solved.

### Descriptions

Prefer explicitly maintained descriptions of purpose. When absent, a candidate
fallback is a title and extracted heading structure, without invented meaning.
Generated summaries are an optional future choice, not part of the proposed
minimum. Any derived description needs revision provenance and a way to detect
staleness. Broad section summaries must not erase unusual documents.

## Constraints

- Apply visibility checks to cards, counts, outlines, links, and descriptions;
  cached summaries must not disclose inaccessible descendants.
- Preserve snapshot semantics and check current access as required by existing
  broker behavior. A navigation result must not silently become a different
  snapshot when used for subsequent reading.
- Preserve search honesty: snapshot/watermark/coverage where applicable, explicit
  truncation, and no implication of completeness from a partial scan.
- Keep durable document truth in the existing store. Any navigation projection
  must be rebuildable and must not become a second authority.
- No new runtime dependencies or on-disk layout changes are approved.
- Avoid requiring an LLM call for each `map` request.

## Evaluation proposal

Compare the existing map, descriptive flat outlines, and adaptive structural
views using the same tasks, agents, accessible corpus, and budgets. Measure:

- required-document discovery and evidence coverage;
- unnecessary document opens and wrong-branch visits;
- tool calls, tokens, latency, and backtracking before reaching sources;
- completion quality, not just small map responses.

Include broad and deep trees, misleading names, missing descriptions, documents
outside their expected topics, multi-document tasks, obsolete decisions, moves,
permission changes, and incomplete indexes. Navigation guarantees should also
be checked without relying on an LLM: continuation must reach every accessible
document, and no output may leak inaccessible content.

## Agreed adoption approach: Superpowers-style bootstrap

The user accepted mandatory workflow instructions delivered reliably to the
agent, without technical barriers that prevent editing or completing work.
A skill alone is insufficient as an entry point: the agent might never select
it. The integration must make the initial Camp check a client-wide workflow rule,
while the skill explains how to perform it.

### Workflow

1. Before beginning meaningful new work, discover accessible Camp spaces,
   select from task and user context, inspect the map and read applicable rules.
   A project directory or Git repository is not required.
2. Let the agent choose subsequent navigation and reading through progressive
   disclosure. Initial orientation is required by the workflow; reading every
   document is not.
3. At completion, preserve significant decisions and results when there are
   any. Do not require meaningless writes just to demonstrate tool usage.
4. Do not repeat discovery on every conversational reply when the agent already
   has the relevant orientation for the current task.

### Default scope, clarified by the user

Ordinary setup/connect must install the bootstrap at the client user level,
independent of a working directory. Camp is a shared working environment, not
only a tool for coding projects. Project-local integration remains optional;
project-to-space association, if present, is extra context for space selection.
The earlier project-only bootstrap implementation did not meet this requirement.

### Client-specific delivery

- Where supported, use a session-start hook to deliver the bootstrap instruction
  into context rather than relying on voluntary skill discovery.
- Elsewhere, use persistently loaded user instructions and the client's
  native skill discovery. Do not assume identical hook support across clients.
- Restore a concise reminder after context compaction where the client supports
  it. Preserve task continuity rather than automatically repeating all reads.
- Keep detailed `map` → `start` → `run` instructions in `using-agent-camp`.
  The always-present bootstrap should remain short.

This mirrors Superpowers' instruction-driven approach, not a guarantee that a
model will obey. Its Claude Code hook injects `using-superpowers` on startup,
clear, and compaction; Codex delivery differs. References:
[bootstrap script](https://github.com/obra/superpowers/blob/main/hooks/session-start),
[hook configuration](https://github.com/obra/superpowers/blob/main/hooks/hooks.json),
and [Codex integration changes](https://github.com/obra/superpowers/blob/main/RELEASE-NOTES.md).
The installed Superpowers 7.0.1 Codex package was also inspected; no equivalent
session-start hook was found there.

### Explicit scope boundary

No external enforcement gate, mandatory tool-call receipt, edit lock, or
completion lock is proposed. Camp's MCP server cannot enforce use of unrelated
editor or terminal tools. Instruction delivery can improve adoption, but neither
delivery nor a recorded read proves that an agent understood the content.

The client adapters now install user-wide MCP configuration, the workflow skill,
and bootstrap delivery. Claude uses user SessionStart hooks, Codex global
instructions, and Cursor user sessionStart hooks. Explicit --project installs
directory-specific integration instead. Actual client/model compliance trials
remain pending. The implementation uses the documented provisional outage
policy: explain what could not be checked, continue independent work, and ask
for essential missing context. No hard stop on all work is introduced.

## Open decisions before implementation

1. Who writes and maintains document and section descriptions, and are any fields
   required or merely conventions?
2. What exact card/outline schema and response budget should `map` expose?
3. How should expansion, continuation, and snapshot handoff to `start` work?
4. Which structural rules choose expansion under a hard budget, including when
   even all immediate children cannot fit?
5. How much navigation uses the stored path tree versus additional explicit
   indexes and links?
6. What freshness, caching, and visibility strategy supports large spaces?
7. Which representative corpus and tasks define acceptable navigation quality?
8. Which bootstrap delivery mechanisms are supported by each client, how is the
   project associated with a Camp space, and how are unavailable-service cases
   handled?

At the user's request, two implementation plans now record concrete proposed
defaults for these choices: [navigation](../plans/2026-11-09-progressive-disclosure.md)
and [bootstrap integration](../plans/2026-11-09-camp-bootstrap.md). Review these
defaults before implementation; their presence in a plan does not mean the user
previously selected each API field or integration policy.
