# API 参考：自动化与通知

自动化由内置 WeKnora 助手以创建者身份执行。以下接口需要已登录用户，空间 Viewer+ 可使用，只能访问本人的任务和通知；API Key 未获准访问这些路由。

## 任务管理 {#tasks}

| 方法与路径 | 用途 |
| --- | --- |
| `GET /api/v1/automations` | 列出本人的任务 |
| `POST /api/v1/automations` | 创建任务 |
| `POST /api/v1/automations/validate-trigger` | 校验计划并预览下次执行时间 |
| `GET /api/v1/automations/by-session/:session_id` | 查询会话关联任务 |
| `GET /api/v1/automations/:id` | 任务详情 |
| `PUT /api/v1/automations/:id` | 更新任务，使用 `AutomationPatch` 中出现的字段 |
| `DELETE /api/v1/automations/:id` | 删除任务 |
| `POST /api/v1/automations/:id/pause` | 暂停计划 |
| `POST /api/v1/automations/:id/resume` | 恢复计划 |
| `POST /api/v1/automations/:id/run` | 立即运行 |

创建请求的必填字段为 `name`、`instruction`、`trigger`。示例：

```json
{
  "name": "每日知识摘要",
  "instruction": "读取我有权访问的知识库，概括新增内容并给出来源，不修改资料。",
  "trigger": {"type": "cron", "expr": "0 9 * * *", "timezone": "Asia/Shanghai"},
  "session_mode": "per_run",
  "access_level": "read_only",
  "notify_on_success": true
}
```

| 字段 | 说明 |
| --- | --- |
| `trigger.type` | `cron` 或 `once` |
| `trigger.expr` | 五字段或六字段 Cron，也接受 `@daily` 等描述；最小间隔 5 分钟 |
| `trigger.at` | 一次性时间，RFC3339 或指定时区的本地时间；不存在的本地时间会被拒绝 |
| `trigger.timezone` | IANA 时区，省略为 UTC |
| `model_id` | 可选模型；空值使用助手默认模型 |
| `session_mode` | `per_run`（默认）或 `persistent` |
| `carry_last_result` | 为每次新会话带入上次成功结果，默认 false |
| `misfire_policy` | `skip`（默认）或 `run_once` |
| `overlap_policy` | `skip`（默认）或 `queue` |
| `access_level` | `read_only` / `confirm`（默认）/ `auto_write`，受助手权限上限约束 |
| `approval_timeout_minutes` | 默认 1440，范围 5–10080 |
| `limits.max_duration_minutes` | 默认 30，最多 240；不计审批等待时间 |
| `notify_on_success` | 默认 false；失败始终通知 |

任务说明支持模板变量 `now`、`last_run_at`、`last_success_at`、`run_id`、`automation_name`，写成双花括号形式。自动化由内置助手执行，协作需求通过任务说明表达。

## 运行记录与授权 {#runs}

| 方法与路径 | 用途 |
| --- | --- |
| `GET /api/v1/automations/:id/runs` | 运行记录 |
| `GET /api/v1/automation-runs/:id` | 一次运行详情 |
| `POST /api/v1/automation-runs/:id/cancel` | 取消该次运行 |
| `GET /api/v1/automations/:id/grants` | 任务的长期授权 |
| `GET /api/v1/automations/:id/grant-candidates` | 从已有审批中列出可授权候选 |
| `POST /api/v1/automations/:id/grants` | 以 `{"prompt_id":"..."}` 添加授权 |

运行状态包括 `queued`、`running`、`awaiting_approval`、`succeeded`、`failed`、`cancelled`、`skipped` 和 `needs_attention`。后者表示执行中断且结果不确定，不应自动重试可能已经生效的写操作。

统一授权查询与撤销接口为 `GET /api/v1/agent/grants` 和 `DELETE /api/v1/agent/grants/:id`；审批答复使用 `POST /api/v1/agent/prompts/:prompt_id`。请求结构与校验见 `internal/handler/mcp_service.go`、`agent_grant.go`。

## 站内通知 {#notifications}

| 方法与路径 | 用途 |
| --- | --- |
| `GET /api/v1/notifications` | 查询本人通知 |
| `POST /api/v1/notifications/read` | 标记已读，请求 `{"ids":["..."]}` |
| `DELETE /api/v1/notifications/read` | 清理已读通知 |

实现参考：`internal/router/routes_automation.go`、`internal/handler/automation.go`、`internal/types/automation.go`。使用方式见[自动化](../03-features/27-automations.md)。
