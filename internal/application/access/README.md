# Resource access boundaries

`access` contains shared permission resolution without depending on Gin or the
application service implementations. HTTP adapters keep parameter parsing, status
codes, RBAC rollout behavior, and response projection at their existing boundaries.

| Entry point | Shared rule | Additional boundary |
| --- | --- | --- |
| Resource mutations with URL/body KB selectors | `CheckOwnershipOrRole`: creator or sufficient tenant role | API-key capabilities, KB scope and tenant visibility remain separate checks |
| KB/document detail and content routes | `ResolveKB`: own tenant, organization share, then read-only shared agent | Tenant role/ownership guards and API-key capabilities |
| Shared documents, chunks, search targets and suggestions | `KBSharePermissions`: organization grants only | Existing user requirements and caller-specific filtering/error behavior |
| FAQ and tag service reads | `resolveKBReadTenant`: execution tenant or organization share | Direct cross-tenant calls require a user; no implicit agent fallback |
| KB list, document search and batch restoration with `agent_id` | Shared-agent lookup, then `SharedAgentKBScope` | API-key intersection; dynamic list/search selections also apply capability filtering |
| Wiki fixer source tenant | Organization Editor permission | Built-in fixer only; exactly one KB |
| Message file shared-KB fallback | Organization Viewer permission | Persisted message reference, exact resource handle, KB/resource owner match |
| KB file proxy | `ResolveKBFile`: exact Viewer grant and current KB binding/reference | Owner tenant and exports namespace; original uploads retain their separate download permission |
| Message file proxy | `ResolveMessageFile`: session ownership and exact persisted output reference | Current shared-agent or organization-KB permission for source-owned resources |
| Message artifact download | `ResolveMessageArtifact`: session ownership and persisted artifact index | Session-owned output remains accessible; source-owned output rechecks current sharing |
| FAQ and tag mutations | `RequireKBWrite`: exact Editor operation grant | API-key ingest capability and KB scope; tenant-role/creator checks remain at the admission boundary |

## Ownership and write roles

URL middleware and body-based handlers use the same lazy ownership decision.
The middleware adapter resolves identity, RBAC rollout configuration and the
configuration-gated superuser flag. API-key principals, sufficient roles,
cross-tenant superusers and disabled enforcement skip the creator lookup.
API keys still require their route capabilities and allowed KB scope.

When a lookup is needed, it must check tenant visibility before returning the
creator. An empty creator never matches an empty user. Middleware passes missing
resources to the handler, reports lookup failures as 503 and audits only actual
policy denials. Body-based handlers retain their existing 404/403/500 mapping.
KB, initialization, Wiki and body-based KB lookups share the same tenant check.

## Caller and resource tenant

`types.Caller` holds the authenticated tenant, user and tenant role. Auth records
it once; `KBRequest.Caller` and `KBAccess.Caller` use that same identity. The
legacy `TenantIDContextKey` continues to scope repository/model execution, so
existing storage APIs do not need a coordinated signature migration.

Use `types.WithExecutionTenant` to switch execution tenants. It captures the
caller first, even when identity is missing, and grants no access by itself.
Use `KBAccess.Context` after authorization to carry both the exact KB grant and
its execution tenant, or `WithGrant` to carry the grant without switching yet.
Grants bind the KB ID, owner tenant, caller and resolved permission; changing
execution tenant cannot turn a Viewer grant into Editor or expose another KB.
HTTP adapters retain Gin's caller keys for existing handlers.

Service reads use `KBPermissions` to check caller ownership, exact upstream
grants, then organization shares for the original caller. This check also applies
to documents/chunks returned by the initial tenant-scoped batch query, not just
rows found during cross-tenant expansion. Search preflight, result enrichment,
FAQ/tag reads, search targets and suggestion scopes follow the same boundary.
General document search enumerates the caller's own/shared KBs; its execution
tenant is not a new identity.

After authorizing a shared agent, `WithSharedAgent` carries its configured,
source-tenant-bound read scope into the pipeline. An arbitrary execution switch
cannot substitute for that grant. `logger.CloneContext` preserves the caller and
resource grants for detached work within the operation. Context grants contain
immutable snapshots; deriving a context does not mutate a sibling search branch.
They are not persisted as authority for subsequent requests. API-key scope is
reapplied when consuming any grant.

## Organization shares

`KBSharePermissions` fixes the caller tenant and role for one operation and caches
the granular decision per KB. Reading many documents/chunks from one KB therefore
queries its membership once. Both denied decisions and errors are cached within
that operation. The next request creates a new resolver and observes revocations.
The resolver is not a process-wide cache and is not shared between goroutines.

The share service remains responsible for the role cap across share permission,
organization membership and caller tenant role. The resolver returns lookup errors
so strict search preflight can report an infrastructure failure while best-effort
result expansion can omit inaccessible rows. `ResolveKB` retains its independent
read-only agent fallback when organization resolution cannot grant access.

