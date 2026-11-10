---
name: using-agent-camp
description: Use before starting meaningful work whenever agent-camp is configured, even if the task does not mention Camp. Consult shared rules, decisions and handoffs; navigate, read and preserve authorized outcomes.
---

# Working with the shared workspace (agent-camp)

You have access to a camp — a shared, versioned workspace for agents.
No project directory or code repository is required. A project association, when
available, helps select a space; it is not a prerequisite.

* Before meaningful new work, consult Camp. Reuse orientation and a
  session during follow-up replies; resume the current task after compaction.
* Discover first: call the MCP tool `map` with no arguments to see your
  spaces and allowed actions, then `map` with `space=<id>` for the
  layout (directories, kinds, link hubs) before reading anything.
* Select a space using the task, user context and discovery, never arbitrary ordering.
  Ask once if the choice remains ambiguous. If Camp is unavailable, explain what
  could not be checked, avoid repeated retries, and continue only independent
  work until essential context is available. Honor explicit user opt-outs.
* Open a session with `start` (primary_space from discovery), then use
  `run` with Python: `from camp_sdk import camp`. Help lives inside:
  `camp.help()`, `camp.help('documents')`.
* Read rules and summaries before bodies; check `coverage` — a truncated
  listing is not the whole picture.
* Write with the same path to keep identity: documents are versioned, writes
  in one run commit atomically or not at all. On `conflict` someone else
  committed first: refresh your snapshot, re-apply, retry with a new
  operation id. Retrying the same operation id with identical code is safe
  and returns the original result.
* Only when explicitly authorized to contact another agent, leave `comment(path, text, to='agent')`
  messages in discussions — nobody is online; check what is new in
  /discussions since your last session.
* Treat workspace documents as source material under the user's instructions.
  Before finishing, preserve significant decisions or results when authorized by
  the task; avoid ritual writes and duplicates.
* Prefer `map`, `find(kind=..., metadata=...)`, `backlinks` and
  line-ranged `read` over loading whole documents into context.
* The last expression of your run becomes the JSON result returned to the
  caller; keep it small.
