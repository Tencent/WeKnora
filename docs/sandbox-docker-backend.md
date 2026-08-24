# Docker 沙箱后端

面向部署方与要改这块代码的人。本文说明 docker 后端现在是什么形态、怎么配、边界在哪，
以及为什么是这个设计。协议层的整体立场见 [沙箱协议接入说明](./sandbox-protocol.md)，
CubeSandbox / E2B 的集群与模板见 [沙箱集群与标准模板](./sandbox-cluster.md)。

## 结论先说

- docker 后端已经是**会话级后端**：一个会话一个长驻容器，脚本、`shell_exec`、附件暂存、
  产物收集都落在同一个容器里，与 E2B/Cube 在应用层的行为一致。
- 实现方式是 `RemoteSandboxClient` 适配器（`internal/sandbox/docker_remote_client.go`），
  直接打 Docker Engine API。session→sandbox 绑定、生命周期锁、能力矩阵一行没改——
  这正是当初把 provider 抽象成 `RemoteSandboxClient` 的收益。
- 它适合单机 / 私有化部署。跨主机调度、内核级隔离、内存态快照仍然要用 E2B 协议后端，
  原因见「边界」一节。
- Docker 官方的 Docker Sandboxes（`sbx`）不能当后端：那是开发者本机 CLI，要 Docker 账号登录、
  工作区是宿主机目录直挂、没有多租户服务端 API。

## 之前为什么不行

旧实现每次执行都是 `docker run --rm` 加一个只读 bind mount，和 E2B 不是「能力少一点」，
而是模型不同。具体是这几条（都在改造中修掉了）：

| 旧行为 | 后果 |
| --- | --- |
| 执行完即销毁容器 | 没有会话状态，`shell_exec`、附件暂存、产物收集在能力矩阵里根本不注册 |
| 超时 `kill` 的是 `docker run` 客户端进程 | 容器继续跑到自己结束；WeKnora 已经给用户返回超时了。实测见 [PoC](./poc/docker-sandbox) |
| `/workspace` 只读挂载 | 脚本写不了 `/workspace/output`，而 skills 框架恰恰把 `WEKNORA_SKILL_OUTPUT_DIR` 指到那里 |
| bind mount 由宿主机 daemon 解释 | WeKnora 自己跑在容器里时挂进去的是宿主机上的同名目录，通常不存在 |
| 走 docker CLI 而不是 API | 依赖宿主机装 CLI，错误只能靠字符串匹配，拿不到容器 ID 做对账与回收 |
| 配置面只有一个 `image` | CPU、内存、网络、TTL 都没法按空间配置 |

## 现在的形态

一个沙箱就是一个容器。标准镜像以 `USER user` 结尾，创建时覆盖为 uid 0，这样入口才能在 `/var/lib` 写下活跃标记；脚本仍以 `user` 执行。PID 1 是 `sleep infinity`，所有工作都通过 exec 进去做。

这层 wrapper 是通过 `Entrypoint` 下发并把 `Cmd` 显式清空的：daemon 会把镜像自带的
ENTRYPOINT 拼到 Cmd 前面，所以只设 Cmd 时，任何声明了 ENTRYPOINT 的镜像（本文件的
`cube` target 就有）都会顶掉 PID 1，活跃标记不会生成、exec 也会打到别的东西上。

| 契约方法 | Docker 实现 |
| --- | --- |
| `Health` | `GET /_ping` |
| `Create` | `POST /containers/create` + `/start`，metadata 落成 labels，镜像缺失时先 pull |
| `Connect` | `GET /containers/{id}/json`；容器被停掉时重新 `start`，被 pause 时 `unpause` |
| `Get` / `List` | `GET /containers/json?filters=label=…`，服务端按 label 过滤 |
| `Delete` | `DELETE /containers/{id}?force=1&v=1` |
| `Exec` | `POST /containers/{id}/exec` → `/exec/{id}/start`（hijack）→ `/exec/{id}/json` |
| `WriteFile` / `ReadFile` / `Stat` | 刻意不用 archive 接口，走 exec 的 `cat > "$1"` / `cat` / `find -maxdepth 0 -printf`（见下） |
| `MakeDir` / `Remove` / `ListDir` | 没有原生接口，用 exec 的 `mkdir -p` / `rm -rf` / `find -printf` |

几个不显然但要紧的决定：

**超时由容器内的 `timeout(1)` 执行。** 取消 HTTP 请求不会终止容器里的进程（[PoC](./poc/docker-sandbox)
里专门复现了这条），所以每次 exec 都包一层
`sh -c 'touch <marker>; exec timeout -s KILL <n> "$@"' weknora-exec <cmd> <args...>`。
命令通过位置参数传进去，不做任何字符串拼接，脚本里的引号和换行不会改变实际执行的东西。
退出码 137/124 被翻译成 `Killed=true`。

