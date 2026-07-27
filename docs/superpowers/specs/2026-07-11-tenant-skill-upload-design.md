# 租户级 Skills 上传与管理设计

## 1. 背景与目标

WeKnora 当前只从 `skills/preloaded/` 读取全局只读 Skill，后端仅提供列表接口，管理端没有上传、更新、启停或删除能力。本功能增加租户级 Skill 上传与管理，使租户管理员可以上传包含 `SKILL.md`、静态资源和脚本的 ZIP 包，并在自动安全校验通过后立即启用。

目标：

- 租户 `owner/admin` 可上传、更新、启停和删除本租户 Skill。
- 租户成员可查看已启用 Skill，并在智能体中选择和使用。
- 不同租户的元数据、文件、查询和脚本执行环境严格隔离。
- 脚本仅在受限 Docker Sandbox 中运行。
- 内置 Skill 与租户 Skill 在统一列表中展示，但保持全局只读。
- 管理页面使用分类 Tab、卡片和批量勾选交互。

非目标：

- 第一版不接入 ClawHub、GitHub 或其他在线 Skill 商店。
- 第一版不允许 API Key 管理 Skill。
- 第一版不提供跨租户共享、购买、评分、评论或推荐算法。
- 第一版不允许 Skill 脚本访问公网或内部网络。

## 2. 权限模型

| 主体 | 查看内置 Skill | 查看本租户 Skill | 使用已启用 Skill | 上传/更新 | 启停/删除 | 跨租户执行 |
|---|---:|---:|---:|---:|---:|---:|
| Tenant owner/admin | 是 | 是 | 是 | 是 | 是 | 否 |
| Tenant contributor/viewer | 是 | 仅已启用 | 是 | 否 | 否 | 否 |
| System Admin | 是 | 可审计元数据 | 默认否 | 默认否 | 默认否 | 否 |
| Tenant API Key | 否 | 否 | 可由已授权 Agent 间接使用 | 否 | 否 | 否 |

`tenant_id` 和操作者 ID 必须从认证上下文获取，任何管理请求都不接受客户端指定 `tenant_id`。Repository 的读取、更新和删除方法必须以 `tenant_id + skill_id` 为查询条件；只凭 Skill ID 的查询不得进入租户业务路径。

## 3. 数据模型

新增 `tenant_skills` 表：

| 字段 | 类型/约束 | 说明 |
|---|---|---|
| `id` | UUID，主键 | 服务端生成，不使用名称作为路径 |
| `tenant_id` | bigint，非空，索引 | 数据隔离边界 |
| `name` | varchar(50)，非空 | 来自 `SKILL.md` frontmatter |
| `description` | varchar(500)，非空 | 来自 frontmatter |
| `category` | varchar(32)，非空 | 受控枚举，非法值归入 `other` |
| `status` | varchar(16)，非空 | `enabled` 或 `disabled` |
| `current_version_id` | UUID，可空，外键 | 当前版本；首次安装提交前为空 |
| `has_scripts` | boolean，非空 | 是否包含允许执行的脚本 |
| `uploaded_by` | user UUID，非空 | 上传者 |
| `created_at/updated_at` | timestamptz | 审计时间 |
| `deleted_at` | timestamptz，可空 | 软删除 |

新增 `tenant_skill_versions` 表，记录 `id`、`tenant_id`、`skill_id`、递增 `version`、`state`、`storage_path`、`content_hash`、`manifest_json`、`created_by`、`created_at` 和 `garbage_at`。`state` 只允许 `staging|ready|current|garbage`，同一 Skill 仅一个版本可为 `current`。

新增 `skill_execution_audits` 表，记录租户、Skill、版本、调用者、脚本相对路径、开始/结束时间、耗时、退出码、超时/资源限制状态和截断后的输出摘要。输入、环境变量和完整输出不落库。

同一租户内，未删除记录的 `name` 唯一。内置 Skill 不写入租户表，列表服务在响应层合并内置与租户 Skill，并返回 canonical reference：`{source, skill_id}`。内置 Skill 使用 `source=preloaded` 与稳定名称 ID；租户 Skill 使用 `source=tenant` 与 UUID。

## 4. 文件存储与版本切换

租户 Skill 根目录：

```text
/data/skills/<tenant_id>/<skill_id>/<version_id>/
```

目录使用不可变 `version_id`；递增 `version` 仅用于 UI 展示和并发更新条件，不参与文件路径解析。

