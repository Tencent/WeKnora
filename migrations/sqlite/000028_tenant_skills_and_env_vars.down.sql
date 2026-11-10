DROP INDEX IF EXISTS idx_user_env_var_config;
DROP INDEX IF EXISTS idx_user_env_var_skill;
DROP INDEX IF EXISTS uq_user_env_var;
DROP TABLE IF EXISTS tenant_user_env_vars;

DROP INDEX IF EXISTS uq_tenant_skill_catalog_name;
DROP TABLE IF EXISTS tenant_skill_catalog;

DROP INDEX IF EXISTS idx_tenant_skill_snapshots_state;
DROP INDEX IF EXISTS idx_tenant_skill_snapshots_config;
DROP TABLE IF EXISTS tenant_skill_snapshots;

DROP INDEX IF EXISTS idx_tenant_skills_catalog;
DROP INDEX IF EXISTS uq_tenant_skills_config_name;
DROP TABLE IF EXISTS tenant_skills;
