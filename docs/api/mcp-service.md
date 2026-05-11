# MCP Service API

[返回目录](./README.md)

MCP（Model Context Protocol）服务管理接口，提供 MCP 服务的 CRUD、连通性测试、工具/资源发现，以及工具人工审批策略配置。

| 方法   | 路径                                              | 描述                                          |
| ------ | ------------------------------------------------- | --------------------------------------------- |
| POST   | `/mcp-services`                                   | 创建 MCP 服务                                 |
| GET    | `/mcp-services`                                   | 获取当前租户的 MCP 服务列表                   |
| GET    | `/mcp-services/:id`                               | 获取 MCP 服务详情                             |
| PUT    | `/mcp-services/:id`                               | 更新 MCP 服务（部分字段更新）                 |
| DELETE | `/mcp-services/:id`                               | 删除 MCP 服务                                 |
| POST   | `/mcp-services/:id/test`                          | 测试 MCP 服务连通性                           |
| GET    | `/mcp-services/:id/tools`                         | 获取 MCP 服务工具列表                         |
| GET    | `/mcp-services/:id/resources`                     | 获取 MCP 服务资源列表                         |
| GET    | `/mcp-services/:id/tool-approvals`                | 列出该服务下各工具的人工审批策略 |
| PUT    | `/mcp-services/:id/tool-approvals/:tool_name`     | 设置/更新某工具的人工审批策略  |
| POST   | `/agent/tool-approvals/:pending_id`               | 处理 Agent 工具调用待审批请求  |

## POST `/mcp-services` - 创建 MCP 服务

**请求参数**:

| 字段             | 类型    | 必填 | 说明                                                                                          |
| ---------------- | ------- | ---- | --------------------------------------------------------------------------------------------- |
| name             | string  | 是   | 服务名称                                                                                      |
| description      | string  | 否   | 服务描述                                                                                      |
| transport_type   | string  | 是   | 传输类型，可选：`sse`、`http-streamable`、`stdio`                                              |
| url              | string  | 条件 | 服务地址；当 `transport_type` 为 `sse` / `http-streamable` 时必填（受 SSRF 安全校验约束）        |
| headers          | object  | 否   | 自定义请求头                                                                                  |
| auth_config      | object  | 否   | 认证配置，支持 `api_key`、`token`                                                              |
| advanced_config  | object  | 否   | 高级配置，支持 `timeout`、`retry_count`、`retry_delay`                                          |
| stdio_config     | object  | 条件 | stdio 传输配置，包含 `command`、`args`；当 `transport_type` 为 `stdio` 时必填                  |
| env_vars         | object  | 否   | 环境变量（stdio 场景常用）                                                                    |
| enabled          | boolean | 否   | 是否启用                                                                                      |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/mcp-services' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "name": "天气查询服务",
    "description": "提供全球天气信息查询",
    "transport_type": "sse",
    "url": "https://mcp.example.com/weather/sse",
    "headers": {
        "X-Custom-Header": "value"
    },
    "auth_config": {
        "api_key": "weather-api-key-xxxxx"
    },
    "advanced_config": {
        "timeout": 30,
        "retry_count": 3,
        "retry_delay": 1
    }
}'
```

**响应**:

```json
{
    "data": {
        "id": "mcp-00000001",
        "tenant_id": 1,
        "name": "天气查询服务",
        "description": "提供全球天气信息查询",
        "enabled": true,
        "transport_type": "sse",
        "url": "https://mcp.example.com/weather/sse",
        "headers": {
            "X-Custom-Header": "value"
        },
        "auth_config": {
            "api_key": "weather-api-key-xxxxx"
        },
        "advanced_config": {
            "timeout": 30,
            "retry_count": 3,
            "retry_delay": 1
        },
        "is_builtin": false,
        "created_at": "2025-08-12T10:00:00+08:00",
        "updated_at": "2025-08-12T10:00:00+08:00"
    },
    "success": true
}
```

**创建 stdio 类型的 MCP 服务示例**:

```curl
curl --location 'http://localhost:8080/api/v1/mcp-services' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "name": "本地文件服务",
    "description": "通过 stdio 访问本地文件系统",
    "transport_type": "stdio",
    "stdio_config": {
        "command": "/usr/local/bin/mcp-file-server",
        "args": ["--root", "/data"]
    },
    "env_vars": {
        "MCP_LOG_LEVEL": "info"
    }
}'
```

## GET `/mcp-services` - 获取 MCP 服务列表

返回当前租户已配置的所有 MCP 服务。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/mcp-services' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": [
        {
            "id": "mcp-00000001",
            "tenant_id": 1,
            "name": "天气查询服务",
            "description": "提供全球天气信息查询",
            "enabled": true,
            "transport_type": "sse",
            "url": "https://mcp.example.com/weather/sse",
            "headers": {},
            "auth_config": {
                "api_key": "weather-api-key-xxxxx"
            },
            "advanced_config": {
                "timeout": 30,
                "retry_count": 3,
                "retry_delay": 1
            },
            "is_builtin": false,
            "created_at": "2025-08-12T10:00:00+08:00",
            "updated_at": "2025-08-12T10:00:00+08:00"
        },
        {
            "id": "mcp-00000002",
            "tenant_id": 1,
            "name": "本地文件服务",
            "description": "通过 stdio 访问本地文件系统",
            "enabled": true,
            "transport_type": "stdio",
            "headers": {},
            "auth_config": null,
            "advanced_config": null,
            "stdio_config": {
                "command": "/usr/local/bin/mcp-file-server",
                "args": ["--root", "/data"]
            },
            "env_vars": {
                "MCP_LOG_LEVEL": "info"
            },
            "is_builtin": false,
            "created_at": "2025-08-12T11:00:00+08:00",
            "updated_at": "2025-08-12T11:00:00+08:00"
        }
    ],
    "success": true
}
```

