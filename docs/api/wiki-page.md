# Wiki 页面管理 API

[返回目录](./README.md)

Wiki 页面接口用于管理启用 Wiki 模式的知识库下的页面、文件夹，以及相关的图谱、统计、诊断功能。

## 说明

- 所有接口需要认证（`X-API-Key` 或 `Authorization: Bearer`）。
- Wiki 功能需要在知识库级别启用（`IsWikiEnabled() == true`）。
- 基础路径中的 `:kb_id` 为知识库 ID（注意路由使用单数形式 `knowledgebase`）。

## 端点一览

### 页面管理

| 方法 | 路径 | 描述 |
| ---- | ---- | ---- |
| GET | `/knowledgebase/:kb_id/wiki/pages` | 列出 Wiki 页面 |
| POST | `/knowledgebase/:kb_id/wiki/pages` | 创建 Wiki 页面 |
| GET | `/knowledgebase/:kb_id/wiki/pages/*slug` | 按 slug 获取页面 |
| PUT | `/knowledgebase/:kb_id/wiki/pages/*slug` | 更新页面 |
| DELETE | `/knowledgebase/:kb_id/wiki/pages/*slug` | 删除页面 |
| PUT | `/knowledgebase/:kb_id/wiki/move-page` | 移动页面到文件夹 |

### 文件夹管理

| 方法 | 路径 | 描述 |
| ---- | ---- | ---- |
| GET | `/knowledgebase/:kb_id/wiki/folders` | 列出文件夹 |
| POST | `/knowledgebase/:kb_id/wiki/folders` | 创建文件夹 |
| PUT | `/knowledgebase/:kb_id/wiki/folders/:folder_id` | 重命名或移动文件夹 |
| DELETE | `/knowledgebase/:kb_id/wiki/folders/:folder_id` | 删除空文件夹 |

### 特殊视图

| 方法 | 路径 | 描述 |
| ---- | ---- | ---- |
| GET | `/knowledgebase/:kb_id/wiki/index` | 获取 Wiki 首页（索引视图） |
| GET | `/knowledgebase/:kb_id/wiki/log` | 获取操作日志 |
| GET | `/knowledgebase/:kb_id/wiki/graph` | 获取链接图谱 |
| GET | `/knowledgebase/:kb_id/wiki/stats` | 获取 Wiki 统计信息 |
| GET | `/knowledgebase/:kb_id/wiki/search` | 全文搜索 Wiki 页面 |

### 诊断与维护

| 方法 | 路径 | 描述 |
| ---- | ---- | ---- |
| GET | `/knowledgebase/:kb_id/wiki/lint` | 运行页面链接诊断 |
| POST | `/knowledgebase/:kb_id/wiki/rebuild-links` | 重建 Wiki 链接 |
| POST | `/knowledgebase/:kb_id/wiki/auto-fix` | 自动修复检测到的问题 |

### Issues 管理

| 方法 | 路径 | 描述 |
| ---- | ---- | ---- |
| GET | `/knowledgebase/:kb_id/wiki/issues` | 列出检测到的页面问题 |
| PUT | `/knowledgebase/:kb_id/wiki/issues/:issue_id/status` | 更新 Issue 状态 |

---

## 页面管理

### GET `/knowledgebase/:kb_id/wiki/pages` - 列出页面

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `kb_id` | string | 知识库 ID |

**查询参数**:

| 字段 | 类型 | 默认 | 说明 |
| ---- | ---- | ---- | ---- |
| `page_type` | string | - | 按页面类型过滤，逗号分隔多个（如 `entity,concept`） |
| `status` | string | - | 按状态过滤 |
| `query` | string | - | 全文搜索 |
| `page` | int | 1 | 页码 |
| `page_size` | int | 20 | 每页条数 |
| `sort_by` | string | - | 排序字段 |
| `sort_order` | string | - | 排序方向：`asc` / `desc` |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledgebase/kb-00000001/wiki/pages?page=1&page_size=20' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "data": {
        "items": [
            {
                "id": "wp-00000001",
                "slug": "产品概述",
                "title": "产品概述",
                "page_type": "entity",
                "status": "published",
                "updated_at": "2025-08-12T10:00:00+08:00"
            }
        ],
        "total": 1,
        "page": 1,
        "page_size": 20
    }
}
```

---

### POST `/knowledgebase/:kb_id/wiki/pages` - 创建页面

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `kb_id` | string | 知识库 ID |

**参数说明（请求体）**:

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `slug` | string | 是 | URL 友好的页面标识符（如 `getting-started`） |
| `title` | string | 是 | 页面标题 |
| `content` | string | 否 | Markdown 内容 |
| `page_type` | string | 否 | 页面类型 |
| `status` | string | 否 | 发布状态：`draft` / `published` |
| `folder_id` | string | 否 | 所属文件夹 ID |
| `tags` | string[] | 否 | 标签列表 |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledgebase/kb-00000001/wiki/pages' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-xxxxx' \
--data '{
    "slug": "getting-started",
    "title": "快速入门",
    "content": "# 快速入门\n\n欢迎使用 WeKnora Wiki...",
    "page_type": "guide",
    "status": "published",
    "tags": ["入门", "指南"]
}'
```

**响应**（201 Created）: 返回完整的 WikiPage 对象。

---

### GET `/knowledgebase/:kb_id/wiki/pages/*slug` - 按 slug 获取页面

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `kb_id` | string | 知识库 ID |
| `slug` | string | 页面 slug（Gin wildcard，如 `/guide/getting-started`） |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledgebase/kb-00000001/wiki/pages/getting-started' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）: 返回完整 WikiPage 对象，包含 content、linked_from、linked_to 等字段。

