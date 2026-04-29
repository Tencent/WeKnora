-- Migration: 000039_prompt_templates
-- Description: Add prompt_templates table for online CRUD on user-facing prompt
-- templates (system_prompt / agent_system_prompt / context_template / rewrite /
-- fallback). The table is globally shared (no tenant scope yet); rows are
-- seeded from config/prompt_templates/*.yaml on first startup if empty, and
-- thereafter become the source of truth. YAML files remain as the versioned
-- default content used to initialise / restore individual templates.
DO $$ BEGIN RAISE NOTICE '[Migration 000039] Creating prompt_templates table'; END $$;

CREATE TABLE IF NOT EXISTS prompt_templates (
    id              VARCHAR(64)              NOT NULL,
    category        VARCHAR(64)              NOT NULL,
    name            VARCHAR(255)             NOT NULL DEFAULT '',
    description     TEXT                     NOT NULL DEFAULT '',
    content         TEXT                     NOT NULL,
    user_prompt     TEXT                     NOT NULL DEFAULT '',
    has_kb          BOOLEAN                  NOT NULL DEFAULT FALSE,
    has_web_search  BOOLEAN                  NOT NULL DEFAULT FALSE,
    is_default      BOOLEAN                  NOT NULL DEFAULT FALSE,
    mode            VARCHAR(32)              NOT NULL DEFAULT '',
    i18n            JSONB                    NOT NULL DEFAULT '{}'::JSONB,
    version         INTEGER                  NOT NULL DEFAULT 1,
    created_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    PRIMARY KEY (category, id)
);

-- Restrict category to the five user-facing groups exposed today. Adding a new
-- category requires an explicit follow-up migration.
ALTER TABLE prompt_templates
    ADD CONSTRAINT chk_prompt_templates_category
    CHECK (category IN (
        'system_prompt',
        'agent_system_prompt',
        'context_template',
        'rewrite',
        'fallback'
    ));

-- Lookup by category (used when building the per-category list shown to the UI).
CREATE INDEX IF NOT EXISTS idx_prompt_templates_category
    ON prompt_templates (category);

-- Used by audit screens / future hot-reload pollers.
CREATE INDEX IF NOT EXISTS idx_prompt_templates_updated_at
    ON prompt_templates (updated_at);

COMMENT ON TABLE prompt_templates IS
    'User-managed prompt templates. Seeded from config/prompt_templates/*.yaml on first startup; thereafter DB is the source of truth. Deleting a row makes the next startup re-seed it from YAML (effective "reset to factory").';
COMMENT ON COLUMN prompt_templates.category    IS 'Template category: system_prompt | agent_system_prompt | context_template | rewrite | fallback.';
COMMENT ON COLUMN prompt_templates.user_prompt IS 'Optional user-side prompt (used by rewrite category).';
COMMENT ON COLUMN prompt_templates.mode        IS 'Optional mode discriminator inside a category (e.g. fallback: "" vs "model").';
COMMENT ON COLUMN prompt_templates.i18n        IS 'Localised name/description: {"zh-CN":{"name":"...","description":"..."}, "en-US":{...}}.';

DO $$ BEGIN RAISE NOTICE '[Migration 000039] prompt_templates table created'; END $$;
