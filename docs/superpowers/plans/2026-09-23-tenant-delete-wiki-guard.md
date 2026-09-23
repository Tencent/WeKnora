# Tenant deletion Wiki task guard

## Goal

Stop deleted tenants from starting or continuing Wiki generation work, so a soft-deleted tenant cannot keep issuing new LLM requests or recreate durable Wiki triggers after restart. Keep the change limited to lifecycle gating; do not introduce tenant-level resource cascade deletion.

## Scope and constraints

- Treat a tenant with `deleted_at IS NOT NULL` as inactive everywhere this Wiki pipeline can enqueue, recover, or make an LLM call.
- Fail closed on tenant lookup errors; no new generation request should be attempted when the lifecycle check cannot be verified.
- Remove durable KB-scoped pending rows when a Worker discovers its tenant is gone, using the existing cleanup path.
- Keep in-flight requests that were already accepted by the provider out of scope; the guard must prevent the next request/attempt.
- Preserve active-tenant behavior and existing KB soft-delete behavior.
- Do not delete tenant resources, storage, Wiki pages, or ACLs as part of this patch.

## Design

1. Add a tenant-service dependency to `wikiIngestService` and a small `ensureTenantActive` helper. The helper reads the tenant ID already injected into Wiki task contexts, returns a dedicated inactive sentinel for a missing/soft-deleted tenant, and returns an error for other lookup failures.
2. Gate `ProcessWikiIngest` and `ProcessWikiFinalize` before loading models. When the tenant is inactive, mark the task as skipped, clear its KB pending rows through the existing cleanup helper, and acknowledge the task without scheduling follow-up work.
3. Gate `generateWithTemplate` immediately before each model attempt. This covers long-running batches and retry loops where the tenant can be deleted after the Worker started.
4. Strengthen `EnqueueIfKnowledgeBaseActive` and `recoverPendingWikiTasks` to require both an active KB and an active parent tenant. This closes the race where a detached Worker could create durable work or a restart could resurrect it.

## Tests

- Repository guard: active tenant + active KB accepts; deleted tenant, deleted KB, missing tenant, missing KB, and tenant mismatch reject.
- Recovery: recreate triggers only for active tenant/KB lanes and delete pending rows for deleted-tenant, deleted-KB, and missing-KB lanes.
- LLM guard: a deleted tenant returns the inactive sentinel before the fake chat model is called; an active tenant keeps the existing template behavior.
- Run focused Go tests for repository, container recovery, and Wiki ingest packages when the Go toolchain is available; run `gofmt` and `git diff --check`.

## Out of scope / follow-up

- Cancelling provider requests already in flight.
- Deleting or migrating tenant-owned resources.
- A tenant-wide Asynq queue purge or operator-facing lifecycle dashboard; these can follow once the fail-closed guard lands.
