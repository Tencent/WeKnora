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

CREATE TRIGGER clear_sync_listing_pages AFTER UPDATE OF status ON sync_logs
WHEN NEW.status IN ('success','partial','canceled')
BEGIN
    DELETE FROM sync_listing_pages WHERE job_id = NEW.id;
END;
