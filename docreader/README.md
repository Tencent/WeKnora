# DocReader Service

DocReader 是 WeKnora 项目中负责文档解析和处理的 gRPC 服务。它支持多种文档格式的读取、OCR 识别、多模态处理等功能。

## Docker Compose 环境变量配置

在 `docker-compose.yml` 文件中，docreader 服务配置了以下环境变量：

```yaml
docreader:
  image: wechatopenai/weknora-docreader:${WEKNORA_VERSION:-latest}
  environment:
    - MINIO_ENDPOINT=minio:9000
    - MINIO_PUBLIC_ENDPOINT=http://localhost:${MINIO_PORT:-9000}
    - MINERU_ENDPOINT=${MINERU_ENDPOINT:-}
    - MAX_FILE_SIZE_MB=${MAX_FILE_SIZE_MB:-}
```

### 环境变量说明

#### 1. MINIO_ENDPOINT

- **说明**: MinIO 服务的内部访问地址（容器间通信）
- **默认值**: `minio:9000`
- **用途**: DocReader 服务使用此地址连接到 MinIO 对象存储服务，用于读取和存储文档处理过程中的文件
- **配置示例**:
  ```yaml
  - MINIO_ENDPOINT=minio:9000  # Docker 网络内部地址
  ```

#### 2. MINIO_PUBLIC_ENDPOINT

- **说明**: MinIO 服务的公开访问地址（外部访问）
- **默认值**: `http://localhost:9000`
- **用途**: 用于生成可从外部访问的文件 URL，例如在文档解析后返回图片链接时使用
- **重要提示**: 
  - 如果需要从其他设备或容器访问，需要将 `localhost` 替换为实际的 IP 地址
  - 可以在 `.env` 文件中配置 `MINIO_PORT` 来自定义端口
- **配置示例**:
  ```bash
  # .env 文件
  MINIO_PORT=9000
  ```
  或直接在 docker-compose.yml 中修改：
  ```yaml
  - MINIO_PUBLIC_ENDPOINT=http://192.168.1.100:9000  # 使用实际 IP
  ```

#### 3. MINERU_ENDPOINT

- **说明**: MinerU 服务的访问地址（可选）
- **默认值**: 空（不使用 MinerU）
- **用途**: MinerU 是一个高级文档解析服务，支持更复杂的文档结构识别和处理。配置此变量后，DocReader 可以调用 MinerU 进行文档解析
- **配置示例**:
  ```bash
  # .env 文件
  MINERU_ENDPOINT=http://mineru-service:8080
  ```

#### 4. MAX_FILE_SIZE_MB

- **说明**: 允许上传的最大文件大小（单位：MB）
- **默认值**: `50` MB
- **用途**: 限制 gRPC 服务接收的文件大小，防止过大的文件导致服务崩溃或性能问题
- **配置示例**:
  ```bash
  # .env 文件
  MAX_FILE_SIZE_MB=100  # 允许最大 100MB 的文件
  ```

## 其他可配置的环境变量

除了 docker-compose.yml 中已配置的变量外，DocReader 还支持以下环境变量（可根据需要添加）：

### gRPC 配置

- `DOCREADER_GRPC_MAX_WORKERS`: gRPC 服务的最大工作线程数（默认：4）
- `DOCREADER_GRPC_PORT`: gRPC 服务监听端口（默认：50051）

### 解析器资源控制

- `DOCREADER_MARKITDOWN_MAX_WORKERS`: MarkItDown 解析的最大并发数（默认：1，设为 0 可关闭限流）
- `DOCREADER_PDF_RENDER_MAX_WORKERS`: 扫描 PDF 渲染为图片的最大并发数（默认：1，设为 0 可关闭限流）
- `DOCREADER_PDF_RENDER_DPI`: 扫描 PDF 渲染 DPI（默认：200）
- `DOCREADER_PDF_JPEG_QUALITY`: 扫描 PDF 输出 JPEG 质量（默认：85，范围会自动限制在 1-95）

### 上传 HTML 的正文抽取

- `DOCREADER_HTML_MAIN_CONTENT`: 上传的 `.html` / `.htm` 文件是否只索引正文（默认：`false`）。开启后用 trafilatura 抽取正文，与 URL 导入路径一致，导航栏 / 侧栏 / 页脚不再进入索引；抽取结果过短（< 250 字符）、不足整页转换的 15%，或抽取器额外重复正文段落时自动回退整页转换。默认关闭，升级后既有行为不变。 正文图片引用会保留在 Markdown 中，供 Go 侧解析；Python 侧不会下载远程图片。

