# WeKnora 沙箱集群与标准模板

本文面向部署和平台管理员，说明如何把 CubeSandbox 或 E2B 接入 WeKnora。Docker、Local、CubeSandbox、E2B 都在同一个空间设置页面通过同一套配置与检查接口管理；只有远端后端需要本文所述的集群和模板准备。普通智能体使用者不需要搭建模板，也不应该逐项猜测运行环境。

## 谁负责什么

| 角色 | 职责 |
| --- | --- |
| WeKnora 发布流程 | 维护 `docker/Dockerfile.sandbox`，发布与 WeKnora 版本匹配的 `wechatopenai/weknora-sandbox` 镜像 |
| 集群管理员 | 部署 CubeSandbox 或开通 E2B，并保证控制面模板 API 可用 |
| 空间管理员 | 在“设置 → 沙箱后端”中填写集群地址和凭据，先完成连接验证，再选择接口返回的模板 |
| 智能体管理员 | 在智能体的 Skills 配置中选择已经验证过的沙箱后端 |

“WeKnora 标准模板”指模板内容由 WeKnora 维护，并不代表所有集群共享同一个模板 ID。CubeSandbox 的模板 ID、E2B 的模板 ID/别名都属于具体集群或账号；跨集群硬编码一个 ID 会指向不存在或内容不一致的模板。

## 标准模板包含什么

标准镜像定义在 `docker/Dockerfile.sandbox`，当前包含：

- Python 3.11；
- Node.js 20、npm 与 npx；
- jq 及基础 Shell 工具；
- `/workspace` 工作目录；
- UID 1000 的非 root `user` 账号（E2B 模板约定的账号名，WeKnora 以它执行脚本与文件操作）。

生产环境应使用与 WeKnora 相同的版本标签，不建议长期指向 `latest`。Skills 新增系统依赖时，应先更新标准镜像并重新注册模板，再切换集群的默认模板 ID。

### 两个镜像变体

`docker/Dockerfile.sandbox` 产出两个 target，内容相同、入口不同：

| 变体 | 标签 | 用途 |
| --- | --- | --- |
| `sandbox`（默认） | `wechatopenai/weknora-sandbox:<版本>` | Docker 后端直接执行脚本；同时作为 E2B 模板的基础镜像 |
| `cube` | `wechatopenai/weknora-sandbox:<版本>-cube` | CubeSandbox 模板 |

