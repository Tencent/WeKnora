# 技能目录与沙箱

技能由 `SKILL.md` 说明、脚本、模板和资料组成，沙箱提供脚本执行环境。将技能添加到空间目录并安装到沙箱后，在智能体中选择对应沙箱和技能，即可用于智能推理对话。

## 配置并使用技能 {#从配置到第一次执行}

1. 空间 Admin/Owner 在「设置 → 沙箱配置」创建配置，选择 Docker、CubeSandbox 或 E2B，填写连接信息。
2. 按向导连接集群、选择模板；需要验证完整链路时运行「完整验证」。完整验证会实际创建沙箱、执行探针并清理。
3. 在「设置 → 技能管理」添加 ZIP 或来源链接，再选择要安装的沙箱。目录收录成功只表示安装包已保存；安装状态就绪后才能执行。
4. 打开智能体编辑器的「技能与沙箱」，选择沙箱，设置技能范围为全部、指定技能或禁用。技能执行用于智能推理模式。
5. 进入对话提问，或用 `@技能` 提示智能体优先使用该技能。需要文件时上传附件，生成的交付文件在对话的文件面板预览、下载。

`@技能` 不会取消智能体对其他已授权技能的访问。未选沙箱、空间关闭脚本或所需后端能力不可用时，执行工具不会注册；提示词无法绕过这些条件。

<Screenshot
  src="/screenshots/skill-catalog.png"
  caption="空间技能目录：查看技能与各沙箱安装状态" />

## 管理安装与更新 {#目录、安装和更新}

空间目录保存一份技能包；每个沙箱配置各有安装记录和包含技能的镜像快照。同一技能可以装到多个沙箱，安装进度和失败原因分别记录。

| 操作 | 结果 |
| --- | --- |
| 添加到目录 | 保存包、名称、版本和说明，不执行安装 |
| 安装 | 在所选沙箱构建运行环境、安装依赖并校验可加载性，成功后发布新快照 |
| 重试 | 复用已保存的包再次安装；相同包已正常就绪时可跳过 |
| 停止安装 | 中止进行中的安装，状态变为 failed，之后可重试或卸载 |
| 停用 | 保留安装记录，只让智能体不再选用该技能 |
| 从沙箱卸载 | 更新该沙箱的技能镜像，保留目录中的安装包，便于其他沙箱继续安装 |
| 删除目录条目 | 要求已无沙箱安装引用；不会隐式卸载全部沙箱 |

安装页面显示百分比、阶段和日志，详细运行过程可在安装记录中查看。关闭进度抽屉或断开进度流不会停止安装。没有实时进度时可刷新技能状态；详细事件日志过期不代表技能安装包已丢失。

`skill_rollout` 控制镜像更新：默认 `next_turn` 在已有会话的下一轮重建沙箱；`new_session` 仅让之后创建沙箱的会话使用新镜像。沙箱重建会丢失旧实例临时运行状态，需交付的文件应写入 `/workspace/output` 并由系统收集。

### 支持的来源

| 输入 | 说明 |
| --- | --- |
| ZIP 文件 | 导出技能目录后上传 |
| `@owner/slug`、`@owner/slug@1.2.0` | ClawHub 指定作者/版本 |
| `slug`、`slug@1.2.0` | ClawHub slug |
| ClawHub、SkillHub、自托管 SkillHub 页面 | 通过对应来源解析 |
| GitHub/GitLab 仓库或目录 URL | 获取对应技能包 |
| `https://skills.sh/owner/repo/slug`、ClawHub 的 skills.sh 页面 | 经安装解析器解析到仓库的具体版本和目录 |
| 直接 ZIP 或 SKILL.md URL | 下载包或入口文件 |

来源必须可匿名读取，下载不会附带用户的私有仓库凭据。私有技能可先导出 ZIP。`owner/slug` 有歧义，应改用 `@owner/slug` 或完整 URL。

技能包上限独立于普通文档：`MAX_SKILL_BUNDLE_SIZE_MB` 默认 256 MiB，未设置时至少为 `MAX_FILE_SIZE_MB`，最高 512 MiB。GitHub 下载按整个仓库压缩包计算，不只计算技能子目录。调整后重启 app 和 frontend，使应用与 Nginx 上限一致。

## 选择沙箱后端

| 后端 | 需要填写 | 运行方式 |
| --- | --- | --- |
| Docker | 镜像；可选 daemon 地址、TLS 证书目录、CPU/内存/PID 限制、网络模式、runtime、空闲 TTL | 一个会话一个长驻容器 |
| CubeSandbox | 控制面地址、数据面代理、沙箱域名、模板；按集群配置 API Key | 会话级远端沙箱 |
| E2B | API Key、模板；自托管时补 API 地址、沙箱域名和数据面代理 | E2B Cloud 或 E2B 兼容控制面 |

