# Responses proof via muse-spark (prototype, issue #13)

## Result: PROVEN — 200 + `proof-ok` text reply

- `POST https://opencode.ai/zen/go/v1/responses`
  `{"model":"muse-spark-1.3-contributor","input":"Reply with exactly: proof-ok","max_output_tokens":300}`
- → `STATUS 200`, response `status: completed`, `output[0].content[0] = {type: output_text, text: "proof-ok"}`
- Usage: 13 in / 265 out (253 reasoning). No key material stored here.

## Request shape that works (redacted)

```python
body = {"model": "muse-spark-1.3-contributor",
        "input": "Reply with exactly: proof-ok",
        "max_output_tokens": 300}
headers = {"Authorization": "Bearer $OPENCODE_API_KEY",
           "Content-Type": "application/json",
           "User-Agent": "WeKnora-probe/1.0"}
```

Key used: the already-configured WeKnora model credential (DB `models.parameters->>'api_key'`,
AES-256-GCM via `SYSTEM_AES_KEY`), decrypted in-process only, never written anywhere.

## Findings for the build (see UX ticket #12)

1. Muse Spark is Responses-only — `/chat/completions` 500s, `/responses` 200s. A translating
   provider (or native `/responses` adapter) is REQUIRED, not optional.
2. Heavy reasoning: 253/265 output tokens were reasoning on a trivial prompt. WeKnora's
   connection test sends `max_tokens: 1` — that would return `status: incomplete`
   (`max_output_tokens`) on reasoning models. The test must allow more tokens or treat
   `incomplete/max_output_tokens` + valid Response envelope as success.
3. First probe with `max_output_tokens: 20` returned `status: incomplete`, empty `output[]`,
   `error: null` — still HTTP 200. Envelope-valid ≠ text-present; parse accordingly.
