# 文件服务 API

[返回目录](./README.md)

文件服务接口用于代理访问租户存储在本地/MinIO/COS/TOS 等后端的文件。

## 说明

- `/files` 和 `/files/presigned-preview` 需要认证（`X-API-Key` 或 `Authorization: Bearer`）。
- `/files/presigned` 使用 HMAC 签名验证，**无需认证**，用于 IM 频道在机器人回复中嵌入图片。
- 所有路径参数 `file_path` 会经过路径穿越和跨租户校验。

## 端点一览

| 方法 | 路径 | 鉴权 | 描述 |
| ---- | ---- | ---- | ---- |
| GET | `/files` | 认证 | 代理获取文件 |
| GET | `/files/presigned` | HMAC 签名 | 通过签名 URL 获取文件 |
| HEAD | `/files/presigned` | HMAC 签名 | 获取文件元信息（用于 IM 预览） |
| GET | `/files/presigned-preview` | 认证（Admin） | 诊断：预览签名 URL |

---

## GET `/files` - 代理获取文件

按租户存储配置代理获取文件。

**查询参数**:

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `file_path` | string | 是 | 文件路径（如 `minio://bucket/path/to/file.png`） |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/files?file_path=minio://weknora/1/kb-00000001/file.pdf' \
--header 'X-API-Key: sk-xxxxx' \
--output file.pdf
```

**响应**: 文件二进制流，`Content-Type` 按文件扩展名设置。

**错误**:
- 缺少 `file_path` → 400
- 路径包含 `..` → 400
- 租户上下文缺失 → 401
- 跨租户访问或无效路径 → 403
- 文件不存在 → 404

---

## GET `/files/presigned` - 通过签名 URL 获取文件

通过 HMAC 签名的 URL 获取文件，无需认证。由 IM 频道用于在机器人回复中嵌入图片。

**查询参数**:

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `file_path` | string | 是 | 文件路径 |
| `tenant_id` | string | 是 | 租户 ID |
| `expires` | string | 是 | 过期时间（Unix 时间戳） |
| `sig` | string | 是 | HMAC 签名 |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/files/presigned?file_path=minio://weknora/1/img.png&tenant_id=1&expires=1731312000&sig=abc123...' \
--output img.png
```

**响应**: 文件二进制流。

**错误**: 缺少参数 → 400；签名无效或过期 → 403；文件不存在 → 404。

---

## HEAD `/files/presigned` - 获取文件元信息

行为同 GET `/files/presigned`，但仅返回状态码和响应头，不传输文件体。IM 平台在渲染图片预览前先发 HEAD 验证 Content-Type / Content-Length。

---

## GET `/files/presigned-preview` - 诊断预览签名 URL

Admin 专用诊断端点，返回当前租户下指定存储路径**将会生成**的签名 HTTP URL。可用于在不发送 IM 消息的情况下验证文件的可公开访问性。

**查询参数**:

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `file_path` | string | 是 | 文件路径 |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/files/presigned-preview?file_path=minio://weknora/1/img.png' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK）:

```json
{
    "file_path": "minio://weknora/1/img.png",
    "provider": "minio",
    "url": "http://localhost:9000/weknora/1/img.png?X-Amz-...",
    "rewritten": true,
    "hint": ""
}
```

若 URL 未改写（本地存储且 `APP_EXTERNAL_URL` 未设置）:

```json
{
    "file_path": "local://data/files/1/img.png",
    "provider": "local",
    "url": "local://data/files/1/img.png",
    "rewritten": false,
    "hint": "URL unchanged; for local storage set APP_EXTERNAL_URL to enable presigned HTTP URLs"
}
```

**错误**: 缺少 `file_path` → 400；非 Admin → 403。
