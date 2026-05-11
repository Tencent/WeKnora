# WeKnora 云镜像打包脚本（Cloud-Agnostic）

> **本文档面向「想把 WeKnora 打包成云镜像（AMI / 自定义镜像 / Snapshot）分发给其他人」的用户。**
> **如果你只是想自己用 WeKnora，请直接看主仓 [README](../../README.md)，`docker compose up -d` 即可。**

## 这套脚本能做什么

帮你把任意一台「能跑 Docker 的 Linux 实例」变成一份**可分发的云镜像模板**：

- 别人基于这份镜像创建新实例后，**首次开机会自动**：
  - 生成全新的随机密钥（DB / Redis / JWT / AES）
  - 启动 WeKnora 全部默认容器
  - 把生成的凭证写到 `/root/weknora-credentials.txt`
  - 自删除一次性初始化脚本
- 实现「**开机即用、零私密泄漏、每实例独立密钥**」

适用平台（任意 systemd + Docker 的 Linux 都行）：

- 腾讯云轻量应用服务器（Lighthouse） / 云服务器 CVM
- AWS EC2 AMI
- 阿里云 ECS 自定义镜像
- 火山引擎 / 华为云 / Vultr Snapshot
- 本地 KVM / Proxmox 模板

各平台具体的「制作镜像 / 共享 / 上架」操作步骤，请参考 [`docs/cloud-image/`](../../docs/cloud-image/) 下对应文档。

---

## 目录结构

```
scripts/cloud-image/
├── README.md                # 本文档
├── prepare.sh               # 步骤一: 装 Docker + 拉运行时 + 装 firstboot
├── cleanup.sh               # 步骤二: 制作镜像前清理(执行后将锁定 SSH)
├── firstboot.sh             # 新实例首次开机自动执行(用户无感)
└── systemd/
    ├── weknora.service           # 开机自启 docker compose
    └── weknora-firstboot.service # 首次启动 init(执行后自删)
```

## 不需要 clone 整个 WeKnora 仓库

WeKnora 所有容器都从 Docker Hub 拉镜像（`wechatopenai/weknora-*`），Go / Python / 前端源码都不需要带到宿主机。

`docker-compose.yml` 实际从宿主机挂载到容器的只有：

```
- ./config/config.yaml      (单文件)
- ./skills/preloaded/       (目录)
```

所以镜像里需要的运行时文件**总共不到 100KB**：

| 文件 | 大小 | 用途 |
|---|---|---|
| `docker-compose.yml` | 12K | 容器编排 |
| `.env` | 12K | 环境变量 |
| `config/config.yaml` | 8K | 后端业务配置 |
| `skills/preloaded/` | 56K | Agent 预置技能 |

`prepare.sh` 用 `curl + tar` 只下载这 4 项，不 `git clone`。

## 镜像里启动哪些容器

WeKnora `docker-compose.yml` 大量服务是 **profile 限定**，本镜像只默认启动核心 5 个。

**默认启动（5 个常驻容器，开机自启）：**

| 容器 | 角色 |
|---|---|
| `frontend` | Vue UI / NGINX 反代 |
| `app` | WeKnora Go 后端 |
| `docreader` | Python 文档解析 (gRPC) |
| `postgres` (ParadeDB) | 主库 + pgvector 向量检索 + BM25 |
| `redis` | 流式输出 / 缓存 / 异步队列 |

> ParadeDB 自带 pgvector，默认场景下不需额外起向量库。

**额外预拉但不常驻：**

- `sandbox` 镜像：Agent Skills 由 app 按需 `docker run`。提前 `pull` 避免新实例首次执行 Skill 卡在下载。

**Profile 限定，不预装（用户需要时自己 `pull`）：**

| profile | 用途 |
|---|---|
| `minio` | 对象存储替代本地文件 |
| `qdrant` / `milvus` / `weaviate` / `doris` | 替代 pgvector |
| `neo4j` | GraphRAG 知识图谱 |
| `jaeger` | OpenTelemetry trace UI |
| `langfuse` | 自建 Langfuse 可观测平台 |
| `dex` | OIDC 登录 |

