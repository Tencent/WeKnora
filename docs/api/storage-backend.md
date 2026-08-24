# Storage Backend API

[返回目录](./README.md)

存储后端（StorageBackend）API 用于管理空间的对象/文件存储实例。一个空间可以注册多个存储实例（`local`、`minio`、`cos`、`tos`、`s3`、`oss`、`ks3`、`obs`），并将不同知识库绑定到不同实例；空间维度还有一个默认实例（`default_storage_backend_id`），未显式绑定的新知识库使用该默认实例。

接口同时管理用户创建的实例（`source: "user"`）以及从环境变量快照生成的只读实例（`source: "env"`）。存储后端 CRUD 需要 **Admin+** 角色；对 API Key 需具备 `manage_storage_backends` capability（或 full-access）。

| 方法   | 路径                              | 描述                                   | 最低权限 |
| ------ | --------------------------------- | -------------------------------------- | -------- |
| GET    | `/storage-backends/types`         | 获取 `STORAGE_ALLOW_LIST` 允许的存储类型 | Viewer+  |
| POST   | `/storage-backends/test`          | 使用原始配置测试连通性（不落库）         | Admin+   |
| POST   | `/storage-backends`               | 创建存储实例                           | Admin+   |
| GET    | `/storage-backends`               | 获取存储实例列表                       | Viewer+  |
| GET    | `/storage-backends/:id`           | 获取存储实例详情                       | Viewer+  |
| PUT    | `/storage-backends/:id`           | 更新存储实例（名称/凭据/状态）          | Admin+   |
| DELETE | `/storage-backends/:id`           | 删除存储实例（软删除）                  | Admin+   |
| POST   | `/storage-backends/:id/test`      | 测试已保存实例的连通性                  | Admin+   |
| PUT    | `/storage-backends/:id/default`   | 设为空间默认存储实例                    | Admin+   |

> 所有响应中的敏感字段（`access_key_id`、`secret_access_key`）都会被掩码。更新时若提交掩码占位符，则保留库中原有的真实凭据，不会被占位符覆盖。

## 存储配置字段（`config`）

不同 provider 使用同一套归一化的配置对象，按 provider 取用其中的子集：

| 字段                | 类型    | 说明                                                          |
| ------------------- | ------- | ------------------------------------------------------------- |
| mode                | string  | MinIO 模式：`docker`（复用环境变量凭据）或 `remote`            |
| endpoint            | string  | 对象存储 endpoint（COS 使用 region，不需要 endpoint）          |
| region              | string  | 区域                                                          |
| access_key_id       | string  | 访问密钥 ID（COS 对应 SecretID）；响应中掩码                   |
| secret_access_key   | string  | 访问密钥 Secret（COS 对应 SecretKey）；响应中掩码             |
| bucket_name         | string  | Bucket 名称                                                   |
| path_prefix         | string  | 对象前缀，必须为相对路径，禁止 `/` 开头或 `..` 上跳            |
| app_id              | string  | 腾讯云 COS AppID                                              |
| use_ssl             | boolean | 是否使用 SSL                                                  |
| force_path_style    | boolean | S3 是否使用 path-style 寻址                                   |
| use_temp_bucket     | boolean | OSS 是否使用临时 bucket                                       |
| temp_bucket_name    | string  | 临时 bucket 名称                                              |
| temp_region         | string  | 临时 bucket 区域                                              |

> `endpoint`、`region`、`bucket_name`、`path_prefix` 决定对象的物理位置，**创建后不可变更**（更新时会被拒绝）；如需迁移请使用存储迁移流程。凭据可通过更新单独轮换。

## GET `/storage-backends/types` - 获取允许的存储类型

