# 知识管理 API

[返回目录](./README.md)

知识（Knowledge）是知识库下的一条可检索内容（来自文件、URL 或手工录入的 Markdown）。本文档涵盖知识的创建、查询、更新、删除、迁移与文件预览/下载等接口。

| 方法   | 路径                                       | 描述                                       |
| ------ | ------------------------------------------ | ------------------------------------------ |
| POST   | `/knowledge-bases/:id/knowledge/file`      | 上传文件创建知识（multipart）             |
| POST   | `/knowledge-bases/:id/knowledge/url`       | 从 URL 创建知识（网页抓取或文件下载）       |
| POST   | `/knowledge-bases/:id/knowledge/manual`    | 创建手工 Markdown 知识                     |
| GET    | `/knowledge-bases/:id/knowledge`           | 列出知识库下的知识（支持分页/筛选）         |
| DELETE | `/knowledge-bases/:id/knowledge`           | 清空知识库下的所有知识（异步任务）         |
| GET    | `/knowledge/batch`                         | 按 ID 列表批量获取知识                     |
| GET    | `/knowledge/:id`                           | 获取知识详情                               |
| PUT    | `/knowledge/:id`                           | 更新知识（标题/描述/标签等）               |
| DELETE | `/knowledge/:id`                           | 删除单条知识                               |
| PUT    | `/knowledge/manual/:id`                    | 更新手工 Markdown 知识                     |
| POST   | `/knowledge/:id/reparse`                   | 重新解析知识（异步）                       |
| GET    | `/knowledge/:id/download`                  | 下载原始文件（attachment）                 |
| GET    | `/knowledge/:id/preview`                   | 内联预览文件（按扩展名设置 Content-Type）  |
| PUT    | `/knowledge/image/:id/:chunk_id`           | 更新分块图像信息                           |
| PUT    | `/knowledge/tags`                          | 批量更新知识标签                           |
| GET    | `/knowledge/search`                        | 跨知识库搜索/过滤知识                      |
| POST   | `/knowledge/batch-delete`                  | 同一知识库内批量删除知识（异步任务）       |
| POST   | `/knowledge/move`                          | 迁移知识到另一知识库（异步任务）           |
| GET    | `/knowledge/move/progress/:task_id`        | 查询知识迁移任务进度                       |

> **公共说明**：
> - 路径中的 `:id`（知识库路径下）为**知识库 ID**，`/knowledge/:id` 中的 `:id` 为**知识 ID**。
> - 所有写操作（创建、更新、删除、迁移、重新解析）需要当前用户在知识库所属组织内具有 `editor` 或 `admin` 权限；清空知识库内容仅 KB **所有者**（admin 且租户匹配）可操作。
> - 关键状态字段：`parse_status` 取值 `pending` / `processing` / `completed` / `failed`；`enable_status` 取值 `enabled` / `disabled`。

## POST `/knowledge-bases/:id/knowledge/file` - 上传文件创建知识

通过 `multipart/form-data` 上传文件创建知识条目。文件大小受 `MAX_FILE_SIZE_MB` 环境变量限制。

**路径参数**:

| 字段 | 类型   | 说明      |
| ---- | ------ | --------- |
| id   | string | 知识库 ID |

**表单字段**:

| 字段                | 类型    | 必填 | 说明                                                                 |
| ------------------- | ------- | ---- | -------------------------------------------------------------------- |
| `file`              | file    | 是   | 待上传的文件                                                         |
| `fileName`          | string  | 否   | 自定义文件名，用于"文件夹上传"时保留相对路径（如 `docs/intro.md`） |
| `metadata`          | string  | 否   | JSON 字符串，会被反序列化为 `map[string]string`                     |
| `enable_multimodel` | string  | 否   | `"true"` / `"false"`，是否启用图文多模态解析                         |
| `tag_id`            | string  | 否   | 标签 ID；传 `__untagged__` 或空字符串表示未分类                      |
| `channel`           | string  | 否   | 来源渠道标识（写入 `channel` 字段，默认 `web`）                      |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/knowledge/file' \
--header 'X-API-Key: sk-xxxxx' \
--form 'file=@"/Users/xxxx/tests/彗星.txt"' \
--form 'enable_multimodel="true"' \
--form 'tag_id="tag-00000001"' \
--form 'metadata="{\"source\":\"manual_upload\"}"'
```

> 注意：使用 `-F`/`--form` 时 curl 会自动设置 `Content-Type: multipart/form-data; boundary=...`，不要再手动加 `--header 'Content-Type: application/json'`，否则请求体会被错误解析。

**响应**（创建成功，`parse_status=processing` 表示解析任务已入队）:

```json
{
    "data": {
        "id": "4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5",
        "tenant_id": 1,
        "knowledge_base_id": "kb-00000001",
        "type": "file",
        "title": "彗星.txt",
        "description": "",
        "source": "",
        "channel": "web",
        "tag_id": "tag-00000001",
        "summary_status": "none",
        "parse_status": "processing",
        "enable_status": "disabled",
        "embedding_model_id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
        "file_name": "彗星.txt",
        "file_type": "txt",
        "file_size": 7710,
        "file_hash": "d69476ddbba45223a5e97e786539952c",
        "file_path": "data/files/1/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5/1754970756171067621.txt",
        "storage_size": 0,
        "metadata": null,
        "created_at": "2025-08-12T11:52:36.168632288+08:00",
        "updated_at": "2025-08-12T11:52:36.173612121+08:00",
        "processed_at": null,
        "error_message": "",
        "deleted_at": null
    },
    "success": true
}
```

文件重复时返回 409 与已存在知识的引用；超过大小限制返回 400 `文件大小不能超过 N MB`。

## POST `/knowledge-bases/:id/knowledge/url` - 从 URL 创建知识

可创建**网页知识**或**远程文件知识**。后端根据下列规则自动判定：

- 当 `file_name` / `file_type` 任一被显式提供，或 URL 路径含已知文件扩展名时，按"文件下载模式"处理（拉取远端文件保存）；
- 否则按"网页抓取模式"处理。

URL 会经过 SSRF 安全校验，禁止指向内网/回环地址。

**请求体**:

| 字段                | 类型    | 必填 | 说明                                              |
| ------------------- | ------- | ---- | ------------------------------------------------- |
| `url`               | string  | 是   | 目标 URL                                          |
| `file_name`         | string  | 否   | 显式指定文件名，强制走文件下载模式                |
| `file_type`         | string  | 否   | 显式指定文件类型（如 `pdf`、`docx`）              |
| `enable_multimodel` | boolean | 否   | 是否启用多模态解析                                |
| `title`             | string  | 否   | 自定义标题                                        |
| `tag_id`            | string  | 否   | 标签 ID                                           |
| `channel`           | string  | 否   | 来源渠道标识                                      |

**请求（网页模式）**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/knowledge/url' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "url": "https://github.com/Tencent/WeKnora",
    "enable_multimodel": true
}'
```

**请求（远程文件模式）**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/knowledge/url' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "url": "https://example.com/papers/whitepaper.pdf",
    "file_name": "whitepaper.pdf",
    "file_type": "pdf"
}'
```

**响应**（HTTP 201）:

```json
{
    "data": {
        "id": "9c8af585-ae15-44ce-8f73-45ad18394651",
        "tenant_id": 1,
        "knowledge_base_id": "kb-00000001",
        "type": "url",
        "title": "",
        "description": "",
        "source": "https://github.com/Tencent/WeKnora",
        "channel": "web",
        "tag_id": "",
        "summary_status": "none",
        "parse_status": "processing",
        "enable_status": "disabled",
        "embedding_model_id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
        "file_name": "",
        "file_type": "",
        "file_size": 0,
        "file_hash": "",
        "file_path": "",
        "storage_size": 0,
        "metadata": null,
        "created_at": "2025-08-12T11:55:05.709266776+08:00",
        "updated_at": "2025-08-12T11:55:05.712918234+08:00",
        "processed_at": null,
        "error_message": "",
        "deleted_at": null
    },
    "success": true
}
```

## POST `/knowledge-bases/:id/knowledge/manual` - 创建手工 Markdown 知识

适用于直接编写 Markdown 内容（无源文件）的场景。

**请求体**:

| 字段      | 类型   | 必填 | 说明                                                |
| --------- | ------ | ---- | --------------------------------------------------- |
| `title`   | string | 是   | 标题                                                |
| `content` | string | 是   | Markdown 正文                                       |
| `status`  | string | 否   | 草稿/发布等业务状态（草稿不会触发解析）             |
| `tag_id`  | string | 否   | 标签 ID                                             |
| `channel` | string | 否   | 来源渠道标识                                        |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/knowledge/manual' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "title": "产品使用指南",
    "content": "# 产品使用指南\n\n## 快速入门\n\n这是一份产品使用指南...",
    "status": "published",
    "tag_id": "tag-00000001"
}'
```

