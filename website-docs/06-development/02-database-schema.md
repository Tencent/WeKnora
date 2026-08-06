# 数据库与迁移

本章梳理 WeKnora 的数据库支持矩阵、`migrations/` 目录全部迁移叠加后的最终表结构、表间关系（ER 图）、golang-migrate 迁移机制，以及新增迁移与常见问题排查。

## 1. 支持的数据库

主应用通过 GORM 连接数据库，驱动由环境变量 `DB_DRIVER` 决定。`internal/container/container.go` 的 `initDatabase()` 中的 switch **只接受两个值**：

| `DB_DRIVER` | 说明 |
| --- | --- |
| `postgres` | 标准模式。既支持原生 PostgreSQL（+pgvector），也支持 **ParadeDB**（PostgreSQL 分支，内置 `pg_search`/BM25，官方 compose 默认镜像 `paradedb/paradedb:v0.22.2-pg17`）。GORM DSN 由 `DB_HOST/DB_PORT/DB_USER/DB_PASSWORD/DB_NAME` 拼装，强制 `sslmode=disable`、`TimeZone=UTC` |
| `sqlite` | Lite 模式。路径取 `DB_PATH`（默认 `./data/weknora.db`），DSN 附加 `_journal_mode=WAL&_busy_timeout=5000&_foreign_keys=on`，并加载 `sqlite-vec` 扩展（`sqlite_vec.Auto()`）做向量检索 |
| 其他值 | 直接报错 `unsupported database driver` |

**MySQL 不是主库选项**：`go.mod` 里的 `go-sql-driver/mysql` 是给 Doris 检索引擎（MySQL 协议、`database/sql`）注册协议驱动用的（见 `container.go` import 注释）。`migrations/mysql/00-init-db.sql` 是一份仅含 7 张核心表（tenants/models/knowledge_bases/knowledges/sessions/messages/chunks）的一次性 MySQL 建表脚本，**没有任何 Go 代码或脚本引用它**，未接入应用启动流程，可视为遗留/外部初始化用途。

检索引擎（向量/关键词索引的存储）与主库解耦，由 `RETRIEVE_DRIVER` 控制（postgres / elasticsearch / qdrant / milvus / sqlite 等，详见《扩展点指南》）。当 `RETRIEVE_DRIVER` 不含 `postgres` 时，迁移 DSN 会带上 `options=-c app.skip_embedding=true`，`embeddings` 表相关迁移通过该 GUC 条件跳过。

## 2. 迁移目录结构

```text
migrations/
├── versioned/     # PostgreSQL/ParadeDB 版本化迁移：000000-000079 共 80 版（160 个 .up/.down.sql 文件）
├── sqlite/        # SQLite 迁移：000000_init（压平的全量 schema）+ 其后的增量版本
├── paradedb/      # ParadeDB 附加脚本：00-init-db.sql（扩展初始化）、01-migrate-to-paradedb.sql（存量库切换）
└── mysql/         # 00-init-db.sql，遗留的一次性 MySQL 建表脚本（未接入代码）
```

- `versioned/` 是唯一的"增量历史"，从 `000000_init` 到 `000079_knowledge_folder_path`；
- `sqlite/` 以 `000000_init` 作为压平后的全量初始化（JSONB→TEXT、SERIAL→AUTOINCREMENT 等方言差异已适配），其后按需追加增量版本（当前有 `000001_remove_wiki_log`、`000002_knowledge_folder_path`），同样由 golang-migrate 顺序执行；
- `paradedb/00-init-db.sql` 创建 `pg_search` 等扩展；BM25 索引使用中文 Lindera 分词器建在 `embeddings.content` 上。

### 2.1 versioned/ 迁移史概览（按主题）