启用方式：

```bash
cd /opt/WeKnora
docker compose --profile neo4j up -d                 # 启用 GraphRAG
docker compose --profile langfuse up -d              # 启用自建 Langfuse
docker compose --profile qdrant up -d                # 切换到 Qdrant
```

---

## 完整流程（云无关）

```
1) 在目标云上买/装一台干净 Linux 实例（建议 4C8G+，Ubuntu 22.04）
2) SSH 进去, 拷入本目录, 执行 prepare.sh
3) 浏览器验证功能
4) 执行 cleanup.sh (清掉私密 + SSH key, 自动关机)
5) 在云控制台「制作镜像 / 创建快照 / 创建 AMI」
6) 用新镜像创建测试实例, 验证 firstboot 工作正常
7) 共享 / 公开镜像（参考各平台文档）
```

### 步骤一：在干净实例上部署

要求：systemd + 联网 + sudo 权限。推荐 Ubuntu 22.04 / Debian 12 / CentOS Stream 9。

**1. 拷入脚本（任选一种，都不用 clone 整个 WeKnora 仓库）。**

> 命令需要写入 `/opt/`，最省心的做法是先 `sudo -i` 切到 root 再粘贴。
> 如果坚持每行加 `sudo`，注意 `>>` 重定向是在你当前 shell 执行的，必须改用 `sudo tee -a`。

```bash
sudo -i      # 切到 root, 后续命令直接执行

# === 方式 A: sparse checkout (~60KB) ===
mkdir -p /opt/weknora-tools && cd /opt/weknora-tools
git init -q && git remote add origin https://github.com/Tencent/WeKnora.git
git config core.sparseCheckout true
echo "scripts/cloud-image/" >> .git/info/sparse-checkout
git pull -q --depth=1 origin main

# === 方式 B: 直接 curl (无 git 时用这个) ===
mkdir -p /opt/weknora-tools/scripts/cloud-image/systemd && cd /opt/weknora-tools
base=https://raw.githubusercontent.com/Tencent/WeKnora/main/scripts/cloud-image
for f in prepare.sh cleanup.sh firstboot.sh README.md; do
  curl -fsSL "$base/$f" -o "scripts/cloud-image/$f"
done
for f in weknora.service weknora-firstboot.service; do
  curl -fsSL "$base/systemd/$f" -o "scripts/cloud-image/systemd/$f"
done
chmod +x scripts/cloud-image/*.sh

# === 方式 C: 从本地 scp 上来 ===
# 在本机执行: scp -r scripts/cloud-image root@<实例IP>:/opt/weknora-tools/scripts/
```

**2. 执行部署：**

```bash
sudo bash /opt/weknora-tools/scripts/cloud-image/prepare.sh

# 想 pin 特定版本（推荐, 保证镜像可复现）
sudo WEKNORA_REF=v0.5.0 bash /opt/weknora-tools/scripts/cloud-image/prepare.sh

# 中国大陆 / Docker Hub 直连不通时，配合加速器（举例：腾讯云）
sudo \
  WEKNORA_REF=v0.5.0 \
  WEKNORA_GH_PROXY=https://gh-proxy.com/ \
  DOCKER_REGISTRY_MIRROR=https://mirror.ccs.tencentyun.com \
  bash /opt/weknora-tools/scripts/cloud-image/prepare.sh
```

> `WEKNORA_GH_PROXY` 用于加速 GitHub tarball 下载（步骤二），
> `DOCKER_REGISTRY_MIRROR` 用于加速 Docker Hub 镜像拉取（步骤三/四）。
> 不同云厂商加速器地址不同：腾讯云 `https://mirror.ccs.tencentyun.com`、
> 阿里云 `https://<your-id>.mirror.aliyuncs.com`、华为云 `https://<id>.mirror.swr.myhuaweicloud.com`。

