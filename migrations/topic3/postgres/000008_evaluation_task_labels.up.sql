CREATE TABLE IF NOT EXISTS evaluation_task_labels (
    tenant_id  BIGINT NOT NULL,
    task_id    VARCHAR(128) NOT NULL,
    label      VARCHAR(64) NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT evaluation_task_labels_pkey PRIMARY KEY (tenant_id, task_id, label),
    CONSTRAINT evaluation_task_labels_label_bytes_check CHECK (octet_length(label) <= 64),
    CONSTRAINT evaluation_task_labels_task_fk FOREIGN KEY (tenant_id, task_id)
        REFERENCES evaluation_tasks (tenant_id, id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_evaluation_task_labels_tenant_label_task
    ON evaluation_task_labels (tenant_id, label, task_id);

CREATE INDEX IF NOT EXISTS idx_evaluation_tasks_tenant_dataset_started
    ON evaluation_tasks (tenant_id, dataset_id, start_time DESC, id DESC)
    WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_evaluation_tasks_tenant_dataset_version_started
    ON evaluation_tasks (tenant_id, dataset_version_id, start_time DESC, id DESC)
    WHERE deleted_at IS NULL;
