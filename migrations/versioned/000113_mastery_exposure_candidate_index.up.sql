-- Migration 000113: index the guidance-exposure lookup that every effective
-- page view performs.
--
-- Recording a page view marks the matching guidance exposure as qualified:
--
--   UPDATE memory_guide_exposures SET qualified_view_at = ...
--   WHERE tenant_id = ? AND subject_id = ? AND knowledge_base_id = ?
--     AND candidate_slug = ? AND qualified_view_at IS NULL
--
-- The table's only index keys on (tenant_id, subject_id, shown_at), which cannot
-- serve the candidate_slug predicate, so that update degrades into scanning the
-- scope's exposure history on every recorded view. The click path (mark clicked)
-- has the same shape. This index lets both seek straight to one candidate's rows.

CREATE INDEX IF NOT EXISTS idx_mastery_exposure_candidate
    ON memory_guide_exposures (tenant_id, subject_id, knowledge_base_id, candidate_slug);