区别在于 Cube 变体额外注入了 envd。Cube 直接把 OCI 镜像变成模板，并以 `GET :49983/health` 探活，这个端点只有 envd 提供；不带 envd 的镜像建模板必然以 `connection refused` 失败。E2B 不需要这个变体，因为它的构建流程会自行注入 envd；Docker 后端则完全不需要 envd。详见 [Cube 自带镜像接入](https://cubesandbox.com/zh/guide/tutorials/bring-your-own-image.html)。

Cube 变体只发布 linux/amd64——envd 的来源镜像 `cubesandbox-base` 没有 arm64，Cube 自身的 PVM 形态也只支持 x86_64。变体内 envd 以 root 运行，脚本仍按请求指定的账号执行，落在同一个 uid 1000 的 `user` 上。

## CubeSandbox

### 先选部署形态

| 形态 | 适用 | 硬性前提 |
| --- | --- | --- |
| 裸金属 / 物理机 | 已有可用 KVM 的机器 | `/dev/kvm` 可读写；root 权限 |
| PVM | 云厂商屏蔽了嵌套虚拟化、`/dev/kvm` 不可用 | 仅 x86_64；需安装 PVM 宿主机内核并重启；ARM64 不支持 |
| Kubernetes（preview） | 已有集群、要多节点 | K8s 1.24+；计算节点打标签；计算节点非裸金属时需开启 PVM bootstrap |

三种形态共同的前提：

- glibc ≥ 2.31（官方二进制基于 Ubuntu 20.04 构建）；
- `/data/cubelet` 挂载 XFS，且开启 reflink（快照的 Copy-on-Write 依赖它）。Ubuntu/Debian 默认 ext4，需要单独准备分区或 loop 设备；
- 至少 50 GB 可用磁盘（要做多个模板则建议 200 GB 以上）、内存 ≥ 8 GB；
- 内核支持 eBPF 且 `/sys/fs/bpf` 已挂载为 bpffs（Cubelet 的网络运行时依赖）；
- 具备 `resolvectl` 或 NetworkManager（安装脚本据此配置 `cube.app` 的 DNS 解析）；
- Docker 可用（MySQL/Redis 以 Docker Compose 运行）。

安装脚本会把 CubeMaster、Cubelet、CubeShim 作为宿主机进程运行，因此**不能在没有 systemd 的容器化环境里部署**。CI 容器、无 init 系统的开发机请改用 K8s 形态或另找一台主机。

### 制作模板并对接 WeKnora

1. 按 [CubeSandbox Quick Start](https://github.com/TencentCloud/CubeSandbox/blob/master/docs/zh/guide/quickstart.md) 完成控制面、计算节点、CubeProxy 与域名解析。生产环境还需按官方文档完成鉴权、TLS、网络策略与多节点部署。
2. 在 WeKnora 的空间设置中填写 CubeAPI、CubeProxy、sandbox domain 和可选 API Key。若这些端点位于 RFC1918/loopback 网络，显式打开“允许访问私网集群地址”。
3. 点击“连接并继续”。WeKnora 先验证控制面地址与凭据，通过后才进入模板步骤并调用集群模板列表；如果集群里没有可用的 WeKnora 标准模板，会从 `wechatopenai/weknora-sandbox:latest-cube` 发起一次构建。已有模板按名称 `weknora` 或镜像识别，因此即使集群没有给模板登记别名也不会重复创建；已有模板构建失败时，走的是原地重建而不是新建一个。
4. 模板构建状态会自动刷新。状态变为 `READY` 后才可选择并进入运行配置；界面显示模板名称、状态和版本，配置内部才保存该集群自己的 `template_id`。

模板镜像必须提供 uid 1000 的 `user` 账号：WeKnora 以该账号执行脚本与文件操作。写权限只保证在 `/workspace/output` 与 `/workspace/input` 下。

多实例 WeKnora 必须配置 Redis，以共享 session 到 sandbox 的绑定。只有单实例开发环境才应使用内存绑定。

### 验证

界面上的“连接验证”只覆盖控制面，“完整验证”会真实创建、执行并销毁一个沙箱。要覆盖 WeKnora 实际依赖的全部语义（会话内状态保持、shell_exec 复用同一沙箱、附件暂存、产物收集、执行超时），在能访问集群的机器上跑一致性测试：

```bash
CUBE_API_URL=http://127.0.0.1:33000 \
CUBE_PROXY_URL=http://127.0.0.1:80 \
CUBE_TEMPLATE_ID=<模板 ID> \
go test -tags=integration ./internal/sandbox -run Integration -count=1 -v
```

测试结束会归还自己创建的沙箱；跑完用 `cubemastercli` 确认没有残留实例。

### 排障

| 现象 | 排查方向 |
| --- | --- |
| 连接验证失败 | CubeAPI 地址是否是控制面端口（不是 Dashboard 端口）；私网地址是否已打开“允许访问私网集群地址” |
| 连接通过但执行报数据面错误 | CubeProxy 地址与 sandbox domain 是否与集群 `CUBE_API_SANDBOX_DOMAIN` 一致；Proxy 是否对 WeKnora 可达 |
| 模板长期停在构建中 | 镜像体积与网络；用 `cubemastercli tpl watch --job-id <id>` 看真实进度 |
| 模板构建失败 | 卡片上会显示集群返回的失败原因；点「刷新模板」会就地重建同一个模板，不会新建一个 |
| 失败原因是 `Get "http://<IP>:49983/health": connect: connection refused` | 模板镜像里没有 envd。确认建模板用的是 `-cube` 变体镜像；若已是该变体，再查 Cube 沙箱网段（默认 `192.168.0.0/18`）是否与物理内网冲突 |
| 沙箱能创建但脚本报权限错误 | 模板缺少 `user` 账号，或脚本写到了 `/workspace` 根目录 |
| 会话重连后状态丢失 | 多副本部署是否配置了 Redis 绑定存储；沙箱是否已被空闲 TTL 回收 |

### 已知限制

- K8s 部署仍是 preview：计算节点资源紧张时 Pod 可能被误驱逐，计算面升级会重建 Big Pod 并中断存量沙箱。
- 官方镜像的 Multi-Arch 覆盖尚不完整，ARM64 环境需自行构建模板镜像。
- 暂不支持 GPU 直通。

其它 E2B 兼容后端（含容器隔离的 Kubernetes 实现）与协议层的统一方案见 [沙箱协议接入说明](./sandbox-protocol.md)。

## E2B 及其它 E2B 兼容实现

E2B 官方托管服务、自建 E2B Infrastructure，以及任意实现 E2B 协议的控制面（例如 Kubernetes 上以容器隔离的 Agent-Sandbox）都通过同一个 E2B 配置接入；自建集群通常还要填写 `proxy_url` 数据面网关，详见 [沙箱协议接入说明](./sandbox-protocol.md)。填写 API Key 后先执行“连接并继续”，流程与 Cube 相同：验证连接后列出账号可见模板，缺少 `weknora` 时通过 E2B Template API 从标准镜像启动后台构建。自建部署还需填写 API URL 和 sandbox domain；E2B 上游通过 Terraform 提供 AWS、GCP 等部署方式，具体以 [E2B self-hosting guide](https://github.com/e2b-dev/infra/blob/main/self-host.md) 和 [E2B Template 文档](https://e2b.dev/docs/template/quickstart) 为准。

## 在设置页面完成接入

1. 打开“设置 → 沙箱后端”，点击“添加沙箱后端”。
2. 填写该集群自己的 API、Proxy、sandbox domain 和凭据；这些值只保存在当前空间配置中，不读取 Sandbox 环境变量。
3. 点击“连接并继续”，验证控制面地址和凭据；连接通过后才加载模板列表。
4. 等待自动创建的标准模板就绪，或选择集群已有的兼容模板；构建中的模板不可选择，状态会自动刷新。
5. 配置运行参数；上线前可执行一次“完整验证”。完整验证会真实创建、执行并销毁一个沙箱。
6. 保存后，在智能体 Skills 配置中选择该后端。对配置的修改只影响之后新建的沙箱；已有会话仍固定使用创建时的配置。

## 上线检查

- 模板版本与 WeKnora 版本匹配，且不依赖浮动的 `latest`；
- WeKnora 到控制面、Proxy 和 sandbox domain 的 DNS、TLS 与防火墙均已打通；
- 多实例部署已配置 Redis；
- API Key 只通过密钥管理或加密配置保存；
- “连接验证”和“完整验证”均通过；
- 已配置运行中与 paused 沙箱的容量监控、费用监控和孤儿清理；
- 切换模板或集群前已确认旧会话和旧沙箱的回收策略。
