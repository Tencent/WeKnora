# Evaluation Dataset Studio 配置中心与知识库分块导入开发计划

## 1. 规划结论

- 将“AI 生成模型配置”和“WeKnora 环境配置”从数据集编辑页移到独立的全局配置中心。数据集只引用配置 ID，不复制地址、参数或 API Key。
- 从 WeKnora 读取已经解析完成的知识库文档分块，经预览、筛选、去重后导入当前数据集的语料，再复用已有 AI 生成能力产生问题和标准答案候选。
- 只修改本地 Evaluation Dataset Studio，不修改本地或生产 WeKnora 后端，也不依赖生产评测接口 401/500 问题先修复。
- 当前仓库接口可以直接实现；生产环境必须先通过只读兼容检查，不能靠猜测接口字段适配。
- 来源信息只保存在 Studio 项目中，最终仍导出严格的 WeKnora 五 Parquet ZIP。

## 2. 要解决的问题

### 2.1 配置重复维护

目前两类配置已经分别保存在 workspace 的 `generation-connections/` 和 `connections/`，但编辑表单仍嵌在“AI 生成”和“评测运行”页中。新建或切换数据集时容易被误解为需要重新填写，也没有统一的默认配置和引用关系管理。

### 2.2 知识库分块不能直接进入语料

当前 WeKnora 已有以下只读接口：

- `GET /api/v1/knowledge-bases`：知识库列表。
- `GET /api/v1/knowledge-bases/{kb_id}/knowledge`：知识库文档列表。
- `GET /api/v1/chunks/{knowledge_id}`：文档分块列表。

Studio 目前只支持人工维护、CSV 和五 Parquet ZIP 导入，缺少“知识库分块 → Studio 语料”的入口。

### 2.3 分块不等于完整评测样本

导入分块只形成评测语料。之后仍需生成或维护问题、标准答案和答案要点，建立问题到黄金语料的关系，经人工审核后才能导出完整数据集。

## 3. 目标流程

```text
配置中心
  ├─ 保存默认 AI 生成模型
  └─ 保存 WeKnora 环境、API Key 和资源绑定
                 │
                 ▼
新建数据集（自动引用默认配置）
                 │
                 ▼
选择知识库 → 文档 → 分块
                 │
                 ▼
预览、去重并导入为语料
                 │
                 ▼
调用引用的 AI 配置生成问题和答案候选
                 │
                 ▼
人工审核 → 校验 → 五 Parquet ZIP → 评测运行
```

## 4. 范围与非目标

### 4.1 本轮交付

1. 独立配置中心。
2. 两类配置的增删改查、测试连接和默认项设置。
3. 数据集引用配置并自动继承 workspace 默认项。
4. WeKnora 知识库、文档和分块的只读兼容检查。
5. 分块分页读取、预览、选择、去重和批量导入。
6. 来源追溯、内容哈希和导入摘要。
7. 分块导入与现有 AI 生成、审核、校验和导出流程打通。

### 4.2 本轮不做

1. 不上传、重新解析或修改 WeKnora 文档和分块。
2. 不把 Studio 语料反向同步到 WeKnora。
3. 不自动删除知识库重建后消失的本地语料。
4. 不假设生产 WeKnora 与当前仓库接口一致。
5. 不让 AI 候选绕过审核直接进入正式数据集。
6. 不把 chunk UUID 直接作为 Parquet `pid`。
7. 暂不在 Studio 内解析 PDF、DOCX 等原始文件；本地文档解析保留为后续能力。

## 5. 配置与数据设计

### 5.1 三层配置边界

| 层级 | 保存内容 | 含密钥 | 生命周期 |
| --- | --- | --- | --- |
| 生成模型配置 | Base URL、模型、提示词、超时、题数、token 和费用参数 | 是 | workspace 全局 |
| WeKnora 环境配置 | 环境、Base URL、Adapter、API Key、默认知识库/模型、dataset ID、部署说明 | 是 | workspace 全局 |
| 数据集配置引用 | `generation_profile_id`、`evaluation_profile_id` | 否 | 随数据集保存 |

继续复用现有两个配置目录，文件权限保持 `0600`。配置中心统一管理入口，但不把两类密钥合并到一个文件。

### 5.2 workspace 默认项

新增 `workspace-settings.json`：

```json
{
  "schema_version": 1,
  "default_generation_profile_id": "deepseek-flash",
  "default_evaluation_profile_id": "production-manual"
}
```

规则：

1. 新建数据集自动绑定当前默认配置。
2. 旧数据集按“数据集绑定 → workspace 默认 → 未选择”解析。
3. 数据集可以更换引用，但编辑配置内容必须进入配置中心。
4. 修改配置对其后续调用生效，不改写历史任务。
5. 历史任务只保存配置 ID 和必要的非敏感快照，不保存 API Key。
6. 被数据集引用的配置禁止直接删除，并显示引用数据集。

