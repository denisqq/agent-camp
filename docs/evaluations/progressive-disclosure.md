# Progressive disclosure: adaptive view checkpoint

Status: Task 2 implementation evidence, not an agent navigation quality study.
Continuation and snapshot handoff are still pending. No model-based document
selection trials have been run, so recall, wrong-branch visits and task quality
are unmeasured. This checkpoint does not claim the overall quality gate passed.

## Deterministic evidence

`camp-server/camp/navigation_view_test.go` covers small/wide branches, document
pages with children, compressed empty chains, direct expansion, partial scans,
metadata bounds, ancestor anchors, and both adaptive and tree views. With 250
siblings, 1 KiB paths and multibyte descriptions, internal output slices fit
4,096 / 12,288 / 32,768-byte budgets and collectively preserve every source card.
These offsets are an internal renderer contract, not public map continuation.

A 100-document fixture with 450 empty path segments per document materializes at
most 301 branch/document nodes at depth 1. This checks representation growth,
not a latency or memory benchmark. Counts are checked independently against
source-path membership, including punctuation and Unicode sibling names.

Broker tests distinguish exactly 3,000 records from 3,001 using a one-record
lookahead. Existing access, revocation, source-failure and object-read tests
remain active. The real Monty/MCP stack exercises old positional map arguments
with the new keyword-only view and byte budget. A local packaged-install smoke
also passed setup, startup hook execution, MCP discovery/start/run, document
write and reconnect after clearing the npm cache. Final build, full tests,
full race suite and vet passed; the final camp race suite took 192.5 seconds
with the ordinary final-changeset fixture. The Docker image also built.

## Large-changeset performance limitation

A 3,000-document fixture created in one changeset exceeded the existing 30-second
sessionless map deadline under concurrent build load. An isolated profiled run
passed in 20.1 seconds total, with about 18.1 seconds in `Broker.Map`. Most CPU
was in existing JSON decoding, `Store.Load`, record resolution and final access
validation, rather than adaptive rendering. `HEAD` includes the most recent
changeset's revision IDs; repeated current-access reads decode this list again.
The full camp race suite containing two such bulk fixtures eventually passed in
448.2 seconds. These are local observations, not controlled comparative results.

The permanent boundary fixture retains all 3,000/3,001 documents and ends with a
single-record revision, separating scan-window correctness from a bulk-HEAD
performance benchmark. Production deadlines, access checks, store format and
caching were not changed. Large-space throughput, including bulk changesets,
needs separate measurement before introducing any cache or index.
