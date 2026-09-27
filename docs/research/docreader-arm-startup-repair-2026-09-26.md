# DocReader ARM 容器启动修复

## 故障与修复

本地 ARM64 镜像能构建，但启动时导入 `cryptography 49.0.0` 原生模块触发
SIGILL（退出码 132）。不传入文档也能复现。扫描 PDF 由 AnyDoc 转交 DocReader，
因此会遇到连接拒绝；普通 AnyDoc 文档可能不受影响。

镜像入口现在用子进程执行加密库导入和 AES-GCM 加解密自检。仅 ARM 上的 SIGILL
会触发 `OPENSSL_armcap=0`，随后再次自检，通过后才启动服务。正常 ARM 和其他
架构不改变 CPU 能力设置；显式环境覆盖被保留，其他异常不会被掩盖。
依赖版本不变，容器直接运行构建时已安装的 Python 环境。

这个选项使用 OpenSSL 的通用 CPU 实现，可能降低加密吞吐量。语义见
[OpenSSL 官方文档](https://docs.openssl.org/master/man3/OPENSSL_armcap/)。
[上游类似问题](https://github.com/pyca/cryptography/issues/14764)记录了 Apple Silicon
容器中的相同导入崩溃；本机具体修复已通过实测验证。

## 构建与部署

```sh
docker build -f docker/Dockerfile.docreader -t wechatopenai/weknora-docreader:main .
docker compose -f docker-compose.dev.yml up -d --no-deps --no-build docreader
docker exec WeKnora-docreader-dev grpc_health_probe -addr=localhost:50051
```

生产环境使用其 Compose 文件和对应镜像标签。云端没有在本轮部署；本地已经
验证 ARM64 构建和运行。x86_64 入口分支有单元测试，未声称完成云端运行验证。

## 请求方错误提示

此前阶段错误只有 `document read failed`，连接拒绝也被标成解析失败。
现在连接中断/服务不可用、超时、文档解析失败分别分类，返回可操作的公共说明，
底层诊断保留在服务端日志和 span 的 error_detail。页面补全三类错误的五种语言
标题及处理建议，并在本次 attempt 已失败、队列等待自动重试时显示原因。
自动重试进行中不提供重复的手动重试按钮。历史记录不改写。

## 验证

- 7 项容器入口测试：正常 ARM、SIGILL 兼容重试、兼容重试失败、其他错误、显式覆盖、
  x86_64、不响应超时。
- Go docparser 全包测试；错误分类和 span 失败相关普通/race 测试通过。
- 20 项前端状态/组件/多语言测试、Vue 类型检查、生产构建通过。
- 实际 16 页扫描 PDF 经 gRPC ReadStream 返回 16 张图片和 16 个来源位置。
- 原文档重新解析后 `parse_status=completed`、`pending_subtasks_count=0`、
  `summary_status=completed`、错误为空；包含 OCR、向量化、摘要及 Wiki 后处理。
- 新容器健康状态 `healthy`，观察期间重启次数为 0。