返回 `STORAGE_ALLOW_LIST` 允许的 provider 列表，可用于前端动态表单生成。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/storage-backends/types' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "success": true,
    "data": ["local", "minio", "cos", "s3"]
}
```

## POST `/storage-backends/test` - 使用原始配置测试连通性

用前端表单中尚未保存的配置执行一次连通性测试，不会写入数据库。

**参数说明（请求体）**:

| 字段     | 类型   | 必填 | 说明                                        |
| -------- | ------ | ---- | ------------------------------------------- |
| name     | string | 是   | 实例显示名                                  |
| provider | string | 是   | 存储类型，取自 `/storage-backends/types`    |
| config   | object | 否   | 该 provider 对应的存储配置字段              |

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/storage-backends/test' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "name": "s3-hot",
    "provider": "s3",
    "config": {
        "endpoint": "https://s3.example.com",
        "region": "ap-test-1",
        "access_key_id": "AKID",
        "secret_access_key": "SECRET",
        "bucket_name": "weknora"
    }
}'
```

**响应（成功）**:

```json
{
    "success": true
}
```

**响应（失败）**:

```json
{
    "success": false,
    "error": "连接被拒绝，请确认服务已启动且端口正确"
}
```

> 测试失败时 HTTP 状态码仍为 `200`，错误信息通过 `success: false` + `error` 返回；`error` 已脱敏，不会泄漏内部主机名、IP、端口或 TLS 细节。

## POST `/storage-backends` - 创建存储实例

为当前空间创建一个新的存储实例。创建前会先校验配置、执行 SSRF 校验（本地存储与 docker 模式 MinIO 除外），并执行一次连通性测试；任一环节失败都会返回 `400`。同一空间内实例名称不允许重复。

**参数说明（请求体）**:

| 字段     | 类型   | 必填 | 说明                                        |
| -------- | ------ | ---- | ------------------------------------------- |
| name     | string | 是   | 实例显示名（空间内唯一）                     |
| provider | string | 是   | 存储类型，取自 `/storage-backends/types`    |
| config   | object | 否   | 该 provider 对应的存储配置字段              |
| status   | string | 否   | `active`（默认）或 `disabled`               |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/storage-backends' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "name": "s3-hot",
    "provider": "s3",
    "config": {
        "endpoint": "https://s3.example.com",
        "region": "ap-test-1",
        "access_key_id": "AKID",
        "secret_access_key": "SECRET",
        "bucket_name": "weknora",
        "path_prefix": "prod"
    }
}'
```

**响应** (201):

```json
{
    "success": true,
    "data": {
        "id": "550e8400-e29b-41d4-a716-446655440000",
        "name": "s3-hot",
        "provider": "s3",
        "config": {
            "endpoint": "https://s3.example.com",
            "region": "ap-test-1",
            "access_key_id": "***",
            "secret_access_key": "***",
            "bucket_name": "weknora",
            "path_prefix": "prod"
        },
        "source": "user",
        "status": "active",
        "legacy_alias": false,
        "created_at": "2026-07-15T10:00:00Z",
        "updated_at": "2026-07-15T10:00:00Z"
    }
}
```

## GET `/storage-backends` - 获取存储实例列表

返回当前空间的所有存储实例（凭据已掩码），并在顶层返回空间默认实例 id。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/storage-backends' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "success": true,
    "data": [
        {
            "id": "550e8400-e29b-41d4-a716-446655440000",
            "name": "s3-hot",
            "provider": "s3",
            "config": { "endpoint": "https://s3.example.com", "access_key_id": "***", "secret_access_key": "***", "bucket_name": "weknora" },
            "source": "user",
            "status": "active",
            "legacy_alias": false
        }
    ],
    "default_storage_backend_id": "550e8400-e29b-41d4-a716-446655440000"
}
```

## GET `/storage-backends/:id` - 获取存储实例详情

根据 ID 获取当前空间下的单个存储实例，凭据已掩码。

**路径参数**:

