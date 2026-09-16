# TreeRAG / WeKnora 内网离线部署包

面向：**已安装 Docker、Docker Compose、Nginx 的 Ubuntu 内网机**。  
方式：**有网机打离线镜像包 → 拷贝本目录 → 内网 `docker load` + `compose up` → Nginx 反代**。

当前预置版本：`v0.8.23`  
预置模型网关：**内蒙古人社AI网关**（`http://10.55.45.50:38080/v1`）

> **本目录已内置 `images/` 离线镜像**（linux/amd64，约 9.6GB）。  
> 内网机可直接：`./scripts/load-images.sh && ./scripts/deploy.sh`

---

## 目录结构

```text
offline-intranet/
├── README.md                 # 本说明
├── VERSION                   # 应用版本号（无 v 前缀）
├── .env                      # 已预填部署与模型配置（含密钥，勿外传）
├── docker-compose.yml        # 最小生产编排（5 容器）
├── config/
│   ├── config.yaml
│   └── builtin_models.yaml   # 对话 / 向量 / 重排内置模型
├── nginx/
│   └── weknora.conf          # 宿主机 Nginx → 127.0.0.1:18080
├── skills/preloaded/         # 可选预装 Skills（可空）
├── images/                   # 离线镜像 tar / 汇总包（由 pack 脚本生成）
└── scripts/
    ├── pack-images.sh        # 【有网机】拉镜像并导出
    ├── download-from-ci.sh   # 从 Actions 制品拉取镜像
    ├── load-images.sh        # 【内网机】docker load
    ├── deploy.sh             # 【内网机】compose up + 可选装 Nginx
    ├── install-nginx.sh      # 安装 Nginx 站点
    ├── verify.sh             # 健康检查 + 网关连通性
    └── undeploy.sh           # 停止服务（默认保留数据卷）
```

---

## 架构

```text
浏览器
  → 宿主机 Nginx :80/:443
    → 127.0.0.1:18080  TreeRAG-frontend (容器内 nginx)
      → app:8080
         ↳ postgres / redis / docreader
         ↳ 内蒙古人社AI网关 10.55.45.50:38080
```

容器端口只绑在本机回环，**不直接对公网暴露**。

| 容器 | 镜像 | 说明 |
|------|------|------|
| frontend | `tree-rag-ui:v0.8.23` | Web UI |
| app | `tree-rag-app:v0.8.23` | API |
| docreader | `tree-rag-docreader:v0.8.23` | 文档解析 |
| postgres | `paradedb/paradedb:v0.22.2-pg17` | DB + 向量检索 |
| redis | `redis:7.0-alpine` | 队列 / 流 |

---

## 一、拷贝到内网机

本包已含镜像时，直接整目录拷贝即可：

```bash
# 在制作机（本仓库）
rsync -avP deploy/offline-intranet/ user@intranet:/opt/treerag/
# 或打成一个总包
tar -C deploy -cvf treerag-offline-intranet-v0.8.23.tar offline-intranet
```

> 合计约 **10GB+**，请预留磁盘与传输时间。`.env` 含密钥，仅限内网介质传递。

### 需要重新打镜像包时（可选）

有网且可登录 ACR：

```bash
cd deploy/offline-intranet
export ACR_USERNAME='...' ACR_PASSWORD='...'
./scripts/pack-images.sh
```

或触发 GitHub Actions：`Pack Offline Images`，再：

```bash
./scripts/download-from-ci.sh
```

---

## 二、内网 Ubuntu：导入并启动

```bash
cd /opt/treerag   # 以实际路径为准

# 1) 导入镜像
./scripts/load-images.sh

# 2) 按需改 .env
#    - APP_EXTERNAL_URL / FRONTEND_BASE_URL 改成对外访问地址
#    - 数据库/Redis/JWT 密码建议现场轮换
#    - 若 embedding 维度不是 1024，见下文「向量维度」

# 3) 启动（默认会尝试安装 Nginx 站点；跳过则 INSTALL_NGINX=0）
./scripts/deploy.sh
# INSTALL_NGINX=0 ./scripts/deploy.sh

# 4) 自检
./scripts/verify.sh
```

