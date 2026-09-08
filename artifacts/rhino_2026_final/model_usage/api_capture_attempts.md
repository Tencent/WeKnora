# Runtime API Capture Attempts

Two attempts were made to capture an authenticated response from:

```text
GET /api/v1/model-usage/analytics
```

Both commands were stopped before execution because the external-command permission
review timed out. No request was sent, no response artifact was created, and no
credential was written. The DB snapshot and deterministic backend/frontend contract
tests remain valid, but they do not constitute runtime API or Dashboard proof.
