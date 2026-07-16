# 空间 RBAC 说明

本文档介绍 WeKnora **空间内权限控制（Workspace RBAC）** 的设计、角色矩阵、资源归属模型、配置方式，以及它与 [共享空间](./共享空间说明.md) 之间的关系。

> 状态：已随 #1303 发布，由配置项 `tenant.enable_rbac` 控制，默认 `true`（强制鉴权）。可临时切到 `false` 进入「仅记录不拦截」的灰度窗口。

## 一、为什么需要 RBAC

在 RBAC 引入之前，只要通过 `X-API-Key` 或 JWT 认证成功，调用方在空间内基本等同于管理员。这在单人自部署场景没问题，但只要一个空间里出现两个及以上的真人成员（团队共享一套知识库），就必须区分：

- 谁可以删除知识库、撤销 API Key（管理员/Owner）；
- 谁可以上传文档、编辑「自己」的知识库（Contributor）；
- 谁只能读取与提问（Viewer）。

RBAC 在原有 JWT / API Key 认证之上，叠加了一层**空间内角色矩阵**，使三种状态都成为一等公民。

## 二、角色矩阵

每个空间成员（`tenant_members` 一行）拥有且仅拥有一个角色：

| 角色 | 标识 | 典型场景 | 关键能力 |
|------|------|----------|----------|
| 只读 | `viewer` | 只查阅、提问的成员 | 仅读，不可发起任何变更 |
| 贡献者 | `contributor` | 上传文档、维护自己的 KB / Agent | 可变更 `creator_id == 自己` 的资源；他人资源等同 Viewer |
| 管理员 | `admin` | 空间内运维 | 可变更空间内任意资源；管理成员；配置共享基础设施（模型、解析器、存储、向量库等） |
| Owner | `owner` | 空间创建者 | Admin 的全部权限 + 可删除空间；不会被其他 Admin 降级；每个空间**有且只有一位** |

角色按 `viewer < contributor < admin < owner` 递增，高角色继承低角色权限。

### 鉴权层的两个例外

- **跨空间超管**：`User.CanAccessAllTenants=true` 且 `enable_cross_tenant_access=true` 时，通过 `X-Tenant-ID` 切换到目标空间后等同 Admin，不需要在目标空间里有 `tenant_members` 行。用于多空间运营方。
- **API Key 调用**：`X-API-Key` 合成的虚拟用户在其所属空间内固定为 Admin（仅删除空间仍需 Owner）。脚本集成无需迁移。
- **孤儿空间自愈**：若一个空间在 `tenant_members` 表里没有任何活跃成员（典型场景：仅 API Key 使用过该空间），首位通过认证的真人会被自动晋升为 Owner，避免锁死。

## 三、资源归属模型

光有角色矩阵不够，否则 Contributor 之间可以互相破坏。为此在迁移 `000043` 中给关键表加了 `creator_id`：

- `knowledge_bases.creator_id` —— 老数据回填为该空间的 Owner；空串/NULL 表示「空间共有，仅 Admin+ 可变更」。
- `custom_agents.creator_id` —— Agent 创建者。
- `custom_agents.runnable_by_viewer` —— 默认 `true`，允许 Viewer 在对话中调用该 Agent；置 `false` 则提升到 Contributor 起步。

子资源沿着归属链回溯到 KB 的 `creator_id`：

```
chunk_id ─► knowledge_id ─► kb_id ─► knowledge_bases.creator_id
```

FAQ 条目、生成的问题、KB 标签、Wiki 页面同理。

由此衍生出两类守卫：

- **角色守卫**：`Viewer()` / `Contributor()` / `Admin()` / `Owner()` —— 只看角色。用于空间级基础设施（模型、向量库、IM 通道等）。
- **归属守卫**：`OwnedKBOrAdmin()` / `OwnedAgentOrAdmin()` / `OwnedChunkKBOrAdmin()` …… —— 「我是这条资源的 `creator_id`」**或**「我至少是 Admin」二者满足其一即可。用于具体资源的写操作。

这样可以让「Contributor 在自己的 KB 里像 Owner，在别人的 KB 里像 Viewer」自然成立。

