# Delete Regression Case d002: Aurora Beacon Calibration Retract

- Target doc: `doc08_aurora_beacon_notes.md`
- Target title: `Aurora Beacon Calibration Notes`
- Delete mode: `retract`
- Evaluation focus: source-ref cleanup, calibration-note term cleanup, stale in/out-link cleanup, false-delete control, and idempotence.

## Why this case matters

The Aurora Beacon note connects the calibration device, Psionic Engine PE-7, Lumen Coil, Helion Crystal, Northstar Labs, Borealis Station, and Stardust Program. It exercises a different cleanup cluster from d001, which focuses on the Borealis incident report.

## Expected impact

- Remove source refs tied to `doc08_aurora_beacon_notes.md`.
- Preserve the core engine/component pages for `entity/psionic-engine`, `entity/aurora-beacon`, `entity/lumen-coil`, and `entity/helion-crystal`.
- Preserve nearby operational pages such as `entity/stardust-program`, `entity/northstar-labs`, and `entity/borealis-station`.
- Do not rewrite unrelated governance/person pages such as `entity/celestial-review-board`, `entity/skyvault-initiative`, and `entity/jonas-reed`.
- Remove stale links such as `summary/{target_knowledge_id}`.
- Remove calibration-note-specific phrases such as `synchronizes the Lumen Coil`, `replacement Lumen Coil assemblies`, and `Borealis Station control must block`.
- Retracting the same target twice should remain idempotent.

## Benchmark note

This file is a materialized delete-regression case for the Stardust benchmark. The canonical execution is still driven by `wiki_eval/eval_weknora.py --run-delete` against `gold/delete_events.json`.
