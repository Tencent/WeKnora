# Stardust Seed Graph

This file is the source-of-truth seed graph for the Stardust wiki benchmark.

## Current corpus status

- `docs_v1`: 8 docs
- `docs_v2`: 3 docs
- `docs_del`: materialized delete-regression set (currently d001,d002,d003)

## Core design goals

1. Relation-dense rather than text-dense.
2. Multi-hop QA ready.
3. Update and delete aware.
4. Stable across different wiki implementations.

## Highest-priority generation order

1. Freeze the seed graph.
2. Generate or revise `docs_v1` around the core graph.
3. Generate `docs_v2` as explicit version deltas.
4. Expand `docs_del` for retract and cleanup regression.
5. Derive gold from the graph, not from free-form prose.

## Delete regression focus

The first delete target is `doc05_borealis_incident.md`.
It is chosen because it is referenced across the incident, replacement, and station cluster and can exercise source-ref cleanup plus idempotence.

## Next implementation step

The delete-regression corpus is now materialized for d001, d002, and d003; next step is to keep expanding the delete set and wire the same seed graph into an end-to-end generator script.
