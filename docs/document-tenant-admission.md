# Document tenant admission

Document parsing uses the existing `default` Core/Shared queue. Producers first persist the complete
`document:process` payload in `task_pending_ops`, scoped to its knowledge base and keyed by knowledge ID.
Only `document:dispatch` wakeups go into the transport queue. Each tenant owns two stable alternating
wakeups; the queued representative count is bounded by active tenants, independent of document count.

A dispatcher selects the next eligible **tenant**, then a ready document across that tenant's knowledge
bases. Adding knowledge bases does not give a tenant more turns. This is admission fairness, not CPU-time
fairness: a long document is not preempted. The default cluster-wide per-tenant parsing limit is **2**.
Configure `knowledge_base.document_tenant_inflight` in YAML to a positive integer; missing, zero or
negative values fall back to 2. All replicas should use the same setting.
The limit is a ceiling; two wakeups per origin do not promise that a single tenant can fill a higher limit.

The database owns both the tenant cursor and execution leases. PostgreSQL serializes admission through
a singleton row update; SQLite acquires its write lock before reading scheduler state and retries bounded
lock contention. Model calls run outside database transactions. Leases live separately from pending rows,
so cancellation or knowledge-base cleanup cannot free a running worker's capacity prematurely. Owner
tokens fence renewal and completion. A failed heartbeat cancels the processing context; bookkeeping uses
a fresh bounded context after processing returns.

Pending cancellation removes durable work immediately. An already running worker observes cancellation
through existing status checkpoints or its next lease heartbeat (every 40 seconds for a two-minute
lease). A stalled renewal can add its ten-second request deadline; cancellation propagation is not
instantaneous, and processing must honor context cancellation.
Cancellation captures immutable pending-row IDs before changing status and removes only those rows;
a concurrently enqueued reparse therefore retains its new pending record. A durable per-document
generation is advanced before a reparse, replacement or move changes source/status. Pending rows and
terminal archives retain that generation independently of the original payload's tracing attempt.
The tombstone remains after cleanup, so an old untagged legacy delivery cannot recreate superseded work.
Generation-fenced writes and atomic span opening stop delayed producers from overwriting newer runs.

Execution is at least once. Lease expiry after a worker crash can allow a replay; external model calls are
not exactly once, and a worker ignoring context cancellation can outlive a network partition. Existing
knowledge status, source identity and parse-attempt guards remain necessary. Completed/finalizing work is
not parsed again. Initial execution plus three failures is the durable budget; backoff releases capacity.
Infrastructure failures before entering processing do not consume an execution. Terminal records contain
the original document payload rather than the generic wakeup token.

Handlers are registered before startup reconciliation. Periodic reconciliation recovers expired leases,
cleans missing/deleted scopes and seeds distinct pending tenants. Lost Redis enqueues and lost Lite-memory
wakeups therefore recover without importing a second hidden copy. A successful durable insert is accepted
even if its transport wakeup fails. Known archived stable wakeups are reactivated through the inspector;
inspection errors are treated as unknown and never cause active task deletion.
Shutdown first stops reconciliation, then cancels active dispatch contexts and waits for heartbeat exit
and fenced completion, including Lite tasks. New admission and wakeup rearming stop during this drain.
The wait has a deadline and reports an error if processing or bookkeeping does not finish in time.

Apply PostgreSQL migration 137 or SQLite migration 53 before running this version. Existing queued
`document:process` tasks remain consumable: their original payload and existing retry count are admitted
into the durable scheduler. During a rolling deployment, older workers still execute their legacy tasks
directly, so the new global admission limit is fully effective once all workers run the new version.
Legacy payloads with attempt zero, including the existing move producer, keep that convention; this
change does not retroactively reconstruct historical parse-attempt identities. Queue deduplication and
terminal replay checks include knowledge-base scope so a source queue cannot absorb a moved document.
An exhausted unversioned payload settles knowledge failure while its durable generation still owns the
original pending row. Superseded executions release their own work without changing a newer run.
Payload Attempt remains unchanged, including zero for existing move tasks; generation ownership is
separate scheduler metadata. Move retries retain their token in the existing transfer checkpoint.
Manual documents, chat attachments, post-processing, Wiki work and model quotas keep their existing queues.
