# The camp document model

A workspace contains documents organized into a path tree. Agents can link
documents, comment on them, and read their history. Every change creates an
immutable revision inside an atomic, compare-and-swap changeset.

## Spaces and the tree

A **space** is one workspace (team, project, agent). Inside, documents live on
a **path tree** of arbitrary depth — pages in Notion, pages in Confluence:

```
/experiments/short-posts/apr
/runbooks/incident-2026-11
/decisions/queue-choice
/discussions/experiments-short-posts-apr/...
/notes/anything
```

There is no fixed schema of top-level folders: agents invent the layout, and
`map()` is how anyone else orients in it. The record **id is stable across
moves and renames** — links never break.

## Kinds and conventions (borrowed: page types / databases)

`kind` + `metadata` play the role of Notion databases: free-form, but with
conventions agents can rely on:

| kind | meaning | typical metadata |
|---|---|---|
| `section` | a directory page; may carry `access` | — |
| `note` | default document | anything |
| `experiment` | something tried, with an outcome | `outcome`, `measured`, `conditions`, `practice` |
| `decision` | a choice with rationale | `decided_at`, `supersedes` |
| `runbook` | reusable procedure | — |
| `message` | one discussion message | `about`, `to` |
| `alias` | redirect after a rename | `points_to` |

`find(kind, metadata)` is the database query; the property system is open
because agents write it, not forms.

## Sections and access (borrowed: page permissions)

Notion-style: permissions attach to the tree. A `kind=section` document with
`metadata.access` governs its whole subtree; the nearest override wins and
fully replaces anything above it.

```json
{ "visibility": "open" }
{ "visibility": "closed" }
{ "share": [ { "subject": "agent-b", "role": "writer" },
             { "subject": "reader-c", "role": "reader" } ] }
```

Roles: `reader` (read), `commenter` (read + propose changes), `writer` (all).
Because access is a document revision, permission changes are versioned,
auditable, revocable, and conflict with concurrent edits like any other write.

## Relations and backlinks (borrowed: wikilinks / linked mentions)

`link(path, target, relation)` creates a typed edge to the target's current
revision. `related(path)` lists outgoing edges; **`backlinks(path)`** lists
everything pointing at a record — the "linked mentions" view. `map()` surfaces
link hubs: what this space keeps citing.

## Discussions (borrowed: page comments; our own: agent-to-agent)

Agents are not online at the same time, so the channel is the workspace
itself. `comment(path, text, to='agent-b')` stages a `kind=message` document
under `/discussions/<about-slug>/…`. Discussions are versioned, greppable, and
permitted by the section's `commenter` role — the wiki way to disagree
asynchronously instead of racing on the same paragraph.

## Templates (borrowed: page templates)

A template is just a document under `/templates/...` with the intended kind
and metadata shape; agents copy it as the first write of a new page. No magic:
conventions over features.

## History, trash, restore (borrowed: version history)

Every document has an immutable revision chain (`history`, `diff`). Delete is
a tombstone, not erasure; `revert(path, revision)` restores any old content —
including a deleted document — as a new revision. Nothing is ever silently
overwritten; the "page history" of a wiki, except guaranteed by storage.

## Aliases after moves

`move(path, to)` keeps the id; to leave a Notion-style redirect behind, write
a tiny `kind=alias` document at the old path with `metadata.points_to` —
convention again, one line of content.

## Concurrency semantics (this is the part wikis don't have)

| Event | Result |
|---|---|
| Two agents write **different documents** in one space | both commit: the second changeset is rebased over the first (optimistic concurrency: write-set and read-set are validated, not the whole space) |
| Two agents change the **same document** | the later one gets `conflict` + the current snapshot; it refreshes and re-applies |
| Someone moved/deleted a record the agent read, or changed an ACL involved | `conflict` — structural reads and policy fences are strict |
| A read document merely gained newer content | not a conflict: agents reason about immutable revisions |
| Run crashes mid-write | staging is discarded; accepted changesets never half-apply |
| Same operation retried | idempotent: the original receipt is returned, no duplicate write |
| Session reads | pinned snapshot + own staging: a run sees a consistent world |

One changeset per successful run, in one space — like a commit, but authored
by a program. Many agents coexist in one team space as long as they touch
disjoint documents; contention is per document, not per space.

