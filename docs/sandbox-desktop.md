# 沙箱图形桌面

会话沙箱可以带一个 XFCE 图形桌面（x11vnc + websockify），由 WeKnora 把 RFB 中继到对话侧栏的「桌面」页。本文说明它如何接入、为什么默认不创建，以及部署时必须避开的几条路径。

Docker 后端目前**没有**桌面路径；只有 CubeSandbox / E2B 的桌面模板能打开该页。集群与 CLI 标准模板见 [沙箱集群与标准模板](./sandbox-cluster.md)。

## 架构

浏览器里的 noVNC **不持有任何沙箱凭据**。流量路径是：

```
浏览器 (noVNC)
  ⇄ WeKnora 中继          一次性票据鉴权、14 字节 RFB 握手、opcode 空闲判定
  ⇄ 提供商网关            e2b-traffic-access-token + Authorization: Basic
  ⇄ 沙箱内 websockify :6080  ⇄ 127.0.0.1:5900 上的 x11vnc（-nopw）⇄ Xvfb ⇄ XFCE
```

两端凭据都只存在于后端 → 沙箱这一跳：

- websockify Basic 密码写在沙箱内 `/run/desktop/secret`，由后端 Exec 读取，**从不**下发给浏览器；
- x11vnc 只绑在 `127.0.0.1:5900`，无 RFB 密码。

桌面是惰性启动的：E2B 以 envd 为 init、Cube 的 ENTRYPOINT 也是 envd，镜像 CMD 不会跑起来。第一次打开桌面 tab 时，后端在已有沙箱上 Exec `/usr/local/bin/start-desktop.sh`。查找连接（只 lookup、不 provision）不会为了预览去创建或恢复微虚拟机。

## 镜像

`docker/Dockerfile.sandbox` 在 CLI runtime（Python 3.12）之上再叠 XFCE：

| 变体 | 标签 | 用途 |
| --- | --- | --- |
| `desktop` | `wechatopenai/weknora-sandbox:<版本>-desktop` | E2B 桌面模板的基础镜像 |
| `desktop-cube` | `wechatopenai/weknora-sandbox:<版本>-desktop-cube` | Cube 桌面模板（amd64，带 envd） |

E2B 桌面模板构建为 4 CPU / 4 GB；Cube 桌面模板的可写层是 8G（标准 CLI 模板是 1G）。不要把 Python 运行时或 `/workspace` 权限改成「桌面专用」——那会连带改掉所有沙箱镜像。

## 模板是显式创建的

打开「设置 → 沙箱后端」并刷新模板列表时，**只会**在缺省时自动 ensure 官方 CLI 模板。桌面模板必须在模板步骤点「创建」才会带 `ensure_desktop`；否则会在集群里悄悄拉起一套更重的镜像。

保存时 `desktop_enabled` 跟所选目录卡片走：选中桌面卡则为 `true`，选中 CLI 卡则为 `false`。已安装 Skill 的配置不能改这个位——技能环境叠在当时的底模上，换 CLI/桌面等于换代。

对话侧栏只在当前智能体的沙箱配置 `desktop_enabled=true` 时显示「桌面」tab。Docker / 未选沙箱的智能体看不到该 tab。共享智能体若本空间拿不到那份配置，仍显示 tab，由后端返回 `DESKTOP_UNSUPPORTED`。

## Cube：6080 不要做宿主机 NAT

Cube 模板的 `exposedPorts` **只**含 envd 的 `49983`。Cube 会把这份列表用 eBPF static NAT 打到宿主机网卡，从而绕过 CubeProxy。

WeKnora 访问 websockify 的方式和访问 envd 一样：CubeProxy Host `{port}-{id}.{domain}`（即 `6080-{sandboxId}.{domain}`）加上入站 token。不要把 6080 写进 `exposedPorts`：沙箱内 agent/技能默认以 root Exec，读得到 `/run/desktop/secret`，一旦 6080 暴露在宿主机上就可以跳过票据中继、空闲断开和审计。

websockify 上的 Basic auth 只是叠加路径上的纵深防御，不是可以把端口公网化的理由。

## 票据与 WebSocket

桌面 WebSocket **不走**全局 JWT 中间件（和终端一样），握手用一次性票据：

1. 已登录的 `POST /api/v1/sessions/:session_id/sandbox/desktop-ticket` 签发票据（TTL 2 分钟）。
2. 浏览器把票据放在 `GET /api/v1/sessions/:id/sandbox/desktop?ticket=` 的 query 上。
3. 服务端 `GETDEL` 消费票据；用过、过期、未知都是同一类错误，避免探测。

票据是不透明随机串，不是 JWT，避免出现在网关 access log 和浏览器历史里。nginx 对该路径使用 `api_no_query` 日志格式，避免把 `ticket` 打进访问日志。

每个会话同一时刻只允许一条桌面中继（跨副本用 Redis 槽位）。技能安装可能重建沙箱；中继用 Redis 键 `weknora:desktop-last-sandbox:`（TTL 7 天）记住上次连上的 sandbox ID，换 ID 时以 `SANDBOX_REBUILT` 关掉连接，避免多副本重连进空白桌面还以为 `/workspace` 还在。未配 Redis 时退回进程内存储，只适合单实例。

## 空闲断开

中继根据 RFB opcode 判断键鼠活动（`FramebufferUpdateRequest` 不算）。浏览器还可以 `POST /api/v1/sessions/:session_id/sandbox/desktop/activity`，但**只有 RFB parser 降级时**才会续 TTL；parser 健康时该 POST 是空操作，避免靠轮询白嫖沙箱寿命。

## 相关接口

手写说明见 [会话管理 API](./api/session.md)。这些端点与终端 WebSocket 一样未进 Swagger。