| 版本段 | 主题 | 引入的关键表/列 |
| --- | --- | --- |
| 000000 | 核心初始化 | `tenants`、`models`、`knowledge_bases`、`knowledges`、`chunks`、`sessions`、`messages` |
| 000001 | 用户认证 + Agent + MCP | `users`、`auth_tokens`、`custom_agents`、`mcp_services`、`knowledge_tags` |
| 000002-000011 | 向量/检索 | `embeddings`（HNSW + BM25，受 `app.skip_embedding` 门控）、`chunks.flags`、`seq_id`、ParadeDB BM25 索引 |
| 000012-000018 | 跨租户协作 | `organizations`、`organization_members`、`kb_shares`、`agent_shares`、`organization_join_requests` |
| 000019-000028 | 消息/IM 增强 | `messages` 扩列（images、rendered_content、agent_duration_ms）、`im_channels`、`im_channel_sessions` |
| 000029-000036 | 数据源与向量库抽象 | `data_sources`、`sync_logs`、`web_search_providers`、`vector_stores`、KB 的 asr_config/vector_store_id |
| 000037-000041 | Wiki 与任务队列 | `wiki_pages`、`wiki_folders`、`wiki_page_issues`、`wiki_log_entries`（已于 000077 移除）、`task_pending_ops`、`task_dead_letters` |
| 000042-000054 | RBAC / 审计 / 邀请 | `mcp_tool_approvals`、`tenant_members`、`audit_logs`、`organization_tenant_members`、`user_resource_favorites`、`tenant_invitations`、`user_kb_pins`、`invitation_tokens` |
| 000055-000060 | 处理管道与嵌入渠道 | `knowledge_processing_spans`、`knowledge_pending_subtasks`、`embed_channels`、HNSW 1024 维索引 |
| 000061-000067 | Wiki 层级 / OAuth / 文档多标签 / 建议问题 | `wiki_pages` 层级列、`mcp_oauth_clients`、`mcp_oauth_tokens`、`knowledge_tag_relations`、`principals`、`principal_models`、`tenant_api_keys`、`message_suggestion_sets`、`message_suggestion_events` |
| 000068-000074 | 存储/资源/临时文档 | `storage_backends`、`resources`、`resource_bindings`、`resource_access_grants`、`temporary_documents`、平台级 API key、OAuth 刷新租期 |
| 000075-000076 | Wiki 版本历史与索引 | `wiki_page_revisions`、`wiki_pages.last_edit_source`/`last_editor_id`、`knowledges.metadata->>'external_id'` 前缀索引 |
| 000077 | 移除 Wiki 操作日志 | DROP `wiki_log_entries`，并删除历史遗留的 `page_type = 'log'` 页面；Wiki 变更统一记入知识库活动流 |
| 000078 | 分块编辑与自定义元数据 | `chunks` 增加 `source_content`/`content_revision`/`index_status`/`last_editor_id`/`context_header`，新增 `chunk_revisions` 表，`knowledges` 增加 `custom_metadata` |
| 000079 | 知识库文件夹树 | `knowledges` 增加 `folder_path` 列并回填历史目录上传（原先路径塞在 `file_name` 里），新增 `(tenant_id, knowledge_base_id, folder_path)` 索引 |

## 3. 最终表结构

以下为全部 up 迁移叠加后的**最终生效结构**（后续迁移对早期表的 ALTER 已合并）。所有业务表统一带 `created_at` / `updated_at`，多数带 `deleted_at`（GORM 软删除），不再逐一列出。

### 3.1 租户与用户