## Shared-agent KB scope

A scope is created only after looking up an accessible shared agent. `all` means
all KBs in that agent's tenant; `selected` means its explicit nonempty ID set.
`none`, unknown modes and empty selections authorize no KBs. A nil/empty ID slice
never implicitly grants an entire tenant. Specifying an agent does not permit
falling back to a different shared agent.

These rules do not replace message/session ownership, persisted file-reference
validation, resource binding, Agent tool search-target restrictions, or embed/IM
capability checks. Raw tenant files and message files have different authorization
contracts and must not be admitted by a generic KB grant alone.

## FAQ and tag writes

FAQ/tag service mutations consume an explicit Editor grant; setting an execution
tenant or supplying a KB ID is insufficient. `ResolveKB` keeps the effective
permission used for response projection, but caps its context grant to the
requested operation. Thus an owner read does not mint a reusable write grant.
HTTP routes retain their existing role/creator admission checks. API-key ingest
capability and KB scope are reapplied when consuming a write grant.

Trusted FAQ-import, data-source sync and automatic-tagging workers use
`WithKBTaskWrite` after loading and validating their target KB and child-resource
bindings. This represents continuation of an admitted job, not renewed user
authorization on every retry. It carries exactly one KB, preserves the caller
or its absence, survives in-operation context cloning, and grants no tenant-wide
role. Services use the KB owner's TenantInfo for model/index configuration.

Batch FAQ updates resolve all source/destination tags, entries and exclusions
before mutation. Batch deletion also resolves all parent documents first.
Tag-only batch updates use the same plan. Tag groups execute in ascending seq_id
order, then explicit entry updates override only their specified fields.
Import retains per-entry content-validation results, but out-of-scope resource
references are rejected before admission and checked again in the worker.
This preflight prevents partial writes caused by invalid resource selections;
it does not add a transaction across the database, task queue and search index.

## File authorization and transport

File resolvers return request-local `FileAccess` locators before any storage
reader is opened. The KB catalog checks live document bindings first, then exact
references in active chunks/wiki pages for historical files. A same-tenant path
or handle prefix cannot replace a KB binding. Message evidence comes from
persisted content, references, images, artifacts and tool results; tool arguments
do not prove that the message returned a file. The organization-shared KB
fallback also checks the catalog for an independent live KB/file binding;
retrieval text and a currently shared KB ID alone are insufficient.

The storage adapter uses the authorized owner and backend. The shared
`filetransport` package owns response headers, safe inline/download disposition,
streaming and reader closure. Seekable readers use standard HTTP Range and HEAD
handling; streaming-only backends return full content with `Accept-Ranges: none`
without buffering the object (RFC 9110 sections 14.2 and 14.3). Permission-controlled responses use `private, no-store` so a later
request rechecks current access. Tenant-wide and signed-token file routes keep
their own authorization and caching contracts.

## Document and chunk writes

Document metadata, manual content, reparse, document tags/folders, image edits, chunk mutations
and generated-question edits consume explicit Editor grants after loading the
persisted document/KB binding. Full chunk objects cannot reparent existing rows.
Batch inputs are deduplicated and fully validated, including missing IDs and
each KB's grant, before writes. Text edits also check the parent/image-child
relationships that they will modify. Body-based HTTP routes explicitly resolve
an Editor operation; a previously cached owner read is not a write admission.

Document deletion shares a preflight plan and executor for single and batch
calls. The plan validates every selection and KB, loads owner TenantInfo and
routing metadata, and captures image references before marking rows as deleting.
Index/graph failures leave the document and original file available for retry.
Physical file cleanup remains best effort after database deletion; this does not
introduce a transaction across database, indexes and storage or durable blob GC.

New queued deletion payloads pin `knowledge_base_id` as well as tenant and exact
document IDs. Workers consume a private cleanup scope bound to those references,
not general KB write permission; already deleted IDs are successful no-ops.
A moved document rejects the batch before side effects. Pre-upgrade payloads that
omit the KB are accepted only when remaining rows resolve to one same-tenant KB;
they cannot reconstruct the KB that existed at enqueue time. Failure callbacks
skip rejected scopes and condition status updates on tenant, KB and deleting
state. Scope/identity errors are not retried; infrastructure failures are.

Batch reparse payloads likewise carry the admitted KB ID. Workers validate the
whole batch before any submission and use exactly one `WithKBTaskWrite` grant;
each reparse reload consumes that grant and rejects a document still moving.
Missing/mixed/moved selections fail without submitting earlier batch members.
Legacy reparse payloads can reconstruct only a present, single-KB binding.

Session-history, temporary-KB and clone cleanup use exact server-derived
references from their already admitted operations. These cleanup contexts cannot
be used for document/chunk updates or additional IDs. Parsing and enrichment
workers use repositories for their internal persistence and rollback; they must
keep their existing task ownership checks, abort checks and resource bindings.
Repository access is not an incoming-request authorization API.

