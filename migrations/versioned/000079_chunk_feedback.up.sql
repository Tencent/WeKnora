-- Minimal message-to-chunk feedback loop.
--
-- The first draft of PR #2358 used migration version 77. Upstream now owns
-- versions 77 and 78, so this migration is version 79 and also converts that
-- draft schema when a contributor has already exercised it locally. Released
-- upstream schemas take the ordinary fresh path below.
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS like_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS dislike_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS positive_rate DOUBLE PRECISION;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS recall_weight DOUBLE PRECISION NOT NULL DEFAULT 1.0;
ALTER TABLE chunks ADD COLUMN IF NOT EXISTS feedback_reset_at TIMESTAMP WITH TIME ZONE;

-- Obsolete draft-only derived flags are deliberately not part of the
-- converged contract. The durable reset baseline is feedback_reset_at.
ALTER TABLE chunks DROP COLUMN IF EXISTS needs_optimization;
ALTER TABLE chunks DROP COLUMN IF EXISTS feedback_updated_at;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'chunks'::regclass
          AND conname = 'chk_chunks_feedback_counts'
    ) THEN
        ALTER TABLE chunks
            ADD CONSTRAINT chk_chunks_feedback_counts
            CHECK (like_count >= 0 AND dislike_count >= 0);
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'chunks'::regclass
          AND conname = 'chk_chunks_positive_rate'
    ) THEN
        ALTER TABLE chunks
            ADD CONSTRAINT chk_chunks_positive_rate
            CHECK (positive_rate IS NULL OR (positive_rate >= 0 AND positive_rate <= 1));
    END IF;
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'chunks'::regclass
          AND conname = 'chk_chunks_recall_weight'
    ) THEN
        ALTER TABLE chunks
            ADD CONSTRAINT chk_chunks_recall_weight
            CHECK (recall_weight > 0);
    END IF;
END
$$;

-- Convert the draft attribution table. Its extra NOT NULL columns would make
-- writes from the converged repository fail, so rebuilding is intentional.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'message_chunk_references'
          AND column_name = 'session_tenant_id'
    ) THEN
        CREATE TABLE message_chunk_references_v79 (
            id VARCHAR(36) PRIMARY KEY,
            message_tenant_id BIGINT NOT NULL,
            chunk_tenant_id BIGINT NOT NULL,
            message_id VARCHAR(36) NOT NULL,
            chunk_id VARCHAR(36) NOT NULL,
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            UNIQUE (message_tenant_id, message_id, chunk_tenant_id, chunk_id)
        );
        INSERT INTO message_chunk_references_v79
            (id, message_tenant_id, chunk_tenant_id, message_id, chunk_id, created_at)
        SELECT
            id, session_tenant_id, chunk_tenant_id, message_id, chunk_id, created_at
        FROM message_chunk_references;
        DROP TABLE message_chunk_references;
        ALTER TABLE message_chunk_references_v79 RENAME TO message_chunk_references;
    END IF;
END
$$;

CREATE TABLE IF NOT EXISTS message_chunk_references (
    id VARCHAR(36) PRIMARY KEY,
    message_tenant_id BIGINT NOT NULL,
    chunk_tenant_id BIGINT NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_message_chunk_reference UNIQUE (message_tenant_id, message_id, chunk_tenant_id, chunk_id)
);
CREATE INDEX IF NOT EXISTS idx_message_reference_message
    ON message_chunk_references (message_tenant_id, message_id);
CREATE INDEX IF NOT EXISTS idx_message_reference_chunk
    ON message_chunk_references (chunk_tenant_id, chunk_id);