| 表 | 用途 | 关键字段 |
| --- | --- | --- |
| `tenants` | 租户（工作空间），多租户体系根 | `id`（SERIAL，起始 10000）、`name`、`api_key`（唯一索引）、`retriever_engines`（JSONB）、`status`、`storage_quota`/`storage_used`、`agent_config`/`context_config`/`conversation_config`/`web_search_config`/`credentials`（JSONB）、`default_storage_backend_id` |
| `users` | 登录用户 | `id`（UUID）、`username`（唯一）、`email`（唯一）、`password_hash`、`tenant_id`（FK→tenants，ON DELETE SET NULL）、`is_active`、`can_access_all_tenants`（系统管理员）、`preferences`（JSON） |
| `auth_tokens` | 登录令牌 | `id`、`user_id`（FK→users，CASCADE）、`token`、`token_type`（access/refresh）、`expires_at`（TIMESTAMPTZ，000072 起）、`is_revoked` |
| `tenant_members` | 租户级 RBAC 成员关系 | `user_id`+`tenant_id`（软删除下唯一）、`role`（owner/admin/contributor/viewer）、`status`、`invited_by`、`joined_at` |
| `tenant_invitations` | 站内邀请 | `tenant_id`、`invitee_user_id`、`role`、`status`（pending/accepted/rejected）、`expires_at`；pending 唯一约束 |
| `invitation_tokens` | 邀请链接令牌（000054） | token 与租户/角色绑定 |
| `tenant_api_keys` | 租户/平台 API Key | `tenant_id`（platform 作用域时为 NULL）、`scope_type`（tenant/platform，CHECK 约束）、`key_hash`（唯一）、`full_access`、`knowledge_base_ids`、`capabilities`、`expires_at`/`revoked_at` |
| `user_kb_pins` | 用户级知识库置顶 | PK（`tenant_id`,`user_id`,`kb_id`）+ `pinned_at` |
| `user_resource_favorites` | 用户收藏 | PK（`user_id`,`tenant_id`,`resource_type`,`resource_id`） |
| `audit_logs` | 审计日志（000044） | `tenant_id`、`actor_user_id`/`actor_role`、`action`、`target_type`/`target_id`/`target_user_id`、`request_path`/`request_method`、`outcome`（success/denied）、`scope_type`/`scope_id`、`details`（JSONB） |

### 3.2 模型与知识库

| 表 | 用途 | 关键字段 |
| --- | --- | --- |
| `models` | AI 模型配置（LLM/embedding/rerank 等） | `id`、`tenant_id`（FK→tenants，CASCADE）、`name`/`display_name`、`type`（embedding/summary/rerank/llm…）、`source`、`parameters`（JSONB）、`is_default`、`is_builtin`、`managed_by`、`status` |
| `knowledge_bases` | 知识库 | `id`（UUID）、`tenant_id`、`name`、`type`（document/faq）、`chunking_config`/`image_processing_config`/`vlm_config`/`faq_config`/`asr_config`/`wiki_config`/`indexing_strategy`（JSONB）、`embedding_model_id`/`summary_model_id`（FK→models）、`vector_store_id`（FK→vector_stores）、`storage_backend_id`（FK→storage_backends）、`creator_id`（FK→users）、`is_temporary`、`activity_scope` |
| `knowledges` | 知识条目（文档/网页/FAQ 等） | `id`、`tenant_id`、`knowledge_base_id`（FK）、`type`、`title`、`source`（VARCHAR(2048)）、`parse_status`（unprocessed/processing/completed/failed）、`enable_status`、`file_name`/`file_type`/`file_size`/`file_path`/`file_hash`、`metadata`（内部入库状态）、`custom_metadata`（JSONB，用户自填元数据，000078）、`folder_path`（目录树路径，000079）、`summary_status`、`channel`、`processed_at`/`error_message`。**没有 `tag_id` 列**——000063 起标签走 `knowledge_tag_relations` 关联表 |
| `chunks` | 分块（检索最小单元） | `id`、`tenant_id`、`knowledge_base_id`、`knowledge_id`（FK）、`content`、`source_content`（解析器原始输出，不可变）、`content_revision`、`index_status`（ready/processing/failed）、`last_editor_id`、`context_header`（索引用标题面包屑）、`chunk_index`、`start_at`/`end_at`、`pre_chunk_id`/`next_chunk_id`（链表）、`parent_chunk_id`（父子分块自引用）、`chunk_type`（text/image/…）、`image_info`/`video_info`、`relation_chunks`/`indirect_relation_chunks`（JSONB）、`is_enabled`、`flags`、`status`、`content_hash`、`seq_id`、`tag_id` |
| `chunk_revisions` | 分块历史版本（000078） | `id`、`tenant_id`、`knowledge_base_id`、`knowledge_id`、`chunk_id`+`revision`（唯一索引）、`content`、`is_enabled`、`editor_id`、`edit_source`、`edited_at` |
| `embeddings` | 向量 + BM25 索引（Postgres/ParadeDB 检索引擎专用，受 `app.skip_embedding` 门控） | `id`、`source_id`+`source_type`（唯一，chunk/wiki 页等来源）、`chunk_id`/`knowledge_id`/`knowledge_base_id`、`content`（BM25 全文）、`dimension`、`embedding`（halfvec，HNSW 索引按 768/1024/3584 维分建）、`is_enabled`、`tag_id` |
| `knowledge_tags` | 知识标签（FAQ 分类等） | `id`、`tenant_id`、`knowledge_base_id`、`name`、`seq_id` |
| `knowledge_tag_relations` | 文档 ↔ 标签多对多（000063） | 复合主键（`knowledge_id`,`tag_id`）+ `created_at`；两侧各建索引。**同时删掉了 `knowledges.tag_id` 列**（存量单标签数据已迁入本表）。FAQ 条目的标签不在这里，仍是 `chunks.tag_id` 单标签 |
| `vector_stores` | 外接向量库连接配置（000032） | `id`、`tenant_id`、`name`（租户内唯一）、`engine_type`、`connection_config`/`index_config`（JSONB） |

