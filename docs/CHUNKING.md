# Chunking Guide

How WeKnora splits uploaded documents before embedding, why the defaults
are what they are, and when to change them.

## Why chunking matters

Retrieval-Augmented Generation (RAG) works by embedding small slices of
your documents into a vector index, then pulling the most relevant
slices back at query time. The way a document is sliced — chunk size,
overlap, where the cuts fall — directly drives retrieval recall and the
quality of the answers your LLM produces.

Empirically (Vecta Feb-2026 benchmark across 50 academic papers):
recursive splitting at ~512 tokens with ~15% overlap is the strongest
single-knob baseline at 69% end-to-end accuracy, beating semantic
chunking and over-engineered hybrids. WeKnora uses that as the
foundation and layers smarter strategies on top when the document gives
us structural cues.

## Adaptive 3-tier chunking

Set per knowledge base via the editor's **Chunking** sidebar (or the
`strategy` field on the KB-config API).

| Strategy | When picked | What it does |
|----------|-------------|--------------|
| `auto` (recommended) | Default for new KBs | Profiles the document and picks the strongest tier from the chain below. |
| `heading` | Markdown-style structure | Splits at `#` / `##` / `###` boundaries. Each chunk gets a breadcrumb context header (`# Top > ## Section`) prepended at embedding time. |
| `heuristic` | PDF-style structure | Splits at form-feeds (page breaks), numbered sections, multilingual chapter markers (DE / EN / ZH), all-caps titles, and visual separators. |
| `legacy` (= `recursive`) | Anything else, or as fallback | Pure recursive separator-based splitter — newest version with priority recursion and overlap-cap fixes. |

A document profiler runs first and counts structural signals (Markdown
headings, form-feeds, chapter markers per language, all-caps lines,
visual separators, blank-line bursts). Auto-strategy picks the tier
chain based on those counts; a validator rejects obviously broken
output (e.g. the heading splitter producing 200 single-line chunks)
and falls through to the next tier.

## Settings reference

### Core

| Setting | Range | Default | Sweet spot for… |
|---------|-------|---------|-----------------|
| **Chunk size** | 100–4000 chars | 512 | Default works for most cases. 200–400 for FAQs / atomic Q&A. 1000–2000 for narrative documents. |
| **Chunk overlap** | 0–500 chars | 80 (~15%) | 0 for FAQs and structured records. 80 (default) for general documents. 150–200 for argumentative texts where reasoning crosses chunks. |
| **Separators** | string list | `["\n\n", "\n", "。", "！", "？", ";", "；"]` | Order matters — splitter tries higher-priority separators first and only falls back to lower ones when a piece is still oversize. |

### Parent-Child chunking

Two-level retrieval: **child chunks** (small, embedded for vector match)
and **parent chunks** (larger, returned to the LLM for context).

| Setting | Range | Default | Notes |
|---------|-------|---------|-------|
| **Enable parent-child** | toggle | on | Recommended for documents > 10 pages. Skip for short FAQs to halve storage cost. |
| **Parent chunk size** | 512–8192 chars | 4096 (~1000 EN tokens) | Larger for long-context LLMs (Claude, GPT-4-Turbo). Smaller (1024–2048) for local LLMs with 4k contexts. |
| **Child chunk size** | 64–2048 chars | 384 (~80 EN tokens) | 128–256 for Q&A-style precise matching. 512–1024 if your embedder accepts >1000 tokens (E5 / BGE-large). |

### Advanced

| Setting | Range | Default | When to set |
|---------|-------|---------|-------------|
| **Token limit** | 0–8192 | 0 (off) | Activate when your embedding model has a small token cap. See table below. |
| **Languages** | `de` / `en` / `zh` (multi-select) | empty (auto-detect) | Set explicitly for homogeneous corpora to narrow heuristic patterns. |

#### Token-limit guide per embedding model

