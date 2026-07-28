# Batch Knowledge Rebuild

## Purpose

When a large knowledge-base upload leaves many documents in a failed state, an operator can filter documents, select all matching results, and submit an asynchronous batch rebuild. This feature lives on the independent `feature/batch-knowledge-rebuild` branch from `upstream/main`; it does not depend on the MySQL or operations feature branches.

## Usage

1. Open a document knowledge base and filter parsing status to **Failed**.
2. Enter batch management, select loaded documents, or choose **all N filtered results**.
3. Choose **Rebuild Knowledge** and confirm.
4. The page reports submitted, skipped, and failed-to-submit counts. Each document's parsing status and error detail remain the final source of truth.

The all-results action appears only while a document filter is active, preventing an accidental rebuild of an entire knowledge base. That selection is rebuild-only; batch delete is disabled.

## Safety Semantics

- Rebuild clears a document's existing chunks before parsing it again, and the confirmation states that impact explicitly.
- Documents already in `pending`, `processing`, or `finalizing` are skipped on the server to prevent duplicate queue entries.
- Read-only members cannot submit rebuilds. The server reuses knowledge-base, tenant, and API-key scope checks.
- The server resolves all filtered results inside the target knowledge base and authorization context. The browser never assembles cross-page IDs itself.
- One operation handles at most 1,000 documents and each maintenance task handles at most 200. Larger accepted operations are split into low-priority tasks so uploads and chat work are not starved.
- One document failing does not block the rest of its task. A successful submission means queued work, not completed rebuilding.

## API

```http
POST /api/v1/knowledge/batch-reparse
```

Existing explicit-ID calls remain compatible:

```json
{
  "kb_id": "kb-123",
  "ids": ["knowledge-1", "knowledge-2"]
}
```

All filtered results use a server-resolved snapshot:

```json
{
  "kb_id": "kb-123",
  "filter": {
    "parse_status": "failed",
    "tag_ids": ["tag-1"],
    "keyword": "invoice"
  }
}
```

The response includes `task_ids`, `submitted_count`, `skipped_in_flight_count`, and `enqueue_failed_count`. One operation can produce multiple maintenance tasks.

## Observability

Each maintenance task emits one aggregated `knowledge.batch_reparsed` audit event with task ID, document count, and failure count. Per-document rebuild audit events inside the task are suppressed, so a large batch cannot flood the audit log.

## Non-goals

This feature does not automatically retry failed documents, re-upload source files, change parsing configuration, or retain old chunks for rollback. Those behaviors need separate feature discussions.
