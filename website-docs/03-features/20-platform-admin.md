# 平台管理与系统管理员

系统管理员负责整个 WeKnora 部署的全局设置、任务队列、平台 API Key 和跨空间审计。空间 Owner 负责单个工作空间，两类身份分别授予。空间角色说明见[租户、用户与认证授权](01-tenant-auth.md)。

两类身份的管理范围如下：

| | 空间 Owner | 系统管理员 |
| --- | --- | --- |
| 作用范围 | 单个工作空间 | 整个部署 |
| 授予方式 | 创建空间时获得，或由原 Owner 转让 | 由现有系统管理员授予，首个管理员通过环境变量引导 |
| 管理内容 | 空间成员、模型、知识库、集成、空间审计 | 全局系统设置、任务队列、平台 API Key、跨空间审计、重置用户密码 |
| 是否自动叠加 | — | **不会**：在某空间是 Owner 不代表是系统管理员，反之亦然 |

跨空间数据访问由 `CanAccessAllTenants` 单独控制。系统管理员身份不会自动授予其他空间的知识库内容访问权。

<Screenshot
  src="/screenshots/settings-system-admin.png"
  caption="平台控制台：系统设置、任务队列、平台 API Key 与系统审计日志"
  hint="以系统管理员身份打开「设置」，展示侧栏的系统设置、模型目录、任务队列、平台 API Key、系统审计日志五个仅系统管理员可见的分区，正文可用系统设置页。" />

## 设置首个系统管理员 {#_1-第一个系统管理员怎么来}

新部署需要先设置首个系统管理员：

1. 先用正常流程注册一个账号；
2. 为 app 服务配置 `WEKNORA_BOOTSTRAP_SYSTEM_ADMIN_EMAIL=<该账号邮箱>`，重启；
3. 启动时仅在部署尚无系统管理员的情况下，将该邮箱对应的用户设为系统管理员。

引导流程只提升已存在的账号。邮箱尚未注册时会记录告警，后续重启再次检查；配置错误不阻断服务启动。部署已有系统管理员后，该变量不再授予权限。

后续可在界面中添加或撤销系统管理员。不能撤销自己，也不能撤销最后一位系统管理员。对应接口为 `POST /system/admin/promote` 和 `/revoke`；重复撤销非管理员返回 200，审计记录以 `changed=false` 标明未发生权限变更。

## 使用平台控制台 {#_2-控制台能做什么}

系统管理员可在「设置」侧栏查看以下管理分区：