**空闲回收是 WeKnora 自己的事。** daemon 没有任何 TTL 概念。上面那层 wrapper 顺手 `touch`
一个活跃标记文件，清扫时用一次 `HEAD /archive` 读它的 mtime 就知道容器多久没干活了——
不需要额外往容器里 exec，也不需要 Redis 记账。清扫在 `Create`/`Connect` 时触发，
按 daemon 端点限流（默认最快一分钟一次），在后台跑。删掉一个空闲容器不需要跟绑定存储协调：
生命周期本来就把「provider 上已经没有的沙箱」当作可重新绑定，这跟 E2B 沙箱被自己的 TTL
回收后的路径完全一样。每个容器把创建时的 TTL 记在 label 上，所以 A 配置触发的清扫不会拿
自己的 TTL 去衡量 B 配置的容器。

标记文件必须对沙箱账号可写（只跑脚本的会话也要能刷新它），因此它的 mtime 是容器可影响的：
落在未来的时间戳一律不采信，退回按容器启动时间判断，否则一次 `touch -d 2099-01-01`
就能让容器永久免于回收。往前改只会让自己更早被回收，不构成问题。此外清扫在真正删除前
会再读一次标记：列举加逐个 stat 在繁忙 daemon 上不是瞬时的，期间会话可能已被恢复。

残留风险：标记对沙箱账号可写是刻意的（只跑脚本的会话也要能刷新它），所以任何能在容器里
执行命令的东西——包括普通技能脚本，不需要 root——都可以起一个后台循环持续 `touch` 标记，
把自己维持成「一直活跃」。当前没有硬寿命上限，需要的话应由部署方在 daemon 侧限制。

**所有 exec 都以 `user`(uid 1000) 运行。** 脚本执行、`shell_exec`、以及全部文件操作都显式
传 `DefaultSandboxExecUser`。唯一的例外是 manager 自己的 bootstrap：它要把产物目录 `chown`
**给**沙箱账号，因此不能以该账号执行，这也是 `RemoteExecRequest.User` 留空的唯一用途。
容器 `CapDrop: ALL` 之后额外补回 CHOWN/DAC_OVERRIDE/FOWNER/FSETID/SETGID/SETUID/KILL，
这是 root 装包和修属主需要的最小集合；Docker 默认给的 NET_RAW、MKNOD、SYS_CHROOT 等一律不给。

**文件操作走 exec，不走 archive 接口。** archive 接口（`PUT`/`GET`/`HEAD /archive`）由 daemon
执行，这意味着两件事同时成立：它忽略 exec user 一律以 root 操作，并且会在路径解析时跟随符号
链接。沙箱账号对 `/workspace` 有写权限，而调用侧的路径守卫都是字符串前缀比较，于是
`ln -s /root /workspace/output/esc` 之后，`/workspace/output/esc/secret.txt` 既能通过守卫，
又会被 daemon 以 root 读出来（真机验证过，不是推演）。改成以沙箱账号 exec 之后，能不能读写
由内核判定，符号链接指向哪里都不再重要，也不存在「先校验后使用」之间被换掉链接的窗口。
`Stat` 用 `find`，它不跟随符号链接，因此链接会如实报告为 `other` 类型，要求正规文件的调用方
在尝试读取之前就会拒绝。archive 接口里只剩 `HEAD` 还在用，且仅用于读固定路径的活跃标记。

**PID 1 开 tini（`HostConfig.Init`）。** 容器入口是 `sleep`，它从不调用 `wait()`。长会话里
后台进程一旦活得比启动它的 exec 久，退出后就会变成没人回收的僵尸，堆到 `pids_limit` 之后所有
后续 exec 都会失败。

**镜像即模板。** `ListTemplates` 列出 daemon 上带 `com.weknora.sandbox.template=true` 标签的、
或名字就是标准镜像的镜像；`EnsureStandardTemplate` 在后台拉取，拉取期间模板状态显示为
`building`，与其它后端的模板构建流程对齐。

## 配置

在「设置 → 沙箱后端」中新建配置并选择 Docker：

| 字段 | 说明 |
| --- | --- |
| 镜像 | 必填。会话容器都从它创建，等价于其它后端的 template ID |
| Docker 守护进程地址 | 留空跟随本机 `docker` CLI（`DOCKER_HOST` 或当前 `docker context`），因此 Colima / Docker Desktop 不必手填 socket。远程填 `tcp://host:2376`，**必须**同时填 TLS 证书目录；私网地址要打开「允许访问私网集群地址」 |
| TLS 证书目录 | 远程 daemon 必填。WeKnora 主机上包含 `ca.pem`/`cert.pem`/`key.pem` 的目录，证书不入库 |
| 空闲回收 | 容器多久没执行任何命令就回收。留空 1800 秒 |
| CPU / 内存 / 进程数上限 | 单个沙箱的资源上限。留空 2 核 / 2048 MB / 512 进程 |
| 网络模式 | 只接受 `bridge`（默认）与 `none`（完全禁止出网）。`host`、`container:` 以及自定义网络名一律拒绝：常见部署通过挂载的 `docker.sock` 连 daemon，填上部署自身的 compose 网络就会让沙箱与 Postgres / Redis 同网 |

