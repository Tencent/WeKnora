# Chat attachment boundaries

Attachments remain readable without a sandbox. Durable storage and the temporary-document
row are authoritative; sandbox files are disposable copies.

| Responsibility | Owner |
| --- | --- |
| Upload, session access, retention, task scheduling | `service/temporary_document.go` |
| Parser, OCR, image extraction, chunking | `service/temporary_document_parse.go` |
| Quick Answer excerpts and Agent attachment manifest | `service/temporary_document_prompt.go` |
| Authorized reads, bounded parse waits | `attachment/Reader` |
| Image preparation, scoped observations and reuse | `attachment/ImageReader` |
| Observation persistence and concurrent updates | `repository/attachment_descriptions.go` |
| Turn-start original/parsed-file reconciliation | `service/session_attachment_staging.go` |
| Copies produced by a successful read | `service/attachment_sandbox.go` |
| URI routing, pagination, image numbering and tool output | `agent/tools/read_file_attachment.go` |
| Vision transport and fallback for other tools | `agent/engine.go`, `agent/tool_images.go` |

`agent_attachments.go` wires session-scoped storage adapters into `Reader`, then
passes only `interfaces.AttachmentReader` to `read_file`. The reader authorizes
against persisted message attachments before looking up the tenant's document,
including documents inherited from a parent session. Neither the tool nor the
Agent engine queries attachment repositories.

## Read and synchronization flow

1. At turn start, stage originals and already-ready parsed text if the selected
   sandbox supports session input files. Host workspaces have no input directory
   and are never used for attachment staging.
2. `read_file attachment://...` resolves the session attachment and waits within
   the configured parse budget. A successful read projects newly-ready parsed
   text immediately, including when parsing finished after turn-start staging.
3. Image reads default to one image with `brief` plus an optional `focus`.
   Use complete `attachment://<handle>/images/N` addresses, never bare `/images/N`.
   Exact chart/table values and calculations use `full` with a focused question;
   full reads return at most one image. Explicit brief batches are capped at four.
   Observations preserve category, series and unit alongside each value. Missing
   associations or inconsistent totals require a focused re-read, not guessed data.
4. Non-vision chat models use the configured VLM. Successful observations are
   keyed by document, prepared-image hash, model ID and normalized reading prompt
   (including detail/focus). Equal requests reuse the observation across turns
   and authorized forks; different requirements do not reuse a narrower answer.
   Descriptions have a 60-second deadline (150 seconds for full reads), starting
   after a model slot is acquired. Failed or empty descriptions
   are not cached, and the engine does not repeat an attempted description.
5. The document metadata stores a bounded observation cache through optimistic
   updates that preserve parser metadata and concurrent writes. Cache entries
   inherit the document's deletion and retention rules. They are not merged into
   parsed Markdown or presented as exhaustive OCR.
6. After an image read, copy the prepared image and any available observation to
   `<original>.assets/`. Tool results expose the paths, and observations carry
   their source, detail, focus, model and timestamp. Normal turn-start staging
   preserves these files while the original belongs to the session. After a
   sandbox rebuild, the next read recreates copies from durable data/cache.

No sandbox is needed for steps 2–5. Projection failures are reported separately
and do not turn a successful durable read into a failure. Native vision sends
images directly to the chat model and copies the image if a sandbox is present;
it does not perform an extra VLM call just to manufacture a cached observation.

The cache reuses observations for the same model ID and prompt, not arbitrary
semantic similarity between focus strings. Raw VLM reasoning from a native vision
response is not an attachment artifact. These boundaries keep cache scope explicit
and avoid speculative re-reading of every extracted image.