**错误**: 页面不存在 → 404。

---

### PUT `/knowledgebase/:kb_id/wiki/pages/*slug` - 更新页面

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `kb_id` | string | 知识库 ID |
| `slug` | string | 页面 slug |

**请求体**: 同创建，所有字段可选。

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledgebase/kb-00000001/wiki/pages/getting-started' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-xxxxx' \
--data '{
    "title": "快速入门 V2",
    "content": "# 快速入门 V2\n\n更新内容..."
}'
```

---

### DELETE `/knowledgebase/:kb_id/wiki/pages/*slug` - 删除页面

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `kb_id` | string | 知识库 ID |
| `slug` | string | 页面 slug |

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/knowledgebase/kb-00000001/wiki/pages/getting-started' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）:

```json
{
    "success": true
}
```

---

### PUT `/knowledgebase/:kb_id/wiki/move-page` - 移动页面到文件夹

**参数说明（请求体）**:

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `page_slug` | string | 是 | 页面 slug |
| `folder_id` | string | 是 | 目标文件夹 ID |

---

## 文件夹管理

### GET `/knowledgebase/:kb_id/wiki/folders` - 列出文件夹

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledgebase/kb-00000001/wiki/folders' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "data": [
        {
            "id": "wf-00000001",
            "name": "用户指南",
            "parent_id": "",
            "page_count": 5,
            "created_at": "2025-08-12T09:00:00+08:00"
        }
    ]
}
```

---

### POST `/knowledgebase/:kb_id/wiki/folders` - 创建文件夹

**参数说明（请求体）**:

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `name` | string | 是 | 文件夹名称 |
| `parent_id` | string | 否 | 父文件夹 ID（空字符串 = 根目录） |

---

### PUT `/knowledgebase/:kb_id/wiki/folders/:folder_id` - 重命名或移动文件夹

---

### DELETE `/knowledgebase/:kb_id/wiki/folders/:folder_id` - 删除空文件夹

仅可删除空文件夹。非空文件夹需先移走或删除其下的页面/子文件夹。

---

## 特殊视图

### GET `/knowledgebase/:kb_id/wiki/index` - Wiki 首页

返回 Wiki 索引视图数据，包含目录结构、最近更新等。

---

### GET `/knowledgebase/:kb_id/wiki/log` - 操作日志

返回 Wiki 最近的操作日志（谁在什么时候创建/编辑/删除了哪个页面）。

---

### GET `/knowledgebase/:kb_id/wiki/graph` - 链接图谱

返回 Wiki 内页面之间的链接关系图谱数据（节点 + 边）。

**响应字段**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `nodes` | array | 节点列表（页面） |
| `edges` | array | 边列表（链接关系） |

---

### GET `/knowledgebase/:kb_id/wiki/stats` - Wiki 统计

返回总页面数、总字数、链接数、最近活跃度等统计信息。

---

### GET `/knowledgebase/:kb_id/wiki/search` - 全文搜索

**查询参数**:

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `q` | string | 是 | 搜索关键词 |
| `page` | int | 否 | 页码 |
| `page_size` | int | 否 | 每页条数 |

---

## 诊断与维护

### GET `/knowledgebase/:kb_id/wiki/lint` - 运行诊断

检测断链、孤儿页面、循环重定向等问题。

**响应**:

```json
{
    "success": true,
    "data": {
        "issues": [
            {
                "id": "iss-00000001",
                "type": "broken_link",
                "severity": "error",
                "page_slug": "guide/old-page",
                "description": "链接到不存在的页面 [[missing-page]]",
                "status": "open"
            }
        ],
        "summary": {
            "total": 1,
            "errors": 1,
            "warnings": 0
        }
    }
}
```

---

### POST `/knowledgebase/:kb_id/wiki/rebuild-links` - 重建链接

重新扫描所有页面并重建链接关系图。通常在批量导入或迁移后使用。

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/knowledgebase/kb-00000001/wiki/rebuild-links' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "message": "链接重建完成",
    "data": {
        "pages_scanned": 50,
        "links_found": 120
    }
}
```

---

### POST `/knowledgebase/:kb_id/wiki/auto-fix` - 自动修复

对检测到的问题自动执行修复操作（如移除断链、合并重复页面等）。

---

## Issues 管理

### GET `/knowledgebase/:kb_id/wiki/issues` - 列出 Issues

**查询参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `status` | string | 按状态过滤：`open` / `resolved` / `ignored` |
| `severity` | string | 按严重程度过滤：`error` / `warning` / `info` |
| `type` | string | 按类型过滤 |

---

### PUT `/knowledgebase/:kb_id/wiki/issues/:issue_id/status` - 更新 Issue 状态

**路径参数**:

| 字段 | 类型 | 说明 |
| ---- | ---- | ---- |
| `issue_id` | string | Issue ID |

**参数说明（请求体）**:

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `status` | string | 是 | 新状态：`resolved` / `ignored` |

---

## 枚举值

### 页面状态 (Page Status)

| 值 | 说明 |
| ---- | ---- |
| `draft` | 草稿 |
| `published` | 已发布 |
| `archived` | 已归档 |

### Issue 严重程度 (Severity)

| 值 | 说明 |
| ---- | ---- |
| `error` | 错误（断链、循环引用等） |
| `warning` | 警告（建议修复） |
| `info` | 提示信息 |

### Issue 状态

| 值 | 说明 |
| ---- | ---- |
| `open` | 待处理 |
| `resolved` | 已解决 |
| `ignored` | 已忽略 |
