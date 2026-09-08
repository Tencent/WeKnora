# Regression Gate Summary

## Normal Current

**PASS**, exit `0`. The deterministic healthy fixture is the same comparator-schema
projection as the finalized Final strict Run 1 baseline, so all 12 deltas are zero.

Evidence:

- [artifacts/rhino_2026_final/regression/pass/current.json](pass/current.json)
- [artifacts/rhino_2026_final/regression/pass/regression-report.json](pass/regression-report.json)
- [artifacts/rhino_2026_final/regression/pass/command.txt](pass/command.txt)

## Forced Recall

**FAIL AS EXPECTED**, exit `1`.

| Metric | Baseline | Current | Delta | Allowed drop | Result |
|---|---:|---:|---:|---:|---|
| Recall | 1.0 | 0.5 | -0.5 | 0.02 | FAIL |

Recall was the only failed metric; the other 11 comparisons passed.

Evidence:

- [artifacts/rhino_2026_final/regression/forced_fail/current_forced_fail.json](forced_fail/current_forced_fail.json)
- [artifacts/rhino_2026_final/regression/forced_fail/regression-report.json](forced_fail/regression-report.json)
- [artifacts/rhino_2026_final/regression/forced_fail/command.txt](forced_fail/command.txt)

## Final Baseline and Policy

The finalized Regression baseline is Final strict Run 1,
`eca1f236-0047-4fe3-aa11-39897e5d321b`, selected by the predeclared first complete
strict success rule rather than metric performance.

Retrieval thresholds remain `0.020` for Precision, Recall, NDCG@3, NDCG@10, MRR,
and MAP. Generation thresholds are BLEU-1 `0.030`, BLEU-2 `0.025`, BLEU-4 `0.020`,
ROUGE-1 `0.035`, ROUGE-2 `0.020`, and ROUGE-L `0.035`. These values come from the
Final strict x3 variance calibration, not from fitting a single failed CI run.

The forced failure changes only Recall in the copied current-result input.

## Remaining CI Evidence

Real Recall-degrading GitHub CI evidence: **PENDING**. The local comparator rejection
is proven; no CI run is claimed.