`prepare.sh` 会：

1. 安装 Docker / Docker Compose plugin（已装则跳过）
2. 用 `curl + tar` 下载 4 个运行时文件到 `/opt/WeKnora`
3. 拉取并启动默认 5 个容器 + 预拉 sandbox 镜像
4. 安装 `weknora.service`（开机自启）+ `weknora-firstboot.service`（首启 init）

完成后访问 `http://<公网IP>`。

### 步骤二：验证

至少验证：

- 能注册管理员、能登录
- 能创建一个知识库
- 能上传一个文档并完成解析
- 能进行一次问答

```bash
sudo docker compose -f /opt/WeKnora/docker-compose.yml ps
curl -f http://localhost:8080/health
```

### 步骤三：清理并制作镜像

> **重要**：`cleanup.sh` 会删除所有 SSH 公钥、清空日志、清空数据库与 docker volume。执行后**不要再 SSH 进来**，直接去云控制台关机制作镜像。

```bash
sudo bash /opt/weknora-tools/scripts/cloud-image/cleanup.sh
```

执行完会自动 `poweroff`。然后到对应云控制台按其文档制作镜像。

### 新实例首次开机行为

用户用你的镜像创建实例后，第一次开机时 `weknora-firstboot.service` 会：

1. 生成随机的 `DB_PASSWORD` / `REDIS_PASSWORD` / `JWT_SECRET` / `SYSTEM_AES_KEY` / `TENANT_AES_KEY`
2. 写回 `/opt/WeKnora/.env`
3. `docker compose up -d` 启动全部服务
4. 把生成的凭证写到 `/root/weknora-credentials.txt`（仅 root 可读）
5. 把自己 disable + 删除自己（确保只跑一次）

之后每次开机都由 `weknora.service` 接管。

> **注意**：`firstboot.sh` 默认**不**禁用注册（`DISABLE_REGISTRATION=false`），第一个注册的人会成为管理员。
> 凭证文件里有「请尽快注册以防被抢注」的醒目提示。需要更严格控制可在 `firstboot.sh` 的 `replace` 调用列表中追加一行 `replace DISABLE_REGISTRATION true` 后再重制镜像。

---

## 升级镜像版本

镜像里没有 git 仓库，升级直接重跑 `prepare.sh`（会覆盖 4 个运行时文件，**不动 `.env` 和 docker volume 数据**）：

```bash
sudo WEKNORA_REF=v0.6.0 bash /opt/weknora-tools/scripts/cloud-image/prepare.sh
sudo bash    /opt/weknora-tools/scripts/cloud-image/cleanup.sh   # 制作新镜像前
```

> **打新镜像时想顺便清掉旧版本镜像层**（每个旧 weknora-* tag 几百 MB，4 个镜像 ~2-4GB）：
> ```bash
> sudo PRUNE_OLD_IMAGES=true WEKNORA_REF=v0.6.0 \
>   bash /opt/weknora-tools/scripts/cloud-image/prepare.sh
> ```
> 默认 `false` 是为了保留回滚路径。打镜像前确认新版本稳定后再开。

## 安全注意事项

- 镜像里**不要**预置任何 LLM API Key、Langfuse Key、个人 SSH key
- 数据库 / Redis / MinIO 端口默认仅对 docker 网络可见，不要在云防火墙里对外开放
- `/root/weknora-credentials.txt` 用 `umask 077` 创建，仅 root 可读
- 每次重制镜像前必须执行 `cleanup.sh`，避免泄漏上一份测试数据 / SSH key / machine-id

## 各云平台具体操作

| 平台 | 文档 |
|---|---|
| 腾讯云轻量应用服务器 / CVM | [`docs/cloud-image/tencent-lighthouse.md`](../../docs/cloud-image/tencent-lighthouse.md) |
| AWS EC2 AMI | （欢迎贡献） |
| 阿里云 ECS | （欢迎贡献） |
