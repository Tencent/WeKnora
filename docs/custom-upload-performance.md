# Custom 视频上传性能配置

## 直传模式

生产环境优先给 `custom-backend` 配置 `MINIO_UPLOAD_URL`（例如
`https://minio-upload.example.com`）。它必须是浏览器可访问的 MinIO/S3 根
endpoint，不要填写 `MINIO_PUBLIC_URL` 的文件代理路径。MinIO CORS 至少需要：

- 允许前端 origin 的 `PUT`、`OPTIONS`；
- 暴露 `ETag`、`Content-Length`、`Content-Type` 响应头；
- 允许请求携带 MinIO presigned URL 所需的标准头。

启用后，浏览器只向 `custom-backend` 请求初始化、签名和确认，视频分片直接写入
MinIO。没有公网 S3 endpoint 时，签名接口自动返回 `/api/custom/uploads/multipart/part`
网关地址，保持兼容但会承受视频流量。

## 调优变量

| 变量 | 默认值 | 说明 |
| --- | ---: | --- |
| `CUSTOM_UPLOAD_PART_SIZE_MB` | `8` | 默认分片大小，服务端会限制在 5–16MB |
| `CUSTOM_UPLOAD_LARGE_FILE_THRESHOLD_MB` | `256` | 达到该大小时默认分片提升到 16MB |
| `CUSTOM_UPLOAD_INITIAL_CONCURRENCY` | `2` | 初始并发 |
| `CUSTOM_UPLOAD_MIN_CONCURRENCY` | `1` | 自适应并发下限 |
| `CUSTOM_UPLOAD_MAX_CONCURRENCY` | `4` | 自适应并发上限 |
| `CUSTOM_UPLOAD_SIGN_TTL_SECONDS` | `3600` | 单片 presigned URL 有效期 |

浏览器会记录总吞吐、单片耗时、重试次数、重试率和最终并发；后端记录 init、sign、
complete、abort 的耗时和直传/网关路径。单片失败只会重签并重传当前 part。
