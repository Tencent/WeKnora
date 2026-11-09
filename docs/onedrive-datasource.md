# Microsoft OneDrive / SharePoint 数据源

本文说明如何部署、授权和维护 WeKnora 的 OneDrive 与 SharePoint 数据源。两种类型共享 Microsoft Graph 文件同步引擎，但使用不同的最小权限授权方式：OneDrive 使用用户委托授权，SharePoint 使用 `Sites.Selected` 应用程序授权。

## 部署配置

在 Microsoft Entra 管理中心注册 Web 应用：

1. 进入 **Entra ID → App registrations → New registration**，填写应用名称。
2. 注册完成后，在 **Overview** 复制 **Application (client) ID** 和 **Directory (tenant) ID**，分别填入 `MICROSOFT_CLIENT_ID` 和 `MICROSOFT_TENANT`。SharePoint 应用授权必须使用具体 tenant ID 或租户域名，不能使用 `common`、`organizations` 或 `consumers`。
3. 如需 OneDrive，进入 **Authentication → Add a platform → Web**，添加与 `MICROSOFT_REDIRECT_URL` 完全一致的 Redirect URI。callback 在后端交换 code，因此平台类型是 **Web**，不是 SPA。
4. 进入 **Certificates & secrets → Client secrets → New client secret**。创建后立即复制只显示一次的 **Value** 填入 `MICROSOFT_CLIENT_SECRET`；不要填写 Secret ID，并在到期前轮换。
5. 进入 **API permissions → Add a permission → Microsoft Graph**：
   - OneDrive：添加 **Delegated permissions** → `Files.Read`。`offline_access` 由授权请求申请，用于后台刷新。
   - SharePoint：添加 **Application permissions** → `Sites.Selected`，并由租户管理员完成 admin consent。不需要 `Sites.Read.All`、`User.Read` 或 `offline_access`。
6. `Sites.Selected` 本身不授予任何站点访问权。由 SharePoint/全局管理员或有权配置站点权限的管理应用，向目标站点授予本应用 `read` 权限：

```http
POST https://graph.microsoft.com/v1.0/sites/{site-id}/permissions
Content-Type: application/json

{
  "roles": ["read"],
  "grantedToIdentities": [{
    "application": {
      "id": "<MICROSOFT_CLIENT_ID>",
      "displayName": "WeKnora"
    }
  }]
}
```

配置时注意：

- OneDrive 支持的账号类型按部署需要选择个人 Microsoft 账号及/或组织目录中的账号；SharePoint 应用授权固定属于配置的组织目录。
- OneDrive 的 Web Redirect URI 必须与 `MICROSOFT_REDIRECT_URL` 完全一致；仅使用 SharePoint 时不需要 Redirect URI。
- Redirect URI 不要求公网可达：本机开发可使用 `http://localhost:<port>/api/v1/datasource-oauth/microsoft/callback`；内网/VPN 部署可使用浏览器可解析访问、证书受信任的内部 HTTPS 域名。
- OneDrive 授权请求委托权限 `Files.Read` 和 `offline_access`；SharePoint 使用应用程序权限 `Sites.Selected` 及目标站点上的 `read` 授权。两者均不申请写权限。
- 不要配置 Graph 写权限，也不要把 client secret 填入数据源页面。

对应的微软官方说明见 [注册应用](https://learn.microsoft.com/en-us/entra/identity-platform/quickstart-register-app)、[添加应用凭据](https://learn.microsoft.com/en-us/entra/identity-platform/how-to-add-credentials)、[Redirect URI 规则](https://learn.microsoft.com/en-us/entra/identity-platform/reply-url) 和 [Graph 权限参考](https://learn.microsoft.com/en-us/graph/permissions-reference)。

在 WeKnora 环境中设置：

```dotenv
MICROSOFT_CLIENT_ID=<application-client-id>
MICROSOFT_CLIENT_SECRET=<application-client-secret>
MICROSOFT_TENANT=<directory-tenant-id>
MICROSOFT_REDIRECT_URL=https://weknora.example.com/api/v1/datasource-oauth/microsoft/callback
SYSTEM_AES_KEY=<exactly-32-bytes>
```

生产/多实例部署还必须配置 Redis。只有明确的 `EDITION=lite` 单实例模式允许把一次性 OAuth state 保存在进程内存中。修改 Redirect URI 或密钥后需要重启应用。

原有 `ONEDRIVE_CLIENT_ID`、`ONEDRIVE_CLIENT_SECRET`、`ONEDRIVE_TENANT`、`ONEDRIVE_REDIRECT_URL` 仍作为兼容别名。新部署应使用 `MICROSOFT_*`。这些变量和 `SYSTEM_AES_KEY` 都属于服务端配置，不是数据源表单字段。

## 使用流程

1. 在知识库的数据源设置中选择 OneDrive 或 SharePoint。
2. OneDrive 点击“连接 Microsoft 账号”，在 Microsoft 页面完成登录、授权及组织目录要求的 MFA。
3. SharePoint 不需要用户登录；填写已向应用授予 `read` 权限的 `https://<tenant>.sharepoint.com/sites/<site>` 站点地址。
4. OneDrive 可选择整个 drive、文件夹或单文件；SharePoint 可先选择文档库，再选择库、文件夹或单文件。一个 SharePoint 数据源只允许选择一个文档库中的内容，如需多个文档库请分别创建数据源。
5. 配置同步周期、增量/全量模式以及是否同步删除，然后保存。

登录密码、Authenticator 验证码和短信验证码始终只提交给 Microsoft，WeKnora 不会要求或保存这些信息。

## 恢复与换绑

- `reauthorization_required`：使用“重新授权”恢复原账号。资源选择、成员索引和 cursor 会保留。
- 误用其他账号重新授权：后端会拒绝覆盖。确需换账号时使用“更换账号”并确认；旧任务会因连接版本变化自动取消，旧索引、cursor 和该数据源产生的旧知识会清理。
- “断开连接”：删除本地 token、暂停调度、清空选择，并删除该连接同步产生的知识。若要在 Microsoft 侧一并撤销 consent，请到 Microsoft 账号或企业应用页面操作。
- 删除数据源：会删除该数据源精确标记的知识；不会按标题、URL 或其他数据源的 `external_id` 误删。

`SyncDeletions=false` 会保留源端已删除的知识，但仍记录删除状态；之后重新启用删除同步时会自动 reconcile 遗留项。

## 安全与排障

- OneDrive 用户 token 使用 `SYSTEM_AES_KEY` 进行 AES-256-GCM 加密。密钥异常时 OneDrive 授权和刷新会失败关闭，不能降级为明文；SharePoint 应用令牌仅缓存在进程内存中。
- 不要轮换或丢失 `SYSTEM_AES_KEY`，除非已先断开所有 OneDrive 数据源并准备重新授权。SharePoint 应用令牌不持久化。
- 429 与临时 5xx 会有限重试；401 只强制刷新一次，仍失败则进入重新授权状态。
- 同步日志会显示新增、更新、删除、跳过、失败和安全化 warning，但不会返回 token、完整 delta link 或预认证下载 URL。
- 若授权弹窗被拦截，向导会显示可在新标签页打开的授权链接。

管理员禁止用户 consent 时，OneDrive 的 `Files.Read` 委托权限需要组织目录管理员批准。SharePoint 不走用户 consent：请确认 `Sites.Selected` 已完成 admin consent，并且目标站点已单独授予本应用 `read`；只完成其中一步仍会返回 403。