上传先写入同租户临时目录：

```text
/data/skills/.staging/<tenant_id>/<upload_id>/
```

服务端完成流式解压、结构校验、frontmatter 解析和安全检查后，先创建 `staging` 版本记录，再将目录移动到正式版本路径并标记 `ready`。随后持有 Skill 行锁，在同一数据库事务中把旧 `current` 标记为 `garbage`、把新版本标记为 `current` 并更新 `current_version_id`。文件系统 rename 与数据库事务不宣称原子，以数据库状态为提交点。

启动恢复与定时 reconciliation 必须处理：

- staging 目录存在但没有版本记录：超过宽限期后删除。
- `staging` 记录长期未推进：验证目录后重试或标记 `garbage`。
- `ready` 版本未成为 current：确认无并发事务后转 `garbage`。
- current 记录对应目录缺失或哈希不匹配：禁用 Skill、阻止执行并产生安全审计。
- garbage 版本：仅在没有运行中执行租约且不再被迁移任务引用时清理。

并发更新使用 Skill 行锁和期望版本条件。后到请求收到 409；旧版本在新版本达到数据库提交点前继续可用。

删除先软删除数据库记录并立即从列表和 Agent 加载器隐藏，文件异步清理。清理器只接受数据库解析得到的租户根目录内路径，并在删除前再次验证路径边界。

## 5. ZIP 与 Skill 校验

默认限制：

- 上传 ZIP 最大 20 MB。
- 解压后总大小最大 100 MB。
- 文件数最大 500。
- 单文件最大 20 MB。
- 路径深度最大 12 层。
- 根目录必须包含 `SKILL.md`。
- 允许 ZIP 内多一层公共顶级目录，规范化后仍需得到唯一 Skill 根目录。

必须拒绝：

- `..` 路径穿越、绝对路径、空路径和重复规范化路径。
- 符号链接、硬链接、设备文件、FIFO 和 socket。
- ZIP Bomb、加密 ZIP、声明大小与实际输出不一致或超限；解压必须流式累计实际字节，不信任 header。
- Unicode 规范化或大小写折叠后重复的路径。
- `.git/`、`.env`、凭证文件和平台保留隐藏文件。
- `SKILL.md` 缺少合法 `name` 或 `description`。
- Skill 名称使用保留词或与本租户现有名称冲突（更新接口除外）。
- ZIP entry 的 Unix mode 不是普通文件或目录，或不在允许列表中的脚本扩展名。

允许脚本扩展名为当前 Sandbox 镜像真实支持的 `.py`、`.sh`、`.js`。Ruby、Go 不在第一版范围，后续增加时必须同时补镜像、解释器映射、资源基线与测试。写入 staging 使用最小权限和 no-follow 打开策略，逐级 `Lstat`；规范化路径必须通过 `filepath.Rel` 的组件边界检查，禁止依赖字符串前缀。打开文件后再次验证目标仍位于预期根目录，降低 TOCTOU 风险。

## 6. Sandbox Runner 与执行边界

第一版新增独立 `skill-runner` 控制面服务。App 不挂载 Docker Socket，只通过内部窄权限 API 提交已解析和授权的执行任务。Runner 是唯一允许连接容器运行时的组件；部署文档必须明确 Docker Socket 具有宿主机等价高权限，生产环境优先使用受限容器运行时代理或独立节点。Runner API 只监听内部网络，使用服务身份认证，拒绝外部入口和任意 Docker 参数。

Compose 新增持久卷 `tenant-skills`，由 App 写入版本内容、Runner 只读读取源内容。Runner 每次执行只把已授权的单个 Skill 版本复制到独立临时 volume，再以只读方式挂载给执行容器；执行容器不会看到包含其他租户内容的源 volume。Runner 自身不对外发布端口，并与 App 使用单独内部网络；共享服务凭证从 Secret 注入，不写入镜像或请求日志。

租户上传脚本必须强制走 Runner，且 `FallbackEnabled=false`。Runner、Docker 或 Sandbox 镜像不可用时明确拒绝执行，绝不回退现有 Local Sandbox。内置 Skill 是否允许旧路径由独立兼容开关控制，不能影响租户脚本硬门禁。

每次调用由 Runner 创建独立 Docker 容器：