## Clone and move operations

`WithKBTransfer` admits the exact source/target pair after HTTP role/creator
checks. Clone consumes source Viewer, target Editor and the API-key
`manage_kbs` capability; move consumes Editor on both sides and `ingest`.
Both operations remain within one tenant. Creating a clone reserves its target
ID before enqueue and carries its creator into the worker. The ID of a new
resource is not checked against an existing-resource API-key allow-list.

Workers reload both KBs, validate their owners and consume a private pair grant.
Document/FAQ plans validate all selected resources, chunks, parents and tags
before destructive work. Lifecycle reads include every chunk type and indexing
state. A document's `_knowledge_transfer` metadata records task identity and
progress; retries reuse the reserved target, replace incomplete clones, skip
completed moves and retry only enqueue for a moved document awaiting parsing.
This field is internal operation state, not caller-supplied custom metadata.
For `reparse`, transfer `done` means target binding and parser admission are
complete, not that parsing has finished. `ParseStatus` remains the parser's
state. `reparse_pending` already admits the target parser; extending the
`moving` write guard to that phase would also block legitimate worker writes.
Locking all writes until parsing finishes is a separate processing/concurrency
contract, including ordinary reparses, and is not provided by this transfer marker.

Failed clone chunk/index writes attempt rollback after conditionally claiming
the original destination. Rollback failures are returned, references retained
when cleanup is uncertain, and later retries replace the failed copy. Accounting
is committed only after the clone succeeds. FAQ containers do not inherit the
source document's transfer marker.

Vector reuse relocates KB metadata while preserving physical vector/chunk IDs.
OpenSearch selects the entire source KB/document pair, even without DB chunks,
and rejects version conflicts, timeouts and partial failures before DB relocation.
It never copies vectors and then deletes by the same document ID. Source and
target must use the same vector store for reuse, and the same concrete file
storage instance for either mode. Doris ANN tables cannot safely replace rows
without a delete/insert gap, so their moves require `reparse`. Clone still
requires compatible embedding models and vector stores; a move also retains
the existing same-model requirement. This is not a cross-storage migration API.

Conditional document checkpoints bind tenant, KB, status and the stored update
time. They keep storage accounting in the same database transaction and read
back timestamp precision. Delete stops when its checkpoint no longer matches.
Ordinary document/chunk writes reject a document in the `moving` phase. Parser
admission and failure callbacks verify the task's persisted tenant/KB binding;
table-summary failure cleanup also stops if its document changed.

## Follow-up work outside this refactor

These are separate follow-ups, not capabilities provided by this package:

1. **Complete worker contracts and concurrency control.** Audit enrichment,
   table-summary, multimodal, wiki and indexing workers end to end. Standardize
   payload resource bindings, processing-attempt IDs and conditional writes,
   including operations admitted before a concurrent move. Do not replace
   these checks with an execution-tenant switch or a blanket task write grant.
2. **Durable recovery across systems.** Add a transactional outbox and explicit
   recovery/cancellation for exhausted transfer tasks. Current retries use the
   same admitted task identity; a new move request is not a resume API. Database,
   queue, vector, graph and blob operations are not one atomic transaction.
   Physical file cleanup remains best effort and needs durable garbage collection.
3. **External-backend integration coverage.** Exercise clone/move, partial
   backend failures, retries, pagination and search/edit behavior against live
   PostgreSQL, Elasticsearch 7/8, OpenSearch, Qdrant, Milvus, Weaviate,
   TencentVectorDB and Doris. Local SQLite vector tests and backend compilation
   do not establish this coverage. Doris ANN vector reuse remains unsupported.
4. **Finish service-layer migration.** Apply explicit operation grants to the
   remaining KB configuration/duplicate/delete, wiki and resource-administration
   service methods, preserving their existing route admission policies. Keep
   user/organization membership, embed, IM and signed-resource contracts separate.
5. **Shared-agent message-file provenance.** Distinguish KB-origin files from
   generated message artifacts, then apply the agent's current KB selection and
   live bindings to KB-origin files. The existing source-owned shared-agent
   fallback checks exact persisted output plus the live agent share, but does
   not yet apply `selected`/`none` KB selection to every file. Do not treat the
   org-shared KB fix as closing that separate path, or blindly apply KB gates
   to generated artifacts that have no KB owner.
6. **Resource-reference indexing and legacy migration.** Backfill historical
   file bindings, measure the request-local text-reference fallback and migrate
   old queued payloads. A legacy delete task without a KB ID can reconstruct
   only its present unambiguous binding, not its original enqueue-time binding.

No public request schema or database column migration is required by this
refactor. Internal service interfaces require operation contexts, queued payloads
add optional scope fields, and malformed/out-of-scope batch selections now fail
before mutation instead of being silently skipped. Callers must also handle a
409 for document edits/deletes while a move is in progress.
