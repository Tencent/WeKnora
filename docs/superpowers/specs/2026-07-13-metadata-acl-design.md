# 基于 Metadata 的文档级 ACL 设计

## 目标

为 WeKnora 增加基于文档 metadata 条件的文档级访问控制能力。

WeKnora 当前已经支持租户、组织、知识库、共享空间等层级的权限控制。本设计在这些既有权限之上增加一层数据访问控制，用于限制用户能够列出、检索、引用和用于 RAG 的文档与分块。

本设计不规定数据源必须写入哪些 metadata。数据源只负责写入业务 metadata；管理员通过“数据访问策略”定义用户、角色或共享权限与 metadata 过滤条件之间的关系。

## 设计目标

- 基于现有 `knowledge.metadata` 支持文档级 ACL。
- 保留现有知识库权限作为第一层访问门禁。
- 在知识库权限通过后，再应用 metadata 过滤。
- 支持在策略中使用动态用户变量，例如 `{{user.username}}` 和 `{{user.email}}`。
- 模型不绑定具体数据源，可用于企业微信、飞书、上传文件、CRM 导出以及未来 connector。
- 在文档列表、检索、聊天、Agent、直接分块访问等路径中保持一致的 ACL 行为。
- 默认兼容现有行为：如果当前用户没有命中任何 metadata ACL 策略，则访问行为不变。

## 非目标

- 不替代现有租户 RBAC 或知识库共享权限。
- 不为所有数据源定义固定 metadata schema。
- 首版不实现任意 SQL、JSONPath 或嵌套表达式语言。
- 首版不支持 deny 规则。
- 首版不修改向量索引 schema。
- 不允许 metadata ACL 绕过知识库权限。metadata ACL 只能进一步收紧权限，不能授予原本没有的知识库访问权限。

## 现有权限模型

WeKnora 当前有固定租户角色：

- `owner`
- `admin`
- `contributor`
- `viewer`

定义位置：`internal/types/tenant_member.go`。

WeKnora 也有固定组织/共享角色：

- `admin`
- `editor`
- `viewer`

定义位置：`internal/types/organization.go`。

当前没有自定义业务角色体系。因此本设计引入独立的数据访问策略层，而不是把文档级 ACL 强行绑定到现有固定 RBAC 角色上。

## 核心模型

一次请求必须同时满足两层权限：

```text
现有知识库访问权限通过
AND
metadata ACL 允许访问该文档/分块
```

metadata ACL 的行为：

```text
如果没有任何启用的 DataAccessPolicy 命中当前用户/请求：
  默认允许，保持现有行为

如果有一条或多条启用的 DataAccessPolicy 命中：
  只允许访问 metadata 匹配至少一条策略的文档
```

策略合并语义：

```text
单条策略内部：所有字段条件 AND
多条命中策略之间：OR
v1 不支持 deny 规则
v1 不支持嵌套 AND/OR/NOT
```

## 数据模型

### `data_access_policies`

保存策略定义。

```text
id              string / uuid
tenant_id       uint64
name            string
scope_type      string
scope_value     string
metadata_filter jsonb
created_by      string
created_at      timestamp
updated_at      timestamp
deleted_at      nullable timestamp
```

`scope_type` 可选值：

- `tenant`
- `knowledge_base`
- `datasource_type`

`scope_value` 示例：

- `tenant` 范围可以为空。
- `knowledge_base` 范围填知识库 UUID。
- `datasource_type` 范围填数据源类型，例如 `wecom_chat_archive`。

### `data_access_policy_bindings`

保存策略绑定到哪些主体。

```text
id              string / uuid
policy_id       string / uuid
subject_type    string
subject_value   string
created_at      timestamp
```

首版 `subject_type`：

- `tenant_role`
- `share_permission`
- `user`

首版 `subject_value` 示例：

- `owner`
- `admin`
- `contributor`
- `viewer`
- `editor`
- 用户 UUID

预留未来主体类型：

- `org_role`
- `group`
- `department`
- `external_subject`

## Metadata Filter 格式

首版使用简单 JSON 对象表达过滤条件。

示例：

