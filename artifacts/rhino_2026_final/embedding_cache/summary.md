# Embedding Cache Controlled Experiment

## Frozen Conditions

All accepted phases used commit `cd21908a652d1330499986e191fbd704b7d9c13b`,
`text-embedding-v4`, dataset SHA
`56fd363d797ee4c1524a5a1a2517b3b30ce955229c37784cf730c0d1dc47fd0d`,
the same 32 logical texts, and the same production pooled-batch path. Only cache
enablement/state changed.

Provider inputs/requests below come from the accepted persisted Model Usage rows.
The phase JSON `cache_stats` is decorator-local; for cache-disabled OFF it reports
zero internal decorator counters and is not the provider-work authority.

## Results

| Phase | Inputs | Hits | Misses | Provider inputs | Provider requests | Elapsed |
|---|---:|---:|---:|---:|---:|---:|
| OFF | 32 | 0 | 0 | 32 | 7 | 670 ms |
| COLD | 32 | 0 | 32 | 32 | 7 | 584 ms |
| WARM | 32 | 32 | 0 | 0 | 0 | 10 ms |

## Conclusion

**PASS.** Warm cache eliminated provider inputs and provider requests for this
controlled 32-input set. Elapsed time is auxiliary evidence, not an SLA.

An earlier invalid setup probe sent one 32-item provider batch and exceeded the
provider per-batch limit. It is retained in the Model Usage evidence and excluded
from the valid OFF/COLD/WARM comparison; all accepted phases used the production
pooled-batch path.

## Evidence

- [artifacts/rhino_2026_final/embedding_cache/off.json](off.json)
- [artifacts/rhino_2026_final/embedding_cache/cold.json](cold.json)
- [artifacts/rhino_2026_final/embedding_cache/warm.json](warm.json)
- [artifacts/rhino_2026_final/embedding_cache/comparison.md](comparison.md)
- [artifacts/rhino_2026_final/embedding_cache/model_usage_rows.json](model_usage_rows.json)