| 分区 | 作用 | 接口 |
| --- | --- | --- |
| 系统设置 | 全局运行时开关（注册模式、空间策略、并发、SSRF 白名单等），按设置项的生效规则应用，见下方设置表 | `GET/PUT/DELETE /system/admin/settings[/:key]` |
| 模型目录 | 查看目录模型及其来源，编辑、添加模型或用 JSON 批量修改（保存即生效），按版本历史恢复；详见[模型目录管理](06-models.md#系统管理员维护模型目录) | `/system/admin/model-catalog*` |
| 任务队列 | 查看队列积压、逐个任务操作与归档；按资料查看入库/Wiki 进度及失败历史，单份或限量批量启动已有 Wiki 待办。Lite 模式仍可读取资料进度 | `/system/admin/runtime/queues*`、`/system/admin/runtime/documents*` |
| 平台 API Key | 面向控制面自动化的 platform 作用域 Key，能力包括 `system_tenants_read/manage`、`system_settings_read/manage`、`system_runtime_read/manage`、`system_audit_read` | `/system/admin/api-keys` |
| 系统审计日志 | `tenant_id = 0` 的平台级事件（改设置、提升/撤销管理员、队列操作等）。空间级审计接口按 tenant 过滤，看不到这些行 | `GET /system/admin/audit-log` |

系统管理员还可执行以下操作：

- **重置用户密码**（`POST /system/admin/users/reset-password`）：替换目标用户的本地密码并吊销其全部会话。**不能给自己重置**——自助改密码仍要求提供旧密码；
- **批量套用默认存储配额**（`POST /system/admin/tenants/apply-default-storage-quota`）：把当前的默认配额写到所有已存在的空间上。该操作更新已有空间的配额数据。

### 按资料查看进度与启动 Wiki

「任务队列」中的资料进度视图支持搜索资料标题、文件名和知识库名，并按未完成、失败或已完成筛选。入库状态与 Wiki 状态分别展示；详情仅显示当前处理轮次的阶段元数据，以及最近 50 条原始失败记录（每条错误预览最多 2048 字符）。恢复成功后历史记录仍保留，历史失败数量不代表当前状态。资料正文、任务载荷和阶段输入输出不在此视图中返回。

存在可领取 Wiki 待办、已完成主要入库阶段且已启用 Wiki 的资料，可点击「立即整理」。批量启动接受 1–100 份，沿当前搜索范围优先安排等待最久的资料；已被 Worker 领取的资料会跳过。重复请求使用稳定任务 ID 去重。返回的数量表示队列已接收，Wiki 完成进度需继续观察。

启动沿用现有 Worker、知识库并发和模型限制；原待办内容、重试次数和失败归档保留。没有可启动待办的失败资料仍需通过现有资料重试流程处理。批量数量限制本次安排的资料数，自动 Worker 会继续处理其他积压。队列部分不可用时，提示会包含已接收数量，可刷新后再次操作。

该视图属于系统管理员的运行元数据权限；平台 API Key 分别要求 `system_runtime_read` 和 `system_runtime_manage`。资料进度直接读取数据库，在 Lite 模式及 Redis Inspector 不可用时仍可查看。

### 管理平台用户

「设置 → 系统设置 → 用户管理」集中展示整个部署的用户，可创建账号、启用或停用账号、重置密码，以及授予或撤销系统管理员身份。停用账号会立即吊销其登录会话；不能停用自己或最后一位仍启用的系统管理员。用户列表不包含密码或密码哈希。

<Screenshot src="/screenshots/system-user-management.png" caption="系统设置：平台用户管理" hint="展示用户列表、启用状态、管理员标记与操作菜单；隐藏真实邮箱，保留创建用户和分页入口。" />

注册模式分为开放注册、仅限邀请注册、禁止注册三档；`invite_only` 对应禁止注册，需凭邀请新建账号时选择 `invite_register`。OIDC 与管理员创建账号不受密码注册档位限制。

### 创建用户

自 v0.8.2 起，系统管理员可在「设置 → 系统设置 → 用户管理」中点击「创建用户」开通本地账号：填写用户名（2–50 字符）和邮箱，保持「自动生成随机密码」开启，或关闭后手动填写符合密码策略的密码。适用于关闭公开注册后由管理员统一开户的场景。空间分配遵循 `auth.default_tenant_mode`：`create_personal` 创建个人空间，`tenantless` 等待用户加入空间。

<Screenshot
  src="/screenshots/system-admin-create-user.png"
  caption="用户创建成功：复制账号信息并保存一次性密码"
  hint="「设置 → 系统设置 → 用户管理」下打开「创建用户」对话框，展示用户名、邮箱与「自动生成随机密码」开关；可另附创建成功后显示一次性密码和「复制账号信息」按钮的结果页。" />

自动生成的密码仅在本次创建结果显示，应当场复制完整账号信息；确认前对话框不能关闭，关闭后不能再次查询明文。每次创建都会写入系统审计日志（`system.user_created`）。重复身份返回已有用户，不改密码；邮箱和用户名分别指向不同用户时拒绝创建。接口见[系统 API](../04-api/02-api-system.md)。

创建用户与首个管理员引导是不同操作：bootstrap 仍只提升已存在用户，不负责创建账号。

## 运行时设置参考 {#_3-运行时可改的系统设置}

支持的运行时设置可在控制台中修改。数据库中的设置值优先于环境变量，多数立即生效；影响新资源或需重建运行时的设置以表中说明为准。

| 键 | 类型 | 默认 | 生效时机 |
| --- | --- | --- | --- |
| `auth.registration_mode` | `self_serve` / `invite_register` / `invite_only` | `self_serve` | 立即 |
| `auth.default_tenant_mode` | `create_personal` / `tenantless` | `create_personal` | 只影响之后注册的新用户 |
| `tenant.self_service_creation_enabled` | bool | `true` | 立即 |
| `tenant.max_owned_per_user` | int | `10`（0 = 用内置默认，负数 = 关闭限额） | 每次建空间时读取 |
| `tenant.default_storage_quota_gb` | int | `10` | MB 设置未启用时的新空间默认配额（GB） |
| `tenant.default_storage_quota_mb` | int | `0` | 正数优先于 GB；0 或负数沿用 GB。包含注册自动创建的个人空间 |
| `tenant.auto_create_api_key` | bool | `false` | 每次建空间时读取 |
| `ssrf.whitelist` | 字符串列表 | 空 | 立即（`SSRF_WHITELIST_EXTRA` 仍只由部署方维护，不在此覆盖） |
| `asynq.core/postprocess/enrichment/maintenance/shared/wiki_concurrency` | int | 见[异步任务系统](../02-architecture/05-async-tasks.md) | 各 worker pool 重新装配 |
| `model.max_concurrency` | int | `32` | 立即 |

以下开关同样遵循数据库优先规则：

| 键 | 默认 | 作用 |
| --- | --- | --- |
| `auth.complex_password_enabled` | false | 新注册/新密码要求大小写字母、数字和特殊字符 |
| `tenant.auto_accept_invitation` | false | 邮箱邀请已注册用户时直接写入成员关系 |
| `sandbox.docker_enabled` | false | 开放 Docker 沙箱配置和实例创建 |

`tenant.auto_create_api_key` 默认关闭。依赖旧版注册响应的集成可启用此兼容开关，使新空间自动创建 full-access Key 并返回明文。

::: tip 重置运行时设置
控制台保存设置后，修改对应环境变量不会覆盖数据库值。排查配置未生效时，先检查运行时设置；重置（`DELETE /system/admin/settings/:key`）会删除数据库覆盖值，恢复使用环境变量或内置默认值。
:::

## 相关文档 {#相关}

- 空间内的四级角色与 API Key：[租户、用户与认证授权](01-tenant-auth.md)
- 队列拓扑与 worker pool：[异步任务系统](../02-architecture/05-async-tasks.md)
- 审计日志与追踪：[可观测性与审计](16-observability.md)
- 接口清单：[系统与平台管理 API](../04-api/02-api-system.md)

## 实现参考

- `cmd/server/bootstrap.go`：首个系统管理员引导。
- `frontend/src/config/settingsAccess.ts`：平台设置分区。
- `internal/application/service/system_setting.go`：运行时设置注册表。

### 默认空间存储配额

手动创建空间、普通注册及 OIDC 注册自动创建个人空间，均在创建服务中写入相同的默认配额。
例如设置 `WEKNORA_TENANT_DEFAULT_STORAGE_QUOTA_MB=50`，新空间获得 50 × 1024² 字节。
MB 设置为正数时优先于 GB 设置；MB 为 0 或负数时沿用原有 GB 设置，GB 为非正数时回退到 10 GB。
每个设置分别遵循 **数据库覆盖值 > 环境变量 > 内置默认值**，因此保存 MB 为 0 可覆盖环境变量并恢复 GB 配置。
超出 int64 字节范围的 MB 值回退到 GB；GB 溢出时回退到 10 GB，不会产生无限配额。

默认值只影响之后新建的空间。显式指定的正数 `storage_quota`（字节）继续生效；已有空间不会自动改写。
如需更新存量空间，管理员可使用“应用到所有现有空间”，确认框展示当前实际生效的 MB/GB 配额。
该操作会覆盖所有现有空间的配额，包括单独调整过的值。

无需修改数据库列默认值。服务写入显式字节值，可避免手工 `ALTER TABLE ... SET DEFAULT` 在升级时被 GORM
模型中的默认值重置；原有列默认值保留以兼容直接 SQL 等未经过服务的写入。