### 5.3 数据集 Schema v2

项目新增两个可选字段：

```json
{
  "generation_profile_id": "deepseek-flash",
  "evaluation_profile_id": "production-manual"
}
```

- v1 项目继续可读，保存时升级。
- 项目复制和备份保留配置 ID，但不包含密钥。
- 导入到缺少对应配置的 workspace 时标记“配置待绑定”，不自动创建或猜测凭据。
- 五 Parquet 导出不受 Schema 升级影响。

### 5.4 语料来源追溯

使用现有 `Passage.Source` 和 `Passage.Metadata` 保存：

```json
{
  "source": "用户手册.pdf / 安装配置",
  "metadata": {
    "source_type": "weknora-chunk",
    "source_profile_id": "production-readonly",
    "source_knowledge_base_id": "...",
    "source_knowledge_id": "...",
    "source_chunk_id": "...",
    "source_document_name": "用户手册.pdf",
    "source_chunk_index": "12",
    "source_content_sha256": "...",
    "imported_at": "2026-09-23T08:00:00Z"
  }
}
```

不保存 API Key、请求头或原始响应。Studio 内部 passage ID 继续使用本地单调递增整数。

## 6. Adapter 与本地 API

### 6.1 新增只读数据源能力

为 Adapter 增加可选的 `ChunkSourceAdapter`：

```go
type ChunkSourceAdapter interface {
    ListKnowledgeBases(...)
    ListKnowledge(kbID string, ...)
    ListChunks(knowledgeID string, ...)
}
```

- `local-current` 实现并声明 `knowledge_chunk_import=true`。
- `manual-export` 不实现，页面明确显示不支持远程导入。
- 生产配置只有在三个只读接口、分页和关键字段检查通过后才开放导入。
- 生产版本差异通过新的版本化 Adapter 解决，不污染 `local-current`。

### 6.2 建议新增 Studio API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET/PUT` | `/api/settings` | 读取或保存 workspace 默认配置 |
| `GET` | `/api/configuration-references/{type}/{id}` | 查询配置引用 |
| `POST` | `/api/source-profiles/{id}/probe` | 只读检查分块契约 |
| `GET` | `/api/source-profiles/{id}/knowledge-bases` | 分页读取知识库 |
| `GET` | `/api/source-profiles/{id}/knowledge-bases/{kb_id}/knowledge` | 分页读取文档 |
| `GET` | `/api/source-profiles/{id}/knowledge/{knowledge_id}/chunks` | 分页读取分块 |
| `POST` | `/api/datasets/{id}/passages/import-weknora-preview` | 预检重复、变化和无效项 |
| `POST` | `/api/datasets/{id}/passages/import-weknora` | 原子化导入选中分块 |

所有远程请求由本地 Go 服务使用保存的配置发出，浏览器不接触明文 API Key。继续禁止重定向，并限制响应体、超时、分页和单次导入量。

### 6.3 P0 导入规则

1. 只导入非空文本分块。
2. 未完成解析、失败或已禁用的文档不可勾选。
3. 导入前显示选中、可导入、已存在、内容变化、空内容和无权限数量。
4. 相同 `source_profile_id + source_chunk_id + source_content_sha256` 默认跳过。
5. 相同 chunk ID 但内容变化时，只允许“作为新语料导入”或“跳过”，不覆盖已被问题引用的语料。
6. 一次导入必须原子化，失败时不写入部分记录。
7. 默认单次 100 条、硬上限 1000 条，大文档分批处理。

## 7. 页面调整

### 7.1 顶层工作区

- `数据集`：语料、问题与答案、AI 生成、校验与导出、评测运行。
- `配置中心`：生成模型、WeKnora 环境、默认项、连接状态和引用数量。

### 7.2 数据集页只保留轻量选择器

“AI 生成”页只显示当前生成配置下拉框、模型/状态摘要和“前往配置中心”；“评测运行”页只显示当前评测环境下拉框、资源/兼容状态摘要和“前往配置中心”。切换选择只修改数据集引用。

### 7.3 从 WeKnora 导入语料

“语料”页增加入口，操作顺序为：

1. 选择已保存的 WeKnora 环境。
2. 选择知识库和已解析文档。
3. 加载分块并展示序号、文本摘要、字符数和来源。
4. 勾选分块并执行导入预检。
5. 确认导入，显示新增、跳过、变化和失败数量。
6. 可一键跳到“AI 生成”，自动选中本批新增语料。

## 8. 开发里程碑

### C0：配置中心与数据集引用

优先级：P0，必须先做  
预计：1.5～2 天；Codex token 成本：中

实施状态（2026-09-23）：已完成。

任务：

1. [x] workspace settings 存储、API 和校验。
2. [x] 项目 Schema v2 与 v1 自动兼容。
3. [x] 独立配置中心，复用现有 CRUD 和连接测试。
4. [x] 默认项、数据集绑定、缺失提示和删除保护。
5. [x] 数据集页的大配置表单改为摘要和选择器。
6. [x] 回归 API Key 自动保存和隔离边界。

