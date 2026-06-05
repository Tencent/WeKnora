# 常见问题

## 1. 如何查看日志？
```bash
docker compose logs -f app docreader postgres
```

## 2. 如何启动和停止服务？
```bash
# 启动服务
./scripts/start_all.sh

# 停止服务
./scripts/start_all.sh --stop

# 清空数据库
./scripts/start_all.sh --stop && make clean-db
```

## 3. 服务启动后无法正常上传文档？

通常是Embedding模型和对话模型没有正确被设置导致。按照以下步骤进行排查

1. 查看`.env`配置中的模型信息是否配置完整，其中如果使用ollama访问本地模型，需要确保本地ollama服务正常运行，同时在`.env`中的如下环境变量需要正确设置:
```bash
# LLM Model
INIT_LLM_MODEL_NAME=your_llm_model
# Embedding Model
INIT_EMBEDDING_MODEL_NAME=your_embedding_model
# Embedding模型向量维度
INIT_EMBEDDING_MODEL_DIMENSION=your_embedding_model_dimension
# Embedding模型的ID，通常是一个字符串
INIT_EMBEDDING_MODEL_ID=your_embedding_model_id
```

如果是通过remote api访问模型，则需要额外提供对应的`BASE_URL`和`API_KEY`:
```bash
# LLM模型的访问地址
INIT_LLM_MODEL_BASE_URL=your_llm_model_base_url
# LLM模型的API密钥，如果需要身份验证，可以设置
INIT_LLM_MODEL_API_KEY=your_llm_model_api_key
# Embedding模型的访问地址
INIT_EMBEDDING_MODEL_BASE_URL=your_embedding_model_base_url
# Embedding模型的API密钥，如果需要身份验证，可以设置
INIT_EMBEDDING_MODEL_API_KEY=your_embedding_model_api_key
```

当需要重排序功能时，需要额外配置Rerank模型，具体配置如下：
```bash
# 使用的Rerank模型名称
INIT_RERANK_MODEL_NAME=your_rerank_model_name
# Rerank模型的访问地址
INIT_RERANK_MODEL_BASE_URL=your_rerank_model_base_url
# Rerank模型的API密钥，如果需要身份验证，可以设置
INIT_RERANK_MODEL_API_KEY=your_rerank_model_api_key
```

2. 查看主服务日志，是否有`ERROR`日志输出

## 4. 没有图片或者显示无效的图片链接？

当使用多模态功能时，如果遇到图片无法显示或显示无效链接的问题，请按照以下步骤排查：

### 1. 确认多模态功能已正确配置

在知识库设置中开启**高级设置 - 多模态功能**，并在界面中配置相应的多模态模型。

### 2. 确认 MinIO 服务已启动

如果多模态功能配置使用的是 MinIO 存储，需要确保 MinIO 镜像已正确启动：

```bash
# 启动 MinIO 服务
docker-compose --profile minio up -d

# 或者启动完整服务（包括 MinIO、Jaeger、Neo4j、Qdrant）
docker-compose --profile full up -d
```

### 3. 检查 MinIO Bucket 权限

确保 MinIO 对应的 bucket 具有正确的读写权限：

1. 访问 MinIO 控制台：`http://localhost:9001`（默认端口）
2. 使用 `.env` 中配置的 `MINIO_ACCESS_KEY_ID` 和 `MINIO_SECRET_ACCESS_KEY` 登录
3. 进入对应的 bucket，检查并设置访问策略为**公开读取**或**公开读写**

**重要提示**：
- Bucket 名称不要包含特殊字符（包括中文），建议使用小写字母、数字和连字符
- 如果无法修改现有 bucket 的权限，可以在配置中填入一个不存在的 bucket 名称，本项目会自动创建对应的 bucket 并设置好正确的权限

### 4. 配置 MINIO_PUBLIC_ENDPOINT

在 `docker-compose.yml` 文件中，`MINIO_PUBLIC_ENDPOINT` 变量默认配置为 `http://localhost:9000`。

**重要提示**：如果你需要从其他设备或容器访问图片，`localhost` 可能无法正常工作，需要将其替换为本机的实际 IP 地址：


## 5. 平台兼容性说明

**重要提示**：`OCR_BACKEND=paddle` 模式在部分平台上可能无法正常运行。如果遇到 PaddleOCR 启动失败的问题，请选择以下解决方案

### 方案一：关闭 OCR 识别

在 `docker-compose.yml` 文件的 `docreader` 服务中删除 `OCR_BACKEND` 配置，然后重启 docreader 服务

**注意**：设置为 `no_ocr` 后，文档解析将不会使用 OCR 功能，这可能会影响图片和扫描文档的文字识别效果。

### 方案二：使用外部 OCR 模型（推荐）

如果需要 OCR 功能，可以使用外部的视觉语言模型（VLM）来替代 PaddleOCR。在 `docker-compose.yml` 文件的 `docreader` 服务中配置：

```yaml
environment:
  - OCR_BACKEND=vlm
  - OCR_API_BASE_URL=${OCR_API_BASE_URL:-}
  - OCR_API_KEY=${OCR_API_KEY:-}
  - OCR_MODEL=${OCR_MODEL:-}
```

