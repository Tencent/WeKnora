# MCP Service API

[返回目录](./README.md)

| 方法   | 路径                              | 描述                   |
| ------ | --------------------------------- | ---------------------- |
| POST   | `/mcp-services`                   | 创建 MCP 服务          |
| GET    | `/mcp-services`                   | 获取 MCP 服务列表      |
| GET    | `/mcp-services/:id`               | 获取 MCP 服务详情      |
| PUT    | `/mcp-services/:id`               | 更新 MCP 服务          |
| DELETE | `/mcp-services/:id`               | 删除 MCP 服务          |
| POST   | `/mcp-services/:id/test`          | 测试 MCP 服务连接      |
| GET    | `/mcp-services/:id/tools`         | 获取 MCP 服务工具列表  |
| GET    | `/mcp-services/:id/resources`     | 获取 MCP 服务资源列表  |

## POST `/mcp-services` - 创建 MCP 服务

**请求参数**:
- `name`: 服务名称（必填）
- `description`: 服务描述（可选）
- `transport_type`: 传输类型，可选值：`sse`、`http-streamable`、`stdio`（必填）
- `url`: 服务地址，当 transport_type 为 `sse` 或 `http-streamable` 时必填
- `headers`: 自定义请求头（可选）
- `auth_config`: 认证配置（可选），包含 `api_key`、`token`、`custom_headers`
- `advanced_config`: 高级配置（可选），包含 `timeout`、`retry_count`、`retry_delay`
- `stdio_config`: stdio 传输配置（可选），包含 `command`、`args`
- `env_vars`: 环境变量（可选）

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

**创建 stdio 类型的 MCP 服务**:

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
    "success": true
}
```

## POST `/mcp-services/:id/test` - 测试 MCP 服务连接

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
