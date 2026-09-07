DROP INDEX IF EXISTS idx_webhook_outbox_status_created;
DROP INDEX IF EXISTS idx_webhook_outbox_event_id;
DROP TABLE IF EXISTS tenant_webhook_outbox;

DROP INDEX IF EXISTS idx_webhook_dlv_event_id;
DROP INDEX IF EXISTS idx_webhook_dlv_event_endpoint;
DROP INDEX IF EXISTS idx_webhook_dlv_delivery_id;
DROP INDEX IF EXISTS idx_webhook_dlv_tenant_created;
DROP INDEX IF EXISTS idx_webhook_dlv_endpoint_id;
DROP TABLE IF EXISTS tenant_webhook_deliveries;

DROP INDEX IF EXISTS idx_webhook_ep_tenant;
DROP TABLE IF EXISTS tenant_webhook_endpoints;
