# Docker 沙箱后端可行性 PoC

配套文档：[Docker 沙箱后端重做调研](../../sandbox-docker-backend.md)。

这个程序直接打 Docker Engine API，逐项验证「Docker 能不能承载 WeKnora
`RemoteSandboxClient` 契约 + E2B 那套 Snapshot 工作流」。它不是产品代码，也不参与主模块构建
（自带 `go.mod`），只作为调研结论的可复现证据。

每一项都打印 `PASS/FAIL` 和实际观测值。带 `(GAP)` 的步骤断言的是「Docker 做不到什么」，
它们同样以 PASS 结束——PASS 表示差距被复现了，不是表示能力具备。

## 跑法

需要一个可达的 Docker daemon（尊重 `DOCKER_HOST`），能拉 `python:3.11-slim`：

```bash
cd docs/poc/docker-sandbox
go run .            # daemon 在本机 unix socket 上时可能需要 sudo -E
```

程序会自建一个带 uid 1000 `user` 账号的模板镜像（对齐 E2B 模板约定），跑完自行删除它创建的容器。
留下的 `weknora-poc/*` 镜像用 `docker image rm` 清理。

## 覆盖的内容

- 生命周期：Create / Connect（换客户端重连）/ List（label 过滤）/ Delete / Pause / Stop+Start
- 执行：user、workdir、env、stdin、stdout+stderr 分离、退出码、超时
- 文件面：archive API 读写与 stat、exec 实现的 mkdir/ls/rm
- 会话语义：`pip install` 与写文件跨 exec 存活
- Snapshot：commit 成镜像、从快照启新沙箱、v1→v2 增量、按 label 列举、层数与体积
- 差距复现：客户端取消不杀进程、CapDrop ALL 后 root 越不过权限位、快照不含内存态、
  daemon 无空闲 TTL、kill `docker run` 会留下还在跑的容器
