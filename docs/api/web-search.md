# Web Search API

[返回目录](./README.md)

包含两组接口：
- `/web-search/providers`：返回**可用的 provider 类型**（只读元数据）
- `/web-search-providers/*`：当前租户**自定义保存**的 provider CRUD 与连通性测试

| 方法   | 路径                                  | 描述                                |
| ------ | ------------------------------------- | ----------------------------------- |
| GET    | `/web-search/providers`               | 获取网络搜索服务商类型列表           |
| GET    | `/web-search-providers/types`         | 获取 Provider 类型元数据（含参数定义） |
| POST   | `/web-search-providers/test`          | 使用原始凭证测试连通性（不落库）       |
| POST   | `/web-search-providers`               | 创建租户级 Provider 配置             |
| GET    | `/web-search-providers`               | 获取当前租户已保存的 Provider 列表     |
| GET    | `/web-search-providers/:id`           | 获取指定 Provider 详情               |
| PUT    | `/web-search-providers/:id`           | 更新 Provider                       |
| DELETE | `/web-search-providers/:id`           | 删除 Provider                       |
| POST   | `/web-search-providers/:id/test`      | 使用已保存凭证测试连通性             |

## GET `/web-search/providers` - 获取网络搜索服务商类型列表

获取系统中可用的网络搜索服务商列表（系统级元数据，与租户无关）。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/web-search/providers' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": [
        {
            "name": "google",
            "label": "Google Search",
            "description": "通过 Google 自定义搜索 API 进行网络搜索",
            "enabled": true
        },
        {
            "name": "bing",
            "label": "Bing Search",
            "description": "通过 Bing Search API 进行网络搜索",
            "enabled": true
        },
        {
            "name": "serpapi",
            "label": "SerpAPI",
            "description": "通过 SerpAPI 进行搜索引擎结果抓取",
            "enabled": false
        }
    ],
    "success": true
}
```

## GET `/web-search-providers/types` - 获取 Provider 类型元数据

返回 UI 表单需要的所有 provider 类型及参数定义（每种 provider 需要哪些字段、类型、是否必填）。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/web-search-providers/types' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "data": [
        {
            "provider": "google",
            "label": "Google Search",
            "description": "...",
            "parameter_schema": [
                { "name": "api_key", "label": "API Key", "type": "string", "required": true },
                { "name": "cx", "label": "Search Engine ID", "type": "string", "required": true }
            ]
        }
    ],
    "success": true
}
```

## POST `/web-search-providers/test` - 使用原始凭证测试连通性

前端表单"测试连接"按钮使用：用尚未保存的凭证发起一次样本搜索。

**参数说明（请求体）**:

| 字段       | 类型   | 必填 | 说明                              |
| ---------- | ------ | ---- | --------------------------------- |
| provider   | string | 是   | provider 类型（如 `google`、`bing`） |
| parameters | object | 是   | 该 provider 所需凭证与参数（与 `/types` 中 `parameter_schema` 对应） |

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/web-search-providers/test' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "provider": "google",
    "parameters": {
        "api_key": "AIza...",
        "cx": "0123456789:abcdefg"
    }
}'
```

**响应**:

```json
{ "success": true }
```

失败时：

```json
{ "success": false, "error": "google api: 403 forbidden" }
```

## POST `/web-search-providers` - 创建 Provider

**参数说明（请求体）**:

| 字段        | 类型    | 必填 | 说明                                       |
| ----------- | ------- | ---- | ------------------------------------------ |
| name        | string  | 是   | Provider 显示名（在租户内唯一友好名）       |
| provider    | string  | 是   | Provider 类型（来自 `/web-search-providers/types`） |
| description | string  | 否   | 备注                                       |
| parameters  | object  | 否   | 凭证与参数                                 |
| is_default  | boolean | 否   | 是否设为当前租户默认 Provider              |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/web-search-providers' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "name": "公司 Google CSE",
    "provider": "google",
    "description": "用于内网搜索",
    "parameters": {
        "api_key": "AIza...",
        "cx": "0123456789:abcdefg"
    },
    "is_default": true
}'
```

**响应**:

```json
{
    "data": {
        "id": "wsp-...",
        "tenant_id": 1,
        "name": "公司 Google CSE",
        "provider": "google",
        "is_default": true,
        "parameters": { "api_key": "***", "cx": "0123456789:abcdefg" }
    },
    "success": true
}
```

## GET `/web-search-providers` - 获取 Provider 列表

返回当前租户已保存的所有 Provider。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/web-search-providers' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "data": [
        { "id": "wsp-001", "name": "公司 Google CSE", "provider": "google", "is_default": true }
    ],
    "success": true
}
```

## GET `/web-search-providers/:id` - 获取 Provider 详情

**路径参数**:

| 字段 | 类型   | 说明        |
| ---- | ------ | ----------- |
| id   | string | Provider ID |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/web-search-providers/wsp-001' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**: 同创建接口。404 表示不存在。

## PUT `/web-search-providers/:id` - 更新 Provider

**说明**：`provider` 字段（类型）创建后不可修改，仅支持更新 `name` / `description` / `parameters` / `is_default`。

**参数说明（请求体）**: 同创建接口，但不包含 `provider`。

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/web-search-providers/wsp-001' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "name": "公司 Google CSE (v2)",
    "parameters": { "api_key": "NEW...", "cx": "0123456789:abcdefg" },
    "is_default": false
}'
```

**响应**: `{ "data": {...更新后实体...}, "success": true }`

## DELETE `/web-search-providers/:id` - 删除 Provider

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/web-search-providers/wsp-001' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**: `{ "success": true }`

## POST `/web-search-providers/:id/test` - 测试已保存的 Provider

使用数据库中已保存的凭证发起一次样本搜索。

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/web-search-providers/wsp-001/test' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**: 同 `POST /web-search-providers/test`。
