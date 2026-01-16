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

### OCR 配置

- `OCR_BACKEND`: OCR 引擎后端，可选值：
  - `paddle`: 使用 PaddleOCR（默认）
  - `no_ocr`: 禁用 OCR 功能
  - `api`: 使用外部 OCR API
- `OCR_API_BASE_URL`: 外部 OCR API 的基础 URL
- `OCR_API_KEY`: 外部 OCR API 的密钥
- `OCR_MODEL`: OCR 模型名称

**示例**：禁用 OCR 功能
```yaml
environment:
  - OCR_BACKEND=no_ocr
```

### VLM（视觉语言模型）配置

用于图像理解和描述生成：

- `VLM_MODEL_BASE_URL`: VLM 模型的 API 地址
- `VLM_MODEL_NAME`: VLM 模型名称
- `VLM_MODEL_API_KEY`: VLM 模型的 API 密钥
- `VLM_INTERFACE_TYPE`: 接口类型，可选值：`openai`（默认）或 `ollama`

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

### 代理配置

如果需要通过代理访问外部服务：

- `EXTERNAL_HTTP_PROXY`: HTTP 代理地址
- `EXTERNAL_HTTPS_PROXY`: HTTPS 代理地址

### 图像处理配置

- `IMAGE_MAX_CONCURRENT`: 图像处理的最大并发数（默认：1）

## 配置示例

### 基础配置（使用 MinIO）

```yaml
docreader:
  environment:
    - MINIO_ENDPOINT=minio:9000
    - MINIO_PUBLIC_ENDPOINT=http://localhost:9000
    - MAX_FILE_SIZE_MB=50
```

### 高级配置（启用 MinerU + 自定义 OCR）

```yaml
docreader:
  environment:
    - MINIO_ENDPOINT=minio:9000
    - MINIO_PUBLIC_ENDPOINT=http://192.168.1.100:9000
    - MINERU_ENDPOINT=http://mineru:8080
    - MAX_FILE_SIZE_MB=100
    - OCR_BACKEND=paddle
    - VLM_MODEL_BASE_URL=http://ollama:11434
    - VLM_MODEL_NAME=llava
    - VLM_INTERFACE_TYPE=ollama
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

## 常见问题

### 1. DocReader 服务无法启动？

如果日志中出现 PaddleOCR 相关错误，可以尝试禁用 OCR：

```yaml
environment:
  - OCR_BACKEND=no_ocr
```

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
  test: ["CMD", "grpc_health_probe", "-addr=:50051"]
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
