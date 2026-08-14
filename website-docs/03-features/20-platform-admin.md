# 平台管理与系统管理员

WeKnora 的权限分两层：**空间内**的四级角色（见[租户、用户与认证授权](01-tenant-auth.md)），以及**平台级**的系统管理员。这一篇讲后者——它管的不是某个知识库，而是整个部署。

先划清界限：

| | 空间 Owner | 系统管理员 |
| --- | --- | --- |
| 作用范围 | 单个工作空间 | 整个部署 |
| 怎么获得 | 注册即成为自己空间的 Owner，或被转让 | 由现有系统管理员提升，首个靠环境变量引导 |
| 管什么 | 空间成员、模型、知识库、集成、空间审计 | 全局系统设置、任务队列、平台 API Key、跨空间审计、重置用户密码 |
| 是否自动叠加 | — | **不会**：在某空间是 Owner 不代表是系统管理员，反之亦然 |

还有第三个标志 `CanAccessAllTenants`（跨空间访问），它管的是「能不能读写别人的空间数据」，与系统管理员也是分开的：系统管理员默认看不到别人空间里的知识库内容。

<Screenshot
  src="/screenshots/settings-system-admin.png"
  caption="平台控制台：系统设置、任务队列、平台 API Key 与系统审计日志"
  hint="以系统管理员身份打开「设置」，展示侧栏底部四个仅系统管理员可见的分区，正文可用系统设置页。" />

## 1. 第一个系统管理员怎么来

新部署里**没有任何系统管理员**。引导流程在 `cmd/server/bootstrap.go`：

1. 先用正常流程注册一个账号；
2. 给 app 服务设 `WEKNORA_BOOTSTRAP_SYSTEM_ADMIN_EMAIL=<该账号邮箱>`，重启；
3. 启动时 `bootstrapSystemAdmin()` 检查——**仅当当前部署一个系统管理员都没有时**，把该邮箱对应的用户提升为系统管理员。

几个刻意的设计：

- **不会创建用户**：邮箱还没注册时只打一条 WARN，下次重启再试。账号创建涉及密码哈希、空间分配、审计，不适合在启动钩子里走捷径；
- **幂等且会自动失效**：一旦存在系统管理员，这个变量就不再授权——避免管理员在界面上刚撤销的权限被下次重启悄悄恢复。所以它可以长期留在部署清单里；
- **失败不阻断启动**：整个 bootstrap 是 best-effort，配错了变量也能把服务拉起来再改。

之后新增/移除管理员在界面上操作即可（对应 `POST /system/admin/promote` / `revoke`）。撤销有两道保险：**不能撤销自己**，也**不能撤销最后一个系统管理员**——否则平台会永久失去系统级管理能力。对已经不是管理员的用户重复撤销返回 200（幂等），但审计记录里 `changed=false`，便于事后区分真实撤销与空操作。

## 2. 控制台能做什么

界面入口在「设置」侧栏底部，仅系统管理员可见（前端白名单见 `frontend/src/config/settingsAccess.ts` 的 `SYSTEM_ADMIN_SETTINGS_SECTIONS`），四个分区：

| 分区 | 作用 | 接口 |
| --- | --- | --- |
| 系统设置 | 全局运行时开关（注册模式、空间策略、并发、SSRF 白名单等），改完即时生效，见 §9.3 | `GET/PUT/DELETE /system/admin/settings[/:key]` |
| 任务队列 | 查看 asynq 各队列实时积压、逐个任务的重试/归档/删除、批量清空归档任务；Lite 模式返回 `available=false` | `/system/admin/runtime/queues*` |
| 平台 API Key | 面向控制面自动化的 platform 作用域 Key，能力包括 `system_tenants_read/manage`、`system_settings_read/manage`、`system_runtime_read/manage`、`system_audit_read` | `/system/admin/api-keys` |
| 系统审计日志 | `tenant_id = 0` 的平台级事件（改设置、提升/撤销管理员、队列操作等）。空间级审计接口按 tenant 过滤，看不到这些行 | `GET /system/admin/audit-log` |

另外两个不在上表、但同属系统管理员的能力：

- **重置用户密码**（`POST /system/admin/users/reset-password`）：替换目标用户的本地密码并吊销其全部会话。**不能给自己重置**——自助改密码仍要求提供旧密码；
- **批量套用默认存储配额**（`POST /system/admin/tenants/apply-default-storage-quota`）：把当前的默认配额写到所有已存在的空间上。之所以挂在 `/tenants` 而不是 `/settings` 下，是因为它改的是空间数据而不是设置行。

## 3. 运行时可改的系统设置

`internal/application/service/system_setting.go` 维护一张注册表，表内的键可以在控制台里改，**数据库值盖过环境变量**，绝大多数改完立即生效，不用重启：

| 键 | 类型 | 默认 | 生效时机 |
| --- | --- | --- | --- |
| `auth.registration_mode` | `self_serve` / `invite_only` | `self_serve` | 立即 |
| `auth.default_tenant_mode` | `create_personal` / `tenantless` | `create_personal` | 只影响之后注册的新用户 |
| `tenant.self_service_creation_enabled` | bool | `true` | 立即 |
| `tenant.max_owned_per_user` | int | `10`（0 = 用内置默认，负数 = 关闭限额） | 每次建空间时读取 |
| `tenant.default_storage_quota_gb` | int | `10` | **仅新建空间时读取**，不回写已有空间 |
| `tenant.auto_create_api_key` | bool | `false` | 每次建空间时读取 |
| `ssrf.whitelist` | 字符串列表 | 空 | 立即（`SSRF_WHITELIST_EXTRA` 仍只由部署方维护，不在此覆盖） |
| `asynq.core/postprocess/enrichment/maintenance/shared/wiki_concurrency` | int | 见[异步任务系统](../02-architecture/05-async-tasks.md) | 各 worker pool 重新装配 |
| `model.max_concurrency` | int | `32` | 立即 |

`tenant.auto_create_api_key` 是个兼容开关：老版本「建空间就自动下发一个 full-access Key 并在响应里返回明文」的行为属于破坏性变更，依赖它的集成可以打开这个开关退回旧行为，默认关闭。

::: warning 配置来源不只有环境变量
上表这些键一旦在控制台里改过，数据库里就有了一行记录，**之后改环境变量不再有效果**。排查「明明改了 env 却没生效」时先看这里；把设置项重置（`DELETE /system/admin/settings/:key`）会删掉 DB 行，重新回落到环境变量或内置默认值。
:::

## 相关

- 空间内的四级角色与 API Key：[租户、用户与认证授权](01-tenant-auth.md)
- 队列拓扑与 worker pool：[异步任务系统](../02-architecture/05-async-tasks.md)
- 审计日志与追踪：[可观测性与审计](16-observability.md)
- 接口清单：[API 参考：模型与系统](../04-api/02-api-model-system.md)