然后重启 docreader 服务

**优势**：使用外部 OCR 模型可以获得更好的识别效果，且不受平台限制。

## 6. 如何使用数据分析功能？

在使用数据分析功能前，请确保智能体已配置相关工具：

1. **智能推理**：需在工具配置中勾选以下两个工具：
   - 查看数据元信息
   - 数据分析

2. **快速问答智能体**：无需手动选择工具，即可直接进行简单的数据查询操作。

### 注意事项与使用规范

1. **支持的文件格式**
   - 目前仅支持 **CSV** (`.csv`) 和 **Excel** (`.xlsx`, `.xls`) 格式的文件。
   - 对于复杂的 Excel 文件，如果读取失败，建议将其转换为标准的 CSV 格式后重新上传。

2. **查询限制**
   - 仅支持 **只读查询**，包括 `SELECT`, `SHOW`, `DESCRIBE`, `EXPLAIN`, `PRAGMA` 等语句。
   - 禁止执行任何修改数据的操作，如 `INSERT`, `UPDATE`, `DELETE`, `CREATE`, `DROP` 等。

## 7. 页面里刚保存的配置几秒后又消失了？

这类问题通常不是配置真的被系统清掉了，而是浏览器代理、缓存或插件干扰导致前端读到了异常响应，页面随后又被旧状态覆盖。

建议按下面顺序排查：

1. 先关闭浏览器代理、抓包工具、自动改写请求的插件，再重新打开页面。
2. 确认浏览器没有把 `localhost` 或当前访问域名走代理；如果配置了 PAC，请将 `localhost`、`127.0.0.1` 和实际部署域名加入直连名单。
3. 强制刷新页面，或直接使用无痕窗口重新登录后再保存一次配置。
4. 打开浏览器开发者工具的 `Network` 面板，确认保存配置相关请求返回的是最新内容，且没有被代理改写、缓存命中或重定向到其他环境。
5. 如果是调试模式部署，可尝试重启 `app` 服务后再验证一次：

```bash
docker compose restart app
```

如果重启后短时间恢复正常，但再次访问又出现相同现象，仍应优先检查浏览器代理、缓存和多环境串连问题，而不是直接判断为后端配置丢失。

## 8. SSRF 校验白名单（`SSRF_WHITELIST`）

可选配置。在 `.env` 中设置 `SSRF_WHITELIST`，用于在 URL 校验等环节将指定目标加入白名单，从而绕过常规 SSRF 限制。值为逗号分隔的多条规则，每条可以是：

- **精确域名**：如 `api.internal`
- **通配域名**：如 `*.example.com`
- **IPv4**：如 `203.0.113.5`
- **IPv6**：如 `2001:db8::1`（不要带方括号）
- **CIDR**：如 `10.0.0.0/8`、`2001:db8::/32`

列入白名单的地址会在 URL 校验等处绕过常规 SSRF 规则，**生产环境请谨慎配置**，仅加入确实需要且可信的目标。

示例（与 `.env.example` 一致，可按需取消注释并修改）：

```bash
# SSRF_WHITELIST=internal.service,*.corp.example,172.16.0.0/12,2001:db8::1,fd00::/8
```


## 9. 如何开启和查看 Langfuse 可观测性追踪？

WeKnora 支持通过 Langfuse 对 Agent 的 ReAct 循环、大模型 Token 消耗、工具调用以及异步任务流水线进行全链路追踪。

**开启步骤**：
1. 准备一个可用的 Langfuse 实例（支持云端版或私有部署版）。
2. 在 `.env` 文件中配置以下环境变量：
```bash
LANGFUSE_PUBLIC_KEY=pk-lf-...
LANGFUSE_SECRET_KEY=sk-lf-...
LANGFUSE_HOST=https://cloud.langfuse.com # 或你的私有部署地址
```
3. 重启服务后，系统会自动对所有支持的模型调用和 Agent 运行轨迹进行追踪，你可以在 Langfuse 的 Traces 面板中直观地看到每次对话和后台任务的详细执行瀑布图与 Token 统计。

## 10. 什么是 Wiki 模式？如何使用？

Wiki 模式允许 Agent 根据原始文档自动生成并维护一套结构化、相互链接的 Markdown Wiki 知识库，从而实现复杂知识的体系化沉淀和图谱化。

**使用方法**：
1. 进入指定**知识库的设置** -> **索引策略 (Indexing Strategy)**。
2. 开启 **Wiki** 索引功能（可同时结合开启**知识图谱**）。
3. 当你向该知识库上传文档时，系统会自动触发异步任务，通过大模型提取文档中的实体与核心概念，并自动生成结构化的 Wiki 页面及页面间的知识图谱链接。
4. 你可以在该知识库的“Wiki”标签页中，使用专用的 Wiki 浏览器查阅、管理页面，并通过可视化的知识图谱查看不同内容之间的关联关系。

## 11. 升级到 0.6.0 后，原本能做的操作变成了「权限不足」？

0.6.0 引入了租户内 RBAC（角色矩阵 + 资源归属），所有写入接口都会按角色 + `creator_id` 鉴权。常见现象：