- 当前 Skill 完整版本目录只读挂载到固定 `/skill`。
- 本次任务临时目录单独读写挂载到固定 `/work`，并实际设置工作目录。
- 不挂载项目目录、其他租户目录、用户 Home 或 Docker Socket。
- 禁止 privileged、host network、host PID/IPC 和新增 Linux capabilities。
- 默认禁用网络；第一版不提供网络白名单 UI。
- 设置 CPU、内存、PIDs、磁盘输出、执行时间和 stdout/stderr 上限。
- 容器使用非 root 用户、强制只读 root filesystem 和 `no-new-privileges`。
- 执行结束后销毁容器和任务临时目录。

Runner 请求只接受规范化的 Skill/版本标识、预注册脚本相对路径、参数和受限 stdin，不接受宿主路径、镜像名、挂载、网络或容器参数。Runner 从受信任存储映射解析目录并再次校验哈希和 no-follow 路径。

`ExecuteConfig` 需扩展并强制设置完整 Skill 根目录、独立 RW WorkDir、`ReadOnlyRootfs=true`、输出字节上限和执行租约。stdout/stderr 使用限长流式 writer，不使用无界 `bytes.Buffer`；超限后截断并继续回收进程。磁盘配额使用 tmpfs/运行时 storage option 与任务后大小核验共同约束。审计通过 App/Runner 的持久化接口写入 `skill_execution_audits`。

## 7. API 设计

```text
GET    /api/v1/skills
GET    /api/v1/skills/:id
POST   /api/v1/skills/upload
PUT    /api/v1/skills/:id/package
PATCH  /api/v1/skills/:id/status
PATCH  /api/v1/skills/status/batch
DELETE /api/v1/skills/:id
```

- `GET /skills`：Viewer+；普通成员只看到内置和本租户已启用 Skill，owner/admin 可看到本租户全部状态。
- `GET /skills/:id`：Viewer+；返回权限范围内的元数据和资源清单，不直接返回脚本文件内容。
- `POST /skills/upload`：Tenant Skill Manager；`multipart/form-data` 上传 ZIP，校验成功后立即启用。
- `PUT /skills/:id/package`：Tenant Skill Manager；上传新包并生成新版本，成功后立即切换并启用。
- `PATCH /skills/:id/status`：Tenant Skill Manager；只接受 `enabled|disabled`。
- `PATCH /skills/status/batch`：Tenant Skill Manager；批量启停并逐项返回成功/失败，不因部分失败回滚已完成项。
- `DELETE /skills/:id`：Tenant Skill Manager；软删除并触发异步文件清理。

所有写接口使用专用 `RequireTenantSkillManager` guard，而不是只挂现有 `g.Admin()`：必须是 JWT principal，拒绝所有 API Key；查询当前租户的 active membership 并要求真实角色为 owner/admin；不受 `ENABLE_RBAC` rollout 开关影响；默认拒绝 cross-tenant System Admin/superuser bypass。System Admin 审计使用独立只读平台接口。

内置 Skill 的 ID 使用 `preloaded:<name>`，租户 Skill 使用 UUID。越权与不存在统一返回 404 防枚举；当前租户可见但只读的内置 Skill 写请求返回 409。错误响应必须区分包格式错误、内容超限、名称冲突、Sandbox 不可用和内部存储失败。

## 8. 前端信息架构与交互

管理入口：`平台设置 → Skills 管理`。

页面结构：

- 顶部操作区：搜索框、状态筛选、上传按钮。
- 分类 Tab：全部、内容处理、数据分析、开发工具、业务流程、其他。
- Skill 卡片：名称、说明、分类、版本、来源、启用状态、脚本标识、上传者和更新时间。
- owner/admin：可勾选租户 Skill，通过批量接口启用/停用，并逐项展示部分成功、失败原因和重试入口；单卡可更新、启停、删除。
- contributor/viewer：只读，无上传和管理控件。
- 内置 Skill：显示“内置/只读”标识，不允许被管理操作选中。

上传交互：选择 ZIP 后展示文件名、大小、校验进度和结果。校验失败使用分项错误列表，不以单一 Toast 隐藏细节；名称冲突时引导用户选择“更新现有 Skill”，不隐式覆盖。

Agent 编辑页使用同一分类 Tab 和卡片式勾选器，只展示当前租户可用且已启用的 Skill。保存 Agent 时写入 `selected_skill_refs: [{source, skill_id}]`，不再以名称作为租户 Skill 身份。旧 `selected_skills: string[]` 仅作为内置 Skill 名称兼容读取；保存旧 Agent 时迁移为 `source=preloaded` 的引用，无法解析的名称显示失效提示而不自动绑定租户 Skill。

