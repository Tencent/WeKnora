# 长期记忆 API

[返回目录](./README.md)

长期记忆空间始终绑定在**当前调用者**上：路径里没有 subject id，服务端从凭证推导身份。因此 Viewer+ 的会话令牌，或 **full-access API Key** 才能访问；带知识库范围的集成 Key 不能继承某个人的记忆。

工作空间管理员先在设置里打开空间级开关，用户还可以再关掉自己的记忆。推断出的条目会停在 `pending`，确认后才进入提示词。

| 方法 | 路径 | 描述 |
| ---- | ---- | ---- |
| GET | `/memory/settings` | 获取合并后的记忆开关（空间级 + 个人级）与条数 |
| PUT | `/memory/settings` | 开启或关闭当前用户自己的长期记忆 |
| GET | `/memory/items` | 分页列出记忆，可按状态过滤 |
| POST | `/memory/items` | 手动新增一条记忆 |
| PUT | `/memory/items/{id}` | 修改内容与重要度（之后不会被后台抽取覆盖） |
| DELETE | `/memory/items/{id}` | 永久删除一条记忆 |
| POST | `/memory/items/{id}/confirm` | 确认一条推断出的记忆 |
| POST | `/memory/items/{id}/reject` | 否决一条推断出的记忆 |
| DELETE | `/memory/items` | 清空当前用户的全部记忆 |
| GET | `/memory/topics` | 列出尚未提升为长期关注的主题计数 |
| POST | `/memory/topics/{id}/promote` | 立即把主题记为长期关注 |
| DELETE | `/memory/topics/{id}` | 停止跟踪一个主题 |
| GET | `/memory/documents` | 列出反复引用的文档（未达习惯门槛的不展示） |
| DELETE | `/memory/documents/{id}` | 停止用某份文档做个性化检索 |
| GET | `/memory/export` | 以 JSON 导出全部记忆 |
| POST | `/memory/consolidate` | 立刻整理（合并近义条目、归档到期事项） |

## GET `/memory/settings`

```curl
curl --location 'http://localhost:8080/api/v1/memory/settings' \
--header 'Authorization: Bearer <token>'
```

**响应**:

```json
{
  "success": true,
  "data": {
    "workspace_enabled": true,
    "user_enabled": true,
    "effective": true,
    "write_mode": "auto",
    "item_count": 12,
    "max_items": 200
  }
}
```

`write_mode` 为 `explicit_only`（只记用户明确要求记住的）或 `auto`（后台从对话蒸馏）。`effective` = 空间开关 ∧ 个人开关。

## PUT `/memory/settings`

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/memory/settings' \
--header 'Authorization: Bearer <token>' \
--header 'Content-Type: application/json' \
--data '{"enabled": true}'
```

`enabled` 必填。只改个人开关，不能用这个接口改空间级配置。

## GET `/memory/items`

查询参数：

| 参数 | 说明 |
| ---- | ---- |
| `status` | 可选：`active` / `superseded` / `archived` / `pending`；省略则不过滤 |
| `limit` | 默认 50，最大 200 |
| `offset` | 默认 0 |

`kind` 取值：`profile` / `preference` / `fact` / `task` / `interest`。

## POST `/memory/items`

```json
{
  "kind": "preference",
  "content": "回答直接给结论，少铺垫",
  "importance": 3
}
```

## POST `/memory/items/{id}/confirm` / `reject`

推断出的记忆（`status=pending`）确认后才注入提示词；否决会留下 tombstone，避免下一轮蒸馏把同一句话再写回来。

## GET `/memory/export`

返回 `{success, total, truncated, data}`，并带 `Content-Disposition: attachment; filename="weknora-memories.json"`。`truncated` 仅在触达导出上限（2 万条）时为 true。

## POST `/memory/consolidate`

不等待每日后台整理，立刻合并意思接近的条目并归档到期事项。返回 `merged` / `demoted` / `expired` / `reviewed` / `candidates`；若什么都没合并，`skipped` 会说明原因（例如 `too_few_items`）。