## 四、与共享空间的关系（重点）

[共享空间](./共享空间说明.md)（Organization）和空间 RBAC 解决的是**不同维度**的问题，必须同时满足才能完成一次跨空间操作：

| 维度 | 解决什么 | 主键模型 | 角色集合 |
|------|---------|---------|---------|
| **空间 RBAC** | 同一空间内「你能对自己 / 别人 / 共享基础设施做什么」 | `tenant_members(user_id, tenant_id, role)` | viewer / contributor / admin / owner |
| **共享空间** | 跨空间「把我的 KB / Agent 让别的空间的人也用」 | `organization_members(user_id, org_id, role)` + 共享关系表 | 管理员 / 编辑者 / 只读 |

两者**正交**：

- 共享空间不持有任何 KB 或 Agent，它只是「某 KB 以某权限被共享到某空间」的关系记录。
- 资源始终归属一个空间，归属与 `creator_id` 都不会因为共享而改变。
- 一次对**他人共享给你**的 KB 的写操作，需要同时满足：
  1. **共享一侧**：该 KB 被以「可写」权限共享到了你和发起方都在的共享空间；
  2. **空间角色一侧**：你在该共享空间内不是「只读」（即至少是编辑者）；
  3. **空间 RBAC 一侧**：你的访问通过共享路径解析为对源空间的「以共享空间身份」访问，仍要经过源空间的 RBAC 检查。具体地，访问检查会在确认 `kb.tenant_id == 你的当前空间` 不成立后，回落到共享路径校验。

简化的判定顺序（见 `internal/middleware/kb_access.go`）：

```text
┌────────────────────────┐
│ KB 属于我当前空间？      │ ──是──► 进入空间 RBAC：
└──────┬─────────────────┘         角色 + creator_id 决定能否写
       否
       ▼
┌────────────────────────┐
│ KB 通过共享空间分享给    │ ──否──► 403 / 404
│ 我所在的某个空间？       │
└──────┬─────────────────┘
       是
       ▼
┌────────────────────────┐
│ 我在该空间是 viewer？    │ ──是──► 只读
│                        │ ──否──► 按共享时设定的「只读/可写」执行
└────────────────────────┘
```

要点整理：

- **共享空间不会绕过空间 RBAC**：若一个 KB 在源空间里被标记为「仅 Admin+ 可写」（例如 `creator_id` 为空的空间共有 KB），即使共享时给了「可写」权限，外空间成员也只能读取——因为没人能跨空间成为源空间的 Admin。
- **API Key 跨空间访问**：API Key 在所属空间内是 Admin，但**不会**因此自动获得对其他空间通过共享空间共享过来的 KB 的写权限——共享空间使用的是 `organization_members.role`，与 API Key 无关。
- **审计也是分开的**：空间内角色变更写入 `audit_logs`（`rbac.member_*` 动作），共享空间内的成员、共享关系变更由共享空间自身的接口记录。

一句话总结：**空间 RBAC 是「纵向」的纵深防御，共享空间是「横向」的协作通道；任何跨空间的有写副作用的操作，都要同时穿过这两道闸口。**

## 五、配置

`config/config.yaml`：

```yaml
tenant:
  # 默认 true，强制鉴权。改为 false 进入「仅记录不拦截」灰度窗口
  enable_rbac: true
  # 跨空间超管开关，默认 false
  enable_cross_tenant_access: false

auth:
  # self_serve（默认）：任何人都可注册，自动建空间 + Owner 成员
  # invite_only       ：禁止公开注册，新用户必须通过 /tenants/:id/members 邀请进入
  registration_mode: self_serve

audit:
  # 审计日志保留天数；每日后台清理；默认 90；置 0 关闭清理
  retention_days: 90
```

环境变量（优先级高于 YAML）：

| 环境变量 | YAML 路径 | 取值 |
|----------|-----------|------|
| `WEKNORA_TENANT_ENABLE_RBAC` | `tenant.enable_rbac` | `true` / `false` |
| `WEKNORA_AUDIT_RETENTION_DAYS` | `audit.retention_days` | 非负整数 |