| Embedder | Token limit | Recommended `tokenLimit` setting |
|----------|-------------|----------------------------------|
| OpenAI `text-embedding-3-small/large` | 8191 | **0 (leave off)** |
| Anthropic Voyage-3 | 32000 | **0** |
| Jina-embeddings-v3 | 8192 | **0** |
| Cohere `embed-multilingual-v3` | 512 | **400** |
| BGE-base / BGE-large / E5-large | 512 | **400** |
| Sentence-Transformer `all-MiniLM-L6-v2` | 256 | **200** |

Rule of thumb: leave at 0 for any modern embedder with > 2000 tokens.
Activate to 80% of the model's hard limit for smaller embedders so
chunks always fit even for CJK content (which is denser per character).

## Use-case presets

| Workload | Strategy | ChunkSize | Overlap | Parent-Child |
|----------|----------|-----------|---------|--------------|
| FAQ / Q&A knowledge base | `auto` (likely picks legacy) | 200–400 | 0 | off |
| Markdown documentation / wikis | `auto` (picks heading) | 512 | 80 | on |
| PDF reports with page breaks | `auto` (picks heuristic) | 800–1200 | 100–150 | on |
| Long-form narrative (books, articles) | `auto` (picks recursive) | 1000–2000 | 150–200 | on |
| Code documentation | `legacy` | 800 | 100 | optional |
| Mixed-language corpus | `auto`, languages = empty | 512 | 80 | on |
| Tabular reports / CSV-derived | `legacy` | 400 | 0 | off |

## Debugging in the UI

The KB editor's **Chunking** sidebar has a "Test with sample text"
collapsible at the bottom:

1. Paste a Markdown / plain-text snippet (max 64 KB).
2. Click **Run preview**.
3. The panel shows:
   - Selected strategy tier as a colored tag
   - Tiers that were rejected and why (e.g. "too many tiny chunks")
   - Document profile (heading counts, form-feeds, chapter markers,
     detected languages)
   - Size statistics over the full chunk set (avg / min / max / stddev)
   - Per-chunk cards with size in chars + approximate tokens, position
     range, the section breadcrumb (when set), and a content preview

This runs read-only against a goroutine-isolated splitter pass (5s
timeout) — no DB writes, no embedding API calls. Use it to compare
configurations against the same sample before triggering a re-upload.

## API

```http
PUT /api/v1/initialization/config/:kbId
Authorization: Bearer <jwt>
Content-Type: application/json

{
  "documentSplitting": {
    "chunkSize": 512,
    "chunkOverlap": 80,
    "separators": ["\n\n", "\n", "。", "！", "？", ";", "；"],
    "strategy": "auto",
    "tokenLimit": 0,
    "languages": ["de", "en"],
    "enableParentChild": true,
    "parentChunkSize": 4096,
    "childChunkSize": 384
  }
}
```

The `strategy`, `tokenLimit`, and `languages` fields use pointer-based
DTOs server-side: omitting them in the payload means "no change",
sending an empty string / 0 / [] explicitly resets to default.

The preview endpoint accepts the same payload shape under
`POST /api/v1/chunker/preview` with an additional `text` field.

## Known trade-offs

- **Tier-1 heading-aware chunking** prepends the section breadcrumb to
  the embedding input, costing ~5% more tokens per chunk in exchange
  for ~30–50% fewer chunks on structured documents (net token savings
  on storage and at query time).
- **Strategy switches do not auto-reindex** existing documents. After
  changing a KB's strategy, re-upload affected files (or trigger
  re-indexing via the UI) to apply the new chunking.
- **OCR artifacts in PDFs** (vertical layout text broken character-
  by-character into separate lines) cannot be fixed by any splitter —
  this is a parser-side limitation. The heuristic tier still keeps
  chunks aligned to page boundaries, which mitigates the worst cases.
- **The `recursive` strategy value** exists in the API for completeness
  but is intentionally hidden from the UI: it is functionally near
  `legacy` and adding a fifth dropdown option dilutes the meaningful
  choice between automatic / Markdown / heuristic / legacy.
