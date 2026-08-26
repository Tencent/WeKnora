# Custom 视频上传性能配置

## 业务闭环约束

- 分片 `complete` 成功只代表视频源文件已经合并；此时必须立即返回可播放的视频记录。
- `play_url` / `file_url` 是播放主链路；`cover_url` / `thumbnail_url` 和 `duration_seconds` 是异步增强字段，不能作为上传成功的前置条件。
- 列表与详情都必须回显播放链接；封面生成较慢、`ffprobe` 失败或封面任务重试时，视频仍然可进入列表并可播放。
- 前端不得因为等待封面超过固定轮询时间而调用 `abort` 或提示“上传失败”。只有分片上传或合并失败才允许中止上传。
- 如果启用 Tongyi/听悟转写，`MINIO_PUBLIC_URL` 还必须是听悟服务可访问的公网 `http(s)` 根地址；`localhost`、Docker 内网主机名和私网 IP 只适合本地播放，不能作为转写源地址。

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

1GB 视频按默认大文件策略使用 16MB 分片，共 64 片。生产环境必须配置浏览器可访问的
`CUSTOM_MINIO_UPLOAD_URL`，否则会退化为后端网关承接全部视频流量，容易受代理超时、带宽和
连接中断影响。Nginx 的 `client_max_body_size` 只保证请求不被入口拒绝，不能替代对象存储直传。

浏览器会记录总吞吐、单片耗时、重试次数、重试率和最终并发；后端记录 init、sign、
complete、abort 的耗时和直传/网关路径。单片失败只会重签并重传当前 part。

## 回归检查清单

1. 使用真实 1GB 以内视频，确认初始化返回 16MB 分片建议值。
2. 确认所有分片完成后 `complete` 返回 `status=uploaded`、`play_url` 非空。
3. 在封面任务仍为 `pending/running` 时刷新列表，视频仍出现且可以打开详情播放。
4. 封面和时长任务完成后，列表/详情自动回显 `cover_url` 与 `duration_seconds`。
5. 取消发生在分片上传阶段才调用 `abort`；合并成功后不得再清理已完成对象。

## 历史故障复盘（禁止回归）

| 反复出现的错误 | 根因 | 固定规则 |
| --- | --- | --- |
| 1GB 文件走一次性上传或后端中转 | 没有按文件大小选择分片和直传 | 视频统一走 multipart；生产优先浏览器直传对象存储 |
| 合并成功后仍提示上传失败 | 把封面/时长任务当成上传主流程 | `complete` 成功即返回可播放记录，增强任务独立重试 |
| 封面未生成时列表没有视频 | 列表查询绑定 `thumbnail_url` | 列表只以合并文件和播放地址为准 |
| 详情页拿不到可播放地址 | 返回字段名称和前端消费不统一 | API 固定同时返回 `file_url`、`play_url`、`thumbnail_url`、`cover_url` |
| 公网接口卡住或响应截断 | 代理把控制响应按小包流式转发 | 控制响应由 Nginx 缓冲；大文件请求体保持流式、不落代理缓存 |
| 重试导致重复任务或对象清理 | 没有区分上传阶段与合并后的阶段 | 只有未完成合并时允许 `abort`；任务使用视频级幂等键 |
