# Historical Wiki Prompt Cache Before/After Audit

## Evidence Identity

- Before commit: `3f9a054ec94e92c3089a6574281aa09760068e38`
- After commit: `22f9120216d554181db041b24e3e191363c34366`
- Artifact commit: `e1ff86d02708ff7d3b98162bd0c63a4f44afdd8d`
- Commit chronology: `3f9a054e` is the direct parent of `22f91202`; the artifact
  commit is a descendant of `22f91202`.
- Tenant: `10000`
- Chat model: `deepseek-v4-pro`
- Model config ID: `5a50bf0a-60f9-4340-974b-0bd85c80b286`
- Configured provider: `generic`; effective provider: DeepSeek; resolved model:
  `deepseek-v4-pro`.
- Workload: four Markdown files from `dataset/benchmark_sources/nebultech_v1`
  (`security_policy`, `incident_response`, `database_policy`, `api_guidelines`).
  Their current SHA-256 hashes match `experiment_config.json`.
- Trial time bounds are not persisted in the JSON artifacts. Existing KB creation
  timestamps identify the four sequential runs on 2026-09-05 UTC, and live
  read-only DB slices reproduce the core per-purpose aggregates.

## Prompt Change

`git diff 3f9a054e..22f91202` proves a real code/assembly before-after:

- Before: `WikiSummaryPrompt` and `WikiCandidateSlugPrompt` place dynamic document
  content (and request-varying slugs) before the shared instruction block.
- After: the stable instructions and assembled business instructions are placed
  before the dynamic document/slugs, creating a reusable provider prefix.
- The same commit also adds stable provider prompt-cache keys for PageModify and
  stable image-placeholder ordering. The corpus is plain text, so the image change
  is not exercised; DeepSeek automatic prefix caching does not directly use the
  explicit application cache key. The `wiki_summary` result is therefore aligned
  specifically with the prompt reordering.

## Metric Definitions

Current Final Candidate definitions:

- Prompt Cache Call Hit Rate = `hit_calls / (hit_calls + miss_calls)`.
  Unsupported, unreported, NULL, timeout, and failed rows are excluded.
- Prompt Token Cache Ratio = `SUM(cache_read_tokens) / SUM(input_tokens)` over
  cache-accounted rows with observed input/read tokens. Missing values remain NULL.

The historical README/SQL labels the token formula as
`cache_read / (cache_read + cache_miss)`. For these DeepSeek `wiki_summary` rows,
`input_tokens = cache_read_tokens + cache_miss_tokens` exactly, so recomputing with
the current formula changes no result. Original JSON files were not modified.

## Recomputed Results: wiki_summary

| Mode | Calls | Hit | Miss | Cache read | Input tokens | Call hit rate | Current token ratio |
|---|---:|---:|---:|---:|---:|---:|---:|
| BEFORE, pair 1 | 4 | 0 | 4 | 0 | 6,952 | 0% | 0% |
| BEFORE, pair 2 | 4 | 0 | 4 | 0 | 6,761 | 0% | 0% |
| **BEFORE total** | **8** | **0** | **8** | **0** | **13,713** | **0%** | **0%** |
| AFTER, pair 1 | 4 | 3 | 1 | 1,536 | 6,661 | 75% | 23.0596% |
| AFTER, pair 2 | 4 | 4 | 0 | 2,048 | 6,773 | 100% | 30.2377% |
| **AFTER total** | **8** | **7** | **1** | **3,584** | **13,434** | **87.5%** | **26.6786%** |

Thus the defensible historical result is limited to the summary layer:
call-hit rate `0% -> 87.5%` and current-formula token-cache ratio
`0% -> 26.6786%`.

## Six-question audit

1. **Real old-vs-optimized prompt ordering: PASS.** Commit ancestry and source diff
   establish the intended structure change. This is not merely a relabelled
   cold/warm comparison.
2. **Workload comparability: PASS for `wiki_summary`, confounded for all-Wiki totals.**
   Corpus, four summary calls per trial, pipeline, markers within each pair, and model
   are controlled. Generated pages differ (BEFORE 59, AFTER 49), causing PageModify
   and all-Wiki call counts to differ; those absolute totals cannot support an
   optimization claim.
3. **Model comparability: PASS.** Both modes record the same model config, resolved
   model, provider path, and tenant.
4. **Metric semantics: PASS after explicit recomputation.** The current denominator
   was applied. Equality with the old denominator is data-specific, not assumed.
5. **Data provenance: PASS for the summary-layer claim, with limitations.** Artifacts
   contain SQL and DB-derived aggregates, are committed, and the still-present DB
   rows reproduce the 8-vs-8 summary counts/tokens. However, the JSON omits exact
   timestamps/usage IDs, `raw_queries.sql` omits an explicit model filter, and some
   `wiki_index_intro` totals show boundary carry-over; therefore the all-Wiki artifact
   is not accepted as clean trial-isolated provenance.
6. **Commit linkage: PASS.** Before/after commits and the precise stable-prefix move
   are directly recoverable from Git, independently of the README narrative.

## Confounders and limitations

- The recorded plan calls itself `AB/BA`, but both actual pair sequences are
  `AFTER then BEFORE`. It is not a balanced crossover.
- DeepSeek's provider-native prefix cache cannot be cleared; pair 2 is warmed by
  earlier requests. The order effect is conservative for pair 1 (AFTER ran first)
  but still prevents a clean cold-state randomized causal estimate.
- Only eight successful summary calls exist per mode; provider variance remains.
- Different generated page counts confound PageModify/all-Wiki tokens, costs, and
  latency. No overall Wiki improvement is claimed.
- Exact per-trial timestamps and row IDs were not retained in the original JSON.
- The four-document corpus is small and contains no images.

## Relationship to Final Candidate A/B

The historical evidence answers directionality: moving shared summary instructions
ahead of dynamic content changes `wiki_summary` from 0/8 hits to 7/8 hits. The Final
Candidate A/B at `cd21908a` answers reproducibility of the resulting implementation:
both independent arms show `wiki_summary` 4/4 hits with 2,048 cache-read tokens, and
both show whole-Wiki eligible hit rate 57/59 (`0.966102`). The Final Candidate A/B is
not another old/new comparison and must not be presented as one.

## Verdict

**VALID WITH LIMITATIONS** for the layer-scoped historical claim that the optimized
ordering improves `wiki_summary` provider-prefix reuse. It is not valid evidence that
all Wiki stages, PageModify, overall latency, or total cost improved.
