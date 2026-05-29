---
name: weknora-rag-search
description: Use when retrieving from or asking questions against a WeKnora knowledge base via the `weknora` CLI — and especially when unsure whether to use `chat`, `session ask`, or `search chunks` for a given goal.
metadata:
  tested_against: v0.9
---

# WeKnora — retrieval & RAG queries

**REQUIRED BACKGROUND:** read the `weknora-shared` skill first (auth, `--kb`
resolution, the JSON envelope, exit codes, streaming/NDJSON output).

WeKnora gives you several ways to "ask about a knowledge base." Picking the wrong
one wastes turns or returns the wrong shape. Use the decision table.

## Pick the command by your goal

| Your goal | Command | LLM synthesis? | Returns |
|---|---|---|---|
| Natural-language **answer** grounded in a KB | `chat "<q>" --kb <kb>` | yes | streaming answer + references |
| Answer via a **custom agent** (its own KB scope, tools, web search) | `session ask --agent <id> "<q>"` | yes (+ tools) | streaming answer + tool events |
| **Raw context chunks** to reason over yourself (no answer) | `search chunks "<q>" --kb <kb>` | no | ranked chunk list |
| Which **documents** match a keyword (title/filename) | `search docs "<q>" --kb <kb>` | no | document list |
| Find a **knowledge base** by name | `search kb "<q>"` | no | KB list |
| Find a past **session** by title | `search sessions "<q>"` | no | session list |

### The three decisions that matter

1. **Answer vs raw context.** Want a written answer → `chat` / `session ask`.
   Want chunks to feed into your *own* reasoning (e.g. you'll synthesize across
   sources) → `search chunks`. Don't call `chat` just to read source text.
2. **`chat` vs `session ask`.** `chat` = plain KB RAG Q&A. `session ask --agent
   <id>` = invoke a *configured custom agent* (it may scope its own KBs, call
   tools, do web search). If the user set up an agent for this, prefer it
   (`weknora agent list` to find ids); otherwise `chat`.
3. **One-shot vs multi-turn.** Both `chat` and `session ask` print an `init`
   event with a `session_id`. Pass `--session <id>` on the next call to continue
   the conversation. See `references/chat.md`.

## Safety / Gotchas

- `--kb` is **required** for `chat`, `search chunks`, `search docs` (no implicit
  project-link fallback) — an unset `--kb` is exit 2, a bad name is exit 1
  (`local.kb_not_found`). Resolve names with `weknora kb list` / `search kb`.
- `chat` / `session ask` stream **NDJSON** by default. Parse line-by-line; keep
  the `init` event's `session_id` **and** `message_id`. Use `--format text` only
  for a human transcript.
- A stalled stream is not stopped by Ctrl-C (that just drops your local
  connection; the server keeps generating + billing). Stop it server-side:
  `weknora session stop <session-id> --message <message-id>` (ids from `init`).
  Re-attach to a stream with `weknora session continue-stream <session-id>
  --message <message-id>`.
- `search chunks --limit` defaults to **8** (tuned for an LLM context window);
  the `search docs/kb/sessions` lists default to 30. Tune retrieval with
  `--vector-threshold` / `--keyword-threshold`, or `--no-vector`/`--no-keyword`
  to disable a channel. Details: `references/search-chunks.md`.

## Quick examples

```bash
# raw retrieval to reason over
weknora search chunks "retry backoff policy" --kb engineering --limit 12

# grounded answer (human transcript)
weknora chat "How do we handle retries?" --kb engineering --format text

# continue the conversation (session id from the prior init event)
weknora chat "And the max attempts?" --kb engineering --session sess_abc

# answer via a custom agent
weknora session ask --agent ag_123 "Summarize this quarter's incidents"
```
