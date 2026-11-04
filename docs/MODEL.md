# The camp document model

The logical model is a wiki, borrowed deliberately from what decades of
Notion/Confluence/wiki use taught humans — because agents need the same
affordances. The difference is underneath: every change is an immutable
revision inside an atomic, compare-and-swap changeset.

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
| Two agents write in one space | serialized per space; the second gets `conflict` + the current snapshot, then refreshes and retries |
| Run crashes mid-write | staging is discarded; accepted changesets never half-apply |
| Same operation retried | idempotent: the original receipt is returned, no duplicate write |
| Session reads | pinned snapshot + own staging: a run sees a consistent world |

One changeset per successful run, in one space — like a commit, but authored
by a program.
