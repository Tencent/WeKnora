# Recursive links in Feishu / Lark tables

Opt in with `FEISHU_RECURSIVE_LINKS=true`. The default is disabled.
`FEISHU_RECURSIVE_MAX_DEPTH` defaults to 3 and is capped at 10. The selected
table is depth 0; its direct links are depth 1.

Wiki and Drive sync can discover HTTPS Feishu/Lark links in spreadsheet cells
and structured Bitable record fields, resolve Wiki wrappers, and recursively
fetch linked documents or tables. Cycles and repeated resource identities
are deduplicated across the sync. Existing credentials and source permissions
are reused: this does not grant the application access to unshared documents.

An unchanged parent table still scans its links so child-only edits are not
missed. Linked content with the same hash/name/title skips re-ingestion.
Removal reconciliation occurs only after the whole traversal completes;
partial listing or discovery errors retain the previous linked-resource set.
Checkpoints retain old identities until the complete traversal commits.

Linked docx resources use the configured blocks/export parse mode. Existing
folder mirroring is retained. Direct file links without a reliable filename,
Drive folder links, mindnotes and unsupported resource types are not expanded.
Changing table exports can still cause re-indexing of an entire linked table;
this is not row-level incremental ingestion. Removed links are reconciled on
a later successful sync while the feature remains enabled.

The API app needs permission to read table records/sheet values as well as
the linked resources. A root Bitable link with a table/view query restricts
link discovery to that table/view, but its binary export follows the existing
Feishu export API contract. Additional API reads/exports may increase sync
latency and downstream parsing/model usage. Start with a small selected source.

Run the fake-API regression tests without live credentials:

```sh
go test ./internal/datasource/connector/feishu/... -run 'TestRecursive|Test.*FullSync|Test.*Cursor' -count=1
```