### 3.3 会话与消息

| 表 | 用途 | 关键字段 |
| --- | --- | --- |
| `sessions` | 会话（对话上下文与检索参数快照） | `id`、`tenant_id`、`title`、`knowledge_base_id`、`agent_id`（FK→custom_agents）、`user_id`、`max_rounds`、`enable_rewrite`、`fallback_strategy`/`fallback_response`、`keyword_threshold`/`vector_threshold`、`embedding_top_k`/`rerank_top_k`/`rerank_threshold`、`rerank_model_id`/`summary_model_id`、`agent_config`/`context_config`（JSONB） |
| `messages` | 消息 | `id`、`request_id`、`session_id`（FK）、`role`、`content`/`rendered_content`、`knowledge_references`（JSONB 引用）、`agent_steps`（JSONB，Agent 推理轨迹）、`mentioned_items`/`images`（JSONB）、`is_completed`/`is_fallback`、`channel`（web/IM 渠道）、`agent_id`+`agent_tenant_id`、`model_id`、`knowledge_id`、`agent_duration_ms`、`execution_context` |
| `message_suggestion_sets` | 建议问题集（000067） | `tenant_id`、`session_id`、`assistant_message_id`、`placement`（starter/follow_up）、`config_hash`+`locale`（缓存键，唯一）、`status`、`questions`（JSONB）、token/延迟统计、`lease_until` |
| `message_suggestion_events` | 建议问题曝光/点击事件 | `suggestion_set_id`（FK，CASCADE）、`question_id`、`event_type`、`actor_id` |
| `temporary_documents` | 会话内临时文档（000070） | `tenant_id`、`session_id`、`resource_ref`、`file_name`/`file_type`/`file_size`、`status`（uploaded/processing/ready/expired）、`content`、`chunks`（JSONB）、`expires_at` |

### 3.4 Agent 与 MCP

| 表 | 用途 | 关键字段 |
| --- | --- | --- |
| `custom_agents` | 自定义 Agent | **复合主键 (`id`,`tenant_id`)**、`name`、`is_builtin`、`created_by`（FK→users）、`runnable_by_viewer`、`config`（JSONB：模式/模型/工具/知识范围） |
| `mcp_services` | MCP 服务配置 | `id`、`tenant_id`、`name`、`enabled`、`transport_type`（stdio/sse/…）、`url`/`headers`/`auth_config`/`stdio_config`/`env_vars`（JSONB）、`is_builtin` |
| `mcp_tool_approvals` | MCP 工具审批策略（000042） | (`tenant_id`,`service_id`,`tool_name`) 唯一、`require_approval` |
| `mcp_oauth_clients` | MCP OAuth 客户端（000062） | (`tenant_id`,`service_id`) 唯一、`client_id`/`client_secret`/`redirect_uri` |
| `mcp_oauth_tokens` | MCP OAuth 令牌 | (`tenant_id`,`user_id`,`service_id`) 唯一、`access_token`/`refresh_token`、`expires_at`、`refresh_lease_id`/`refresh_lease_until`（000074，防并发刷新） |
| `principals` / `principal_models` | 主体—模型授权（000064） | 主体（用户/租户）可用模型映射 |

### 3.5 跨租户协作（组织）

