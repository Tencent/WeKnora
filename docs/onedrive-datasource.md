# Microsoft OneDrive 数据源

本文说明如何部署、授权和维护 WeKnora 的 OneDrive 数据源。SharePoint 站点、共享文档库和应用权限不在当前版本范围内。

## 部署配置

在 Microsoft Entra 管理中心注册 Web 应用：

1. 进入 **Entra ID → App registrations → New registration**，填写应用名称。
2. 按部署范围选择账号类型：单组织部署优先选单租户；需要跨组织或个人 Microsoft 账号时，选择对应的多租户/个人账号选项。
3. 注册完成后，在 **Overview** 复制 **Application (client) ID**，填入 `ONEDRIVE_CLIENT_ID`。单组织部署还可复制 **Directory (tenant) ID** 填入 `ONEDRIVE_TENANT`；需要兼容多组织或个人账号时使用 `common`。
4. 进入 **Authentication → Add a platform → Web**，添加与 `ONEDRIVE_REDIRECT_URL` 完全一致的 Redirect URI。callback 在后端交换 code，因此平台类型是 **Web**，不是 SPA。
5. 进入 **Certificates & secrets → Client secrets → New client secret**。创建后立即复制只显示一次的 **Value** 填入 `ONEDRIVE_CLIENT_SECRET`；不要填写 Secret ID，并在到期前轮换。
6. 进入 **API permissions → Add a permission → Microsoft Graph → Delegated permissions**，添加 `Files.Read`。`offline_access` 由授权请求申请，用于后台刷新。

配置时注意：

- 支持的账号类型按部署需要选择个人 Microsoft 账号及/或组织目录中的账号。
- Web Redirect URI 必须与 `ONEDRIVE_REDIRECT_URL` 完全一致。
- Redirect URI 不要求公网可达：OAuth 完成后是用户浏览器跳回该地址。本机开发可使用 `http://localhost:<port>/api/v1/datasource-oauth/onedrive/callback`；内网/VPN 部署可使用浏览器可解析访问、证书受信任的内部 HTTPS 域名。远端服务器地址不能写成用户电脑上的 `localhost`。
- 委托权限只需 Microsoft Graph `Files.Read`；授权时还会申请 `offline_access` 以支持定时刷新。
- 不要配置 Graph 写权限，也不要把 client secret 填入数据源页面。

对应的微软官方说明见 [注册应用](https://learn.microsoft.com/en-us/entra/identity-platform/quickstart-register-app)、[添加应用凭据](https://learn.microsoft.com/en-us/entra/identity-platform/how-to-add-credentials)、[Redirect URI 规则](https://learn.microsoft.com/en-us/entra/identity-platform/reply-url) 和 [Graph 权限参考](https://learn.microsoft.com/en-us/graph/permissions-reference)。

在 WeKnora 环境中设置：

```dotenv
ONEDRIVE_CLIENT_ID=<application-client-id>
ONEDRIVE_CLIENT_SECRET=<application-client-secret>
ONEDRIVE_TENANT=common
ONEDRIVE_REDIRECT_URL=https://weknora.example.com/api/v1/datasource-oauth/onedrive/callback
SYSTEM_AES_KEY=<exactly-32-bytes>
```

生产/多实例部署还必须配置 Redis。只有明确的 `EDITION=lite` 单实例模式允许把一次性 OAuth state 保存在进程内存中。修改 Redirect URI 或密钥后需要重启应用。

`ONEDRIVE_CLIENT_ID`、`ONEDRIVE_CLIENT_SECRET` 和 `SYSTEM_AES_KEY` 都属于服务端配置，不是数据源表单字段。生产环境应通过 secret manager 或部署平台的加密变量注入，不要提交到 Git。

## 使用流程

1. 在知识库的数据源设置中选择 OneDrive。
2. 点击“连接 OneDrive”，在 Microsoft 页面完成登录、授权及组织目录要求的 MFA。
3. 返回向导，展开 OneDrive 资源树，选择整个 drive、文件夹或单文件。
4. 配置同步周期、增量/全量模式以及是否同步删除，然后保存。

登录密码、Authenticator 验证码和短信验证码始终只提交给 Microsoft，WeKnora 不会要求或保存这些信息。

## 恢复与换绑

- `reauthorization_required`：使用“重新授权”恢复原账号。资源选择、成员索引和 cursor 会保留。
- 误用其他账号重新授权：后端会拒绝覆盖。确需换账号时使用“更换账号”并确认；旧任务会因连接版本变化自动取消，旧索引、cursor 和该数据源产生的旧知识会清理。
- “断开连接”：删除本地 token、暂停调度、清空选择，并删除该连接同步产生的知识。若要在 Microsoft 侧一并撤销 consent，请到 Microsoft 账号或企业应用页面操作。
- 删除数据源：会删除该数据源精确标记的知识；不会按标题、URL 或其他数据源的 `external_id` 误删。

`SyncDeletions=false` 会保留源端已删除的知识，但仍记录删除状态；之后重新启用删除同步时会自动 reconcile 遗留项。

## 安全与排障

- token 使用 `SYSTEM_AES_KEY` 进行 AES-256-GCM 加密。密钥缺失、长度不为 32 字节或密文无法解密时，OneDrive 授权和刷新会失败关闭，不能降级为明文。
- 不要轮换或丢失 `SYSTEM_AES_KEY`，除非已先断开所有 OneDrive 数据源并准备重新授权。
- 429 与临时 5xx 会有限重试；401 只强制刷新一次，仍失败则进入重新授权状态。
- 同步日志会显示新增、更新、删除、跳过、失败和安全化 warning，但不会返回 token、完整 delta link 或预认证下载 URL。
- 若授权弹窗被拦截，向导会显示可在新标签页打开的授权链接。

管理员禁止用户 consent 时，需要由组织目录管理员批准 `Files.Read` 委托权限；这不是密码或 MFA 错误。
