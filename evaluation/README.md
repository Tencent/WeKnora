# Evaluation regression gate

This directory contains the deterministic quality, latency, and cost gate used
by pull requests and the scheduled CI job.

## Run locally

```bash
make evaluation-gate
```

This single command runs the executable production-pipeline retrieval gate and
the report calculators, then writes `evaluation-report.json` and
`cache-comparison.json`. The JSON printed to the terminal and the checked report
are generated from the same inputs, so reviewers can compare them directly.

The result can be either the evaluation API response envelope or its `data`
object. Dot-separated paths in `baseline.json` address numeric result fields.
The command exits with status 1 when any threshold fails and status 2 when its
input or configuration is invalid.

## Adjust the baseline

- `require_success` requires the task status to be successful.
- `require_complete` requires `task.finished` to equal a positive `task.total`.
- `minimum` contains quality metrics that must not fall below their thresholds.
- `maximum` contains latency and cost metrics that must not exceed their limits.

The checked-in fixture makes the CI gate deterministic. Running evaluations
against a deployed WeKnora instance is intentionally not configured here: the
deployment URL, API token, dataset, and result-retention policy must be approved
before CI is allowed to send credentials or evaluation data to another system.

CI also runs `fixtures/regression_degraded.json`, requires it to exit with
status 1, and verifies that the report names
`metric.retrieval_metrics.recall` as the failed metric. This is an executable
proof that a recall regression blocks the job rather than a screenshot-only
claim.

## Compare cache optimization runs

Capture the same document batch before and after enabling the optimization,
then record the embedding request/provider-call counters and exported model
calls in the JSON shape shown by `fixtures/cache_before.json`. The comparison
command calculates embedding call reduction and Wiki provider-cache hit-rate
change without uploading prompts, tokens, or credentials:

```bash
go run ./cmd/cachebench \
  -before evaluation/fixtures/cache_before.json \
  -after evaluation/fixtures/cache_after.json \
  -report cache-comparison.json
```

The checked-in before/after files are deterministic examples for testing the
calculation only; they are not claimed as measurements from a deployed model.
Replace them with exports from two runs over the same documents, model, and
chunking configuration for the submission report.
