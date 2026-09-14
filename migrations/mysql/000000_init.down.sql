-- Migration: 000000_init (MySQL) — rollback
DROP TABLE IF EXISTS message_suggestion_events;
DROP TABLE IF EXISTS message_suggestion_sets;
DROP TABLE IF EXISTS chunk_revisions;
DROP TABLE IF EXISTS task_pending_ops;
DROP TABLE IF EXISTS system_settings;
DROP TABLE IF EXISTS tenant_members;
DROP TABLE IF EXISTS organizations;
DROP TABLE IF EXISTS messages;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS chunks;
DROP TABLE IF EXISTS knowledges;
DROP TABLE IF EXISTS knowledge_bases;
DROP TABLE IF EXISTS models;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS tenants;
