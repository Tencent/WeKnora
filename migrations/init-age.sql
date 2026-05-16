-- Apache AGE 初始化脚本
-- 此脚本用于在 PostgreSQL 中安装和配置 Apache AGE 扩展
-- 执行时机：PostgreSQL 首次启动时自动执行（作为 initdb 脚本）

-- 创建 AGE 扩展（如果尚未安装）
CREATE EXTENSION IF NOT EXISTS age;

-- 加载 AGE 扩展到当前会话
LOAD 'age';

-- 设置搜索路径，确保 AGE 的 catalog 在搜索路径中
SET search_path = ag_catalog, "$user", public;

-- 注意：图的创建由应用程序在运行时自动完成
-- 应用启动时会检查图是否存在，如果不存在则自动创建
-- 默认图名称为 'weknora_kg'，可通过 AGE_GRAPH_NAME 环境变量配置

-- 可选：如果需要预先创建图，可以取消下面的注释
-- SELECT create_graph('weknora_kg');