-- Convert the draft feedback row while preserving rating-event time. A
-- reason-only edit continues to use this feedback_at value after migration.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'message_feedbacks'
          AND column_name = 'session_tenant_id'
    ) THEN
        CREATE TABLE message_feedbacks_v79 (
            id VARCHAR(36) PRIMARY KEY,
            tenant_id BIGINT NOT NULL,
            user_id VARCHAR(64) NOT NULL,
            session_id VARCHAR(36) NOT NULL,
            message_id VARCHAR(36) NOT NULL,
            feedback_type VARCHAR(16) NOT NULL,
            reason_code VARCHAR(16),
            feedback_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
            UNIQUE (tenant_id, user_id, message_id),
            CHECK (feedback_type IN ('like', 'dislike')),
            CHECK (
                (feedback_type = 'like' AND reason_code IS NULL)
                OR
                (feedback_type = 'dislike' AND reason_code IS NOT NULL
                    AND reason_code IN ('inaccurate', 'irrelevant', 'incomplete', 'outdated', 'other'))
            )
        );
        INSERT INTO message_feedbacks_v79
            (id, tenant_id, user_id, session_id, message_id, feedback_type,
             reason_code, feedback_at, created_at, updated_at)
        SELECT
            id,
            session_tenant_id,
            LEFT(user_id, 64),
            session_id,
            message_id,
            feedback_type,
            CASE
                WHEN feedback_type = 'like' THEN NULL
                WHEN reason_code IN ('inaccurate', 'irrelevant', 'incomplete', 'outdated', 'other')
                    THEN reason_code
                ELSE 'other'
            END,
            feedback_at,
            created_at,
            updated_at
        FROM message_feedbacks;
        DROP TABLE message_feedbacks;
        ALTER TABLE message_feedbacks_v79 RENAME TO message_feedbacks;
    END IF;
END
$$;

CREATE TABLE IF NOT EXISTS message_feedbacks (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id BIGINT NOT NULL,
    user_id VARCHAR(64) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    message_id VARCHAR(36) NOT NULL,
    feedback_type VARCHAR(16) NOT NULL,
    reason_code VARCHAR(16),
    feedback_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT uq_message_feedback_actor UNIQUE (tenant_id, user_id, message_id),
    CONSTRAINT chk_message_feedback_type CHECK (feedback_type IN ('like', 'dislike')),
    CONSTRAINT chk_message_feedback_reason CHECK (
        (feedback_type = 'like' AND reason_code IS NULL)
        OR
        (feedback_type = 'dislike' AND reason_code IS NOT NULL
            AND reason_code IN ('inaccurate', 'irrelevant', 'incomplete', 'outdated', 'other'))
    )
);
CREATE INDEX IF NOT EXISTS idx_message_feedback_session
    ON message_feedbacks (tenant_id, session_id);
CREATE INDEX IF NOT EXISTS idx_message_feedback_message
    ON message_feedbacks (tenant_id, message_id);

CREATE TABLE IF NOT EXISTS chunk_feedback_audits (
    id BIGSERIAL PRIMARY KEY,
    chunk_tenant_id BIGINT NOT NULL,
    chunk_id VARCHAR(36) NOT NULL,
    actor_tenant_id BIGINT NOT NULL,
    actor_user_id VARCHAR(64) NOT NULL,
    action VARCHAR(32) NOT NULL,
    trigger_source VARCHAR(16) NOT NULL DEFAULT 'legacy',
    old_weight DOUBLE PRECISION NOT NULL,
    new_weight DOUBLE PRECISION NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT chk_chunk_feedback_audit_action CHECK (action IN ('feedback_weight_changed', 'feedback_reset')),
    CONSTRAINT chk_chunk_feedback_audit_trigger_source CHECK (
        trigger_source IN ('like', 'dislike', 'cancel', 'admin_reset', 'content_delete', 'legacy')
    )
);

-- Preserve draft weight history as bounded legacy audit entries before
-- removing the obsolete table.
DO $$
BEGIN
    IF to_regclass(format('%I.%I', current_schema(), 'chunk_feedback_weight_logs')) IS NOT NULL THEN
        INSERT INTO chunk_feedback_audits
            (chunk_tenant_id, chunk_id, actor_tenant_id, actor_user_id,
             action, trigger_source, old_weight, new_weight, created_at)
        SELECT
            chunk_tenant_id,
            chunk_id,
            actor_tenant_id,
            LEFT(actor_user_id, 64),
            'feedback_weight_changed',
            'legacy',
            old_weight,
            new_weight,
            created_at
        FROM chunk_feedback_weight_logs;
        DROP TABLE chunk_feedback_weight_logs;
    END IF;
END
$$;

CREATE INDEX IF NOT EXISTS idx_chunk_feedback_audit_chunk
    ON chunk_feedback_audits (chunk_tenant_id, chunk_id, created_at DESC);
