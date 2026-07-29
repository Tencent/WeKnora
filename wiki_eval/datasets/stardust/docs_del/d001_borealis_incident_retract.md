# Delete Regression Case d001: Borealis Station Incident Retract

- Target doc: `doc05_borealis_incident.md`
- Target title: `Borealis Station Incident Report`
- Delete mode: `retract`
- Evaluation focus: source-ref cleanup, stale in/out-link cleanup, false-delete control, and idempotence.

## Why this case matters

The incident report is referenced across the Borealis / Psionic Engine / Northstar Labs cluster, so it is a good regression target for cleanup after retract.

## Expected impact

- Remove source refs tied to the target knowledge item.
- Preserve the core pages for `entity/stardust-program`, `entity/psionic-engine`, `entity/borealis-station`, and `entity/northstar-labs`.
- Do not rewrite unrelated pages such as `entity/aurora-beacon` and `entity/celestial-review-board`.
- Remove stale links such as `summary/{target_knowledge_id}`.
- Remove incident-specific terms such as `Lumen Coil batch LC-19`, `unstable field event`, and `delayed by twelve days`.
- Retracting the same target twice should remain idempotent.

## Benchmark note

This file is a materialized delete-regression case for the Stardust benchmark. The canonical execution is still driven by `wiki_eval/eval_weknora.py --run-delete` against `gold/delete_events.json`.