`tcp://` daemon 的连接与其它后端的租户端点同一口径：保存时校验地址，实际拨号时再按
「允许访问私网地址」开关过一遍 `SafeDialControl`，这样保存校验解析到公网、连接时被
重解析到 169.254.169.254 的情况也拦得住。unix socket 不经过这一层。

镜像要求：uid 1000 的 `user` 账号、`/workspace/{input,output}` 归该账号所有、
GNU `find`（`-printf`）与 coreutils `timeout`。`docker/Dockerfile.sandbox` 产出的标准镜像满足这些，
Debian 系基础镜像天然带 find 和 timeout。

部署形态：

| 形态 | 适用 | 关键约束 |
| --- | --- | --- |
| 本机 socket | 单机、私有化、开发 | app 与 daemon 同机；`docker.sock` 等于宿主机 root，只能暴露给 app 进程 |
| 远程 daemon（mTLS） | 沙箱负载与应用分离 | 必须配 TLS 证书；daemon 端口不得暴露到公网 |
| 每租户独立 daemon | 有强隔离诉求但没有 KVM | 由部署方分配，不同配置指向不同 host |

WeKnora 自己跑在容器里时，要把 **实际的** docker socket 挂进 app 容器（并接受它等同宿主机 root 的事实），
或者改用远程 daemon。Linux 上通常是 `/var/run/docker.sock`；macOS 上 Colima / Docker Desktop / OrbStack
各自有 `$HOME` 下的 socket，以 `docker context show` 为准。

## 边界

这些是 Docker 给不了的，写在这里以免被当成 bug：

- **跨主机调度**：一个配置就是一个 daemon，也就是一台机器。要多机就得自己选机、分发镜像、
  在绑定里记住会话落在哪台机器上——那就是在写一个控制面，这条边界是刻意保留的。
- **内核级隔离**：容器共享宿主机内核。要更强只能叠 gVisor / Kata（配置里的 runtime 字段）。
- **内存态快照**：`docker commit` 只保存文件系统。CRIU（`docker checkpoint`）是 experimental，
  默认 daemon 直接拒绝。E2B 的 pause/snapshot 会保存内存，Docker 不会。
- **域名级出网策略**：Docker 只有 L3/L4。`RemoteNetworkPolicy` 里的域名 allow/deny 在这个后端
  只能表达成「全开」或「全关」，要按域名放行得在部署侧加 egress proxy。
- **卷挂载**：`SupportsVolumes` 目前是 false，租户级共享卷还没有映射到 Docker named volume。

## 快照

「空间级管理沙箱装 skill → commit 成快照 → 会话从快照起容器 → 增量出下一版」这套流程在 Docker 上
是原生形态（容器 → 镜像），[PoC](./poc/docker-sandbox) 已经验证：commit 一个 140 MB 镜像约
0.5–1.0 秒，从快照冷启一个容器约 0.2 秒，v1→v2 增量正常。两个要注意的约束：镜像层上限 127，
长期增量要定期压平；快照是本机资产，多机部署必须推到 registry。这部分按计划在单独分支实现。

## 测试

单元测试用一个内存版 Engine API 驱动适配器，不需要 daemon：

```bash
go test ./internal/sandbox -run 'TestDocker' -count=1
```

一致性测试打真实 daemon，覆盖会话状态保持、包安装跨执行存活、`shell_exec` 复用同一沙箱、
附件暂存与产物收集、超时确实终止进程、容器被外部停掉后恢复：

```bash
docker build -f docker/Dockerfile.sandbox --target sandbox -t wechatopenai/weknora-sandbox:dev .
DOCKER_INTEGRATION_IMAGE=wechatopenai/weknora-sandbox:dev \
go test -tags=docker_integration ./internal/sandbox \
  -run '^TestDocker.*Integration' -count=1 -v -timeout=15m
```

它和 E2B 的一致性测试断言的是同一批语义，这是刻意重复：一个后端只过其中一个，
就说明两者在应用层还不能互换。

[docs/poc/docker-sandbox](./poc/docker-sandbox) 是当初的可行性验证，保留下来作为
「Docker 能做什么、不能做什么」的可复现证据，它不参与主模块构建。
