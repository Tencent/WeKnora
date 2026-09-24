-- Knowledge guidance exposure KB scoping (Lite). Mirrors versioned 000113.

ALTER TABLE memory_guide_exposures ADD COLUMN knowledge_base_id VARCHAR(36) NOT NULL DEFAULT '';

