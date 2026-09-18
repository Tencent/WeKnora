# Document tag indexing

Ordinary document tag searches preserve `TagIDs` through knowledge search,
hybrid search, normal chat, and Agent retrieval. Tencent VectorDB filters the
string-array `tag_ids` on index records. FAQ keeps its existing scalar `tag_id`.
Other backends resolve tags to document IDs in the relational database.

## Persistent bootstrap marker

`knowledge_bases.document_tag_ready` means the historical tag projection has
been initialized. It is **not** a live cross-store consistency guarantee.

- The database default is false, including existing KBs after migration.
- Application-created empty KBs explicitly start true.
- Normal writes, tag edits, and incremental failures do not invalidate it.
- A successful full background bootstrap sets it true.
- Copying legacy indices explicitly resets the destination to false.
- Renaming or saving an unrelated KB setting cannot overwrite it.

Search only reads this marker. False uses relational tag-to-document lookup;
true uses native filtering where supported. Schema/query failures fall back for
that request without changing the marker. No query performs migration work.
Cross-KB searches preserve each KB's authorized scope and select its own path;
a low-level retrieval containing multiple KBs falls back if any is not ready.

## Incremental synchronization

The production knowledge/tag repositories call `DocumentTagSyncService.Sync`
after committing relation changes. This covers replace, add (including automatic
tagging), clear, and tag deletion. Deletion captures affected document IDs before
removing relations. Repeating the same requested tag set still synchronizes.

The index wrapper hydrates newly saved document indices with the latest tags.
Metadata synchronization and index writes use short per-KB database row locks
(SQLite acquires its write lock with a no-op update). Each synchronization reads
current relations while holding this lock; tasks contain IDs, never tag snapshots.
Untagged and soft-deleted documents receive empty arrays. Updates are scoped by
both KB and document ID, cover all index variants, and never re-embed content.
Moving a document clears its KB-scoped tags in the same vector metadata update
that changes the KB ID, before relational cleanup finishes.

A failed incremental synchronization returns an error stating that tags were
saved and attempts to enqueue a repair. Ready remains unchanged. SQL commit,
vector update, and enqueue are not atomic: a crash or simultaneous queue outage
can leave a stale projection. There is no transactional outbox and no promise
that every failure automatically converges. Native queries cannot detect stale
metadata when the backend returns success.

## Background bootstrap and repair

Task type: `document:tags:sync`, maintenance queue (`low`). Payload:

```json
{"tenant_id":7,"knowledge_base_id":"kb-id","knowledge_ids":["doc-id"]}
```

An empty/omitted `knowledge_ids` requests full bootstrap; `force: true` permits
an operator to repair an already-ready KB without resetting its marker. This is
an internal worker payload, not a new public HTTP API. Existing task tooling can
submit/retry it. Validate the tenant/KB before resolving any target store.

New index writes and tag changes on false KBs enqueue bootstrap. A startup and
10-minute sweep retries false document KBs on supported backends, including
read-only legacy KBs. Redis deployments deduplicate bootstrap triggers and use a
renewable per-KB task lock. Lite uses the existing goroutine task executor and
an in-process bootstrap guard; the persisted marker survives restarts.

Each full attempt prepares the array filter index, then scans documents by ID
in pages of 100. Incremental writes can run between pages. All participating
native backends must complete before ready becomes true. A new/asynchronous
filter index causes a retry until its schema is ready. The task allows 10 retries
and a 30-minute attempt timeout; a later sweep can retrigger failed bootstraps.
Attempts restart from the beginning, so there is no revision or durable cursor.
Progress/retry/error information remains in the existing task system. Missing
vector records are not reconstructed by a tag metadata repair.

## Deployment and maintenance

Apply PostgreSQL migration `000104_document_tag_ready`, or SQLite migration
`000024_document_tag_ready`, before starting the new application. Official
installations upgrade through the existing migration sequence normally.

If an installation ran the earlier **unmerged** revision prototype, its version
103/23 now collides with upstream's message-artifacts migration. Before starting
the new application, stop old writers and explicitly execute the official
`migrations/versioned/000103_message_artifacts_table.up.sql` (PostgreSQL), or
`migrations/sqlite/000023_message_artifacts_table.up.sql` (SQLite), against that
database. Verify that `message_artifacts` exists, then run the normal migration
sequence to 104/24. Do not mark the official migration complete without applying
its SQL: the version runner cannot distinguish two migrations with the same
number. An existing `document_tag_revision` column is unused and can be removed
after all old application instances are retired. Fresh DBs do not create it.

All writers must use the new synchronization path before marking legacy KBs
ready. Mixed old/new writers can leave stale native metadata without detection.
The stored VectorStore binding and index configuration are immutable through
normal APIs. If an operator replaces an environment-configured store or rebuilds
an index externally, explicitly reset affected KBs' marker to false and run the
bootstrap task; do not inherit readiness from another index. For ordinary
incremental failures, use an ID-scoped repair or a forced full repair instead.

The schema's readiness is separate from the KB's marker. A new KB using a legacy
shared Collection may need the backend to finish building `tag_ids` before its
first index write succeeds; retry that write when the schema is available.