```json
{
  "source": {
    "eq": "wecom_chat_archive"
  },
  "participant_userids": {
    "contains": "{{user.username}}"
  }
}
```

含义：

```text
metadata.source == "wecom_chat_archive"
AND
metadata.participant_userids 包含当前用户 username
```

v1 支持操作符：

- `eq`
- `neq`
- `in`
- `contains`
- `exists`

可选数字操作符：

- `gt`
- `gte`
- `lt`
- `lte`

v1 不支持嵌套 `$and`、`$or` 或 `$not`。

## 变量

filter 的值可以包含运行时变量。

首版变量：

```text
{{user.id}}
{{user.username}}
{{user.email}}
{{tenant.id}}
{{tenant.role}}
```

预留未来变量：

```text
{{user.external.<provider>.<field>}}
{{org.id}}
{{org.role}}
```

按邮箱隔离文档的示例：

```json
{
  "owner_email": {
    "eq": "{{user.email}}"
  }
}
```

企业微信参与者过滤示例：

```json
{
  "source": {
    "eq": "wecom_chat_archive"
  },
  "participant_userids": {
    "contains": "{{user.username}}"
  }
}
```

## 访问判定流程

### 第一步：现有授权

现有授权逻辑保持不变：

- 身份认证
- 租户 RBAC
- 知识库访问权限
- 组织/共享空间权限
- 共享 Agent 权限

如果现有授权失败，不再评估 metadata ACL。

### 第二步：解析命中策略

ACL service 根据当前请求解析策略，输入包括：

- 当前活跃租户 ID
- 有效租户 ID
- 用户 ID
- 租户角色
- 知识库 ID
- 数据源类型，如果当前路径可获取
- 共享权限，如果当前路径可获取

策略解析规则：

- 忽略未启用策略。
- 忽略不在当前请求范围内的策略。
- 忽略绑定主体不匹配当前用户、角色或共享权限的策略。

### 第三步：无策略命中时默认允许

如果用户已有知识库访问权限，并且没有任何启用策略适用于该用户，则全部文档保持当前可见行为。

这保证现有部署升级后不会突然失去文档访问权限。

### 第四步：过滤文档

如果至少一条策略适用于当前用户，则文档只有在 metadata 匹配至少一条策略时才可访问。

过滤后的文档 ID 将用于限制检索和文档列表。

## 检索侧强制执行

### v1 推荐方案：预过滤允许访问的 Knowledge IDs

首版应避免一次性修改所有向量/关键词后端。

流程：

```text
HybridSearch
  -> 现有知识库授权
  -> 解析 DataAccessPolicies
  -> 如果无策略：保持现有路径
  -> 从 knowledges.metadata 查询允许访问的 knowledge IDs
  -> 将 allowed IDs 注入 retrieval params
  -> retriever 只召回允许文档的 chunks
  -> 结果返回前再做一次 defense-in-depth 过滤
```

优点：

- 可复用现有 retriever。
- 不需要改索引 schema。
- 可复用现有 `KnowledgeIDs` / exclude 过滤能力。
- 首版实现风险较低。

缺点：

- 大知识库可能产生较大的 `IN (...)` 过滤条件。
- 后续版本可以把 metadata predicate 下推到 retriever index 中。

### Defense-in-Depth 后置过滤

即使已经做了检索前过滤，最终组装结果前仍应再校验一次 ACL。

原因：

- parent / nearby / relation chunk enrichment 可能额外加载分块。
- direct chunk API 可能绕过主检索路径。
- 未来新增路径可能忘记传入 allowed ID filter。

## 后端集成点

### Search

主要接入点：

- `internal/application/service/knowledgebase_search.go`

应在知识库授权后、构建 retrieval params 前计算 ACL。

相关函数：

- `HybridSearch`
- `buildRetrievalParams`

### Retrieval Params

修改：

- `internal/types/retriever.go`

可新增字段：

```go
AllowedKnowledgeIDs []string
```

或与现有 `KnowledgeIDs` 做交集。

推荐设计：

- 用户请求指定的 `KnowledgeIDs` 与 ACL 生成的 allowed IDs 分开保存。
- 在 `buildRetrievalParams` 中做交集。
- 保留过滤来源信息，方便调试和测试。

