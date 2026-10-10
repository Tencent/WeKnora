# 用户管理

「设置 → 系统管理 → 用户管理」把分散在多个位置的账号操作（创建、禁用、删除）与登录源配置（通用 OIDC、LDAP 目录）集中到一个页面，供**系统管理员**使用。三个标签页共用页头，权限由 `/system/admin/*` 分组上的 `SystemAdmin()` 守卫统一授予，空间 Owner 看不到入口。

| 标签页 | 作用 | 是否需要额外前置条件 |
| --- | --- | --- |
| 本地用户 | 列出平台内全部账号，支持搜索、启用 / 禁用、软删除，并显示每个账号的登录来源 | 无 |
| 通用 OIDC | 在界面上配置 OpenID Connect 提供商，替代逐条设置 `OIDC_AUTH_*` 环境变量 | 无（未配置时保持原行为） |
| LDAP | 配置 Active Directory / OpenLDAP 目录绑定，并提供连通性、检索、绑定三步测试 | 有一个可访问的目录服务 |

<Screenshot
  src="/screenshots/user-management-local-users.png"
  caption="用户管理 → 本地用户：统计卡片、账号列表与登录来源标记"
  hint="以系统管理员身份打开「设置 → 系统管理 → 用户管理」，展示顶部统计（账号总数 / 已启用 / 已禁用 / 管理员 / LDAP / OIDC）与表格中的登录来源列。" />

## 本地用户

列表分页返回部署内所有账号，`keyword` 对用户名与邮箱做模糊匹配。每行给出该账号的**登录来源**：

| 来源 | 含义 |
| --- | --- |
| `local` | 使用本地密码登录 |
| `oidc` | 由 OIDC 首次登录自动创建（旧数据通过 `oidc_only_login` 偏好回推） |
| `ldap` | 由 LDAP 首次登录自动创建（受 `auto_create_user` 控制） |

`has_local_password` 表示账号是否具备可用的本地密码。OIDC / LDAP 账号默认没有本地密码，自助改密码与「忘记密码」流程对它们不适用。

### 启用与禁用

禁用会立即撤销该账号的全部会话，用户随即退出。为防止把操作者自己踢下线，**不允许禁用自己**；最后一位系统管理员也受保护。启用可随时恢复。两者都会写入平台审计日志（`system.user_disabled` / `system.user_enabled`）。

### 删除

删除是**墓碑化软删除**：记录被软删除并撤销全部会话，同时释放邮箱与用户名，便于之后用同一身份重新开通。受两条约束：

- 不能删除自己；
- 不能删除最后一位系统管理员。

审计动作为 `system.user_deleted`，详情中标注 `soft_delete`。

新建账号与重置密码不在本页，而是复用平台管理的既有接口（`POST /system/admin/users/create`、`POST /system/admin/users/reset-password`），见[平台管理与系统管理员](20-platform-admin.md)。

## 通用 OIDC

该页管理的是**部署级**的 OIDC 提供商。保存后配置写入数据库，优先级为：

```
数据库行  →  OIDC_AUTH_* 环境变量  →  内置默认值
```

页面顶部会显示当前配置来源（`database` / `environment` / `default`），并列出仍被环境变量覆盖的项，便于清理遗留变量。

| 配置来源 | 何时出现 | 行为 |
| --- | --- | --- |
| `environment` | 从未在本页保存过，且设置了 `OIDC_AUTH_*` | 沿用环境变量，等价于升级前的行为 |
| `default` | 既无数据库行也无环境变量 | 提供商关闭，登录页不显示 OIDC 按钮 |
| `database` | 在本页保存过 | **数据库完全接管**，环境变量不再参与解析 |

::: warning 数据库接管是"全字段"的
一旦保存，数据库行对**每个字段**都是权威的：被清空的字段保持为空，不会从环境变量回填。这样修改 issuer 时不会残留上一个提供商的 discovery 地址。环境变量只在首次保存前充当表单初值。

虽然数据库接管后环境变量不再生效，页面仍会列出它们，以便你确认哪些变量可以安全移除。
:::

### 必填项与校验

启用状态下必须能端到端解析，否则登录按钮会渲染出来再点击失败：

- `client_id` 必填；
- 需要 `issuer_url`、`discovery_url`，或同时提供 `authorization_endpoint` 与 `token_endpoint` 三者之一。

只填 `issuer_url` 时，系统按 `issuer_url` 推导 discovery 地址（去除末尾 `/` 后拼接 `/.well-known/openid-configuration`）。显式填写 `discovery_url` 时以显式值为准。

### 密钥只写不读

`client_secret` **永不回传**。读接口只返回 `has_secret` 布尔值，保存请求里：

| 请求中的 `client_secret` | 结果 |
| --- | --- |
| 省略（`null`）或空字符串 | 保留已存密钥（表单拿不到明文，不回填就不会误清） |
| 填写新值 | 覆盖为新密钥 |
| 置 `clear_secret: true` | 清除密钥 |

密钥在服务端以 AES-256-GCM 加密后落库（配置了 `SYSTEM_AES_KEY` 且长度恰为 32 字节时）。若密钥被轮换或移除，系统会把凭据视为未配置并记录告警，而不是拿密文当密钥使用。

