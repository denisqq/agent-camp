# Superpowers design and implementation artifacts

Specifications and implementation plans follow the Superpowers workflow.
Specifications live in `specs/` as `YYYY-MM-DD-<topic>-design.md`; plans live in
`plans/` as `YYYY-MM-DD-<topic>.md`.

| Work | Specification | Implementation plan | Status |
|---|---|---|---|
| Camp setup and agent onboarding | [Design](specs/2026-11-05-agent-camp-onboarding-design.md) | [Plan](plans/2026-11-05-agent-camp-onboarding.md) | Product scope agreed; implementation pending. |
| README product presentation | [Design](specs/2026-11-06-readme-presentation-design.md) | [Plan](plans/2026-11-06-readme-presentation.md) | Copy and banner implemented; validation recorded in the plan. |
| Progressive disclosure and document navigation | [Discussion and research](specs/2026-11-09-progressive-disclosure-design.md) | [Navigation plan](plans/2026-11-09-progressive-disclosure.md) | Descriptive cards implemented; adaptive views and continuation pending. |
| Camp startup workflow | [Agreed adoption approach](specs/2026-11-09-progressive-disclosure-design.md#agreed-adoption-approach-superpowers-style-bootstrap) | [Bootstrap plan](plans/2026-11-09-camp-bootstrap.md) | Default user-wide bootstrap and optional project integration implemented and tested; client/model activation trials pending. |
| Release pipeline completion | [Onboarding design](specs/2026-11-05-agent-camp-onboarding-design.md) | [Plan](plans/2026-11-09-release-pipeline.md) | Implementation and local acceptance checks complete; native matrix and publication require CI/owner access. |

Plans name their source specification, constraints, interfaces, ordered checklist
steps, verification, and task commit checkpoints. The selected execution workflow
must be used when implementation begins.