| 表 | 用途 | 关键字段 |
| --- | --- | --- |
| `organizations` | 组织（跨租户协作单元，000012） | `id`、`name`、`owner_id`（FK→users）、`owner_tenant_id`、`invite_code`（唯一）+ 过期控制、`require_approval`、`searchable`、`member_limit` |
| `organization_members` | 组织的用户成员 | `organization_id`（FK，CASCADE）、`user_id`、`tenant_id`、`role` |
| `organization_tenant_members` | 组织的租户成员（000045） | (`organization_id`,`tenant_id`) 唯一、`role`（admin/editor/viewer）、`representative_user_id` |
| `organization_join_requests` | 加入/升级申请 | `organization_id`、`user_id`、`status`（pending 唯一）、`requested_role`、`request_type`（join/upgrade）、审批字段 |
| `kb_shares` | 知识库共享到组织 | (`knowledge_base_id`,`organization_id`) 软删除下唯一、`source_tenant_id`、`permission` |
| `agent_shares` | Agent 共享到组织 | FK (`agent_id`,`source_tenant_id`)→custom_agents 复合主键、`organization_id`、`permission` |
| `tenant_disabled_shared_agents` | 租户禁用某共享 Agent | PK（`tenant_id`,`agent_id`,`source_tenant_id`） |

### 3.6 Wiki

| 表 | 用途 | 关键字段 |
| --- | --- | --- |
| `wiki_pages` | AI 生成的 Wiki 页面（000037） | `id`、`tenant_id`、`knowledge_base_id`、`slug`（KB 内唯一）、`title`、`page_type`（summary/index/…）、`status`、`content`/`summary`、层级列（000061：`parent_slug`、`folder_id`、`category_path`、`wiki_path`、`depth`、`sort_order`）、`source_refs`/`chunk_refs`/`in_links`/`out_links`（JSONB）、`version`；全文 GIN/tsvector + trigram 索引 |
| `wiki_folders` | Wiki 文件夹树 | `knowledge_base_id`、`parent_id`（邻接表）、`name`（同父下唯一）、`path`（物化路径）、`depth`、`sort_order` |
| `wiki_page_issues` | 页面问题上报 | `knowledge_base_id`、`slug`、`issue_type`、`description`、`suspected_knowledge_ids`、`status`、`reported_by` |
| `wiki_page_revisions` | Wiki 页面历史版本（000075） | `page_id`+`version`（唯一索引）、标题/正文/摘要/类型/状态/别名快照、`edit_source`（pipeline/agent/user/revert）、`editor_id`、`edited_at`；两级保留上限：软 50 版（只裁 pipeline 与空来源）/ 硬 200 版 |

### 3.7 数据源 / 渠道 / 搜索

| 表 | 用途 | 关键字段 |
| --- | --- | --- |
| `data_sources` | 外部数据源连接（Feishu/Notion/语雀/RSS，000029） | `id`、`tenant_id`、`knowledge_base_id`、`type`、`config`（JSONB 凭证）、`sync_schedule`（cron）、`sync_mode`（incremental/full）、`conflict_strategy`、`sync_deletions`、`last_sync_at`/`last_sync_cursor`/`last_sync_result` |
| `sync_logs` | 每次同步的执行记录 | `data_source_id`（FK，CASCADE）、`status`、`started_at`/`finished_at`、`items_total/created/updated/deleted/skipped/failed`、`error_message` |
| `im_channels` | IM 渠道接入配置（企业微信/飞书/Slack 等） | `tenant_id`、`platform`、`agent_id`、`knowledge_base_id`、凭证配置 |
| `im_channel_sessions` | IM 用户/线程 ↔ session 映射 | `im_channel_id`、`session_id`、`agent_id`、平台用户/会话标识 |
| `embed_channels` | 网页嵌入聊天组件渠道（000060） | `tenant_id`、`agent_id`、公开 token/域名配置 |
| `web_search_providers` | 联网搜索引擎配置（000030） | `id`、`tenant_id`、`name`、`provider`（bing/google/tavily/searxng…）、`parameters`（JSONB API key）、`is_default` |

### 3.8 存储 / 资源 / 任务 / 可观测

