DROP TABLE IF EXISTS skill_execution_audits;
ALTER TABLE IF EXISTS tenant_skills DROP CONSTRAINT IF EXISTS fk_tenant_skills_current_version;
DROP TABLE IF EXISTS tenant_skill_versions;
DROP TABLE IF EXISTS tenant_skills;