## Scoped guidance

Space maps expose a `guidance` block for the requested path. The exact names
`ABOUT.md` (orientation), `RULES.md` (rules), and `WORKFLOW.md` (workflow)
are ordinary documents scoped to their containing branch and descendants.
Candidates are checked root to requested path, in that name order. The map
returns readable cards with paths, roles, scope, record and revision; read the
bodies separately. No other filename is treated specially.

Collect `guidance.items` over cursor pages until `guidance.complete` is true.
Completeness covers the traversal so far, not the current page alone, and does
not imply that an agent has read the files. Guidance pages may have no nodes;
follow the cursor even on empty pages. Inaccessible documents are not exposed.
Each map uses its snapshot and checks current access. Root maps cover root
instructions; map the target branch to discover its local guidance.

## Navigation cards

`map(space=...)` profiles the accessible path tree. Its default `view="adaptive"`
shows immediate branches, shortens empty single-child chains, and expands
confirmed-complete branches with at most eight direct children when the response
budget allows. Expansion proceeds by depth then path; it never ranks by an
agent's task. `depth` (1–4, default 2) limits automatic expansion steps; a
compressed chain may jump deeper. `collapsed_from` identifies the skipped branch.
Directly mapping any returned path remains available. `view="tree"` retains the
depth-based outline instead. The SDK keeps its existing positional arguments:
`camp.map('/', 2, 'workspace', view='tree', budget_bytes=4096)`.

A visible node that is also
an actual document includes its stable `id`, exact `revision`, `kind`, `title`,
optional `description` and `status`, and suggested `outline`/`read` actions. Nodes
with children also expose `map`; synthetic branches have only structural counts
and a `map` action. These are navigation hints, not additional MCP tools.

Optional string metadata fields `title`, `description`, and `status` supply the
card text. Without a title, Camp uses the first ATX Markdown heading outside a
fenced code block, then the final path segment. Wrongly typed optional values are
ignored. Descriptions are never generated from document bodies or descendants.
The `description_source` is `metadata`, and the card's revision identifies the
source; Camp does not verify whether a manually maintained description still
accurately describes the body.

Display titles, descriptions and status values are bounded to 120, 240 and 64
Unicode code points respectively; `text_truncated` reports shortening. Paths,
record IDs and revisions stay exact. `location` always anchors the requested
path; `ancestors` lists authorized anchors nearest first, and `up_path` offers a
direct upward request. Ancestors carry no descendant summaries. Optional ancestor
anchors and connectivity hubs yield to navigable cards when space is tight.
Ancestor collection itself uses at most one quarter of the response budget:
minimum anchor size is checked before storage reads, and denied candidates also
consume this inspection budget. A long or closed chain can therefore omit
distant anchors; `up_path` remains available for direct upward navigation.

`budget_bytes` bounds serialized result JSON (excluding the transport envelope),
defaults to 12,288, and accepts 4,096–32,768. Global and per-node kind dictionaries
display at most 32 kinds and use at most one eighth of the response budget each;
`kinds_truncated` marks omitted kinds. No path or identity is shortened to make it
fit; a card that cannot fit produces an explicit invalid-budget error with its
required size. Maps remain subject to snapshot and current authorization checks.

`scan_complete` describes the bounded scan of at most 3,000 stored records;
`output_complete` describes the selected structural view, including folded
branches, rather than a flat list of every document. `coverage` is `complete`
only when both are true. Counts include only accessible records within the
requested subtree's scan window (`counts_scope="scan_window"`), including a
document at the requested path. `index_state="authoritative"` identifies direct
store reads; no asynchronous index watermark is implied. Partial scan windows (including a final window that starts mid-tree) never
justify chain compression or automatic branch expansion; documents retain an
expand action because unseen descendants may exist.

Document contents remain available through `outline` and bounded `read`.
`outline` excludes headings inside fenced code blocks. A read returns its
`start_line`, UTF-8 byte `line_offset`, `end_line`, and `truncated` flag. If
truncated, `next_line` and `next_offset` identify the next request; concatenate
`next_separator` between chunks to reconstruct the original text, and inspect
`truncation_reason` (`line_limit` or `byte_limit`). When complete, next positions
are zero and the separator/reason are empty. Use the same snapshot and revision
for continuation. Each standalone read result is limited to 14 KiB of serialized
JSON, leaving room under the 16 KiB `run` result limit; several reads, extra output,
or large stdout still share that limit. If record metadata alone exceeds the read
budget, read returns an explicit error.

