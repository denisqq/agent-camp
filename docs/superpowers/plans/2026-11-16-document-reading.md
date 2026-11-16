# Document reading improvements

**Goal:** Make bounded document reads resumable and safe to return through a run.

- [x] Test line and byte-budget continuation, long Unicode lines, and outline fence handling through Broker.Call.
- [x] Add explicit next line/offset and truncation reason while keeping existing read arguments compatible; bound a standalone serialized read result below the run limit.
- [x] Update Python SDK forwarding, help, model and agent guide. Preserve snapshot, access and atomicity.
- [x] Verify targeted tests, full checks, real Monty smoke and Docker build for runner changes.

Existing HTTP `/v1/files/download` streams full documents/attachments; no new download endpoint is required. Multiple reads in a single run still share its total result limit.
