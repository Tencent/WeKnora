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

## P.S.
如果以上方式未解决问题，请在issue中描述您的问题，并提供必要的日志信息辅助我们进行问题排查