<Screenshot
  src="/screenshots/user-management-oidc.png"
  caption="用户管理 → 通用 OIDC：配置来源提示、启用开关与端点字段"
  hint="展示「配置来自数据库」横幅、启用开关、Client ID / Issuer URL / Discovery URL 等字段，以及标注为只写不回显的 client_secret 输入框。" />

### 测试连接

「测试连接」只读取不修改状态。它按上面的规则确定 discovery 地址并拉取文档，回报签发方与各端点。若未配置 `discovery_url` 但提供了显式的 authorization / token 端点，则跳过联网探测，直接使用手工端点并给出提示。

## LDAP

配置 Active Directory / OpenLDAP 目录，供本地密码不适用时做绑定认证。

| 字段 | 说明 | 默认值 |
| --- | --- | --- |
| `host` | 主机名或 IP，**不带协议前缀**（写 `ldaps://` 会被拒绝） | — |
| `port` | 端口，`use_tls` 时习惯 636，否则 389 | `389` |
| `use_tls` | 使用 `ldaps://` 直连 | 关 |
| `start_tls` | 明文端口上先执行 StartTLS 升级 | 关 |
| `skip_tls_verify` | 跳过证书校验，仅建议实验环境 | 关 |
| `bind_dn` / `bind_password` | 服务账号；`bind_dn` 留空表示匿名绑定 | 空 |
| `base_dn` | 搜索基准 DN，启用时必填 | — |
| `user_search_base` | 用户搜索子树，留空回退到 `base_dn` | 空 |
| `user_filter` | 用户过滤器，`%s` 会被替换为转义后的标识符 | 见下 |
| `user_name_attr` / `user_email_attr` / `user_display_attr` | 属性映射 | `sAMAccountName` / `mail` / `displayName` |
| `timeout_seconds` | 每次目录调用的超时 | 内置默认（8 秒） |

`use_tls` 与 `start_tls` **互斥**，同时开启会被拒绝。

默认 `user_filter`：

```
(&(objectClass=person)(|(mail=%s)(sAMAccountName=%s)(uid=%s)))
```

### 登录标识符与过滤器转义

用户在登录页填写的是邮箱（或 sAMAccountName），系统把它代入 `user_filter` 的 `%s` 位置。标识符中的 LDAP 过滤器元字符会先按 RFC 4515 转义，无法借此扩大或改写过滤器：

| 输入 | 转义结果 |
| --- | --- |
| `*` | `\2a` |
| `(` | `\28` |
| `)` | `\29` |
| `\` | `\5c` |

因此 `*)(objectClass=*` 这类注入尝试会原样留在值内部，不会闭合属性组。

::: tip 不带 %s 的过滤器
如果 `user_filter` 中不含 `%s`，则原样使用（例如由运维按组拼好的固定过滤器），标识符不参与过滤。
:::

### 稳定主体标识

系统优先用目录暴露的**稳定标识**作为主体 id，依次为：

1. `entryUUID`（OpenLDAP / 389DS）；
2. `objectGUID`（Active Directory，按 8-4-4-4-12 规范文本渲染）；
3. 以上都没有时回退到条目 DN。

测试面板会回报实际命中的属性，避免运维猜测 `subject_attribute`。

### 测试连接

测试分三步，任一步失败都会就地停止并给出原因：

1. **服务账号绑定**——验证连通性与凭据；
2. **用户检索**——用填写的用户名按 `user_filter` 查找条目（可留空跳过）；
3. **用户绑定**——用填写的密码以该条目 DN 重新绑定验证（可留空跳过）。

未启用 TLS 时会提示绑定密码将以明文传输；启用 TLS 但跳过证书校验时也会给出提示。

<Screenshot
  src="/screenshots/user-management-ldap.png"
  caption="用户管理 → LDAP：连接参数、TLS 选项与服务账号绑定信息"
  hint="展示主机 / 端口、「使用 TLS (ldaps)」与「使用 StartTLS」开关、跳过证书校验与超时，以及服务账号绑定（bind_dn / bind_password）区块。" />

### 自动开通与租户归属

`auto_create_user` 开启时，目录用户首次登录将即时创建本地账号。新账号的租户归属由 `default_tenant_mode` 决定：`create_personal` 创建个人空间，`tenantless` 等待用户自行加入空间；留空则遵循全局的 `auth.default_tenant_mode`。

### 失败节流

同一标识符连续绑定失败达到阈值后会被**临时锁定**，避免向目录持续施压触发账号锁定策略。节流在进程内维护，成功绑定即清零。

## 审计与权限

三个标签页的全部写操作都记入平台审计日志（`tenant_id = 0`），系统管理员可在「设置 → 系统审计日志」中查看：

| 动作 | 触发时机 |
| --- | --- |
| `system.user_enabled` / `system.user_disabled` | 启用 / 禁用账号 |
| `system.user_deleted` | 软删除账号 |
| `system.auth_provider_updated` | 保存 OIDC 或 LDAP 配置 |

接口清单见 [API 参考：系统与平台管理](../04-api/02-api-system.md)。