| 表 | 用途 | 关键字段 |
| --- | --- | --- |
| `storage_backends` | 对象存储后端配置（000068） | `id`、`tenant_id`、`name`（租户内唯一）、`provider`（local/minio/cos/oss/s3/obs/tos/ks3）、`config`（JSONB）、`source`（user/system）、`legacy_alias` |
| `resources` | 统一资源注册表（000069） | `id`、`handle`（22 位短句柄，唯一）、`tenant_id`、`storage_backend_id`、`provider`、`physical_path`、`location_hash`（租户内唯一）、`mime_type`/`original_name`/`size`/`content_hash`、`lifecycle`（persistent/temporary）+`expires_at`、`state` |
| `resource_bindings` | 资源 ↔ 属主（消息/知识/会话）多态绑定 | (`resource_id`,`owner_type`,`owner_id`,`relation`) 唯一 |
| `resource_access_grants` | 资源临时访问令牌 | `token_hash`（唯一）、`resource_id`、`access_scope`、`expires_at`/`revoked_at` |
| `task_pending_ops` | 通用待处理任务队列（000041） | `tenant_id`、`task_type`、`scope`+`scope_id`、`op`、`dedup_key`、`payload`（JSONB）、`fail_count`、`enqueued_at`/`claimed_at`（并发领取） |
| `task_dead_letters` | 失败任务死信归档 | `task_type`、`scope`/`scope_id`/`related_id`、`payload`、`last_error`、`fail_count`、`failed_at` |
| `knowledge_pending_subtasks` | 知识处理子任务队列（000056） | `knowledge_id`、`attempt`、`task_type`、payload |
| `knowledge_processing_spans` | 文档处理管道 trace（000055） | (`knowledge_id`,`attempt`,`span_id`) 唯一、`parent_span_id`、`name`（DocReader/Chunking/Embedding…）、`kind`、`status`、`input`/`output`/`metadata`（JSONB）、`error_code`/`error_message`、`duration_ms` |
| `schema_migrations` | golang-migrate 状态表（自动维护） | `version`、`dirty` |

## 4. ER 图（核心表）

```mermaid
erDiagram
    tenants ||--o{ users : "tenant_id (SET NULL)"
    tenants ||--o{ tenant_members : "租户成员"
    users ||--o{ tenant_members : "user_id"
    users ||--o{ auth_tokens : "登录令牌"
    tenants ||--o{ models : "模型配置"
    tenants ||--o{ knowledge_bases : "知识库"
    tenants ||--o{ tenant_api_keys : "API Key"
    tenants ||--o{ audit_logs : "审计"
    users ||--o{ audit_logs : "actor_user_id"

    knowledge_bases ||--o{ knowledges : "文档"
    knowledge_bases }o--|| models : "embedding_model_id"
    knowledge_bases }o--o| vector_stores : "vector_store_id"
    knowledge_bases }o--o| storage_backends : "storage_backend_id"
    knowledge_bases }o--o| users : "creator_id"
    knowledges ||--o{ chunks : "分块"
    chunks ||--o| chunks : "parent_chunk_id (父子分块)"
    chunks ||--o| embeddings : "source_id (向量/BM25)"
    knowledge_bases ||--o{ knowledge_tags : "标签"
    knowledge_tags ||--o{ knowledge_tag_relations : "多标签关联"
    knowledges ||--o{ knowledge_tag_relations : "knowledge_id"

    tenants ||--o{ sessions : "会话"
    sessions ||--o{ messages : "消息"
    sessions }o--o| custom_agents : "agent_id"
    sessions }o--o| knowledge_bases : "knowledge_base_id"
    messages }o--o| knowledges : "knowledge_id"
    messages ||--o{ message_suggestion_sets : "建议问题"
    message_suggestion_sets ||--o{ message_suggestion_events : "事件"
    sessions ||--o{ temporary_documents : "临时文档"

    tenants ||--o{ custom_agents : "自定义 Agent"
    tenants ||--o{ mcp_services : "MCP 服务"
    mcp_services ||--o{ mcp_tool_approvals : "工具审批"
    mcp_services ||--o{ mcp_oauth_clients : "OAuth 客户端"
    mcp_services ||--o{ mcp_oauth_tokens : "OAuth 令牌"

    users ||--o{ organizations : "owner_id"
    organizations ||--o{ organization_tenant_members : "租户成员"
    organizations ||--o{ kb_shares : "知识库共享"
    organizations ||--o{ agent_shares : "Agent 共享"
    organizations ||--o{ organization_join_requests : "加入申请"
    knowledge_bases ||--o{ kb_shares : "被共享"
    custom_agents ||--o{ agent_shares : "被共享 (id, tenant_id)"

    knowledge_bases ||--o{ wiki_pages : "Wiki 页面"
    wiki_pages }o--o| wiki_folders : "folder_id"
    wiki_folders ||--o{ wiki_folders : "parent_id (树)"
    knowledge_bases ||--o{ wiki_page_issues : "问题上报"

    knowledge_bases ||--o{ data_sources : "数据源"
    data_sources ||--o{ sync_logs : "同步日志"
    tenants ||--o{ web_search_providers : "联网搜索配置"
    tenants ||--o{ im_channels : "IM 渠道"
    im_channels ||--o{ im_channel_sessions : "渠道会话映射"
    im_channel_sessions }o--|| sessions : "session_id"
    tenants ||--o{ embed_channels : "嵌入渠道"

    tenants ||--o{ storage_backends : "存储后端"
    tenants ||--o{ resources : "资源"
    resources }o--o| storage_backends : "storage_backend_id"
    resources ||--o{ resource_bindings : "多态绑定 (message/knowledge/session)"
    resources ||--o{ resource_access_grants : "访问授权"

    tenants ||--o{ task_pending_ops : "任务队列"
    tenants ||--o{ task_dead_letters : "死信"
    knowledges ||--o{ knowledge_processing_spans : "处理 trace"
```

