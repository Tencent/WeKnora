CREATE TABLE sync_listing_pages (
    job_id VARCHAR(36) NOT NULL REFERENCES sync_logs(id) ON DELETE CASCADE,
    tenant_id BIGINT NOT NULL,
    source_id VARCHAR(36) NOT NULL,
    selection VARCHAR(64) NOT NULL,
    page_key VARCHAR(64) NOT NULL,
    payload TEXT NOT NULL,
    checksum VARCHAR(64) NOT NULL,
    PRIMARY KEY (job_id, tenant_id, source_id, selection, page_key)
);

CREATE FUNCTION clear_sync_listing_pages() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    DELETE FROM sync_listing_pages WHERE job_id = NEW.id;
    RETURN NEW;
END;
$$;
CREATE TRIGGER clear_sync_listing_pages AFTER UPDATE OF status ON sync_logs
FOR EACH ROW WHEN (NEW.status IN ('success','partial','canceled'))
EXECUTE FUNCTION clear_sync_listing_pages();
