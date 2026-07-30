# VLM image artifact cache

WeKnora persists the canonical OCR and caption produced for an image. The cache boundary is the exact byte slice passed to the VLM: `image_digest = SHA256(exact_image_bytes)`. URLs, temporary paths, filenames, chunk IDs, knowledge IDs, image indexes, and task IDs are never image identities.

## Artifacts and keys

OCR and caption are separate provider calls and therefore use separate artifact kinds: `multimodal.ocr` and `multimodal.caption`. Both store JSON encoded as `multimodal-artifact/v1`:

```json
{"schema_version":"multimodal-artifact/v1","result":"canonical text","present":true}
```

`present` distinguishes a valid empty model result from an absent/corrupt result. Unknown schemas, malformed JSON, missing `present`, or a non-JSON encoding fail closed and are not returned as hits.

The `artifactkey.KeyInput` contains the kind, stable `tenant:<id>` scope, image digest, resolved VLM model ID, model revision when available, explicit prompt version, output-affecting config digest, and producer version. The current VLM interface does not expose a model revision, so PR5 deliberately uses a stable empty `ModelRevision`; providers can populate it in a later explicit interface change. PR5 uses `multimodal-prompt/v1` and `multimodal-producer/v1`; neither is a commit hash or build timestamp. The config digest covers the effective prompt, including language, source-specific OCR instructions, and custom instructions, but excludes API keys, BaseURL credentials, embedding settings, chunk settings, Wiki settings, and unrelated ingestion configuration.

## Canonical freeze and coordination

On a miss, one worker claims the artifact, calls the VLM, applies the existing conservative OCR normalization (or caption newline/edge-whitespace normalization), and completes the artifact. Every later hit uses those stored bytes without calling or rewriting through the VLM. Empty results are reusable.

A concurrent worker that observes `busy` polls for a bounded period while respecting context cancellation. It never bypasses the cache to issue a duplicate request. Expired leases may be taken over. The active worker renews its two-minute lease every two-thirds minute while the VLM is running; loss of ownership prevents completion, so an old worker cannot overwrite a takeover result. Provider failures mark the artifact failed with a restricted error summary and a later attempt may claim it again.

The production defaults are a two-minute lease, three-minute bounded wait, 100 ms poll interval, and two-second failure-cleanup timeout. They are instance-level service settings with zero-value defaults; tests can shorten them without global mutable state. When the request context is already cancelled, failure persistence uses a cleanup context with that strict timeout rather than an unbounded background context.

Repositories and corrupt payloads fail closed. Artifact payloads, prompts, image bytes, complete digests, credentials, and owner tokens are not logged or attached to observations. Tenant scope prevents cross-tenant reuse, while identical images referenced by different knowledge documents in one tenant share artifacts.

The production container always injects `DerivedArtifactRepository`, and the production constructor rejects a nil repository. The nil fallback inside `cachedMultimodalPredict` exists only for legacy tests or explicit non-production direct struct construction; repository construction or runtime failures never silently downgrade production ingestion to uncached VLM calls.

## Observation semantics

A hit reports `cache_status=hit`, `reused_items=1`, `computed_items=0`, and no VLM request. A successful miss reports `cache_status=miss`, `computed_items=1`, and the actual provider request recorded by the existing observed/counting VLM wrapper. Claim, busy, polling, completion, and lease renewal are not model requests. Infrastructure or provider failures report an error/failed event without exposing cache content. OCR and Caption have separate prefixed observation fields; the former top-level `cache_status=not_supported` field was removed because it falsely described an image operation whose child operations are cached and could not unambiguously represent mixed OCR/Caption outcomes.

## Exact invalidation examples

| Change | Result |
| --- | --- |
| Image bytes change | VLM recomputes |
| Resolved VLM model changes | VLM recomputes |
| Prompt version or effective prompt config changes | VLM recomputes |
| Producer/schema/preprocessing version changes | VLM recomputes |
| Embedding model changes | VLM artifact survives |
| Chunk size changes | VLM artifact survives |
| Wiki configuration changes | VLM artifact survives |
| Ordinary rebuild/retry with identical dependencies | Cache hit |