`auth.registration_mode` 没有专属环境变量，沿用历史的 `DISABLE_REGISTRATION=true`——一旦设置，启动时会把 `auth.registration_mode` 强制改成 `invite_only`，保证后端 API 和 `/auth/config` 驱动的前端注册入口一致。

启动日志会打印一行总结，确认本次启动到底使用了哪一组配置以及覆盖来源。

## 六、审计日志

`audit_logs` 表统一记录权限相关事件：

| Action | Outcome | 触发时机 |
|--------|---------|----------|
| `rbac.member_added` | success | `POST /tenants/:id/members` 成功 |
| `rbac.member_removed` | success | `DELETE /tenants/:id/members/:user_id` 成功 |
| `rbac.member_role_changed` | success | `PUT /tenants/:id/members/:user_id` 成功 |
| `rbac.member_left` | success | `POST /tenants/:id/members/leave` 成功 |
| `rbac.access_denied` | denied | `RequireRole` / `RequireOwnershipOrRole` 拒绝时（**仅 enforcement 开启时**） |

`access_denied` 采用 1 分钟滑动窗口去重，防止恶意探测刷表；同样的拒绝在应用日志（`[rbac] role insufficient ...`）里仍然条条可见。

后台 goroutine `AuditLogRetentionRunner` 启动 ~10 分钟后开始首轮清理，之后每 24 小时清扫一次超过 `audit.retention_days` 的旧行；保留期为 `0` 时整条 goroutine 短路，不产生任何 DB 流量。

## 七、灰度上线建议

无论是自部署运维还是上游仓库本身，从「仅记录」切到「强制鉴权」都建议走以下流程：

1. **升级**：若想保留观察窗口，升级前先设置 `tenant.enable_rbac=false`（或环境变量）。否则默认就是强制鉴权——schema 落地、`tenant_members` 自动回填（每空间一个 Owner，其余 Contributor）、所有 KB 自动写入 `creator_id`。
2. **核对成员**：调用 `GET /api/v1/tenants/:id/members` 确认：
   - 每个空间都只有一位 Owner；
   - Contributor / Viewer 划分符合预期；
   - 通过 `PUT /api/v1/tenants/:id/members/:user_id` / `DELETE` 调整。每次调整都会写入 `audit_logs`。
3. **观察日志**：抓取应用日志里的 `[rbac] role insufficient (logged but not enforced) ...`，这些就是切换到强制鉴权后会变成 403 的请求。逐条修正成员角色或客户端身份。
4. **切回强制鉴权**：删除 `tenant.enable_rbac=false` 覆盖（或显式置 `true`），重启服务。此后：
   - 角色不足 → 403；
   - 同时写入 `audit_logs.rbac.access_denied`（受去重控制）。
5. **可选：禁用公开注册**：把 `auth.registration_mode` 改为 `invite_only`。登录页注册入口会自动消失，`POST /auth/register` 直接 403。

### 回滚

```bash
export WEKNORA_TENANT_ENABLE_RBAC=false
# 重启服务即可回到观察模式
```

`tenant_members` 行与 `creator_id` 列保留，下次再启用无需重做回填。除非彻底放弃这个功能，否则**不要**回滚 `000043` / `000044` 迁移——`down.sql` 会丢弃 `tenant_members` 和 `audit_logs` 整张表。

## 八、前端表现

Pinia 中的 `authStore` 暴露：

- `authStore.currentTenantRole`：成员信息加载完成前为 `''`（loading 信号，按钮等待解析后再渲染，避免「先亮再灰」的闪烁）；之后为四种角色之一。
- `authStore.hasRole('admin')` 等：按层级判断的便捷函数。
- 各资源页面再叠加 `isOwner`（如 `kb.creator_id === authStore.user?.id`）做 per-resource 判断。

这是后端守卫的镜像：**任何在后端会 403 的按钮，前端直接隐藏而不是让用户点了再吃错误。**

### 前端实际界面

