-- Wiki authorization takes the exclusive counterpart before reading the source
-- attempt snapshot. Span writes share this relation/source-keyed transaction lock
-- until commit, so an INSERT cannot cross the snapshot-to-publication boundary.
-- TG_RELID keeps independently migrated schemas on separate advisory-lock keys.
CREATE OR REPLACE FUNCTION wiki_source_attempt_fence() RETURNS TRIGGER
LANGUAGE plpgsql AS $$
BEGIN
    PERFORM pg_advisory_xact_lock_shared(
        hashtextextended(
            'weknora:wiki-source-attempt:' || TG_RELID::text || ':' || NEW.knowledge_id,
            0
        )
    );
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_wiki_source_attempt_fence ON knowledge_processing_spans;

-- UPDATE OF excludes ordinary status/timing updates. BEFORE INSERT also protects
-- INSERT ... ON CONFLICT before either its insert or conflict action can proceed.
CREATE TRIGGER trg_wiki_source_attempt_fence
    BEFORE INSERT OR UPDATE OF knowledge_id, attempt ON knowledge_processing_spans
    FOR EACH ROW
    EXECUTE FUNCTION wiki_source_attempt_fence();

-- Keep the fence active even when session_replication_role is set to replica.
ALTER TABLE knowledge_processing_spans
    ENABLE ALWAYS TRIGGER trg_wiki_source_attempt_fence;
