# WeKnora 沙箱：以 E2B 协议为唯一接入契约

本文说明 WeKnora 为什么把 E2B 协议当作沙箱后端的唯一对接契约、现在还有哪些例外、以及可以直接拿来用的开源实现有哪些。面向部署方与要新接一种沙箱后端的开发者。

## 结论

- 跨主机、需要内核级隔离的部署走 E2B 协议（控制面 REST + 数据面 envd）：WeKnora 只维护一套这样的客户端，具体隔离能力由社区实现提供，见下面的选型表。
- 单机 / 私有化部署可以直接用 `docker` 后端：它现在也是会话级后端（一个会话一个长驻容器），在应用层与 E2B 行为一致，代价是空闲回收、执行超时这些控制面职责由 WeKnora 承担。详见 [Docker 沙箱后端](./sandbox-docker-backend.md)。
- `local` 后端在 WeKnora 主机上直接跑脚本，没有任何隔离，只适合可信的开发空间。

## 当前的后端形态

| 后端 | 协议 | 会话内状态 | shell_exec / 附件暂存 / 产物收集 | 定位 |
| --- | --- | --- | --- | --- |
| `e2b` | E2B 协议 | 持久（一个会话一个沙箱） | 支持 | 生产主路径，可指向任意 E2B 兼容控制面 |
| `cube` | E2B 兼容（走 Cube 官方 Go SDK） | 持久 | 支持 | CubeSandbox 专用适配器，见“为什么还留着 cube 适配器” |
| `docker` | 无（Docker Engine API） | 持久（一个会话一个容器） | 支持 | 单机 / 私有化部署，见 [Docker 沙箱后端](./sandbox-docker-backend.md) |
| `local` | 无（本机进程） | 无 | 不支持 | 本机开发调试，隔离性最弱 |

`docker` 与 E2B 协议后端的分界不在能力而在规模：一个 docker 配置就是一台 daemon，跨主机调度、内核级隔离、内存态快照都不在它的能力范围内，那些正是 E2B 兼容实现提供的东西。`local` 依然不参与会话级能力集。能力矩阵在 `internal/sandbox/capabilities.go` 中显式表达，agent 侧据此决定是否注册 shell/文件类工具。

## 可直接使用的开源实现

