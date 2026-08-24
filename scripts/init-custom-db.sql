-- 自研后端数据库初始化（Postgres init container 自动跑）
-- 与 WeKnora 库隔离：在 POSTGRES_DB（默认库）上下文里 CREATE DATABASE vidsage
-- custom-backend 用 CUSTOM_DB_NAME=vidsage 连过来跑 migrations

-- 幂等：若已存在则忽略
SELECT 'CREATE DATABASE vidsage OWNER postgres ENCODING ''UTF8'''
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = 'vidsage')\gexec