# 长期记忆 API

[返回目录](./README.md)

长期记忆空间始终绑定在**当前调用者**上：路径里没有 subject id，服务端从凭证推导身份。因此 Viewer+ 的会话令牌，或 **full-access API Key** 才能访问；带知识库范围的集成 Key 不能继承某个人的记忆。

工作空间管理员先在设置里打开空间级开关，用户还可以再关掉自己的记忆。

记忆分三层存放，三组接口各管一层：

| 层 | 内容 | 写入方式 |
| ---- | ---- | ---- |
| 画像（`MemoryDigest`） | 每人一份文档，带 `## ` 小标题，每轮对话都注入 | 后台从会话记忆整体改写，也可由用户直接编辑 |
| 会话记忆（`MemoryEpisode`） | 每段对话一份完整叙述 | 对话安静一段时间后由模型写成 |
| 原话笔记（`MemoryNote`） | 用户明确要求记住的句子，按原文保存 | 对话里说「记住……」即写入，也可手工添加 |

| 方法 | 路径 | 描述 |
| ---- | ---- | ---- |
| GET | `/memory/settings` | 获取合并后的记忆开关（空间级 + 个人级）与会话记忆条数 |
| PUT | `/memory/settings` | 开启或关闭当前用户自己的长期记忆 |
| GET | `/memory/profile` | 读取常驻画像，尚未生成时返回 `null` |
| PUT | `/memory/profile` | 用用户自己写的正文替换画像 |
| DELETE | `/memory/profile` | 清空画像，会话记忆保留 |
| GET | `/memory/episodes` | 分页列出会话记忆，按时间倒序 |
| GET | `/memory/episodes/{id}` | 读取一段会话记忆的完整叙述 |
| DELETE | `/memory/episodes/{id}` | 永久删除一段会话记忆 |
| GET | `/memory/notes` | 列出用户要求记住的原话 |
| POST | `/memory/notes` | 新增一条原话笔记 |
| DELETE | `/memory/notes/{id}` | 永久删除一条原话笔记 |
| DELETE | `/memory/all` | 清空当前用户的三层记忆 |
| GET | `/memory/export` | 以 JSON 导出全部记忆 |
| POST | `/memory/consolidate` | 立刻用最近的会话记忆重写一次画像 |

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
    "episode_count": 12,
    "max_episodes": 200
  }
}
```

`write_mode` 为 `explicit_only`（只记用户明确要求记住的）或 `auto`（后台从对话蒸馏）。`effective` = 空间开关 ∧ 个人开关。`max_episodes` 是每人保留多少段会话记忆，默认 200，上限 500。

## PUT `/memory/settings`

```curl
curl --location --request PUT 'http://localhost:8080/api/v1/memory/settings' \
--header 'Authorization: Bearer <token>' \
--header 'Content-Type: application/json' \
--data '{"enabled": true}'
```

`enabled` 必填。只改个人开关，不能用这个接口改空间级配置。

## GET `/memory/profile`

返回 `{success, data}`，`data` 为 `MemoryDigest`；有一段会话记忆就会生成第一份画像，在那之前为 `null`。

字段：`body`（带 `## ` 小标题的文档）、`revision`、`episode_count`（由多少段会话记忆改写而来）、`user_edited_at`、`generated_at` 和创建/修改时间。

## PUT `/memory/profile`

```json
{
  "body": "## 用户画像\n- 在做医学影像后端\n\n## 用户偏好\n- 回答先给结论\n"
}
```

返回 `{success, data: {revision}}`。`body` 最长 2400 个字符，为空或超长返回 400。这条路径会记录 `user_edited_at`，表示文档里有人工措辞；一次自动改写替换的是整份文档，所以手写的段落会被下一次改写整体替换。

## DELETE `/memory/profile`

清空画像并返回 `{"success": true}`。会话记忆保留，下次整理会重新生成一份画像。

## GET `/memory/episodes`

查询参数：

| 参数 | 说明 |
| ---- | ---- |
| `limit` | 默认 20，合法范围 1–100，越界回落 20 |
| `offset` | 默认 0，负值归零 |

返回 `{success, data, total}`，`data` 为 `MemoryEpisode` 数组。字段：`id`、`session_id`、`slug`、`title`、`outcome`、`summary`、`keywords`、`from_at`、`to_at`、`use_count`、`last_used_at` 和创建/修改时间。

