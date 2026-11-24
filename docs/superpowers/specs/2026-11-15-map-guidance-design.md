# Scoped guidance in Camp maps

Status: approved for implementation. The user selected known filenames and agreed
to `ABOUT.md`, `RULES.md`, and `WORKFLOW.md`.

## Intent and scope

Agents must discover the context and conventions needed to work in a space or
branch, even when the user provides an exact document path. Map exposes guidance
explicitly; the Camp skill teaches agents to read it and return its text to their
context before acting. This change does not enforce that a model follows prose.

Use the existing map/read/run workflow. Do not add automatic text injection into
start/run, a read acknowledgement protocol, write gates, task ranking, embeddings,
new dependencies, or a stored guidance index.

## Approaches

Considered explicit metadata identifying guidance documents and fixed filenames.
The user selected fixed filenames for simpler authoring and deterministic lookup.
An initial AGENTS.md/CLAUDE.md proposal was replaced by Camp conventions:

| Filename | Role | Purpose |
|---|---|---|
| ABOUT.md | orientation | What this area contains, its layout, and useful entry points |
| RULES.md | rules | Formatting, terminology, and conventions for working with documents |
| WORKFLOW.md | workflow | Steps, related updates, and checks required to complete work |

Names are exact and case-sensitive. All three are optional. They remain ordinary
versioned documents with existing read/write/delete/move and access semantics.
Their content is user-authored; Camp does not synthesize rules or summaries.
Other names remain ordinary documents, and no alias is followed automatically.

## Scope and order

A guidance document describes its containing path and descendants, within one
space. The scope of /RULES.md is /; /runbooks/RULES.md has scope /runbooks.
Branch guidance adds local context; the server does not merge prose, resolve
contradictions, or give a role/name authority over another document.

For map(path=/runbooks/db), inspect these scopes in order: /, /runbooks,
/runbooks/db. Within each scope use ABOUT.md, RULES.md, WORKFLOW.md order.
Guidance in sibling branches is excluded. This ordering is a reading order,
not an authorization or instruction-priority hierarchy.

Map's requested path remains a navigation scope, even if it is also a document
with children. Its guidance applies to that path and descendants. For working
on a known document, the skill maps its parent branch; that also discovers rules
before creating a document that does not yet exist. Mapping a guidance document
itself does not recursively expand links in its contents.

Root maps expose root guidance. Automatic expansion of descendant nodes does
not claim to deliver their local guidance; the skill maps the target branch
before dependent work. Sessionless map() discovery remains unchanged.

## Response interface

Both MCP map(space=...) and SDK camp.map return the same additional block:

```json
{
  "guidance": {
    "applies_to": "/runbooks/db",
    "items": [
      {
        "path": "/ABOUT.md",
        "scope": "/",
        "role": "orientation",
        "id": "record-id",
        "revision": "revision-id",
        "title": "Workspace overview",
        "actions": ["read"]
      }
    ],
    "complete": false
  }
}
```

The enclosing map snapshot applies to these cards. Identity, path, scope, role,
revision and actions are required. Title is optional and uses existing bounded
card-title extraction/truncation rules. Bodies are not included. Guidance files
remain ordinary nodes where the normal structural view includes them.

`complete` is cumulative for this cursor traversal: all candidate addresses have
been inspected and all accessible guidance cards delivered across its pages.
It does not assert that the latest page contains the entire list, that the model
has read it, or that inaccessible guidance does not exist. An empty complete
list means no readable guidance was found. Document this alongside cursor usage.

## Bounded discovery and pagination

Look up known filenames directly for each ancestor scope; do not depend on the
3,000-record document scan or the optional, budget-truncated ancestors list.
Use one top-level cursor with two phases: guidance, then ordinary navigation.
Return guidance first, bounded by the existing budget_bytes. A proposed initial
limit is 24 candidate lookups per request, charging absent and denied candidates
too. This limit is an implementation tuning choice, backed by storage-call tests.

If candidates or output remain, return a guidance-only page with nodes empty,
guidance.complete=false, and a nonempty cursor. Do not perform the ordinary tree
scan on those pages. Such a page reports scan_complete=false,
output_complete=false and coverage=truncated; counts are zero because the tree
has not been scanned. Retain location and snapshot, but omit optional decoration.

Once guidance is complete, advance to ordinary navigation using the same cursor.
It is acceptable to emit a transition page before tree scanning. On subsequent
node pages guidance.items is empty and guidance.complete=true: clients retain
the earlier items. Existing scan-window counts/completeness retain their meaning.
Overall coverage cannot be complete while guidance is incomplete.

Reserve actual serialized envelope/cursor space. Do not shorten paths or IDs.
If one minimal card cannot fit even after dropping its title and optional map
decoration, return an explicit budget error. Cursor state must advance on
absent/denied-only pages and must not skip an emitted-but-unfittable candidate.

## Snapshots and authorization

Reuse snapshot resolution and current access/shared-source checks for each
candidate. Skip denied/missing documents without exposing their existence,
metadata, counts, or raw scan position. Propagate other errors.
This feature does not make inaccessible rules a write constraint.

Extend the authenticated encrypted cursor with the phase and candidate position.
Bind it to existing principal, query, budget, snapshot and expiry constraints.
Keep the original 30-minute expiry and staged-write continuation restriction.
Previously emitted data is not evidence of continuing authorization; bounded
reads recheck access. Use an absolute candidate position, not an offset into the
filtered accessible list, so visibility changes cannot shift later candidates.
The result is an authorized view per page, not a lease on earlier access.

No persistent layout changes are required. Builders may version the in-memory
cursor format; invalid/old cursors must yield an explicit restart instruction.

## Skill and documentation integration

Update both distributed skill copies and the bootstrap wording to identify the
three names and roles. Explain that map guidance pages must be accumulated until
complete, then relevant bodies returned from a read-only run. A later writing
run lets the model consider the text. Referenced templates/glossaries are read
when needed for the task. User instructions and existing access controls remain
authoritative. Do not imply delivery equals comprehension or compliance.

Update MODEL, README and tool/SDK help with the actual response contract in the
implementation commit. The existing skill edits in this checkout predate this
filename decision; revise those examples when implementing this design.

## Acceptance cases

- Root/nested scopes, absent files, all three names, and deterministic order.
- Case sensitivity, siblings excluded, and move/delete/revision behavior.
- Same guidance for tree/adaptive maps regardless of depth or chain compression.
- Direct discovery beyond the normal subtree scan window and missing section pages.
- Deep paths: bounded storage work, progress through absent/denied candidates,
  and paginated delivery without lost or duplicated guidance cards.
- All pages obey budget_bytes, including cursor/envelope; oversized identities
  fail explicitly and incomplete guidance never claims complete coverage.
- Snapshot advance, current revocation, shared-source changes, cursor tampering,
  restart/expiry, staged changes, and readable child beneath a denied ancestor.
- Matching MCP/SDK output and normal navigation after guidance continuation.
- Skill scenario: exact target path plus partial map still causes local guidance
  discovery and visible text retrieval before a separate editing run.

## Implementation boundaries

Changes belong in navigation collection/rendering/cursors, tool and SDK help,
skills and behavioral documentation. Preserve transaction, idempotency, rebase,
access and sandbox invariants. Run the repository check chain; add adversarial
behavior tests rather than assertions that merely mirror field construction.
No server-side record of instruction consumption is introduced.
