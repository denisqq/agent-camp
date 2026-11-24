# Scoped Map Guidance Implementation Plan

**Goal:** Surface applicable ABOUT.md, RULES.md and WORKFLOW.md documents in map.

**Architecture:** Resolve bounded direct candidate paths on the session snapshot with current authorization. Paginate guidance ahead of structural navigation using the authenticated cursor, and maintain the response byte budget.

**Spec:** docs/superpowers/specs/2026-11-15-map-guidance-design.md

- [x] Add failing behavioral tests for root-to-branch order, roles, visibility and deep continuation.
- [x] Implement guidance discovery and cursor phase, maintaining existing map snapshot and budget guarantees.
- [x] Update skill, bootstrap, README, model and SDK/MCP descriptions.
- [x] Run targeted tests, full build/test/race/vet and review the diff.
