-- Migration: 000068_task_pending_claim_leases
-- Description: Add ownership tokens for renewable pending-op claim leases.

ALTER TABLE task_pending_ops
    ADD COLUMN IF NOT EXISTS claim_token VARCHAR(64) NOT NULL DEFAULT '';

COMMENT ON COLUMN task_pending_ops.claim_token IS
    'Current renewable claim lease owner. Ownership-scoped renew/release/delete prevents stale workers from settling rows reclaimed after a crash.';