### Knowledge Repository

修改：

- `internal/application/repository/knowledge.go`

新增类似方法：

```go
FindAllowedKnowledgeIDsByMetadataPolicies(ctx, scope, policies) ([]string, error)
```

该方法基于 `knowledges.metadata` 评估 metadata filters。

### Result Filtering

修改：

- `internal/application/service/knowledgebase_search_results.go`

确保最终 `SearchResult` 返回前再次应用 ACL 过滤。

### 文档列表和浏览

需要覆盖文档列表/搜索路径：

- `ListKnowledge`
- `SearchKnowledgeInScopes`
- 文档卡片 API
- 共享知识库文档列表

如果用户因 metadata ACL 无法访问某文档，该文档不应出现在列表结果和总数中。

### Direct Chunk / Agent Tool 路径

以下路径也需要调用同一套 ACL helper：

- 直接列出 chunk
- 按 chunk ID 获取 chunk
- grep chunks
- Agent 知识检索工具
- MCP-like search 路径

这些路径不应各自实现独立策略逻辑，而应复用统一 ACL service。

## 前端 UI

在知识库设置中新增入口：

```text
知识库设置 -> 数据访问策略
```

### 策略列表

展示字段：

- 名称
- 启用状态
- 作用范围
- 绑定主体
- metadata filter 摘要
- 更新时间
- 操作：编辑、禁用、删除、测试

### 策略编辑器

字段：

- 名称
- 描述
- 启用状态
- 作用范围
  - 当前知识库
  - 数据源类型
  - 全租户，仅 owner/admin 可配置
- 绑定主体
  - 租户角色多选
  - 共享权限多选
  - 用户选择器
- metadata filter

### Filter 编辑器

提供两种模式：

1. 表格模式
2. 高级 JSON 模式

表格模式列：

```text
metadata 字段 | 操作符 | 值
```

示例：

```text
source               eq        wecom_chat_archive
participant_userids  contains  {{user.username}}
```

高级模式：

```json
{
  "source": {"eq": "wecom_chat_archive"},
  "participant_userids": {"contains": "{{user.username}}"}
}
```

### 保存校验

保存前需要校验：

- JSON 合法。
- 操作符合法。
- 变量合法。
- 不允许空 filter。
- 不允许未知 subject type。
- tenant-wide 策略需要 admin/owner 权限。

### 策略测试工具

新增测试接口：

```text
POST /api/v1/data-access-policies/:id/test
```

输入：

- 示例 metadata JSON
- 可选模拟用户
- 可选 KB ID

输出：

- matched: true/false
- resolved filter
- reason / failed condition

这可以帮助管理员调试 metadata 策略。

## API 设计

知识库范围端点：

```text
GET    /api/v1/knowledge-bases/:id/data-access-policies
POST   /api/v1/knowledge-bases/:id/data-access-policies
```

策略端点：

```text
GET    /api/v1/data-access-policies/:id
PUT    /api/v1/data-access-policies/:id
DELETE /api/v1/data-access-policies/:id
POST   /api/v1/data-access-policies/:id/test
```

创建请求示例：

```json
{
  "name": "WeCom viewer self access",
  "description": "Viewers can only search WeCom conversations they participated in",
  "enabled": true,
  "scope_type": "knowledge_base",
  "scope_value": "kb_xxx",
  "bindings": [
    {
      "subject_type": "tenant_role",
      "subject_value": "viewer"
    }
  ],
  "metadata_filter": {
    "source": {
      "eq": "wecom_chat_archive"
    },
    "participant_userids": {
      "contains": "{{user.username}}"
    }
  }
}
```

## 安全行为

- Metadata ACL 不能单独授予知识库访问权限。
- 如果知识库访问权限失败，返回现有 forbidden / not found 行为。
- 如果 metadata ACL 拒绝某文档，该文档应表现为不可见。
- 搜索和列表 API 不应暴露不可访问文档的数量。
- 错误信息不应泄露受限文档是否存在。
- 策略内容只对管理员可见，不对普通用户暴露。
- 策略评估应可预测、可测试、可审计。