**响应**:

```json
{
    "data": {
        "id": "5a3b2c1d-0e9f-4a8b-7c6d-5e4f3a2b1c0d",
        "tenant_id": 1,
        "knowledge_base_id": "kb-00000001",
        "type": "manual",
        "title": "产品使用指南",
        "description": "",
        "source": "",
        "channel": "web",
        "tag_id": "tag-00000001",
        "summary_status": "none",
        "parse_status": "processing",
        "enable_status": "disabled",
        "embedding_model_id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
        "file_name": "",
        "file_type": "md",
        "file_size": 0,
        "file_hash": "",
        "file_path": "",
        "storage_size": 0,
        "metadata": null,
        "created_at": "2025-08-12T12:00:00.000000+08:00",
        "updated_at": "2025-08-12T12:00:00.000000+08:00",
        "processed_at": null,
        "error_message": "",
        "deleted_at": null
    },
    "success": true
}
```

## GET `/knowledge-bases/:id/knowledge` - 列出知识库下的知识

支持分页与按标签/关键词/文件类型筛选。

**路径参数**:

| 字段 | 类型   | 说明      |
| ---- | ------ | --------- |
| id   | string | 知识库 ID |

**查询参数**:

| 字段        | 类型    | 默认 | 说明                                |
| ----------- | ------- | ---- | ----------------------------------- |
| `page`      | integer | 1    | 页码（从 1 开始）                  |
| `page_size` | integer | 20   | 每页条数                            |
| `tag_id`    | string  | -    | 按标签 ID 过滤                      |
| `keyword`   | string  | -    | 按标题/内容关键词过滤               |
| `file_type` | string  | -    | 按单个文件扩展名过滤（如 `pdf`）    |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/knowledge?page=1&page_size=1&tag_id=tag-00000001' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "data": [
        {
            "id": "9c8af585-ae15-44ce-8f73-45ad18394651",
            "tenant_id": 1,
            "knowledge_base_id": "kb-00000001",
            "type": "url",
            "title": "",
            "description": "",
            "source": "https://github.com/Tencent/WeKnora",
            "channel": "web",
            "tag_id": "tag-00000001",
            "summary_status": "none",
            "parse_status": "pending",
            "enable_status": "disabled",
            "embedding_model_id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
            "file_name": "",
            "file_type": "",
            "file_size": 0,
            "file_hash": "",
            "file_path": "",
            "storage_size": 0,
            "metadata": null,
            "created_at": "2025-08-12T11:55:05.709266+08:00",
            "updated_at": "2025-08-12T11:55:05.709266+08:00",
            "processed_at": null,
            "error_message": "",
            "deleted_at": null
        }
    ],
    "page": 1,
    "page_size": 1,
    "total": 2,
    "success": true
}
```

## DELETE `/knowledge-bases/:id/knowledge` - 清空知识库下的所有知识

异步提交"清空任务"，删除该知识库下的全部知识条目；知识库本身保留。**仅 KB 所有者（admin 且租户匹配）可操作**。

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/knowledge-bases/kb-00000001/knowledge' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（已入队）:

```json
{
    "success": true,
    "message": "Knowledge base contents clear task submitted",
    "data": { "deleted_count": 42 }
}
```

知识库已为空时：

```json
{
    "success": true,
    "message": "Knowledge base is already empty",
    "data": { "deleted_count": 0 }
}
```

## GET `/knowledge/batch` - 批量获取知识

按 ID 列表一次性获取多条知识详情，常用于刷新页面后恢复选中列表。

**查询参数**:

| 字段        | 类型     | 必填 | 说明                                                                  |
| ----------- | -------- | ---- | --------------------------------------------------------------------- |
| `ids`       | string[] | 是   | 知识 ID，重复 `ids=...` 传多个                                        |
| `kb_id`     | string   | 否   | 限定知识库范围；共享知识库场景下用于按 KB 校验权限并解析有效租户       |
| `agent_id`  | string   | 否   | 共享 Agent ID；按 Agent 所属租户拉取，常用于共享场景刷新后的文件回填   |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge/batch?ids=9c8af585-ae15-44ce-8f73-45ad18394651&ids=4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "data": [
        {
            "id": "9c8af585-ae15-44ce-8f73-45ad18394651",
            "tenant_id": 1,
            "knowledge_base_id": "kb-00000001",
            "type": "url",
            "title": "",
            "source": "https://github.com/Tencent/WeKnora",
            "parse_status": "pending",
            "enable_status": "disabled",
            "created_at": "2025-08-12T11:55:05.709266+08:00",
            "updated_at": "2025-08-12T11:55:05.709266+08:00"
        },
        {
            "id": "4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5",
            "tenant_id": 1,
            "knowledge_base_id": "kb-00000001",
            "type": "file",
            "title": "彗星.txt",
            "file_name": "彗星.txt",
            "file_type": "txt",
            "file_size": 7710,
            "parse_status": "completed",
            "enable_status": "enabled",
            "created_at": "2025-08-12T11:52:36.168632+08:00",
            "updated_at": "2025-08-12T11:52:53.376871+08:00"
        }
    ],
    "success": true
}
```