### OCR / VLM

扫描 PDF 会被渲染为 JPEG 图片后交由 Go App 调用 VLM 提取文字。DocReader 可选用 RapidOCR 为文档照片做几何预处理，返回摆正、裁剪后的图片；最终文字识别仍由 Go App 的 VLM 完成。

在知识库的「图像处理 → 智能模式 → 文档预处理」中显式选择「自动」或「总是」才会启用。默认「关闭」，未保存该设置的旧知识库也保持关闭。「总是」仍受前面的 OCR 策略限制。此功能需要 gRPC docreader；HTTP/云端 reader 使用原图。

预处理使用独立的 RPC 执行器和受限子进程，不占用普通 `Read` / `ReadStream` 的工作线程。没有可用子进程时立即返回原图，不等待推理队列。一个子进程串行复用一套模型，空闲 60 秒后退出，释放模型内存。关闭服务、请求取消或超过本地/RPC 中较短的截止时间时，终止并回收对应子进程，后续请求会创建替代进程。

| 环境变量 | 默认值 | 含义 |
|---|---:|---|
| `DOCREADER_PREPROCESS_MAX_WORKERS` | `1` | 每个 docreader 实例最多同时运行的预处理子进程 |
| `DOCREADER_PREPROCESS_TIMEOUT_SECONDS` | `30` | 单次处理上限，含启动、模型加载和编码；RPC 截止时间可以进一步缩短 |
| `DOCREADER_PREPROCESS_THREADS` | `2` | 每个 ONNX 模型的算子内线程数及 OpenCV/数值库线程设置；算子间线程数为 1 |
| `DOCREADER_PREPROCESS_MEMORY_MB` | `2048` | 每个子进程的内存预算，单位 MiB；Linux 同时设置硬地址空间上限 `RLIMIT_AS`，父进程监测 RSS，超额即终止 |
| `DOCREADER_PREPROCESS_MAX_PIXELS` | `16000000` | 解码前检查图片头的像素上限，过大的输入直接回退 |
| `DOCREADER_PREPROCESS_PROBE_EDGE` | `2000` | 仅用于检测的工作图最长边；RapidOCR 自身也把检测器限在 2000，裁切仍回原图 |
| `DOCREADER_PREPROCESS_MAX_EDGE` | `2000` | 裁切结果的最长边上限，超过才按比例缩小；不改写原图分辨率 |

所有限制必须为正数；非法值回落到默认值，`0` 不会关闭限制。编码后的输入最多为 20 MiB。未启用此功能时不启动子进程、不加载 RapidOCR 模型。

内存预算按子进程计算，部署时还需为主进程和普通解析预留空间。Linux 的地址空间上限包含共享库与映射文件，可能在 RSS 达到预算前拒绝分配；这类失败也会回退原图。macOS 开发环境使用 RSS 监测，监测间隔约 50 ms，不能代替容器的总内存限制。提高并发会同时放大模型内存和 CPU 开销。

`PreprocessImageResponse.status` 会区分 `busy`、`timeout`、`cancelled`、`memory_limit`、`image_too_large`、`worker_failed` 等回退原因。Go 侧沿用原图 OCR 的降级路径。几何阈值 `WEKNORA_PREPROCESS_*` 在预处理子进程启动时记录。

### 存储配置

DocReader 支持多种存储后端：

#### MinIO/S3 存储（推荐）

- `STORAGE_TYPE`: 设置为 `minio`
- `MINIO_ACCESS_KEY_ID`: MinIO 访问密钥 ID（默认：minioadmin）
- `MINIO_SECRET_ACCESS_KEY`: MinIO 访问密钥（默认：minioadmin）
- `MINIO_BUCKET_NAME`: MinIO 存储桶名称（默认：WeKnora）
- `MINIO_PATH_PREFIX`: 文件路径前缀
- `MINIO_USE_SSL`: 是否使用 SSL（默认：false）

#### 腾讯云 COS 存储

