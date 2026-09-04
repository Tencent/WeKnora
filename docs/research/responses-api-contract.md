# Responses API contract + opencode zen behavior (research, issue #10)

Primary sources only: OpenAI API reference (developers.openai.com), openai-python SDK (spec-generated types), opencode.ai/docs (zen, go). No API keys pasted.

## 1. POST /responses request/response shapes

- Endpoint: `POST https://api.openai.com/v1/responses` — "Creates a model response" from text/image inputs; supports function calling and built-in tools (web/file search). ([create ref](https://developers.openai.com/api/reference/resources/responses/methods/create/))
- Required fields: `model` + `input` (string or structured input items). Optional: `instructions`, `stream`, `tools`, `tool_choice`, `reasoning`, `text`, `temperature`, `top_p`, `max_output_tokens`, `max_tool_calls`, `store`, `background`, `conversation` / `previous_response_id`, `metadata`, `service_tier`, `truncation`, `include`, `prompt_cache_key`, `safety_identifier`, `stream_options`. ([create ref](https://developers.openai.com/api/reference/resources/responses/methods/create/), [response_create_params.py](https://github.com/openai/openai-python/blob/main/src/openai/types/responses/response_create_params.py))
- Non-streaming response: `Response` object with `id`, `created_at`, `model`, `status` (queued|in_progress|completed|incomplete|failed), `output` (typed array), `error`, `incomplete_details`, `usage`, `metadata`, `previous_response_id`. ([response.py](https://github.com/openai/openai-python/blob/5e8f09c2/src/openai/types/responses/response.py), [migrate guide](https://developers.openai.com/api/docs/guides/migrate-to-responses))

## 2. Streaming event protocol (SSE)

- Opt in with `stream: true`; transport is server-sent events; typed events with `type` discriminator. ([streaming guide](https://developers.openai.com/api/docs/guides/streaming-responses))
- Lifecycle: `response.created` → `response.in_progress` → (`response.output_item.added` / `response.content_part.added` / `response.output_text.delta` …) → `response.output_text.done` / `response.content_part.done` / `response.output_item.done` → `response.completed` (`incomplete` / `failed` on abnormal end). Deltas ordered by `sequence_number`. ([streaming events ref](https://developers.openai.com/api/reference/resources/responses/streaming-events/))
- Tool events: `response.function_call_arguments.delta|done`, `response.file_search_call.in_progress|searching|completed`, `response.web_search_call.in_progress|…`, `response.refusal.delta|done`, plus `error` stream event. ([streaming events ref](https://developers.openai.com/api/reference/resources/responses/streaming-events/))

## 3. tools / reasoning / text fields

- `tools`: array of built-in, MCP/connector, or function tools; `tool_choice` selects; `parallel_tool_calls`, `max_tool_calls` bound execution. ([response_create_params.py](https://github.com/openai/openai-python/blob/main/src/openai/types/responses/response_create_params.py))
- `reasoning`: config (`effort`, summary options) for reasoning models. ([reasoning guide](https://developers.openai.com/api/docs/guides/reasoning))
- `text`: plain text vs structured JSON (`format`), verbosity controls.

## 4. Error shapes

- `{"error": {"message", "type", "param", "code"}}` (code/param optional). ([error codes guide](https://developers.openai.com/api/docs/guides/error-codes), [_exceptions.py](https://github.com/openai/openai-python/blob/main/src/openai/_exceptions.py))
- 401 invalid auth, 403 unsupported region, 429 rate/quota/spend, 500 server error (retry), 503 `server_is_overloaded`. Unknown model on OpenAI proper is 404 `model_not_found` (secondary sources).

## 5. opencode zen: base path, auth, go scope, muse-spark-1.3-contributor

- Caller auth: `Authorization: Bearer <OPENCODE_API_KEY>` (Zen API key from opencode.ai/auth). ([zen](https://opencode.ai/docs/zen/))
- Zen base `https://opencode.ai/zen/v1/` with per-model routes: `/responses`, `/messages`, `/models/<id>`, `/chat/completions`. List: `GET /zen/v1/models`. Config `opencode/<model-id>`. ([zen](https://opencode.ai/docs/zen/))
- Go scope: same key + $10/mo subscription; base `https://opencode.ai/zen/go/v1/`; list `GET /zen/go/v1/models`; config `opencode-go/<model-id>`; send proper UA + `x-opencode-session`. ([go](https://opencode.ai/docs/go/))
- Go: `muse-spark-1.3-contributor` at `/zen/go/v1/responses` (`@ai-sdk/openai`); Zen PAYG: `muse-spark-1.3-contributor-free` at `/zen/v1/responses`. Go limits: $12/5h, $30/wk, $60/mo.

## 6. Why POST /chat/completions could 500 for a Responses-only model

- No primary source documents a 500. Zen routes Muse Spark only to `/responses` (both scopes), so `/chat/completions` is undocumented for it — a 500 is an undocumented gateway/proxy failure, not a contracted error. Inference only; needs repro. ([zen](https://opencode.ai/docs/zen/), [go](https://opencode.ai/docs/go/), [error codes guide](https://developers.openai.com/api/docs/guides/error-codes))

## Gaps

- Exact required-vs-optional of `input` when `previous_response_id`/`conversation` is set — re-check live reference.
- Actual zen status code for `/chat/completions` with a Responses-only model — needs live repro (see prototype ticket).