> 上述响应字段省略了空值与一些大字段，实际返回的是完整 `Knowledge` 对象。

## GET `/knowledge/:id` - 获取知识详情

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "data": {
        "id": "4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5",
        "tenant_id": 1,
        "knowledge_base_id": "kb-00000001",
        "type": "file",
        "title": "彗星.txt",
        "description": "彗星是由冰和尘埃构成的太阳系小天体，接近太阳时会形成彗发和彗尾。",
        "source": "",
        "channel": "web",
        "tag_id": "tag-00000001",
        "summary_status": "completed",
        "parse_status": "completed",
        "enable_status": "enabled",
        "embedding_model_id": "dff7bc94-7885-4dd1-bfd5-bd96e4df2fc3",
        "file_name": "彗星.txt",
        "file_type": "txt",
        "file_size": 7710,
        "file_hash": "d69476ddbba45223a5e97e786539952c",
        "file_path": "data/files/1/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5/1754970756171067621.txt",
        "storage_size": 33689,
        "metadata": null,
        "created_at": "2025-08-12T11:52:36.168632+08:00",
        "updated_at": "2025-08-12T11:52:53.376871+08:00",
        "processed_at": "2025-08-12T11:52:53.376573+08:00",
        "error_message": "",
        "deleted_at": null
    },
    "success": true
}
```

## PUT `/knowledge/:id` - 更新知识

更新知识条目的元信息（标题/描述/标签等）。请求体为 `Knowledge` 结构，仅服务侧白名单字段会被实际更新。

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledge/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "title": "彗星 - 天文百科",
    "description": "彗星条目，已校对",
    "tag_id": "tag-00000001",
    "enable_status": "enabled"
}'
```

**响应**:

```json
{
    "success": true,
    "message": "Knowledge chunk updated successfully"
}
```

## DELETE `/knowledge/:id` - 删除单条知识

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/knowledge/9c8af585-ae15-44ce-8f73-45ad18394651' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "success": true,
    "message": "Deleted successfully"
}
```

## PUT `/knowledge/manual/:id` - 更新手工 Markdown 知识

**请求体**：同 `POST /knowledge-bases/:id/knowledge/manual`，字段全部可选。

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledge/manual/5a3b2c1d-0e9f-4a8b-7c6d-5e4f3a2b1c0d' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "title": "产品使用指南 V2",
    "content": "# 产品使用指南 V2\n\n## 更新内容\n\n..."
}'
```

**响应**:

```json
{
    "data": {
        "id": "5a3b2c1d-0e9f-4a8b-7c6d-5e4f3a2b1c0d",
        "tenant_id": 1,
        "knowledge_base_id": "kb-00000001",
        "type": "manual",
        "title": "产品使用指南 V2",
        "parse_status": "processing",
        "enable_status": "enabled",
        "created_at": "2025-08-12T12:00:00.000000+08:00",
        "updated_at": "2025-08-12T12:30:00.000000+08:00"
    },
    "success": true
}
```

## POST `/knowledge/:id/reparse` - 重新解析知识

