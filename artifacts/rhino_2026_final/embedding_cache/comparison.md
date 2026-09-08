# Embedding Cache OFF / COLD / WARM

Final candidate: `cd21908a652d1330499986e191fbd704b7d9c13b`

Dataset: `benchmark_v1`, semantic SHA-256 `56fd363d797ee4c1524a5a1a2517b3b30ce955229c37784cf730c0d1dc47fd0d`

All three accepted phases used the same 32 corpus texts, the same embedding model (`c24c212d-ae14-4bed-b4c3-be95eeb36464`, `text-embedding-v4`), the production pooled-batch path, and returned 32 vectors. Only cache enablement/state changed.

| Phase | Cache state | Hits | Misses | Provider inputs | Provider requests | Harness elapsed |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| OFF | disabled | 0 | 0 | 32 | 7 | 670 ms |
| COLD | enabled, empty prefix | 0 | 32 | 32 | 7 | 584 ms |
| WARM | enabled, populated prefix | 32 | 0 | 0 | 0 | 10 ms |

The WARM pass achieved a 100% cache hit rate and eliminated provider inputs/requests for this controlled input set. Its observed harness time was 98.3% below COLD (10 ms versus 584 ms). This is a local controlled observation, not a general latency SLA.

Model Usage row IDs: OFF `86b59a63-919d-459d-a82b-c4b16fcd692d`; COLD `5a1a90d3-dd18-4165-aeb4-1f94b472302d`; WARM `bd15e611-cf92-47e7-88b0-1c25ffdd34a8`.

An earlier setup probe (`a51ab543-5e70-4e2f-a736-952b1358aa7f`) used a single 32-item provider batch and failed because the provider limit is 10. It is excluded from the comparison; the accepted passes use the application's production pooled-batch path.