- `STORAGE_TYPE`: 设置为 `cos`
- `COS_SECRET_ID`: COS 访问密钥 ID
- `COS_SECRET_KEY`: COS 访问密钥
- `COS_REGION`: COS 区域
- `COS_BUCKET_NAME`: COS 存储桶名称
- `COS_APP_ID`: COS 应用 ID
- `COS_PATH_PREFIX`: 文件路径前缀
- `COS_ENABLE_OLD_DOMAIN`: 是否使用旧域名（默认：true）

#### 阿里云 OSS 存储

- `STORAGE_TYPE`: 设置为 `oss`
- `OSS_ACCESS_KEY_ID`: OSS 访问密钥 ID
- `OSS_ACCESS_KEY_SECRET`: OSS 访问密钥
- `OSS_ENDPOINT`: OSS 端点（如 `oss-cn-hangzhou.aliyuncs.com`）
- `OSS_BUCKET_NAME`: OSS 存储桶名称
- `OSS_REGION`: OSS 区域（如 `cn-hangzhou`）
- `OSS_PATH_PREFIX`: 文件路径前缀

### 代理配置

如果需要通过代理访问外部服务：

- `EXTERNAL_HTTP_PROXY`: HTTP 代理地址
- `EXTERNAL_HTTPS_PROXY`: HTTPS 代理地址

### 图像处理配置

扫描 PDF 会被渲染为 JPEG 图片后交给 Go App 侧 OCR 处理。如果在导入多个大 PDF 时出现资源占用过高，
可以优先调低 `DOCREADER_PDF_RENDER_MAX_WORKERS` 或 `DOCREADER_MARKITDOWN_MAX_WORKERS`。

## 配置示例

### 基础配置（使用 MinIO）

```yaml
docreader:
  environment:
    - MINIO_ENDPOINT=minio:9000
    - MINIO_PUBLIC_ENDPOINT=http://localhost:9000
    - MAX_FILE_SIZE_MB=50
```

### 高级配置（启用 MinerU）

```yaml
docreader:
  environment:
    - MINIO_ENDPOINT=minio:9000
    - MINIO_PUBLIC_ENDPOINT=http://192.168.1.100:9000
    - MINERU_ENDPOINT=http://mineru:8080
    - MAX_FILE_SIZE_MB=100
```

### 使用腾讯云 COS

```yaml
docreader:
  environment:
    - STORAGE_TYPE=cos
    - COS_SECRET_ID=your_secret_id
    - COS_SECRET_KEY=your_secret_key
    - COS_REGION=ap-guangzhou
    - COS_BUCKET_NAME=your-bucket
    - COS_APP_ID=your_app_id
    - MAX_FILE_SIZE_MB=50
```

### 使用阿里云 OSS

```yaml
docreader:
  environment:
    - STORAGE_TYPE=oss
    - OSS_ACCESS_KEY_ID=your_access_key_id
    - OSS_ACCESS_KEY_SECRET=your_access_key_secret
    - OSS_ENDPOINT=oss-cn-hangzhou.aliyuncs.com
    - OSS_BUCKET_NAME=your-bucket
    - OSS_REGION=cn-hangzhou
    - MAX_FILE_SIZE_MB=50
```

## 常见问题

### 1. DocReader 服务无法启动？

检查容器日志中是否存在依赖缺失或权限相关错误，必要时确认 `MINIO_ENDPOINT` / 存储相关环境变量是否正确配置。

### 2. 图片无法显示？

检查 `MINIO_PUBLIC_ENDPOINT` 配置：
- 确保使用的是可从浏览器访问的地址
- 如果从其他设备访问，不要使用 `localhost`，应使用实际 IP 地址

### 3. 文件上传失败？

检查 `MAX_FILE_SIZE_MB` 配置，确保限制足够大。同时需要确保前端和后端服务的文件大小限制保持一致。

## 服务健康检查

DocReader 服务配置了健康检查：

```yaml
healthcheck:
  test: ["CMD", "grpc_health_probe", "-addr=localhost:50051"]
  interval: 30s
  timeout: 10s
  retries: 3
  start_period: 60s
```

可以通过以下命令检查服务状态：

```bash
docker ps | grep docreader
docker logs WeKnora-docreader
```

## 更多信息

- 服务端口：50051（gRPC）
- 容器名称：WeKnora-docreader
- 网络：WeKnora-network
- 重启策略：unless-stopped