## 9. 分类规则

受控分类枚举：

- `content`：内容处理
- `data`：数据分析
- `development`：开发工具
- `workflow`：业务流程
- `other`：其他

`SKILL.md` frontmatter parser、内部 metadata、DTO 和前端类型新增可选 `category`。缺失或非法值归入 `other`，同时在上传结果中给出非阻塞提示。旧内置 Skill 无需迁移文件。第一版不支持用户自定义分类或多分类，避免 Tab 数量失控。

## 10. Agent 加载与缓存一致性

Skill Loader 增加租户来源，由认证后的 Agent/chat 执行上下文提供 `tenant_id`。加载顺序为内置 Skill 与当前租户已启用 Skill，名称冲突时拒绝租户上传，不允许覆盖内置 Skill。缓存键至少包含 `tenant_id/source/skill_id/version`，不能只使用名称。

每次读取指令、资源或执行脚本前，都以 `tenant_id + skill_id` 查询当前记录，核对 enabled、current version、哈希和存储根目录；客户端路径不能参与根目录解析。上传、更新、启停和删除成功后发布租户级缓存失效事件。即使缓存事件丢失，执行前的数据库检查仍阻止已停用、删除或换版后的旧引用继续运行。

## 11. 错误处理与可恢复性

- 上传中断：清理 staging；数据库不产生记录。
- 校验失败：返回稳定错误码、文件路径和原因；不保存包内容。
- 更新失败：旧版本继续服务。
- Sandbox Runner 不可用：Skill 元数据、上传和更新仍可使用；脚本调用明确失败，不降级到宿主机执行。API 分别返回 `tenant_upload_available` 与 `script_execution_available`，避免把管理能力和执行能力耦合。
- 数据库成功、异步清理失败：版本保持 `garbage` 并由 reconciliation 重试，不阻塞删除响应。
- 存储目录缺失或哈希不匹配：停止执行并记录安全审计事件。
- 并发更新：使用版本条件或行锁，后到请求收到 409，不静默覆盖。

## 12. 测试与验收

后端：

- Repository 对所有 CRUD 覆盖跨租户不可见、版本状态机与软删除。
- owner/admin/viewer/contributor/System Admin/API Key 权限矩阵测试，包含 `RBAC=false`、cross-tenant superuser 和所有 API Key scope。
- ZIP 路径穿越、前缀碰撞、Unicode/case-fold 重复、绝对路径、符号链接、加密 ZIP、ZIP Bomb、header 欺骗、大小/数量/深度超限测试。
- 上传、更新、提交点前后故障注入、启动恢复、reconciliation、失败回滚和并发冲突测试。
- 内置与租户 Skill 合并列表、名称冲突和缓存失效测试。
- Runner 服务认证与外部不可达测试；Sandbox 的完整 Skill RO 挂载、独立 RW 目录、无网络、只读 rootfs、资源/磁盘/输出限制、超时、fork bomb 和跨租户文件不可见测试。
- Docker/Runner 不可用时租户脚本必须失败，且绝不执行 Local Sandbox。
- Agent 保存后 Skill 被删除、停用、换版、缓存失效事件丢失或文件哈希被篡改时的运行检查。

前端：

- 分类 Tab、搜索、状态筛选和卡片勾选测试。
- 不同角色下按钮和批量操作可见性测试。
- 上传进度、校验错误、冲突、更新和删除确认测试。
- Agent 编辑页只展示已启用且当前租户可用 Skill。

端到端：

1. 租户 A 的 admin 上传含脚本 Skill，校验后立即启用。
2. 租户 A 的成员在 Agent 中选择并通过 Sandbox 成功执行。
3. 租户 B 无法列表、读取、选择或执行该 Skill。
4. 租户 A 停用后，新调用立即被拒绝；已有 Agent 配置不能绕过状态检查。
5. 更新失败或进程在文件移动/数据库提交之间崩溃时，reconciliation 恢复一致状态且旧版本继续可用；成功更新后新调用使用新版本。

验收完成条件：上述自动化测试通过；核心 Compose 服务健康；现有内置 Skill、Agent、聊天和 RBAC 回归通过；文档列出新增持久卷、迁移、环境变量和运维清理策略。