验收：配置只创建一次；新建数据集自动引用默认项；切换数据集不需重填；旧项目可无损打开；被引用配置不能误删。

### C1：分块只读契约与兼容检查

优先级：P0  
预计：1～1.5 天；Codex token 成本：中

实施状态（2026-09-23）：已完成。

任务：

1. [x] Adapter 增加知识库分块读取能力。
2. [x] 实现当前仓库的知识库、文档、分块分页客户端。
3. [x] 严格校验关键字段并扩展兼容性报告。
4. [x] 区分 401/403、404、5xx 和结构不兼容。
5. [x] 增加脱敏 fixture 和契约测试。

验收：当前本地 WeKnora 可以读取分块；probe 只发送 GET；生产不兼容时禁用导入，但不影响人工、CSV 和 ZIP 流程。

### C2：分块预览与导入

优先级：P0  
预计：2～3 天；Codex token 成本：中到高

实施状态（2026-09-23）：已完成。

任务：

1. [x] 数据源代理 API 与安全边界。
2. [x] 知识库、文档、分块三级选择和分页预览。
3. [x] 导入预检、来源 metadata、SHA-256 和重复判断。
4. [x] 原子写入项目并递增 passage ID。
5. [x] 导入后跳转 AI 生成并带入新增语料。
6. [x] 回归备份、快照、复制、校验和五 Parquet 导出。

验收：选择 10 个分块后新增 10 条语料；重复导入新增 0 条；内容变化不静默覆盖；每条语料可追溯；metadata 不进入 Parquet。

### C3：导入到 AI 生成的完整闭环

优先级：P0  
预计：1 天；Codex token 成本：中

实施状态（2026-09-24）：已完成。

任务：

1. [x] 本批新增语料自动成为生成任务默认选择。
2. [x] 调用数据集引用的生成配置。
3. [x] 候选关系仍由后端绑定，模型不能指定 passage ID。
4. [x] 审核批准后检查 queries、answers、qrels 和 qas 完整性。
5. [x] 增加“分块导入 → AI 生成 → 审核 → ZIP”的端到端测试。

验收：全流程无需重复输入地址或 API Key；未审核候选不会进入导出包。

### C4：文档与交付

优先级：P1  
预计：0.5～1 天；Codex token 成本：低到中

实施状态（2026-09-24）：已完成。

任务：[x] 更新 README、迁移和故障排查；[x] 完成浏览器冒烟测试；[x] 验证重启后配置、引用和来源可复读。

## 9. 执行顺序

| 顺序 | 任务 | 依赖 |
| --- | --- | --- |
| 1 | C0 配置中心与引用 | 无 |
| 2 | C1 分块契约与兼容检查 | C0 |
| 3 | C2 分块预览与导入 | C1 |
| 4 | C3 AI 生成闭环 | C0、C2、现有生成 P0 |
| 5 | C4 文档与交付 | C0～C3 |

每个里程碑独立测试和验收，避免同时大范围修改 `model.go`、`app.js` 和配置存储。

## 10. 测试重点

- workspace 默认项读写、无效引用和原子保存。
- 项目 v1 → v2 升级、复制和备份。
- 配置引用统计与删除保护。
- 三类列表的正常、空数据、分页、401、403、404、500 和字段缺失 fixture。
- 分块来源哈希、重复和内容变化判断。
- 批量导入失败不产生部分写入。
- metadata 不进入五 Parquet。
- 重启后配置、引用、来源和任务记录可读。

## 11. 风险控制

| 风险 | 处理 |
| --- | --- |
| 生产接口不同 | probe 通过后启用；不同版本新增 Adapter |
| API Key 权限不足 | 明确显示 401/403，不降级猜测 |
| 重新解析导致 chunk 变化 | 保存 chunk ID 和哈希，不自动覆盖 |
| 大文档分块过多 | 分页、单批硬上限和响应体限制 |
| 删除配置导致数据集不可用 | 删除保护和引用列表 |
| 公共配置修改影响多数据集 | 保存前显示引用数量和影响范围 |
| 用户误认为导入语料即完成评测集 | 显示未生成、未审核和已覆盖语料数量 |

## 12. 总体验收标准

1. 两类配置都能创建一次、跨数据集复用，新数据集自动引用默认项。
2. 当前兼容 WeKnora 可以按知识库、文档、分块读取并导入语料。
3. 重复、变化、空分块和不兼容环境均有明确结果。
4. 导入语料可以直接进入现有 AI 生成和审核流程。
5. ZIP 仍严格只包含五个标准 Parquet 文件。
6. 不修改生产 WeKnora，不依赖生产评测接口修复。
7. API Key 不进入数据集、任务、快照、报告、日志或导出包。
