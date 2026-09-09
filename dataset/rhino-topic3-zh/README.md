# Rhino Topic 3 Chinese acceptance dataset

This deterministic, self-authored dataset demonstrates that `dataset_id`
selects different content rather than acting as a display-only parameter. It
covers four Topic 3 acceptance dimensions: model-call observability,
regression gating, Wiki cache benchmarking, and evidence integrity.

`source.json` is the human-readable source of truth. The five Parquet files are
generated representations consumed by the existing evaluation service. IDs are
stable, every query has exactly one reference answer and one relevant passage,
and two additional passages act as non-relevant retrieval candidates.

The dataset contains no external or user-provided content and is licensed under
the same terms as this repository.
