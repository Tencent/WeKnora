# Delete Regression Case d003: Celestial Review Board Minutes Retract

- Target doc: `doc06_review_board_minutes.md`
- Target title: `Celestial Review Board Minutes`
- Delete mode: `retract`
- Evaluation focus: source-ref cleanup, stale-link cleanup, false-delete control, and idempotence.

## Why this case matters

This case covers the governance / approval cluster. It is distinct from the incident and calibration cases and checks that retracting a review note does not wipe unrelated operational pages.

## Expected impact

- Remove source refs tied to `doc06_review_board_minutes.md`.
- Preserve the core pages for `entity/celestial-review-board`, `entity/stardust-program`, `entity/psionic-engine`, `entity/acme-corporation`, `entity/borealis-station`, and `entity/nightfall-protocol`.
- Do not rewrite unrelated pages such as `entity/aurora-beacon`, `entity/lumen-coil`, `entity/helion-crystal`, and `entity/northstar-labs`.
- Remove stale links such as `summary/{target_knowledge_id}`.
- Remove board-minute-specific phrases such as `approved Stardust Program Phase Alpha`, `rejected Nightfall Protocol integration`, `post-incident analysis`, and `monthly technical risk reports`.
- Retracting the same target twice should remain idempotent.

## Benchmark note

This file is a materialized delete-regression case for the Stardust benchmark. The canonical execution is still driven by `wiki_eval/eval_weknora.py --run-delete` against `gold/delete_events.json`.