## GET `/mcp-services/:id` - 获取 MCP 服务详情

**路径参数**:

| 字段 | 类型   | 说明           |
| ---- | ------ | -------------- |
| id   | string | MCP 服务 ID    |

> 注：内置（`is_builtin: true`）服务在响应中会隐藏敏感凭证字段。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/mcp-services/mcp-00000001' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "id": "mcp-00000001",
        "tenant_id": 1,
        "name": "天气查询服务",
        "description": "提供全球天气信息查询",
        "enabled": true,
        "transport_type": "sse",
        "url": "https://mcp.example.com/weather/sse",
        "headers": {},
        "auth_config": {
            "api_key": "weather-api-key-xxxxx"
        },
        "advanced_config": {
            "timeout": 30,
            "retry_count": 3,
            "retry_delay": 1
        },
        "is_builtin": false,
        "created_at": "2025-08-12T10:00:00+08:00",
        "updated_at": "2025-08-12T10:00:00+08:00"
    },
    "success": true
}
```

## PUT `/mcp-services/:id` - 更新 MCP 服务

支持部分字段更新，可传入下列任意子集：`name`、`description`、`enabled`、`transport_type`、`url`、`stdio_config`、`env_vars`、`headers`、`auth_config`、`advanced_config`。其中 `url` 若提供，会再次执行 SSRF 安全校验。

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/mcp-services/mcp-00000001' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "name": "天气查询服务（更新）",
    "description": "提供全球天气信息查询，支持实时数据",
    "enabled": false
}'
```

**响应**:

```json
{
    "data": {
        "id": "mcp-00000001",
        "tenant_id": 1,
        "name": "天气查询服务（更新）",
        "description": "提供全球天气信息查询，支持实时数据",
        "enabled": false,
        "transport_type": "sse",
        "url": "https://mcp.example.com/weather/sse",
        "headers": {},
        "auth_config": {
            "api_key": "weather-api-key-xxxxx"
        },
        "advanced_config": {
            "timeout": 30,
            "retry_count": 3,
            "retry_delay": 1
        },
        "is_builtin": false,
        "created_at": "2025-08-12T10:00:00+08:00",
        "updated_at": "2025-08-12T12:00:00+08:00"
    },
    "success": true
}
```

## DELETE `/mcp-services/:id` - 删除 MCP 服务

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/mcp-services/mcp-00000001' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "success": true,
    "message": "MCP service deleted successfully"
}
```

## POST `/mcp-services/:id/test` - 测试 MCP 服务连通性

后端会以已保存配置建立一次 MCP 连接，返回连接结果及探测到的工具/资源列表。连接失败时 HTTP 仍为 200，但 `data.success` 为 `false`，错误原因在 `data.message` 中。

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/mcp-services/mcp-00000001/test' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": {
        "success": true,
        "message": "连接成功",
        "tools": [
            {
                "name": "get_weather",
                "description": "获取指定城市的天气信息",
                "inputSchema": {
                    "type": "object",
                    "properties": {
                        "city": {
                            "type": "string",
                            "description": "城市名称"
                        }
                    },
                    "required": ["city"]
                }
            }
        ],
        "resources": [
            {
                "uri": "weather://cities",
                "name": "城市列表",
                "description": "支持查询的城市列表",
                "mimeType": "application/json"
            }
        ]
    },
    "success": true
}
```