- **看得到但点不动**：你大概率是该资源的 `Viewer` 或非创建者的 `Contributor`，UI 已经把写操作隐藏/置灰。检查 **用户菜单 → 当前工作区** 角色徽章。
- **共享空间里的 KB / Agent**：他人共享给你的 KB 默认按 `Viewer` 看待；要写需要在源租户里被授予 `Admin+`。
- **API Key 调用**：`X-API-Key` 合成虚拟用户固定为所属租户的 `Admin`（仅删除租户需 `Owner`），脚本一般无需迁移。
- **跨租户超管**：要 `User.CanAccessAllTenants=true` 且 `enable_cross_tenant_access=true`，并通过 `X-Tenant-ID` 切租户。

如需临时回退到「仅审计、不拦截」灰度窗口，可在配置里设置 `tenant.enable_rbac=false`（或环境变量 `WEKNORA_TENANT_ENABLE_RBAC=false`）。完整的角色矩阵和归属链请见 [`docs/RBAC说明.md`](./RBAC说明.md)。

## 12. 为什么登录后没有自动回到上次的工作区？

升级到 0.6.0 后系统会记住「最后活跃工作区」并在登录后自动恢复。若仍未恢复，通常是：

1. 浏览器清理了 LocalStorage / 切换了浏览器；
2. 你最后访问的那个工作区已经把你移除（`/leave` 或被管理员剔除）— 系统会回退到默认租户；
3. JWT 中携带了 `tenant_id` 但已无效 — 退出重登录即可。

## 13. 如何让多人协作时正确分配权限？

按照 [`docs/RBAC说明.md`](./RBAC说明.md) 的角色矩阵：

- 只读用户 → `Viewer`
- 普通成员（上传文档、维护「自己」的 KB / Agent）→ `Contributor`
- 运维人员（管理共享模型、向量库、解析器等基础设施）→ `Admin`
- 租户所有者（拥有删除租户权限，每租户唯一）→ `Owner`

如果你希望开启「invite-only」（不允许自助注册到本租户），可在租户设置里打开邀请制，并通过「邀请」入口签发邀请码或链接。

## 14. 文档解析卡在「处理中」/ 解析追踪时间线打不开怎么办？

0.6.1 起每个文档解析都会记录一棵 Langfuse 风格的 Span 树（`knowledge_processing_spans` 表），可在知识库卡片菜单或卡片上的「Trace」入口打开侧边时间线，逐阶段查看进度。常见情况：

- **文档长时间停在「处理中」**：先打开时间线看是哪个阶段没有推进（解析 / 切分 / 向量化 / 后处理）。0.6.1 已修复多数「卡死」场景，并加入看门狗轮询；如确认是某次解析挂死，可在时间线面板点击「中止解析」，文档会进入 finalizing 后处理状态后结束。
- **时间线一直显示「更新中」但无数据**：通常是轮询请求静默失败（网络 / 反向代理截断 SSE）。0.6.1 会显式暴露轮询失败，刷新页面或检查 Nginx 是否缓冲了响应即可。
- **升级后没有时间线数据**：确认数据库迁移 `000055_knowledge_processing_spans`、`000056_knowledge_pending_subtasks` 已执行（服务启动会自动迁移）。

## 15. 如何启用 OpenSearch 作为向量库？

0.6.1 新增了 OpenSearch 向量库驱动（k-NN）。在 **设置 → 向量库** 中新增 OpenSearch 引擎并填写连接地址、凭据即可；KB 可绑定该向量库。注意：

- 连接地址会经过 SSRF 策略校验，内网 / 回环地址需符合放行规则；可用「测试连接」先行校验。
- 集成测试与索引映射细节见 [`docs/dev/opensearch-integration-test.md`](./dev/opensearch-integration-test.md)。

## 16. 内置模型（builtin models）如何用 YAML 声明式管理？

0.6.1 起平台内置模型由 `config/builtin_models.yaml` 声明式驱动，支持 `${ENV}` 变量插值，并通过 `managed_by` 字段与漂移巡检保持数据库与 YAML 一致。常见问题：

- **改了 YAML 不生效**：内置模型在服务启动时做生命周期对账（drift sweep）；确认重启了服务，且条目通过了 schema 校验（ID 长度、必填字段）。
- **Docker 下环境变量未注入**：`builtin_models` 依赖 `env_file` 数组形式注入变量，确认 compose 中按数组形式挂载了 `.env`。
- 参考样例：`config/builtin_models.yaml.example`。

## 17. 系统管理员（System Admin）与平台设置怎么用？

0.6.1 引入了系统管理员与统一平台设置面板（含平台审计日志），与租户内 RBAC 区分：系统管理员管理的是「平台级」配置，而非单个租户内的资源。首次启用需通过系统管理员 bootstrap 流程晋升首个管理员；撤销管理员权限有安全防护（避免误撤导致无人可管）。相关迁移为 `000053_system_admin_and_settings`。

## P.S.
如果以上方式未解决问题，请在issue中描述您的问题，并提供必要的日志信息辅助我们进行问题排查