异步重新解析：删除现有分块/向量并按最新配置重新解析。常用于解析配置变更或上次解析失败重试的场景。

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/knowledge/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5/reparse' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "success": true,
    "message": "Knowledge reparse task submitted",
    "data": {
        "id": "4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5",
        "tenant_id": 1,
        "knowledge_base_id": "kb-00000001",
        "type": "file",
        "title": "彗星.txt",
        "parse_status": "pending",
        "enable_status": "enabled",
        "created_at": "2025-08-12T11:52:36.168632+08:00",
        "updated_at": "2025-08-12T13:00:00.000000+08:00"
    }
}
```

调用后 `parse_status` 会先变为 `pending`，再由后台 worker 转为 `processing` → `completed`/`failed`。

## GET `/knowledge/:id/download` - 下载原始文件

以 `attachment` 方式下载知识对应的原始文件。

**响应头**:

```
Content-Type: application/octet-stream
Content-Disposition: attachment; filename="彗星.txt"
```

**请求**:

```curl
curl --location -OJ 'http://localhost:8080/api/v1/knowledge/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5/download' \
--header 'X-API-Key: sk-xxxxx'
```

响应体为文件二进制流。

## GET `/knowledge/:id/preview` - 内联预览文件

返回原始文件用于浏览器**内嵌预览**：

- `Content-Type` 按文件扩展名映射（`.pdf` → `application/pdf`，`.png` → `image/png`，`.txt`/`.md`/`.json` 等 → 对应文本 MIME 并带 `charset=utf-8`，未知扩展名回落到 `application/octet-stream`）。
- `Content-Disposition: inline; filename="<原文件名>"`，浏览器会内嵌渲染而非下载。
- `Cache-Control: private, max-age=3600`。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5/preview' \
--header 'X-API-Key: sk-xxxxx' \
-D -
```

**响应头**示例：

```
HTTP/1.1 200 OK
Content-Type: text/plain; charset=utf-8
Content-Disposition: inline; filename="彗星.txt"
Cache-Control: private, max-age=3600
```

响应体为文件内容（按 `Content-Type` 解读）。

## PUT `/knowledge/image/:id/:chunk_id` - 更新分块图像信息

为指定知识下的某个图像分块更新描述/替代文本等元信息。

**路径参数**:

| 字段       | 类型   | 说明     |
| ---------- | ------ | -------- |
| `id`       | string | 知识 ID  |
| `chunk_id` | string | 分块 ID  |

**请求体**:

| 字段         | 类型   | 必填 | 说明                                 |
| ------------ | ------ | ---- | ------------------------------------ |
| `image_info` | string | 是   | 图像信息（业务侧 JSON 字符串）       |

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledge/image/4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5/df10b37d-cd05-4b14-ba8a-e1bd0eb3bbd7' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "image_info": "{\"description\":\"产品架构图\",\"alt_text\":\"WeKnora 系统架构\"}"
}'
```

**响应**:

```json
{
    "success": true,
    "message": "Knowledge chunk image updated successfully"
}
```

## PUT `/knowledge/tags` - 批量更新知识标签

批量为多条知识设置/清除标签。

**请求体**:

| 字段      | 类型                       | 必填 | 说明                                                                       |
| --------- | -------------------------- | ---- | -------------------------------------------------------------------------- |
| `updates` | object<string, string\|null> | 是   | 知识 ID → 标签 ID 的映射；值为 `null` 表示清除该条知识的标签                |
| `kb_id`   | string                     | 否   | 限定知识库范围；指定时按该 KB 校验编辑权限（共享 KB 场景必填）             |

未传 `kb_id` 时，服务会从 `updates` 中取首个 knowledge ID 推断其所属知识库并据此鉴权。

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/knowledge/tags' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "kb_id": "kb-00000001",
    "updates": {
        "4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5": "tag-00000001",
        "9c8af585-ae15-44ce-8f73-45ad18394651": null
    }
}'
```

**响应**:

```json
{ "success": true }
```

## GET `/knowledge/search` - 跨知识库搜索/过滤知识

按关键词在当前租户（含已共享给当前租户）的知识中检索；可按文件类型过滤；指定 `agent_id` 时按共享 Agent 所配置的知识库范围检索。

**查询参数**:

| 字段         | 类型    | 默认 | 说明                                                                  |
| ------------ | ------- | ---- | --------------------------------------------------------------------- |
| `keyword`    | string  | -    | 关键词（可选）                                                       |
| `offset`     | integer | 0    | 偏移量                                                                |
| `limit`      | integer | 20   | 返回条数                                                              |
| `file_types` | string  | -    | 逗号分隔的扩展名列表，例如 `txt,pdf,docx`                            |
| `agent_id`   | string  | -    | 共享 Agent ID；按该 Agent 的 KB 选择模式（`all`/`selected`/`none`）限定范围 |

**请求**:

```curl
curl --location --get 'http://localhost:8080/api/v1/knowledge/search' \
--header 'X-API-Key: sk-xxxxx' \
--data-urlencode 'keyword=彗星' \
--data-urlencode 'offset=0' \
--data-urlencode 'limit=10' \
--data-urlencode 'file_types=txt,pdf'
```