## GET `/mcp-services/:id/tools` - 获取 MCP 服务工具列表

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/mcp-services/mcp-00000001/tools' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": [
        {
            "name": "get_weather",
            "description": "获取指定城市的天气信息",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "city": {
                        "type": "string",
                        "description": "城市名称"
                    }
                },
                "required": ["city"]
            }
        },
        {
            "name": "get_forecast",
            "description": "获取未来天气预报",
            "inputSchema": {
                "type": "object",
                "properties": {
                    "city": {
                        "type": "string",
                        "description": "城市名称"
                    },
                    "days": {
                        "type": "integer",
                        "description": "预报天数"
                    }
                },
                "required": ["city"]
            }
        }
    ],
    "success": true
}
```

## GET `/mcp-services/:id/resources` - 获取 MCP 服务资源列表

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/mcp-services/mcp-00000001/resources' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json'
```

**响应**:

```json
{
    "data": [
        {
            "uri": "weather://cities",
            "name": "城市列表",
            "description": "支持查询的城市列表",
            "mimeType": "application/json"
        },
        {
            "uri": "weather://config",
            "name": "服务配置",
            "description": "当前服务配置信息",
            "mimeType": "application/json"
        }
    ],
    "success": true
}
```

## GET `/mcp-services/:id/tool-approvals` - 列出工具人工审批策略

返回该 MCP 服务下各工具持久化的 `require_approval` 标记。仅返回数据库中已显式配置过的工具记录；未出现在列表中的工具默认无需审批。

**路径参数**:

| 字段 | 类型   | 说明        |
| ---- | ------ | ----------- |
| id   | string | MCP 服务 ID |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/mcp-services/mcp-00000001/tool-approvals' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "data": [
        {
            "tool_name": "delete_file",
            "require_approval": true,
            "updated_at": "2025-09-20T15:30:00+08:00"
        },
        {
            "tool_name": "get_weather",
            "require_approval": false,
            "updated_at": "2025-09-20T15:31:00+08:00"
        }
    ],
    "success": true
}
```

## PUT `/mcp-services/:id/tool-approvals/:tool_name` - 设置工具人工审批策略

为指定 MCP 服务下的某个工具设置/更新人工审批要求。当 `require_approval` 为 `true` 时，Agent 在调用该工具前会阻塞并产生一条待审批记录，需要前端调用 `POST /agent/tool-approvals/:pending_id` 完成审批。

**路径参数**:

| 字段       | 类型   | 说明                                                                |
| ---------- | ------ | ------------------------------------------------------------------- |
| id         | string | MCP 服务 ID                                                         |
| tool_name  | string | 工具名（由 Gin 自动 URL 解码，调用方需对名称中的 `%`、`/` 做 URL 编码） |

**请求体**:

| 字段              | 类型    | 必填 | 说明                                |
| ----------------- | ------- | ---- | ----------------------------------- |
| require_approval  | boolean | 是   | 是否要求人工审批后才能执行该工具    |

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/mcp-services/mcp-00000001/tool-approvals/delete_file' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "require_approval": true
}'
```

**响应**:

```json
{
    "success": true
}
```

## POST `/agent/tool-approvals/:pending_id` - 处理待审批工具调用

用于 Agent 在执行过程中阻塞等待人工审批的场景：当 Agent 命中一个 `require_approval = true` 的工具时会生成一条 `pending_id`，前端拿到这个 ID 后调用此接口将审批结果回传给 Agent，Agent 才会继续执行（或终止）。

**鉴权要求**：请求上下文中必须有已认证用户（`user_id`），且该用户必须是当前 pending 会话的所有者；租户与用户两层都会做 fail-close 校验。

**路径参数**:

| 字段        | 类型   | 说明                |
| ----------- | ------ | ------------------- |
| pending_id  | string | 待审批记录 ID       |

**请求体**:

| 字段           | 类型   | 必填 | 说明                                                                                                              |
| -------------- | ------ | ---- | ----------------------------------------------------------------------------------------------------------------- |
| decision       | string | 是   | 审批结论，必须为 `approve` 或 `reject`                                                                            |
| modified_args  | object | 否   | 仅在 `approve` 时生效，允许人工修改本次工具调用的参数；必须是非 null 的 JSON 对象，否则返回 400                    |
| reason         | string | 否   | 审批理由（任意，便于审计）                                                                                        |

**请求（通过）**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/agent/tool-approvals/pending-abcdef123456' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "decision": "approve",
    "modified_args": {
        "path": "/tmp/safe-target.txt"
    },
    "reason": "已确认目标路径安全"
}'
```

**请求（驳回）**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/agent/tool-approvals/pending-abcdef123456' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "decision": "reject",
    "reason": "目标路径在受保护目录"
}'
```

**响应**:

```json
{
    "success": true
}
```

**错误码说明**:

| HTTP | 触发条件                                                                                |
| ---- | --------------------------------------------------------------------------------------- |
| 400  | `decision` 不是 `approve`/`reject`；或 `modified_args` 是 `null`/非对象；或租户/用户错配 |
| 401  | 上下文缺失认证用户（中间件未注入 `user_id`）                                            |
| 404  | `pending_id` 不存在或已完成（超时/取消已先一步消费）                                    |
