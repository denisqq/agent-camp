# Onboarding behavior evaluations

Fixtures for evaluating whether agents actually follow the shared-workspace
workflow — not merely whether a hook fired or a config file exists.

## Protocol

1. Pin client and model versions; record them with the results.
2. For each case in `cases.jsonl`, start a fresh session, send `prompt`
   verbatim, and record every camp tool call (or the absence of calls).
3. Score each case against its `expect` block:
   - `reads`: camp reads that should happen (map/overview/relevant docs);
   - `writes`: workspace writes that should happen;
   - `no`: behaviors that must not happen.
4. Run negative controls (`kind: "negative"`) in the same harness; passing
   only positives is not onboarding.
5. Repeat across several fresh sessions; report variance.

## Reporting

Keep evaluation results out of this directory (they are not fixtures).
Report separately, per client:

- configuration-tested (adapter installed and verified), and
- behaviorally verified (these cases pass with pinned versions).

A configured skill or hook is never reported as guaranteed behavior — the
model choosing to follow the workflow is what is measured. Skills are
selected implicitly when task descriptions match; see the client vendors'
skill guidance (OpenAI Codex, Claude Code) — that is model selection
behavior, not a guarantee.

## Fixtures

Each line of `cases.jsonl` is one case: `id`, `kind` (positive/negative),
`prompt`, and the `expect` block above. The prompts never mention Camp:
an onboarded agent recognizes the situations from the workflow skill.