## 5. 迁移机制（golang-migrate）

迁移工具是 **golang-migrate/migrate v4**（`go.mod`：`github.com/golang-migrate/migrate/v4 v4.19.1`），状态记录在 `schema_migrations` 表（`version` + `dirty`）。有两条执行路径：

### 5.1 应用启动时自动迁移（默认）

`internal/container/container.go` 的 `initDatabase()`：

- `AUTO_MIGRATE != "false"` 时（**默认开启**），调用 `database.RunMigrationsWithOptions(migrateDSN, opts)`；
- `AUTO_RECOVER_DIRTY != "false"` 时（**默认开启**）设置 `MigrationOptions.AutoRecoverDirty = true`，遇到 dirty state 自动尝试恢复；
- 迁移失败**只打 Warn 日志不阻断启动**（假设迁移可能由外部管理），排查问题时务必看启动日志；
- postgres 的 migrate DSN 会拼上 `options=-c app.skip_embedding=<true|false>`（取决于 `RETRIEVE_DRIVER` 是否包含 `postgres`），控制 `embeddings` 相关迁移是否实际建表建索引。

`internal/database/migration.go` 中的路径选择逻辑：

```go
// internal/database/migration.go
migrationsPath := "file://migrations/versioned"
if strings.HasPrefix(dsn, "sqlite3://") {
    migrationsPath = "file://migrations/sqlite"
}
```

即 postgres/ParadeDB 走 `migrations/versioned/`，SQLite 走 `migrations/sqlite/`。

### 5.2 手工执行：scripts/migrate.sh

`scripts/migrate.sh` 是 `migrate` CLI 的包装（Makefile 的 `migrate-*` 目标调用它）：

- 自动加载根目录 `.env`；
- DSN 优先取 `DB_URL`（并把 `sslmode=require/prefer` 强制替换为 `disable`），否则由 `DB_HOST/DB_PORT/DB_USER/DB_PASSWORD/DB_NAME` 拼装（默认 `localhost:5432/postgres/WeKnora`），密码用 Python `urllib.parse.quote` URL 编码以兼容特殊字符；
- 迁移目录默认 `MIGRATIONS_DIR=migrations/versioned`；
- 未安装 `migrate` 时提示：`go install -tags 'postgres' github.com/golang-migrate/migrate/v4/cmd/migrate@latest`。

