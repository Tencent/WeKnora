# Knowledge Base Custom Metadata API

Custom metadata is available for document knowledge bases. Definitions belong to one knowledge base; values belong to one document and are tenant scoped. Required fields report completion state without blocking document creation or retrieval.

## Definitions

List the active schema:

```http
GET /api/v1/knowledge-bases/{kb_id}/metadata-definitions
```

Create or update a definition:

```http
POST /api/v1/knowledge-bases/{kb_id}/metadata-definitions
PUT /api/v1/knowledge-bases/{kb_id}/metadata-definitions/{definition_id}
Content-Type: application/json

{
  "name": "document_type",
  "desc": "Primary document classification",
  "value_type": "single_select",
  "required": true,
  "filterable": true,
  "sort_order": 10,
  "options": [
    { "label": "API", "sort_order": 10 },
    { "label": "Guide", "sort_order": 20 }
  ]
}
```

`value_type` is one of `text`, `single_select`, `multi_select`, `number`, `date`, or `boolean`. Option IDs returned by create/update are stable and must be reused on later updates. A definition's type is immutable after it has a value or automatic rule.

Archive a definition:

```http
DELETE /api/v1/knowledge-bases/{kb_id}/metadata-definitions/{definition_id}
```

Archived definitions and options remain attached to historical values but are excluded from new edits and filters. Document metadata reads return active fields plus any archived field that still has a stored value; archived fields without a value are omitted. A document with no metadata schema still returns `200` and an empty `values` array. `GET /knowledge/{id}/metadata-values` returns `404` only when the document itself is missing.

## Automatic Rules

Configure one active rule for a definition:

```http
PUT /api/v1/knowledge-bases/{kb_id}/metadata-definitions/{definition_id}/auto-rule
Content-Type: application/json

{
  "strategy": "source_mapping",
  "config": { "source_key": "product_line" }
}
```

For LLM extraction:

```json
{
  "strategy": "llm_extract",
  "config": {
    "instruction": "Identify the product line stated in the document",
    "model_id": "optional-chat-model-id"
  }
}
```

Omitting `model_id` uses the knowledge base summary model. Rule updates increment the revision. Delete a rule with `DELETE` on the same `/auto-rule` path.

Automatic filling runs after document post-processing. It writes pending-review values and honors each value's `allow_auto_overwrite` policy. Manual reruns are available for one document or an explicit document set:

```http
POST /api/v1/knowledge/{knowledge_id}/metadata-values/auto-fill

POST /api/v1/knowledge-bases/{kb_id}/metadata-values/auto-fill
Content-Type: application/json

{ "knowledge_ids": ["document-id-1", "document-id-2"] }
```

Both endpoints return `202 Accepted`. The single-document response contains
`data.task_id`; the batch response contains `data.task_ids` and
`data.enqueued_count`. Active tasks are deduplicated by tenant, knowledge base,
document, and the enabled rule revisions.

## Document Values

Read, update, and confirm values:

```http
GET /api/v1/knowledge/{knowledge_id}/metadata-values

PATCH /api/v1/knowledge/{knowledge_id}/metadata-values
Content-Type: application/json

{
  "changes": [{
    "metadata_definition_id": "definition-id",
    "value": "option-id-or-typed-value",
    "allow_auto_overwrite": false,
    "expected_version": 2
  }]
}

POST /api/v1/knowledge/{knowledge_id}/metadata-values/confirm
Content-Type: application/json

{ "metadata_definition_ids": ["definition-id"] }
```

Send `null` as `value` to clear a field. An omitted `value` changes only the overwrite policy. New values use `expected_version: 0`; existing values require the current version. A stale version returns `409 Conflict` with the latest value in error details.

Batch read up to 200 documents:

```http
POST /api/v1/knowledge/metadata-values/batch-get
Content-Type: application/json

{ "knowledge_ids": ["document-id-1", "document-id-2"] }
```

All document IDs in one batch must belong to the same knowledge base. Access is
checked against that knowledge base before any values are returned.

File, URL, and manual document creation accept an optional `metadata_values` array using the same item shape as `changes`. For multipart file uploads the array is JSON encoded in the `metadata_values` form field. Missing required values remain allowed and are returned with completion state `incomplete`. Invalid `metadata_values` are rejected before the knowledge row is created. Create-time items must include a value and use `expected_version: 0`. If the knowledge row is created and metadata persistence then fails, the API returns `500` with `knowledge created, but metadata was not saved`; the document remains and metadata can be completed from the document editor. `PATCH` metadata changes are applied in one transaction.

## Filtering

Document list filtering passes a JSON-encoded `metadata_conditions` query parameter. Session search and QA requests use `metadata_filters`, grouped by knowledge base:

```json
{
  "metadata_filters": [{
    "knowledge_base_id": "kb-id",
    "conditions": [{
      "metadata_definition_id": "definition-id",
      "operator": "contains_any",
      "values": ["option-id-a", "option-id-b"]
    }]
  }]
}
```

Conditions within a knowledge base use AND semantics. Explicit `knowledge_ids` are intersected with the metadata scope. A zero-match scope short-circuits retrieval.

Supported operators:

| Type | Operators |
|---|---|
| text | `equals`, `contains`, `is_empty`, `is_not_empty` |
| single select | `in`, `is_empty`, `is_not_empty` |
| multi select | `contains_any`, `contains_all`, `is_empty`, `is_not_empty` |
| number | `eq`, `gt`, `gte`, `lt`, `lte`, `between`, `is_empty`, `is_not_empty` |
| date | `on`, `before`, `after`, `between`, `is_empty`, `is_not_empty` |
| boolean | `eq`, `is_empty`, `is_not_empty` |

## Errors

| Status | Meaning |
|---|---|
| `400` | Invalid type, operator, option membership, rule config, or request body |
| `401` | Tenant identity is missing |
| `403` | Tenant or knowledge base scope does not match the caller |
| `404` | Knowledge, definition, rule, or value was not found |
| `409` | Definition name conflict or optimistic concurrency conflict |

## Legacy Tag Compatibility

Document tags remain a separate feature. Use metadata definitions for typed document fields, and keep tags for free-form labels. Document data-source sync does not create a synthetic tag for each source; use the source filter or a `source_mapping` metadata rule instead.

Deleting a document, a document batch, or a knowledge base explicitly removes its metadata values and related option links. Database foreign keys provide a second cascade safeguard for hard deletion.