| 实现 | 隔离方式 | 部署前提 | 适用场景 |
| --- | --- | --- | --- |
| [CubeSandbox](https://github.com/TencentCloud/CubeSandbox)（Apache-2.0） | KVM MicroVM，eBPF 网络隔离 | 裸金属/物理机需 `/dev/kvm`；普通云主机可用 PVM 内核；`/data/cubelet` 需 XFS（reflink）；K8s 部署为 preview | 需要内核级隔离、高密度、快照/回滚 |
| [Agent-Sandbox](https://github.com/agent-sandbox/agent-sandbox)（Apache-2.0） | Kubernetes Pod（容器），可叠加 gVisor/Kata runtimeClass | 一个 K8s 集群（1.26+），`kubectl apply -f install.yaml` | 已有 K8s、想要“容器版 E2B”、不想引入虚拟化依赖 |
| [e2b-dev/infra](https://github.com/e2b-dev/infra)（Apache-2.0） | Firecracker MicroVM | Nomad/Consul + 云厂商 Terraform（AWS/GCP） | 想自建与 E2B Cloud 完全一致的栈 |
| [E2B Cloud](https://e2b.dev) | 托管 MicroVM | 只需 API Key | 不想自己运维 |

选型要点：

- 只有容器可用（没有 KVM、也不想上 PVM 内核）时，走 Agent-Sandbox 这类 K8s 原生实现，而不是给 WeKnora 加一个 Docker 控制面。
- 单机、有 KVM 或可装 PVM 内核，走 CubeSandbox。
- 上述实现都通过同一个 `e2b` 配置接入，WeKnora 侧零改动。

不建议采用的方向：`e2bgateway`、`circlesac/sandbox`、`Cage` 这类项目虽然也宣称 E2B 兼容并支持 Docker 后端，但当前 star 数与维护强度都在个位数量级，作为生产依赖风险过高。

## 怎么接入一个 E2B 兼容控制面

在“设置 → 沙箱后端”中新建配置，选择 `E2B`，填写：

| 字段 | 说明 |
| --- | --- |
| `api_key` | 控制面凭据。自建集群通常是它自己签发的 token |
| `api_url` | 控制面地址，例如 `http://agent-sandbox.internal/e2b/v1`。留空则用 E2B Cloud |
| `sandbox_domain` | 沙箱域名。数据面地址形如 `49983-<sandboxID>.<sandbox_domain>` |
| `proxy_url` | 数据面网关地址。见下 |
| `template_id` | 模板 / 镜像标识 |
| 允许访问私网集群地址 | 集群位于 RFC1918/loopback 时必须打开 |

`proxy_url` 是自建集群的关键：E2B Cloud 通过公网 DNS 解析每个沙箱的域名并提供证书，自建集群通常把所有沙箱收敛到一个网关地址、按 Host 头路由。填了 `proxy_url` 之后，WeKnora 会把数据面请求直接拨到该网关，同时保留沙箱域名在 Host 头里；网关是 `http://` 时还会把数据面 scheme 一并降级——E2B SDK 把它写死成 https，这一步省掉了为泛域名申请证书的成本。控制面请求不受影响，仍走共享连接池（实现见 `internal/sandbox/gateway_transport.go`）。

配置保存前先执行“连接并继续”，上线前执行一次“完整验证”，后者会真实创建、执行并销毁一个沙箱。

## envd 协议的兼容性坑

数据面 envd 的契约和 `github.com/matiasinsaurralde/go-e2b` 的实现之间有两处偏差，WeKnora 在 `internal/sandbox/envd_compat_transport.go` 里统一补齐：

- 认证：envd 要求 `Authorization: Basic base64("<user>:")`，SDK 发的是 `X-User-ID` 头。E2B Cloud 对此宽容，其他实现直接返回 `unauthenticated: no user specified`。
- 文件上传：envd 的 `POST /files` 只接受 `multipart/form-data`，SDK 发的是裸 `application/octet-stream`，会得到 500。

另外健康探针改用 `GET /v2/sandboxes`：旧的 `GET /sandboxes` 已不在客户端其他调用路径上，部分 E2B 兼容实现也只实现了 v2，用旧接口探活会把健康的后端判成不可用。文件操作现在也显式声明执行账号（`user`），与脚本运行账号保持一致，而不是依赖各实现的默认值。

模板镜像需要提供 `user` 账号（uid 1000），这是 E2B 模板的既定约定；WeKnora 以该账号执行脚本与文件操作。写权限只保证在 `/workspace/output`（产物目录，执行前由 WeKnora 创建并授权）与 `/workspace/input`（附件暂存）下，脚本不应假设 `/workspace` 根目录可写。

## 一致性测试

`internal/sandbox/e2b_compatible_integration_test.go` 是面向任意 E2B 兼容控制面的一致性测试，覆盖会话内状态保持、shell_exec 复用同一沙箱、附件暂存、产物收集、执行超时。接一种新后端时先跑它：

```bash
E2B_INTEGRATION_API_URL=http://127.0.0.1:18080/e2b/v1 \
E2B_INTEGRATION_API_KEY=<token> \
E2B_INTEGRATION_TEMPLATE=code-interpreter \
E2B_INTEGRATION_SANDBOX_DOMAIN=localhost \
E2B_INTEGRATION_PROXY_URL=http://127.0.0.1:18080 \
go test -tags=e2b_integration ./internal/sandbox \
  -run '^TestE2BCompatibleControlPlaneConformance' -count=1 -v -timeout=15m
```

针对 E2B Cloud 时不要设置 `E2B_INTEGRATION_PROXY_URL`。该套件已在 Kubernetes 上的 Agent-Sandbox（容器后端）实测通过。

### 在本机复现一个容器版 E2B 后端

只需要 Docker，用 kind 起一个单节点集群即可，全程不涉及 KVM：

```bash
kind create cluster --name e2b-poc
kubectl create namespace agent-sandbox
kubectl apply -n agent-sandbox -f https://raw.githubusercontent.com/agent-sandbox/agent-sandbox/main/install.yaml

# 控制面需要一份模板配置；集群里没有 gVisor 时，先把模板的 runtimeClassName 去掉
kubectl -n agent-sandbox create configmap agent-sandbox \
  --from-file=sandbox.yaml --from-file=templates.json

kubectl -n agent-sandbox port-forward svc/agent-sandbox 18080:80
```

之后把 `api_url` 指向 `http://127.0.0.1:18080/e2b/v1`、`proxy_url` 指向 `http://127.0.0.1:18080`、`sandbox_domain` 填 `localhost`，即可用上面的命令跑一致性测试。默认 token 在 install.yaml 中，生产部署务必替换。

## 为什么还留着 cube 适配器

CubeSandbox 兼容 E2B SDK，理论上可以只用 `e2b` 配置接入。目前仍保留独立适配器，原因是它使用 Cube 官方 Go SDK，模板构建、网络策略等控制面能力与 Cube 的 API 一一对应，而这些在通用 E2B 客户端里还没有等价物。合并的前置条件是：在真实 Cube 集群上跑通上面的一致性测试，并把模板构建、网络策略两块能力对齐到通用客户端。数据面路由已经不再是障碍——`proxy_url` 已经泛化成所有远端后端共用的能力。
