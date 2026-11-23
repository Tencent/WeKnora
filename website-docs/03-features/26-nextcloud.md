# Nextcloud 数据源

将 Nextcloud 的指定文件或目录通过 WebDAV 同步到知识库。可以按计划增量同步，任务中断后从已确认的进度继续。管理员可以决定源文件删除时是否同步删除知识库中的副本。[通用同步设置](10-datasource.md)

## 准备账号和应用密码 {#account-and-app-password}

1. 使用有权读取目标目录的账号登录 Nextcloud。
2. 打开个人设置中的 **Security → Devices & sessions**，创建一个名为 WeKnora 的应用密码，并保存界面上显示的用户名和应用密码。密码只显示一次。
3. 在 WeKnora 填入这一组凭据。使用双因素认证或外部登录的账号同样使用应用密码。需要轮换时，在 Nextcloud 的同一处撤销旧密码、生成新密码，再在 WeKnora 重新录入整组凭据。

连接使用 HTTP Basic 认证，建议填写安装实例的正式 HTTPS 地址。WeKnora 的数据源凭据接口只返回配置状态。[Nextcloud 应用密码](https://docs.nextcloud.com/server/stable/user_manual/en/session_management.html) · [WebDAV 文件操作](https://docs.nextcloud.com/server/stable/developer_manual/client_apis/WebDAV/basic.html)

## 在知识库选择同步范围 {#configure-in-weknora}

1. 打开**知识库设置 → 数据源 → 添加**，选择 **Nextcloud**。
2. 填写安装地址、用户名和应用密码。独立域名示例为 `https://nextcloud.example.com`，带子路径的部署可填 `https://nextcloud.example.com/cloud`。连接器会自行拼接账号的 WebDAV 路径。
3. 测试连接，确认账号可以访问。可选填要包含或排除的扩展名，例如 `.pdf, .md`。大小写和开头的点均可忽略，排除规则优先。范围树可浏览所有支持的格式，过滤在同步时生效。
4. 至少选择一个目录或文件。目录包含今后加入的子项，选中最上层的 **Nextcloud** 会覆盖整个账号。接口使用相对账号根目录的绝对路径，例如 `/docs`。多个重叠选择会合并。
5. 设置同步计划、冲突策略与**同步删除**，先选择一个小目录手动同步，核对导入的文件和解析状态。

<Screenshot src="/screenshots/datasource-nextcloud.png" caption="使用示例账号填写 Nextcloud 连接信息" />

<Screenshot src="/screenshots/release-v091-nextcloud.png" caption="在读书会演示知识库中选择需要同步的目录" />

账号需要能够列举选中范围内的每个目录。连接器从账号向知识库单向同步，支持通过 WebDAV 读取的内容。公开分享链接和端到端加密的内容不在此范围。

## 私网部署与访问控制 {#network-and-ssrf-protection}

私有部署按服务端现有网络策略配置。在 `SSRF_WHITELIST` 中保留已有条目，按需加入自己的 Nextcloud 主机后重启后端。部署维护的附加条目也可使用 `SSRF_WHITELIST_EXTRA`。连接测试和文件读取均限定在配置的源站，请填写最终使用的主机、协议和端口。受反向代理保护的服务需保留 WebDAV 的 PROPFIND、Depth、身份认证和条件请求头。

## 增量同步和删除 {#incremental-synchronization-and-deletion}

每轮遍历所选范围，用 ETag、修改时间、大小及可用的 Nextcloud 文件 ID 识别变化。文件至少要有 ETag 或修改时间。未变化的文件跳过下载。下载会检查版本和字节数，校验通过后才记录进度。知识库在 `Nextcloud/` 下保留目录层级。

| 源端变化 | 同步结果 |
| --- | --- |
| 新增或修改文件 | 下载并导入或更新 |
| 删除文件 | 成功遍历整个范围后，按“同步删除”设置处理 |
| 移动或重命名文件 | 如新路径仍在范围内则导入新路径，按删除处理旧路径 |
| 选中的根目录被删除或改名 | 停止本轮，保留已有内容，请重新选择范围 |
| 调整目录或扩展名过滤 | 已跟踪但不再属于新范围的路径按删除处理 |
| 更换服务器或账号 | 新建数据源，原有同步进度属于原账号和实例 |
| 轮换应用密码 | 保留同步进度及文件对应关系 |

同步进度按已确认项目保存。强制完整同步会重新下载文件，并保留之前用于核对删除的记录。列举目录失败或响应不完整会停止整轮的删除核对。个别文件读取失败会记录错误，后续同步可以重试。凭据失效会停止本轮任务，单次请求超时为 120 秒。

关闭“同步删除”期间已记入进度的删除不会在重新开启时补删。移除数据源会保留已导入的知识。下载完成后还需查看文档的解析和索引状态。

## 文件格式与限制 {#formats-and-limits}

- 导入格式与知识库上传一致。图片和音频需要相应的知识库模型。扩展名过滤配置为 `config.settings.include_extensions` 和 `exclude_extensions`，API 也接受字符串数组。
- 单文件默认最大 50 MiB，由 `MAX_FILE_SIZE_MB` 控制。提高限制时同步核对解析服务的 `DOCREADER_GRPC_MAX_FILE_SIZE_MB`。
- 单次目录响应最多 32 MiB、10 万项，单轮遍历最多 100 万项、深度 256。超过限制可缩小所选目录。连接器使用 Depth 0 或 1 的 PROPFIND，依赖完整、有效的 WebDAV 响应。
- 常见错误包括权限不足、文件消失、超出大小、空文件、下载时文件变化或网络策略拦截。具体状态见数据源的同步记录。

## 验证连接器 {#verification}

运行连接器本地测试只需进程内的 WebDAV 测试服务。

```bash
go test ./internal/datasource/connector/nextcloud -count=1
```

真实服务的只读验证使用 `NEXTCLOUD_LIVE_URL`、`NEXTCLOUD_LIVE_USER`、`NEXTCLOUD_LIVE_APP_PASSWORD`、`NEXTCLOUD_LIVE_ROOT`。四项均配置时才运行。选择含有一个受支持非空文件的小目录，测试期间保持文件内容稳定。验证覆盖账号访问、范围列举、下载和下一轮增量跳过。

```bash
go test ./internal/datasource/connector/nextcloud -run '^TestLive$' -count=1 -v
```