`outcome` 取值 `success` / `partial` / `fail` / `uncertain`，是从对话原文判断这段对话到底做成了什么；模型没有说清时落到 `uncertain`，而不是 `success`。

一段对话只有一份会话记忆：对话继续后就地改写，`slug` 保持不变，而不是追加一份新的。`use_count` 是全系统唯一的保留信号，所以它也决定超出 `max_episodes` 时谁先被裁掉；画像还没读过的会话记忆不参与裁剪，那是这段对话的唯一副本。

## DELETE `/memory/episodes/{id}`

返回 `{"success": true}`。已经引用这段会话记忆的画像不会立即重写，那需要一次模型调用；下次整理会自然修正。找不到当前身份下的记录返回 404，别人的记录与不存在的记录返回同一个 404。

## GET `/memory/notes`

`limit` 默认且上限均为 20。返回 `{success, data}`，`data` 为 `MemoryNote` 数组，按时间倒序。字段：`id`、`content`、`source_session_id`、`source_message_id` 和创建/修改时间。

## POST `/memory/notes`

```json
{
  "content": "回答先给结论，再解释依据"
}
```

`content` 最长 300 个字符，为空或超长返回 400。每人最多 20 条，写满后继续新增会报错而不是静默丢弃最旧的一条；重复提交同一句话返回已存在的那条笔记，不新增。笔记按原文注入，模型不会改写，下一轮对话即生效。聊天里说出「记住……」这类指令会同步写成笔记，不必调这个接口。

## 自动提取

一段对话安静 12 分钟后写成会话记忆；调用者转去别的会话后，尚无会话记忆的那段不再等窗口，约 90 秒内写。一直在进行的对话不会无限期推迟，距上一份会话记忆满 2 小时会先按已有内容写一份，之后安静下来再改写。只要用户说过话就会写，包括只有一轮的对话。画像在写下会话记忆的同一次后台运行里紧接着改写（一次运行只改写一次），没有新的会话记忆时不改写、也不产生模型调用。

自动提取在独立的 `memory_extraction_sessions` 表按会话保存 `(created_at, message_id)` 游标、排队状态和重试计数，每次最多领取 32 个会话。用户记录只保存工作进程租约，读取记忆时不加载全部会话历史。已完成的游标仍保留，避免旧会话再次提取全部历史。

无法解析或持续截断的模型输出按消息段最多尝试三轮（每轮截断后可增加预算重试一次），随后记录失败范围并推进该段游标；同会话后续消息及其他会话继续处理。进度表保留该会话最近一次失败的起止游标、错误类别、次数和跳过时间，不保存原始模型输出；被跳过的范围不会自动补提取。网络、模型配置和数据库错误不适用跳过策略，仍保留进度供重试。批次截断、工作进程重启和提取期间的新消息都保留待处理状态。

升级时沿用旧的用户级游标作为各会话的初始边界，之后该边界不再推进；旧待处理列表在首次调度时导入独立表。这样避免升级触发全量历史重放，但不会自动恢复旧实现已跳过、且位于旧游标之前的消息。

## DELETE `/memory/all`

清空当前身份的画像、全部会话记忆与原话笔记，返回 `{success, removed}`，`removed` 是删掉的行数。

## GET `/memory/export`

返回 `{success, total, truncated, data}`，`data` 为 `{profile, episodes, notes}`，并带 `Content-Disposition: attachment; filename="weknora-memories.json"`。导出是快照而非一页，会一直翻到末尾；`truncated` 仅在触达导出上限（2 万段会话记忆）时为 true。

## POST `/memory/consolidate`

不等待后台整理，立刻用最近的会话记忆重写一次画像。返回 `{merged, demoted, expired, reviewed, candidates, skipped?}`，其中只有 `reviewed`（这次改写读了多少段会话记忆）和 `skipped` 会被填充，`merged` / `demoted` / `expired` 恒为 0。

`skipped` 取值：`too_soon`（刚刚整理过，或另一次改写正在进行）、`too_few_items`（会话记忆还不够写出画像）、`model_unavailable`（模型暂时不可达，原画像保持不变）。为空表示画像已重写。

---

参数无效返回 400；找不到当前身份的记录返回 404；认证/权限不满足返回 401/403。接口没有管理员读取他人记忆的 subject 参数。实现见 `internal/handler/memory.go` 与 `internal/router/routes_memory.go`。