**响应**:

```json
{
    "success": true,
    "data": [
        {
            "id": "4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5",
            "tenant_id": 1,
            "knowledge_base_id": "kb-00000001",
            "type": "file",
            "title": "彗星.txt",
            "description": "彗星是由冰和尘埃构成的太阳系小天体...",
            "file_name": "彗星.txt",
            "file_type": "txt",
            "file_size": 7710,
            "parse_status": "completed",
            "enable_status": "enabled",
            "created_at": "2025-08-12T11:52:36.168632+08:00",
            "updated_at": "2025-08-12T11:52:53.376871+08:00"
        }
    ],
    "has_more": false
}
```

> 注意：与其他列表接口不同，此处的 `data` 是**数组**而非 `{data, has_more}` 嵌套对象；`has_more` 与 `data` 同级。

`agent_id=...&` 且该 Agent 的 KB 选择模式为 `none` 时，将直接返回 `data: []` 与 `has_more: false`。

## POST `/knowledge/batch-delete` - 同一知识库内批量删除

按 ID 列表在单个知识库内批量删除知识（异步任务）。单次最多 200 个 ID；服务侧会校验所有 ID 属于同一 `kb_id`。

**请求体**:

| 字段    | 类型     | 必填 | 说明                              |
| ------- | -------- | ---- | --------------------------------- |
| `kb_id` | string   | 是   | 目标知识库 ID                     |
| `ids`   | string[] | 是   | 待删除的知识 ID 列表（≤ 200）     |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge/batch-delete' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "kb_id": "kb-00000001",
    "ids": [
        "4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5",
        "9c8af585-ae15-44ce-8f73-45ad18394651"
    ]
}'
```

**响应**:

```json
{
    "success": true,
    "message": "Batch delete task submitted",
    "data": {
        "task_id": "kg_delete_1_kb-00000001_xxxx",
        "deleted_count": 2
    }
}
```

任一 ID 不属于 `kb_id` 或不存在时，返回 400 并整批拒绝。

## POST `/knowledge/move` - 迁移知识到另一知识库

将一条或多条**处于 `completed` 状态**的知识从源 KB 迁到目标 KB（异步）。约束：

- 源/目标 KB 必须属于当前租户；
- 源/目标必须为同一 KB 类型且**使用相同的 Embedding 模型**；
- 源 KB ≠ 目标 KB；
- 仅 `parse_status=completed` 的知识可迁移。

**请求体**:

| 字段            | 类型     | 必填 | 说明                                                                            |
| --------------- | -------- | ---- | ------------------------------------------------------------------------------- |
| `knowledge_ids` | string[] | 是   | 待迁移的知识 ID 列表（至少 1 个）                                              |
| `source_kb_id`  | string   | 是   | 源知识库 ID                                                                     |
| `target_kb_id`  | string   | 是   | 目标知识库 ID                                                                   |
| `mode`          | string   | 是   | 迁移模式：`reuse_vectors`（复用向量数据，零成本） / `reparse`（在目标库重新解析） |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge/move' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "knowledge_ids": ["4c4e7c1a-09cf-485b-a7b5-24b8cdc5acf5"],
    "source_kb_id": "kb-00000001",
    "target_kb_id": "kb-00000002",
    "mode": "reuse_vectors"
}'
```

**响应**:

```json
{
    "success": true,
    "data": {
        "task_id": "kg_move_1_kb-00000001_xxxx",
        "source_kb_id": "kb-00000001",
        "target_kb_id": "kb-00000002",
        "knowledge_count": 1,
        "message": "Knowledge move task started"
    }
}
```

获取 `task_id` 后通过下一个接口轮询进度。

## GET `/knowledge/move/progress/:task_id` - 查询迁移进度

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/knowledge/move/progress/kg_move_1_kb-00000001_xxxx' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "success": true,
    "data": {
        "task_id": "kg_move_1_kb-00000001_xxxx",
        "source_kb_id": "kb-00000001",
        "target_kb_id": "kb-00000002",
        "status": "completed",
        "progress": 100,
        "total": 1,
        "processed": 1,
        "failed": 0,
        "message": "迁移完成",
        "error": "",
        "created_at": 1731312000,
        "updated_at": 1731312045
    }
}
```

`status` 取值：`pending` / `processing` / `completed` / `failed`；`progress` 为 0-100 的整数百分比；`created_at` / `updated_at` 为 Unix 秒时间戳。
