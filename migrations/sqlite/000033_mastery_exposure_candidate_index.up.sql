-- Guidance-exposure candidate index (Lite). Mirrors versioned 000113.
--
-- Every recorded page view marks the matching exposure as qualified by
-- (knowledge_base_id, candidate_slug); without this index that update scans the
-- scope's exposure history.

CREATE INDEX IF NOT EXISTS idx_mastery_exposure_candidate
    ON memory_guide_exposures (tenant_id, subject_id, knowledge_base_id, candidate_slug);
