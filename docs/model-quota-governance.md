# Model token counting and quota governance

WeKnora governs outbound chat, embedding, and VLM requests at the model-client
boundary. This is the only layer that sees interactive conversations and every
background ingestion/enrichment path, so limits cannot be bypassed by choosing
a different worker queue.

## Token accounting

Three values have deliberately different meanings:

- **Preflight input estimate** counts the complete request shape: message
  framing, text, tool definitions and schemas, tool calls/results, response
  format, reasoning replay, and multimodal inputs.
- **Reserved output tokens** come from `max_completion_tokens` / `max_tokens`,
  with a conservative fallback when the caller does not set one.
- **Provider usage** is the authoritative post-request value used for tracing
  and TPM reconciliation. Embedding providers that return usage are captured
  directly; other providers use their model tokenizer as a fallback.

Agent context capacity is calculated as:

```text
input budget = model context window - reserved output - 2% safety margin
```

`parameters.context_window_tokens` should match the selected model/deployment.
Existing records that omit it use the application fallback of 200,000 tokens.

## Quota groups

Models using the same upstream account or deployment quota should set the same
`parameters.quota_group`. Groups are automatically namespaced by tenant. When
the field is empty, the model ID is used, preserving independent per-model
limits.

Each group can enforce:

- `max_concurrency`: total in-flight interactive and background calls;
- `requests_per_minute`: RPM/QPM token bucket;
- `tokens_per_minute`: TPM token bucket;
- `interactive_concurrency_reserve`: concurrency slots background tasks cannot
  consume.

For model fields, `0` inherits the system default and `-1` disables that
dimension. Positive values override the system default. Global RPM and TPM
default to `0` (unlimited) so upgrading does not unexpectedly reject existing
traffic; global concurrency remains `8`.

TPM admission reserves estimated input plus the maximum output budget before
the request. When the provider returns usage, the limiter refunds unused tokens
or charges an overage. A request whose reservation is larger than the entire
TPM capacity is rejected immediately instead of waiting forever.

## Distributed and Lite modes

Redis deployments use one atomic Lua admission for concurrency, RPM, and TPM,
with renewable concurrency leases so crashed workers recover automatically.
Limiter backend failures are explicit errors: a Redis outage must not silently
disable configured provider protection.

Lite mode uses the same token-bucket and priority semantics in process. Limits
are evaluated on every admission, so edits to System settings take effect
without recreating channels or restarting the process.

## Configuration

System defaults can be set in Settings -> System settings or via environment:

```text
WEKNORA_MODEL_MAX_CONCURRENCY=8
WEKNORA_MODEL_REQUESTS_PER_MINUTE=0
WEKNORA_MODEL_TOKENS_PER_MINUTE=0
WEKNORA_MODEL_INTERACTIVE_CONCURRENCY_RESERVE=1
```

Start with provider-published account limits, group every model sharing that
account, then monitor provider 429 responses and observed usage before raising
burst/concurrency values.