<table>
  <tr>
    <td colspan="2" align="center">
      <b>成员管理页</b><br/>
      <img src="./images/rbac-member-management.png" alt="成员管理" width="100%"/>
      <br/><sub>同时展示「待接受的邀请」和「空间成员」两组列表；只有 Owner 可以新增 / 移除成员；右上角的「审计日志」入口跳转到 <code>audit_logs</code> 视图。</sub>
    </td>
  </tr>
  <tr>
    <td width="50%" align="center">
      <b>用户菜单 + 工作区切换器</b><br/>
      <img src="./images/rbac-workspace-switcher.png" alt="用户菜单 + 切换空间" width="100%"/>
      <br/><sub>左侧：当前空间角色徽章 / 设置入口 / 退出；右侧：切换到其它空间，「当前」角标标识活跃工作区。</sub>
    </td>
    <td width="50%" align="center">
      <b>自助创建工作区</b><br/>
      <img src="./images/rbac-create-workspace.png" alt="创建新空间" width="100%"/>
      <br/><sub>任何用户都可以自助创建空间，创建后自动成为新空间的 Owner（受 <code>WEKNORA_TENANT_MAX_PER_USER</code> 上限保护）。</sub>
    </td>
  </tr>
  <tr>
    <td colspan="2" align="center">
      <b>待处理邀请弹窗</b><br/>
      <img src="./images/rbac-pending-invitation.png" alt="我的邀请" width="80%"/>
      <br/><sub>用户菜单上的邀请铃铛会展示来自其它空间的待处理邀请，可直接「接受 / 拒绝」；7 天未响应自动过期。</sub>
    </td>
  </tr>
</table>

## 九、常见问题

### 升级后所有人都变成了 Contributor，找不到 Admin？

回填逻辑选「每个空间里最早活跃的用户」作为 Owner，其余人统一变成 Contributor。如果创建空间的账号其实是一个机器人 / 共用账号，可能需要先用 `PUT /api/v1/tenants/:id/members/:user_id` 把机器人降级、把真人 Admin 提升。

### 切到强制鉴权后某个脚本开始 403？

大概率脚本的 JWT 对应的成员是 Viewer / Contributor 而非 Admin。两种解法：
- 通过 `tenant_members` 把对应用户升级到 Admin；
- 或者把脚本切到 `X-API-Key` 调用 —— API Key 在所属空间内固定 Admin。

### 共享空间里的成员为什么读不到我「以可写共享」过去的 KB？

请按第四节的判定顺序排查：
- KB 是否真的属于源空间、`tenant_id` 配置是否正确；
- 该共享关系当前是否还存在（未被取消）；
- 调用方在共享空间里是不是 Viewer；
- 若 KB 的 `creator_id` 在源空间内为空且共享权限要求写，源空间的 RBAC 仍会要求 Admin+，导致跨空间写无法成立。

### 审计日志怎么有些 403 没记录？

两种可能：
- 1 分钟滑动窗口去重，同一 `(actor, path, action)` 一分钟内只会写一行。完整序列在应用日志里。
- `tenant.enable_rbac=false` 时仅记录成员管理事件，不写 `rbac.access_denied`。

### 我能不能做比「角色 + 归属」更细的 ACL？

v1 不支持。这个矩阵刻意保持成一个小固定格（Viewer < Contributor < Admin < Owner）+ 每种资源一个「creator escape hatch」。再细的策略（例如「Viewer 可以看自己的审计日志」）属于后续。

## 十、测试与可观测性

- `make test` 覆盖 `internal/middleware/rbac_test.go`、`internal/handler/rbac_lookups_test.go`、`internal/application/service/audit_log_test.go`、`internal/middleware/rbac_audit_test.go` 等约 25 个用例。
- Langfuse / OpenTelemetry span 上携带解析后的 `TenantRole` 和 `TenantID`，一次被拒绝的请求在 trace 中即可看到对应角色，不需要再去手工关联日志。

## 相关文档

- 跨空间协作：[`共享空间说明.md`](./共享空间说明.md)
- 多空间认证背景：[`OIDC认证调用流程.md`](./OIDC认证调用流程.md)
- 配置项与环境变量：[`.env.example`](../.env.example)