浏览器访问：`http://<服务器IP>/`（经 Nginx）。  
本机直连前端：`http://127.0.0.1:18080/`。

首次打开 Web UI，使用**空库自举**注册第一个系统管理员账号（`DISABLE_REGISTRATION=true` 时仅首个用户可注册）。

---

## 三、Nginx

配置文件：`nginx/weknora.conf`（反代 `127.0.0.1:18080`）。

```bash
./scripts/install-nginx.sh
# 或手动：
# sudo cp nginx/weknora.conf /etc/nginx/sites-available/weknora.conf
# sudo ln -sf /etc/nginx/sites-available/weknora.conf /etc/nginx/sites-enabled/
# sudo nginx -t && sudo systemctl reload nginx
```

有 HTTPS 证书时，编辑配置文件中的 SSL 示例段，并同步把 `.env` 里的 `APP_EXTERNAL_URL` / `FRONTEND_BASE_URL` 改为 `https://...`。

---

## 四、预置模型（内蒙古人社AI网关）

| 能力 | 模型名 | 接口 |
|------|--------|------|
| 对话 | `Qwen3.6` | `POST /v1/chat/completions` |
| 向量 | `bge` | `POST /v1/embeddings` |
| 重排 | `bge-reranker-v2-m3` | `POST /v1/rerank` |

- Base URL：`http://10.55.45.50:38080/v1`
- API Key：已写入 `.env`（`LLM_API_KEY` 等）
- `builtin_models.yaml` 在 **每次 app 启动** 时 UPSERT 内置模型
- **SSRF**：已配置 `SSRF_WHITELIST=10.55.45.50,10.0.0.0/8`，否则容器访问内网 IP 会被拦截

容器内需能路由到 `10.55.45.50:38080`（与宿主机同网段通常即可）。若不通：

```bash
docker compose exec app curl -sS -m 5 http://10.55.45.50:38080/v1/models \
  -H "Authorization: Bearer $LLM_API_KEY"
```

### 向量维度

`config/builtin_models.yaml` 中 embedding `dimension` 默认 **1024**。  
若入库/检索报维度错误，在内网机对网关实测后改 YAML 并重启：

```bash
# 看返回向量长度
curl -sS http://10.55.45.50:38080/v1/embeddings \
  -H "Authorization: Bearer <KEY>" \
  -H "Content-Type: application/json" \
  -d '{"model":"bge","input":"test"}' | python3 -c "import sys,json; d=json.load(sys.stdin); print(len(d['data'][0]['embedding']))"

# 修改 config/builtin_models.yaml 中 dimension 后：
docker compose restart app
```

---

## 五、常用运维

```bash
docker compose ps
docker compose logs -f app
docker compose restart app
./scripts/undeploy.sh          # 停服务，保留数据
docker compose down -v         # 危险：清空数据库与文件卷
```

升级到新版本：在有网机改 `VERSION` / `.env` 的 `WEKNORA_VERSION`，重新 `pack-images.sh`，内网 `load-images.sh` 后 `docker compose up -d`。

---

## 六、安全提示

1. `.env` 含数据库密码、JWT、AES、网关 API Key，**仅限内网分发**，勿提交到公开仓库。
2. 上线前建议轮换 `DB_PASSWORD` / `REDIS_PASSWORD` / `JWT_SECRET` / `SYSTEM_AES_KEY`（AES 须恰好 32 字节）。
3. 修改 AES Key 会导致已加密的模型凭据无法解密，需重新下发模型配置。

---

## 七、故障排查速查

| 现象 | 处理 |
|------|------|
| `load-images` 报无 tar | 有网机未执行 `pack-images.sh`，或未拷贝 `images/` |
| app 不健康 | `docker compose logs app`；检查 postgres/redis/docreader |
| 模型 401 / 连不上 | 检查网关 IP、Key、容器出网；看 SSRF 白名单 |
| 页面 502 | Nginx 是否指向 `127.0.0.1:18080`；`docker compose ps frontend` |
| 上传大文件失败 | `.env` 的 `MAX_FILE_SIZE_MB` 与 Nginx `client_max_body_size` 保持一致 |
