# WeKnoraCloud API

[返回目录](./README.md)

WeKnoraCloud 接口用于管理 WeKnoraCloud SaaS 凭证。

## 说明

- 所有接口需要认证（`X-API-Key` 或 `Authorization: Bearer`）。

## 端点一览

| 方法 | 路径 | 描述 |
| ---- | ---- | ---- |
| POST | `/weknoracloud/credentials` | 保存 WeKnoraCloud 凭证 |
| GET | `/models/weknoracloud/status` | 检查 WeKnoraCloud 凭证状态 |

---

## POST `/weknoracloud/credentials` - 保存凭证

保存 WeKnoraCloud 的 APPID/APPSECRET 到当前租户配置，不会自动创建模型。

**参数说明（请求体）**:

| 字段 | 类型 | 必填 | 说明 |
| ---- | ---- | ---- | ---- |
| `app_id` | string | 是 | WeKnoraCloud 应用 ID |
| `app_secret` | string | 是 | WeKnoraCloud 应用密钥 |

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/weknoracloud/credentials' \
--header 'Content-Type: application/json' \
--header 'X-API-Key: sk-xxxxx' \
--data '{
    "app_id": "your_app_id",
    "app_secret": "your_app_secret"
}'
```

**响应**（200 OK）:

```json
{
    "success": true,
    "message": "凭证保存成功"
}
```

**错误**: 参数校验失败 → 400。

---

## GET `/models/weknoracloud/status` - 检查凭证状态

检查当前租户的 WeKnoraCloud 凭证是否完好。

**请求**:

```curl
curl --location 'http://localhost:8080/api/v1/models/weknoracloud/status' \
--header 'X-API-Key: sk-xxxxx'
```

**响应**（200 OK，凭证完好）:

```json
{
    "success": true,
    "needs_reinit": false
}
```

**响应**（凭证缺失或无效）:

```json
{
    "success": true,
    "needs_reinit": true,
    "message": "需要重新保存凭证"
}
```

**错误**: 服务内部错误 → 500。
