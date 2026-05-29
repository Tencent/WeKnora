# `search chunks` — hybrid retrieval (no LLM)

Raw vector + keyword retrieval against one knowledge base. Returns ranked chunks
for you to reason over; it does NOT synthesize an answer (use `chat` for that).

## Command & flags

```
weknora search chunks "<query>" --kb <name-or-id> [flags]
```

| Flag | Default | Meaning |
|---|---|---|
| `--kb` | (required) | KB name or UUID |
| `--limit`, `-L` | 8 | max chunks returned (1..1000); 8 is tuned for an LLM context window |
| `--vector-threshold` | 0 (off) | min vector similarity, per-channel pre-fusion |
| `--keyword-threshold` | 0 (off) | min keyword score, per-channel pre-fusion |
| `--no-vector` | false | disable the vector channel (keyword-only) |
| `--no-keyword` | false | disable the keyword channel (vector-only) |

You cannot disable both channels. `--limit` is a hard cap on returned chunks
applied client-side (the server may internally retrieve a larger pool for recall,
then the CLI trims).

## Output (`--format json`)

`data` is an array of chunk objects; `meta.count` is the number returned.

```jsonc
{"ok":true,"meta":{"count":3},"data":[
  {"id":"chunk_…","content":"…","knowledge_id":"doc_…","knowledge_title":"…",
   "chunk_index":4,"score":0.82,"match_type":"hybrid","chunk_type":"text"}
]}
```

- `score` is the fused rank; `match_type` indicates which channel(s) hit.
- `knowledge_id` / `knowledge_title` attribute the chunk to its source document.
- Project just what you need with `--jq`, e.g.
  `weknora search chunks "q" --kb eng --jq '.data[] | {score,content}'`.

## When to use vs alternatives

- Need an **answer** → `chat` (it does retrieval + synthesis internally).
- Building your **own** prompt/context from sources → `search chunks` (this).
- Finding **which documents** exist by keyword → `search docs --kb <kb>`.
- Inspecting/debugging a specific document's chunks → `chunk list --doc <id>`.