| 字段 | 类型   | 必填 | 说明          |
| ---- | ------ | ---- | ------------- |
| id   | string | 是   | 存储实例 ID   |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/storage-backends/550e8400-e29b-41d4-a716-446655440000' \
--header 'X-API-Key: sk-xxxxx'
```

## PUT `/storage-backends/:id` - 更新存储实例

更新实例的可变字段（`name`、凭据、`status`）。`provider` 与物理位置字段（`endpoint`、`region`、`bucket_name`、`path_prefix`）不可变更，尝试更改会返回 `400`。环境变量来源（`source: "env"`）的实例只读，不可更新。更新同样会执行校验与连通性测试。

> 若 `access_key_id` / `secret_access_key` 提交为掩码占位符（`***`），则保留库中原有真实凭据。

**禁用保护**：将当前为默认实例、或仍有知识库绑定的实例改为 `disabled` 会被拒绝（`400`）。

**路径参数**:

| 字段 | 类型   | 必填 | 说明          |
| ---- | ------ | ---- | ------------- |
| id   | string | 是   | 存储实例 ID   |

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/storage-backends/550e8400-e29b-41d4-a716-446655440000' \
--header 'X-API-Key: sk-xxxxx' \
--header 'Content-Type: application/json' \
--data '{
    "name": "s3-hot-renamed",
    "provider": "s3",
    "config": {
        "access_key_id": "***",
        "secret_access_key": "NEW_SECRET"
    }
}'
```

## DELETE `/storage-backends/:id` - 删除存储实例

对存储实例执行软删除。以下情况删除会被拒绝（`400`）：实例是空间默认实例、仍有知识库绑定、环境变量来源（只读）、或为 legacy 别名（旧文件路径可能仍引用它）。删除在事务中执行；PostgreSQL 上对目标行加行锁以避免并发绑定竞态。

**路径参数**:

| 字段 | 类型   | 必填 | 说明          |
| ---- | ------ | ---- | ------------- |
| id   | string | 是   | 存储实例 ID   |

**请求**:

```curl
curl --location --request DELETE 'http://localhost:8080/api/v1/storage-backends/550e8400-e29b-41d4-a716-446655440000' \
--header 'X-API-Key: sk-xxxxx'
```

**响应（成功）**:

```json
{
    "success": true
}
```

## POST `/storage-backends/:id/test` - 测试已保存实例的连通性

对已保存的存储实例用其存储的凭据执行一次连通性测试。

**路径参数**:

| 字段 | 类型   | 必填 | 说明          |
| ---- | ------ | ---- | ------------- |
| id   | string | 是   | 存储实例 ID   |

**请求**:

```curl
curl --location --request POST 'http://localhost:8080/api/v1/storage-backends/550e8400-e29b-41d4-a716-446655440000/test' \
--header 'X-API-Key: sk-xxxxx'
```

**响应（成功）**:

```json
{
    "success": true
}
```

> 与 `/storage-backends/test` 一致，测试失败时 HTTP 状态码仍为 `200`，错误经脱敏后通过 `success: false` + `error` 返回。

## PUT `/storage-backends/:id/default` - 设为空间默认实例

将某个存储实例标记为空间默认实例。仅 `active` 状态的实例可以设为默认。未显式绑定存储实例的新知识库将使用默认实例。

**路径参数**:

| 字段 | 类型   | 必填 | 说明          |
| ---- | ------ | ---- | ------------- |
| id   | string | 是   | 存储实例 ID   |

**请求**:

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/storage-backends/550e8400-e29b-41d4-a716-446655440000/default' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**:

```json
{
    "success": true
}
```

## 环境变量存储实例

通过 `STORAGE_TYPE` 等环境变量配置的存储会以只读实例（`source: "env"`、`legacy_alias: true`）形式参与实例解析，使 env-only 部署与用户管理的实例走同一套解析路径。这类实例在每次启动时按环境变量刷新，且不可通过 API 更新或删除。

## 错误码

| HTTP 状态码 | 含义                                                             |
| ----------- | ---------------------------------------------------------------- |
| 400         | 请求参数错误、校验失败、SSRF 校验失败、连通性测试失败、尝试更改不可变字段、修改只读实例、删除受保护实例、禁用被引用实例、设为默认时实例非 active |
| 401         | 未认证（缺少空间上下文或 API Key）                               |
| 403         | 权限不足（需 Admin+ 或 API Key `manage_storage_backends` capability） |
| 404         | 存储实例不存在                                                   |
| 409         | 同名存储实例已存在                                               |
