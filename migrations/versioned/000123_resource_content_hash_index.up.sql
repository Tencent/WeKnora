-- Migration 000123: make the resource content hash a usable identity.
--
-- resources.content_hash has been written since the catalog was introduced but
-- nothing ever queried it, so it could not answer "do I already store these
-- exact bytes?". Register() only deduplicates by physical location
-- (location_hash), and identical bytes extracted onto new paths — the same
-- logo pulled out of several pages — therefore mint one handle per occurrence.
--
-- The index satisfies the lookup with the tenant scope and the live-row
-- predicate in one pass. It is deliberately NOT unique: existing rows already
-- hold duplicate payloads, and a unique index would refuse to build. The
-- partial predicate also keeps deleted rows and rows without a hash (legacy
-- registrations, temporary or non-hashed writes) out of the index.
CREATE INDEX IF NOT EXISTS idx_resources_tenant_content_hash
  ON resources(tenant_id, content_hash)
  WHERE deleted_at IS NULL AND content_hash <> '';