```bash
make migrate-up                    # 应用全部待执行迁移
make migrate-down                  # 回滚
make migrate-version               # 查看当前版本与 dirty 标志
make migrate-create name=add_xxx   # 创建 000080_add_xxx.up.sql / .down.sql
make migrate-force version=74      # 强制标记版本（恢复 dirty）
make migrate-goto version=60       # 迁移/回滚到指定版本
```

## 6. 如何新增一个迁移

1. **创建文件**：`make migrate-create name=add_my_feature`，在 `migrations/versioned/` 下生成下一个版本号（当前最大为 `000079`，新迁移将是 `000080_add_my_feature.up.sql` / `.down.sql`）；
2. **编写 up SQL**：注意 PostgreSQL 方言（JSONB、部分索引、`TIMESTAMP WITH TIME ZONE`）；若涉及 `embeddings` 表，参考既有迁移用 `app.skip_embedding` GUC 做条件门控（`SELECT current_setting('app.skip_embedding', true)`），保证非 postgres 检索引擎部署也能通过迁移；
3. **编写 down SQL**：必须可逆（drop column/table/index），否则回滚链会断；
4. **同步 SQLite**：`migrations/sqlite/000000_init.up.sql` 是压平的全量 schema，**新增列/表必须合并进去**（注意方言转换：JSONB→TEXT、SERIAL→INTEGER AUTOINCREMENT、无部分索引语法差异等）。若变更需要在已有 Lite 库上生效（例如删表、删数据），还要在 `migrations/sqlite/` 追加一个增量版本；
5. **同步 GORM 模型**：在 `internal/types/` 对应 struct 增加字段（GORM 只做 ORM 映射，生产库**不使用 AutoMigrate** 建表，schema 完全由 SQL 迁移驱动）;
6. **验证**：`make migrate-up` → `make migrate-down` → `make migrate-up` 三连确认可逆；SQLite 侧用 `DB_DRIVER=sqlite` 启动一次 Lite 版验证初始化脚本。

## 7. 常见迁移问题排查

### 7.1 dirty state（最常见）

迁移中途失败/进程被杀后，`schema_migrations.dirty = true`，后续迁移拒绝执行。

```bash
# 1. 确认状态
make migrate-version            # 输出形如 "74 (dirty)"
# 或直接查表
# SELECT version, dirty FROM schema_migrations;

# 2. 人工检查该版本的 up SQL 实际执行到哪，把残留补齐或清理

# 3. 强制回到上一个干净版本后重试
make migrate-force version=73
make migrate-up
```

应用默认 `AUTO_RECOVER_DIRTY` 开启（`container.go`），启动时会自动尝试恢复；若关闭（设为 `false`），日志会提示手工使用 force。

### 7.2 迁移"成功"但表没建出来

检查启动日志：自动迁移失败只是 Warn（`Database migration failed ... Continuing with application startup`），不会让进程退出。另外 `embeddings` 相关对象受 `app.skip_embedding` 门控——若 `RETRIEVE_DRIVER` 不含 `postgres`，不建 `embeddings` 索引属预期行为。

### 7.3 密码特殊字符导致连接失败

`migrate` CLI 要求 URL 形式 DSN，密码含 `@ # !` 等字符必须 URL 编码。`scripts/migrate.sh` 和 `container.go` 都已处理（分别用 Python `quote` 与 Go `url.QueryEscape`）；自己手拼 `DB_URL` 时需自行编码。

### 7.4 ParadeDB / 原生 Postgres 差异

BM25 索引（`USING bm25`、Lindera 中文分词）只在 ParadeDB 可用；原生 Postgres 部署需保证相应迁移的条件分支生效或改用 Elasticsearch 等外部检索引擎。存量原生 Postgres 库切到 ParadeDB 可参考 `migrations/paradedb/01-migrate-to-paradedb.sql`。

### 7.5 版本文件冲突

多个分支同时新增同一个版本号（如两个 `000080_*`）会冲突：golang-migrate 按数字排序且版本号唯一。合并时后合入者需要把自己的迁移改成下一个空闲版本号（up/down 两个文件都要改名）。
