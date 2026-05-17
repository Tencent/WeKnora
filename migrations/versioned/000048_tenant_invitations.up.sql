-- Migration: 000048_tenant_invitations
-- Adds an explicit "invitation -> accept" flow on top of the
-- tenant-member RBAC model introduced in 000043. Previously, POST
-- /tenants/:id/members wrote an active tenant_members row in one shot
-- so the invitee was added without their knowledge or consent. The
-- table below records the pending intent; the row is only promoted
-- into a real tenant_members entry after the invitee accepts.
--
-- Why a separate table instead of reusing tenant_members.status='invited':
--   - Mixing the two states pollutes every existing tenant_members
--     read path with an extra "AND status='active'" filter, and the
--     PRs that introduced kb / agent ownership already assume any
--     non-deleted membership is active.
--   - Declined / revoked / expired invitations carry forensic value
--     (e.g. "did Bob ever try to add Eve?"); keeping them in their
--     own table lets us retain the full history without touching the
--     authoritative roster.
--   - Re-inviting the same user is a natural use case; a separate
--     table accumulates rows naturally with a partial unique index
--     guarding the in-flight one.
--
-- Status machine:
--   pending  -> accepted  (terminal; invitee accepts)
--   pending  -> declined  (terminal; invitee rejects)
--   pending  -> revoked   (terminal; owner cancels)
--   pending  -> expired   (terminal; lazy sweep after expires_at)
DO $$ BEGIN RAISE NOTICE '[Migration 000048] Creating table: tenant_invitations'; END $$;

CREATE TABLE IF NOT EXISTS tenant_invitations (
    id              BIGSERIAL   PRIMARY KEY,
    tenant_id       INTEGER     NOT NULL,
    invitee_user_id VARCHAR(36) NOT NULL,
    invited_by      VARCHAR(36),
    role            VARCHAR(20) NOT NULL,
    status          VARCHAR(20) NOT NULL DEFAULT 'pending',
    message         VARCHAR(500),
    expires_at      TIMESTAMP WITH TIME ZONE NOT NULL,
    responded_at    TIMESTAMP WITH TIME ZONE,
    created_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted_at      TIMESTAMP WITH TIME ZONE
);

-- Partial unique index: at most one PENDING invitation per (tenant,
-- invitee). Terminal-state rows (accepted/declined/revoked/expired)
-- can accumulate freely so the history of past invites stays intact.
CREATE UNIQUE INDEX IF NOT EXISTS idx_tenant_invitations_unique_pending
    ON tenant_invitations(tenant_id, invitee_user_id)
    WHERE status = 'pending' AND deleted_at IS NULL;

-- Read paths:
--   - Tenant management UI: list invitations for a tenant.
--   - "My invitations" inbox: list invitations for a user.
CREATE INDEX IF NOT EXISTS idx_tenant_invitations_tenant
    ON tenant_invitations(tenant_id) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_tenant_invitations_invitee
    ON tenant_invitations(invitee_user_id) WHERE deleted_at IS NULL;

DO $$ BEGIN RAISE NOTICE '[Migration 000048] tenant_invitations table ready'; END $$;
