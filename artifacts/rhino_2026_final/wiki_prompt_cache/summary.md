# Wiki Prompt Cache Evidence

## Historical Before/After

- Before commit: `3f9a054ec94e92c3089a6574281aa09760068e38`
- After commit: `22f9120216d554181db041b24e3e191363c34366`
- Accepted scope: `wiki_summary` only

| Mode | Calls | Hit/Miss | Call Hit Rate | Token Cache Ratio |
|---|---:|---:|---:|---:|
| BEFORE | 8 | 0/8 | 0% | 0% |
| AFTER | 8 | 7/1 | 87.5% | 26.6786% |

Verdict: **VALID WITH LIMITATIONS**. The Git diff proves that shared instructions
moved before dynamic document content, and existing DB rows reproduce the summary
layer metrics under the current formula.

## Historical Limitations

- Both historical pairs ran AFTER→BEFORE, not as a balanced crossover.
- Provider cache could not be explicitly cleared; warm-state/order effects remain.
- Sample size is eight summary calls per side.
- Generated page counts differed: BEFORE 59, AFTER 49.
- Original trial JSON lacks exact time windows and usage IDs.

Do not extrapolate the result to all Wiki stages, `wiki_page_modify`, latency, or
cost.

## Final Candidate A/B

| Metric | A | B |
|---|---:|---:|
| Eligible calls | 59 | 59 |
| Hit/miss | 57/2 | 57/2 |
| Eligible hit rate | 96.6102% | 96.6102% |
| `wiki_summary` | 4/4 hit | 4/4 hit |
| Summary cache-read tokens | 2,048 | 2,048 |
| Provider timeout rows | 1 | 10 |

## Final Conclusion

The historical experiment supports the optimization direction. The Final Candidate
A/B supports reproducible optimized prompt-cache reuse. A full AB/BA crossover was
not completed, and no claim is made that every Wiki stage improved.

## Evidence

- [artifacts/rhino_2026_final/wiki_prompt_cache/historical_before_after_audit.md](historical_before_after_audit.md)
- [artifacts/rhino_2026_final/wiki_prompt_cache/ab/a1.json](ab/a1.json)
- [artifacts/rhino_2026_final/wiki_prompt_cache/ab/b1.json](ab/b1.json)
- [artifacts/rhino_2026_final/wiki_prompt_cache/ab/comparison.json](ab/comparison.json)
- [artifacts/rhino_2026_final/wiki_prompt_cache/ab/comparison.md](ab/comparison.md)