## 兼容性

默认行为保持不变：

```text
没有命中策略 -> 按现有知识库权限允许访问全部文档
```

这避免破坏现有部署。

敏感部署可以显式添加策略。

未来可以增加知识库级设置：

```text
metadata_acl_mode:
  - disabled
  - permissive_default_allow
  - strict_default_deny
```

但 v1 固定为默认允许。

## 性能考虑

v1 使用 allowed knowledge IDs 预过滤。

风险：

- 大知识库可能生成很大的 allowed ID 列表。
- 策略很多时 metadata 查询成本会上升。
- JSONB metadata 查询需要针对常用字段建立索引。

缓解措施：

- 尽量将策略 scope 限定在具体知识库。
- 短时间缓存已解析策略定义。
- 为 `knowledges.metadata` 增加 GIN 索引。
- 后续版本将 filter predicate 下推到 retriever index。
- 后续支持 SQL subquery，而不是总是物化大 ID 列表。

## 测试计划

### 单元测试

- filter parser 校验。
- 变量解析。
- 操作符行为。
- policy binding 匹配。
- 多策略 OR 语义。
- 无策略命中时默认允许。
- 有策略但不匹配时拒绝。

### Repository 测试

- JSONB metadata filter 查询。
- `eq`、`in`、`contains`、`exists`。
- KB-scoped allowed knowledge ID 查询。
- 租户隔离。

### Search 测试

- `HybridSearch` 在检索前排除不可访问文档。
- 用户指定 `KnowledgeIDs` 与 ACL allowed IDs 做交集。
- 结果后置过滤能移除意外混入的不可访问结果。
- 无策略路径保持现有行为。

### API 测试

- policy CRUD 端点。
- policy 管理权限。
- policy test 端点。
- 文档列表可见性。
- 文档搜索可见性。

### E2E 测试

准备一个包含三个文档的知识库：

- doc A: metadata owner = Alice
- doc B: metadata owner = Bob
- doc C: 无受限 metadata

配置策略：

```json
{
  "owner_email": {"eq": "{{user.email}}"}
}
```

预期：

- Alice 只能看到 doc A。
- Bob 只能看到 doc B。
- 两者都看不到对方文档。
- doc C 的行为遵循当前策略语义。

企业微信示例：

```json
{
  "source": {"eq": "wecom_chat_archive"},
  "participant_userids": {"contains": "{{user.username}}"}
}
```

预期：

- 用户只能检索 metadata 中包含自己 username 的企业微信会话文档。
- admin/owner 行为取决于是否有策略绑定到对应主体。

## 推进计划

1. 增加数据模型和 migration。
2. 增加 policy repository 和 service。
3. 增加 filter evaluator 和变量解析器。
4. 增加 policy API。
5. 在 HybridSearch 中加入 ACL 预过滤。
6. 增加最终结果后置过滤。
7. 增加文档列表/搜索 ACL 过滤。
8. 增加前端策略管理 UI。
9. 补齐 direct chunk 和 agent tool 覆盖。
10. 增加文档和示例。

## 待确认问题

- owner/admin 是否需要自动 bypass 策略，还是仅在没有策略绑定到它们时保持默认允许。
- v1 是否允许 tenant-wide 策略，还是只支持 knowledge-base scope。
- 当文档 metadata 为空时，有策略存在的情况下是否一律不匹配。
- `contains` 对逗号字符串应按 substring、token，还是两者都支持。
- v1 是否支持 JSON array 语义。
- 策略审计日志只记录 policy CRUD，还是也记录每次检索拒绝。

## v1 推荐决策

- 不内置 owner/admin 自动 bypass。现有 default-allow 行为已经保证未绑定策略时它们仍可访问。
- 优先支持 knowledge-base scope；datasource-type 和 tenant scope 后续扩展。
- 空 metadata 不匹配任何字段条件。
- `contains` 支持字符串 substring 和数组成员匹配。
- 鼓励 datasource metadata 使用 JSON array，但保留字符串兼容。
- v1 审计 policy CRUD；每次查询拒绝的审计后续再做。