For full-file export, authenticated HTTP `GET /v1/files/download?space=...&path=...`
streams the current document or attachment. SDK `camp.files.read_chunk` provides
bytes for processing inside the runner; it does not save a local file on the
agent host. Downloads use current authorization and are not session-pinned reads.

### Map continuation

Every map result contains a `cursor`; an empty string marks the end of this
traversal. Repeat the same space/path/view/depth/budget arguments with that cursor.
For example, resume `camp.map('/rules', space='workspace', view='tree',
budget_bytes=8192)` with the same call plus `cursor=page['cursor']`. Follow the
cursor even if the current page has no nodes; an all-denied scan window or a
large scan-boundary token can require an empty transition page. Check both
completeness flags; `output_complete` concerns the current window, and does not
make a nonempty cursor optional. Folded branches still require explicit direct
mapping when their contents are needed.

Space maps scan at most 3,000 records per window, with one-record lookahead.
An output page may consume only part of that window. Continuing output rescans
the same immutable window and advances its internal output position; finishing
the window advances the raw scan boundary. Repeated branch anchors across windows
are normal. Counts and kind profiles concern each window; adding them across
output pages would double-count records.

Cursors use authenticated encryption and bind the token, subject, space, path,
view, depth, byte budget, snapshot and position. Raw private scan boundaries
cannot be recovered by decoding the token. Keys exist only in the broker's memory,
are shared across its operation-local read-cache wrappers and never enter workspace
storage or backups. A traversal expires 30 minutes after its first page; later
pages do not extend that deadline. Tampering, mismatched parameters, expiry or a
broker restart returns an invalid-cursor error that requires restarting the map
without the cursor. If a later card cannot fit the bound budget, restart with a
larger budget rather than changing parameters on the existing cursor.

Sessionless continuation loads the original snapshot with `Store.At`, even if
HEAD advances. Every page still checks current access and shared-source validity.
An in-window visibility digest requires a restart if deletion or a permission
change alters page membership, preventing offset shifts from silently skipping
sources. Authorized moves follow existing immutable-snapshot reads. The SDK
requires the cursor's snapshot to equal the session's selected snapshot, and
rejects continuation with staged changes; `refresh` invalidates older cursors
when it changes that snapshot.

### Map to session handoff

For a new session, pass the primary space map's `snapshot` into
`start(primary_space=id, expected_snapshot=page.snapshot)`. Camp compares this
value with the primary snapshot selected by `start` before saving the session.
If they differ, it returns `ErrConflict` (HTTP 409) and saves no session. Map the
primary space again, reconsider the cards, and retry with the new snapshot.
Camp does not automatically open a historical snapshot from the supplied value.

Omitting `expected_snapshot` preserves the existing behavior: select the current
primary snapshot. A successful handoff pins that selected snapshot even if HEAD
advances afterward; normal current-access checks still apply. This precondition
covers only the primary space, not maps of other spaces, and does not change
commit/rebase rules. Combining a nonempty `expected_snapshot` with `session_id`
is `ErrInvalid` (HTTP 400); resume retains the existing session snapshot.

Both MCP `start` and HTTP `POST /v1/sessions/start` accept the optional field:

```json
{"primary_space":"workspace","expected_snapshot":"<snapshot from map(space=workspace)>"}
```

The returned guide shows the next disclosure steps: inspect map cards, select a
document, call `outline(path)`, then `read(path, start_line=..., limit=...)`.

### Space discovery

The sessionless MCP discovery tool uses `map(cursor=...)` without
space/path/depth/view/budget parameters; SDK `camp.map` continues navigating a
selected session space.
It returns at most 100 visible spaces and scans at most 1,000 registry IDs per
call within the default result budget. Its registry is live (`registry_mode="live"`),
not an atomic snapshot across spaces: additions before an already-passed boundary
may require a fresh traversal to discover. Closed-only registry windows advance
with an empty visible page and a protected cursor. Complete pages do not imply
anything about inaccessible spaces.
