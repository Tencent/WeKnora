# Wiki Prompt Cache — Final Candidate A/B Pair (AB sequence)

- **Repository**: `qyc060615/WeKnora` · Branch `feat/topic3-evaluation`
- **Commit**: `cd21908a652d1330499986e191fbd704b7d9c13b`
- **Provider**: `deepseek-v4-pro` (DeepSeek automatic prefix caching / Context Caching on Disk)
- **Design**: controlled A/B pair, AB sequence. The two arms differ **only** in the
  cohort marker placed in `wiki_config.content_instructions` (which enters the static
  instruction prefix of every Wiki LLM prompt). Corpus, models, temperature, chunking
  and the Wiki pipeline are identical across arms.

## 1. Trial summary

| | A (ab-a1) | B (ab-b1) |
|---|---|---|
| marker | `final-wiki-cohort-a` | `final-wiki-cohort-b` |
| knowledge_base_id | `6b01c0a2-a53e-47cb-9ea7-833c6bea6d22` | `bcebcf3d-6534-46c7-885b-5ac08c3140e5` |
| started_at (UTC) | 2026-09-07T09:10:13Z | 2026-09-07T10:04:03Z |
| finished_at (UTC) | 2026-09-07T09:25:43Z | 2026-09-07T10:22:55Z |
| wall time | 930.4 s (~15.5 min) | 1132.5 s (~18.9 min) |
| knowledge files | 4/4 `completed` | 4/4 `completed` |
| wiki_pages | 50 (`published`) | 50 (`published`) |
| provider timeout calls (retained) | 1 | 10 |

## 2. Prompt-cache metrics (scope: `wiki_*` purposes, `call_type=chat`)

| Metric | A | B |
|---|---:|---:|
| eligible calls (hit + miss) | 59 | 59 |
| hit calls | 57 | 57 |
| miss calls | 2 | 2 |
| timeout calls (excluded, NULL status) | 1 | 10 |
| **Prompt Cache Call Hit Rate** | **0.966102** | **0.966102** |
| input_tokens | 171 319 | 175 740 |
| cache_read_tokens | 81 408 | 94 976 |
| cache_write_tokens | 0 | 0 |
| **Prompt Token Cache Ratio** | **0.475184** | **0.540435** |
| total latency_ms | 3 053 857 | 5 358 023 |

- The 2 `miss` calls per arm are **structural**, not regression: `wiki_taxonomy_plan`
  and `wiki_index_intro` are each invoked exactly once per ingest, so they have no
  repeated prefix to reuse. All 57 remaining calls hit the provider prefix cache.
- `cache_write_tokens` is 0 in both arms: DeepSeek's automatic prefix caching does not
  report explicit cache writes; reuse is observed via `cache_read_tokens`.

## 3. Per-purpose reuse (success calls)

| purpose | A calls / status / cache_read | B calls / status / cache_read |
|---|---|---|
| wiki_candidate_slug | 4 × hit / 8 576 | 4 × hit / 8 576 |
| wiki_chunk_citation | 4 × hit / 2 560 | 4 × hit / 2 560 |
| **wiki_summary** | **4 × hit / 2 048** | **4 × hit / 2 048** |
| wiki_page_modify | 45 × hit / 68 224 | 45 × hit / 81 792 |
| wiki_taxonomy_plan | 1 × miss / 0 | 1 × miss / 0 |
| wiki_index_intro | 1 × miss / 0 | 1 × miss / 0 |

## 4. Conclusion (data-supported scope only)

1. **`wiki_summary` layer demonstrates stable prompt-cache reuse**: both arms report
   4/4 `hit` with identical `cache_read_tokens = 2048` for the summary stage. This is
   the strongest, most consistent signal in the trial and matches the prior
   before/after evidence that the summary layer benefits from cache-friendly prompts.
2. **Prompt-cache hit behaviour is reproducible** across two independent runs under
   the same frozen configuration: `Call Hit Rate = 0.966102` in both arms, with the
   same 57-hit / 2-miss split.
3. **Token Cache Ratio shows run-to-run variance** (0.475 vs 0.540), driven almost
   entirely by `wiki_page_modify` (`cache_read_tokens` 68 224 vs 81 792). This is
   consistent with the generation-side variance already documented in the benchmark
   three-run evidence; it is **not** interpreted as a before/after improvement.
4. **`wiki_page_modify` is not claimed as improved**: no before/after contrast exists
   in this single-commit cohort A/B, so no per-stage "improvement" is asserted.
5. **Latency is reported as auxiliary only.** The B arm ran during a provider-degraded
   window (10 provider timeouts vs 1 in A), which extends wall time and total latency
   without changing the cache hit rate. These timeouts are retained, not hidden.

**Permitted statement**: *The Wiki summary layer demonstrates prompt-cache reuse;
prompt-cache call hit rate (0.966) is reproducible across two controlled arms.*

**Not asserted**: *All Wiki stages achieve significant cache improvement.*

## 5. BA replication

Not run. The B arm's provider-degraded window (10 timeouts) inflated a single trial to
~19 min and then provider limits were reached. The A/B pair is valid evidence of
Final-Candidate `wiki_summary` reuse, but it does **not** satisfy the requested full
AB/BA crossover; that acceptance item remains incomplete. The historical
`artifacts/wiki_prompt_cache_v1/` before/after data remains available as auxiliary
sanity evidence but is not claimed as a Final Candidate run.