`local` 宿主机进程后端已移除。当前配置的完整字段见[沙箱与技能 API](../04-api/02-api-sandbox-skills.md)。

Docker 后端默认关闭。系统管理员在「系统设置 → 网络安全」启用，或用 `WEKNORA_SANDBOX_DOCKER_ENABLED=true` 作为未落库时的回退。本机连接还需要把实际 Docker socket 挂给 app；这授予 app 控制宿主机 Docker 的能力。远端 TCP daemon 要配置 TLS 证书目录，其中包括 `ca.pem`、`cert.pem`、`key.pem`。Docker 网络仅接受 `bridge` 或 `none`，可选 `runsc` 等已安装 OCI runtime。

自托管 E2B/Cube 的 `proxy_url` 指向数据面网关：WeKnora 连接网关但保留沙箱 Host，用于没有泛域名 DNS 的集群。`allow_private_endpoints` 允许连接私网/回环的集群地址，仍不放行 link-local/云元数据地址；它与沙箱里脚本能否出网是不同配置。

脚本默认以沙箱内的 `root` 账号执行，模板中的 `user` 账号需显式选择。执行隔离由容器或远端沙箱提供，`/workspace` 只约定工作目录，不限制 root 命令的文件访问权限。

### 网络策略

Cube/E2B 的 `config.network` 同时用于对话沙箱、技能安装和完整验证：

- 默认允许出站；`deny_egress_by_default=true` 改为默认拒绝，再通过 `allow_out` 放行 IP、CIDR 或域名。
- `deny_out` 接受 IPv4/CIDR；域名允许规则需配合默认拒绝。
- Cube 的 `cube_rules` 可按 Host/SNI、方法和路径设置规则、调整顺序、配置审计及 HTTPS 头注入；E2B 的 `e2b_host_rules` 配置已放行域名的请求头注入。
- 注入的凭据加密保存，响应脱敏。入站始终需要凭据，旧字段 `allow_public_inbound` 不会开放匿名入站。
- Docker 使用 `docker.network_mode` 控制出网，不能照搬 Cube/E2B 的细粒度规则。默认拒绝出网后，安装依赖需要的源站也必须显式放行。

已有实例不会因为修改策略自动获得新配置，应在新建/重建沙箱后验证。修改后端身份或删除配置前，系统会查询运行中/暂停的实例和关联智能体；存在占用时拒绝操作，设置页会展示占用信息。

## 环境变量与凭据

个人变量入口在个人资料的环境变量设置；空间级技能变量在技能管理中配置。列表只显示变量声明、是否设置和来源，不回显秘密值。

| 层级 | 作用 |
| --- | --- |
| 空间沙箱 `config.env_vars` | 注入该配置创建的沙箱，供其脚本使用 |
| 空间技能变量 | 管理员为技能已声明的变量填默认值 |
| 个人沙箱变量 | 本人在该沙箱配置执行命令时使用 |
| 个人技能变量 | 本人在该技能执行时使用 |

技能变量解析中，**个人技能值 > 个人沙箱值 > 空间技能值**；未设置的名称才回退。空间沙箱环境是运行环境的一部分，不应在其中放脚本不应读取的秘密。删除个人覆盖后重新使用下层值；关闭技能不会删除个人凭据。

个人技能变量只能使用技能已声明的名称，其中可包含技能所需的 `WEKNORA_*` 凭据。个人沙箱变量不接受 `WEKNORA_*`、`PATH` 等保留名。系统从执行命令中识别的变量只补充未设置的个人值，不覆盖已有个人或空间配置。字段和示例见[个人变量 API](../04-api/02-api-sandbox-skills.md#个人环境变量)。

## 生成和下载文件 {#文件和交付}

`read_file(path="skill://<name>/SKILL.md")` 读取说明；技能附带脚本通过 `shell_exec(skill_name=..., command=...)` 执行。命令中的 `$WEKNORA_SKILL_DIR` 指向技能实际安装目录；`skill://` 是读取地址，不能直接当作 shell 路径。

附件暂存到 `/workspace/input`，工作脚本放在 `/workspace`，可下载产物放在 `/workspace/output`。用 `write_sandbox_file` 新建/续写，用 `edit_sandbox_file` 局部替换，用 `read_file` 分页读取。文件工具边界、输出预算和重建行为见[Agent 引擎](07-agent.md)，交付入口见[会话与对话体验](18-chat-experience.md)。

<Screenshot
  src="/screenshots/skill-sandbox-chat.png"
  caption="沙箱生成 Word 文件后，在对话中预览并下载" />

## 实现参考

- `internal/handler/sandbox_config.go`、`sandbox_skill.go`、`skill_catalog.go`、`me_env_var.go`
- `internal/application/service/tenant_skill_install.go`、`user_env_resolver.go`
- `internal/types/tenant.go`、`sandbox_network_policy.go`
- `internal/sandbox/remote_client.go`、`docker_remote_client.go`、`gateway_transport.go`
