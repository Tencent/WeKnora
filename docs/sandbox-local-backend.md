# 本地沙盒后端（host）

面向 Lite 桌面版用户，以及要改这块代码的人。远程沙箱（Docker / E2B / Cube）仍以 E2B 协议为接入契约，见 [沙箱协议接入说明](./sandbox-protocol.md)。

## 适用范围

- **只在 Lite 桌面版**（`cmd/desktop`，edition=`lite`）启用，并且 **只在带 `desktop` build tag 时编进二进制**。`wails build` 本身会带上这个 tag；`wails.json` 的 `build:tags` 让 `wails dev` 同样带上，避免开发态落到空实现。`cmd/server`（含 `make build-lite`）不会链接 `internal/localsandbox`。标准版继续只用远程沙箱。
- **当前仅 macOS**。实现走 `/usr/bin/sandbox-exec`（Seatbelt）。Windows 后端是占位：`Available()` 失败，**不会**退回裸跑。Linux 同样 unsupported。
- 用户不必先建一条沙箱配置。没有具名 Docker / E2B / Cube 配置时，Lite 回落到 `host`。配了远程配置的智能体仍走远程路径，工作区仍是 `/workspace`，行为不变。
- 后端类型叫 **`host`**，刻意不叫 `local`。已删除的 `local` 是本机裸跑、零隔离；`host` 的约束由内核强制，子进程与孙进程自动继承。

失败时倾向关闭而不是放开：没有 `sandbox-exec`、策略编译失败、平台不支持，一律不注册本地沙盒能力，新对话页不展示「选择项目」。

## 工作区：两种来源

每个会话恰好有一个工作区。路径是**真实主机路径**，没有 `/workspace` 虚拟根。`pwd`、编译器报错、`git status` 里看到的就是磁盘上的目录。

### 项目工作区

用户在**新对话输入框正上方**点「选择项目」，系统文件夹选择器会把该目录写入本机批准列表并绑定到即将创建的会话。不选就直接开聊，会话走自动工作区。沙箱配置页只管远程沙箱（Docker / E2B / Cube），不负责本机项目。同项目下的会话**共用同一个目录**，彼此能看到对方写下的文件——这就是「让 agent 操作我的项目」。

- 创建会话时 `project_dir` 必须落在已批准列表里，否则 `POST /sessions` 返回 400。接口不能自行扩大范围；批准只能来自系统选择器。
- 绑定写在 `sessions.host_workspace_dir`，创建后不可变。目录不在批准列表里时，原会话按未绑定处理，回落到下面的自动工作区，不再读写那个项目。
- 项目内的 `.git` **只读**，`rm -rf .git` 会被内核拦住。

### 会话工作区（未选项目）

未绑定项目时，系统在默认根下按日期与会话自动建目录：

```
~/Documents/WeKnoraLite/<YYYY-MM-DD>/session-<会话 ID 短码>/
```

放在 `Documents` 是为了能在访达里直接找到产物。系统创建的目录名不含空格；用户选定的项目路径可以含空格，行为不变。

### 目录布局

远程沙箱把附件放 `/workspace/input`、产物放 `/workspace/output`。本地不建这两个目录：工作目录就是用户选定的宿主机目录（或自动会话目录），agent 改完的文件直接留在那里。

| | 项目工作区 | 会话工作区 |
| --- | --- | --- |
| 主工作目录（cwd） | 用户选定的项目路径 | `~/Documents/WeKnoraLite/<日期>/session-…/` |
| `.git` | 只读 | 不保护（那是 agent 自己 init 的） |
| 附件暂存 | 不暂存进工作区 | 同左 |
| 产物收集 | 不收集，文件留在工作区 | 同左 |

相对路径锚在工作区根。工具描述用「当前工作目录」这类相对措辞，不把完整家目录路径灌进模型上下文。

## 与远程沙箱不同的会话行为

按远程经验去预期会出错，这几条是刻意的：

1. **不建 `input/` / `output/`，也不把聊天附件拷进工作区。** 文件就在用户目录里；对话附件仍走原有消息附件，不会对账删除工作区文件。
2. **不会把工作区扫进对象存储。** 远程沙箱仍收集 `/workspace/output`；host 上 `sandbox:` 下载链不会从本机项目根生成。
3. **Fork 只复制到分叉点为止的消息**，不拷贝、不 snapshot、不 `git reset` 工作区。同项目的子会话继续共用当前磁盘状态；无项目则解析出新的空会话目录。API 返回 `degraded=false`——「不带回文件系统」是成功语义，不是远程那种降级 fork。
4. **删除会话不删除任何工作区文件**。项目目录和自动会话目录都留在磁盘上。host 会话也不会被 pin：结束后 `sessions.sandbox_config_id` 仍为空。
5. **没有检查点。** 不会在用户仓库里 `git commit`。
6. **没有交互式终端、图形桌面、快照、pause/resume。** 前端不展示这些入口。
7. **同一项目目录上的 shell 与文件写入串行。** 两个对话共用一个工作区时，后到的 `Run` / 写文件会等前一个结束，避免互相踩 git 和工作树。自动会话目录互不影响。

## 权限模式（一期只开「帮我批准」）

三档决定生成什么策略、以及越界时是否询问。模式只影响 `host`；远程配置不受这三档控制。


| | 请求批准 `ask` | 帮我批准 `auto`（默认） | 完全访问 `full` |
| --- | --- | --- | --- |
| 读 | 工作目录 + 工具链 + 系统路径（家目录其余部分拒绝） | 同左 | 全盘 |
| 写 | 每次询问 | 工作目录内自动 | 全盘，不问 |
| shell | 每次询问 | 沙盒内自动 | **不套沙盒** |
| 网络 | 默认关，需要时询问 | 默认关，需要时询问 | 开 |
| 拒绝读 | 强制 | 强制 | 无 |

**一期只实现 `auto`。** 写入 `ask` / `full` 会被拒绝；UI 不展示模式选择器。允许存那两档会让用户以为越界会弹卡片、或以为完全访问能跑，实际都未接上。

`auto` 下：工作区内读写与执行自动放行；网络默认拒绝（`curl https://example.com` 会失败，常见是 DNS 解析失败而不是 `EPERM`）；越界写入由内核拦截。审批卡片与「完全访问」是第二期。

## 读权限：家目录整体不可读

没有这道边界，「沙盒能写工作区」并不等于「读不到私钥」。

macOS Seatbelt 在 darwin 25 上必须以无条件 `file-read*` 才能启动进程——去掉它，即使给足 `/usr/lib`、`/usr/bin`、`/System`、`/private/etc` 的读白名单，`sandbox-exec` 依然 SIGABRT。所以策略不是「白名单放行」，而是**先全放、再把家目录整体拒掉、然后重新开出必要的几处**。sbpl 后写的规则优先，四段顺序就是策略本身：

| 顺序 | 内容 |
| --- | --- |
| 1 | 基础 profile：`(deny default)` + 无条件 `file-read*` + 进程/终端许可 |
| 2 | 工作区写许可、网络许可 |
| 3 | **拒读 `$HOME` 整棵树** |
| 4 | 重新开读：工作区、`~/.nvm` `~/.cargo` `~/.pyenv` 等工具链目录、`~/.zshrc` 等登录 shell 启动文件 |
| 5 | 凭据拒绝 + 可写根锚定，最终生效 |

结果：`~/Documents`、`~/Desktop`、其他项目、浏览器数据、邮件——shell 都读不到；工作区和工具链照常工作。家目录之外的系统路径（`/usr`、`/opt`、`/Applications`）仍可读，那是 exec 本身需要的。

第 5 段的凭据拒绝在重新开读之后，所以 `~/.cargo` 可读但 `~/.cargo/credentials.toml` 不可读：

- `~/.ssh`、`~/.aws`、`~/.gnupg`、`~/.kube`、`~/.docker`、`~/.azure`
- `~/.netrc`、`~/.git-credentials`、`~/.npmrc`、`~/.pypirc`、`~/.password-store`
- `~/.config/gh`、`~/.config/gcloud`、`~/.terraform.d`
- `~/.cargo/credentials{,.toml}`、`~/.gem/credentials`、`~/.m2/settings.xml`、`~/.gradle/gradle.properties`
- `~/Library/Keychains`
- WeKnora 自身数据目录（macOS 上是 `~/Library/Application Support/WeKnora Lite/data`）

WeKnora 数据这一条不能由用户撤掉：沙盒能改自己的配置就等于能解除约束。

代价是工具链目录采白名单：装在家目录里、又不在 `homeReadableNames` 上的工具会读不到，表现为普通的命令失败。需要时往那张表里加。

文件类工具（`read_file` / `write_sandbox_file` / `edit_sandbox_file` / `list_sandbox_files`）走的是另一条路径守卫，本身就是白名单，只能看见工作区，比 shell 更窄。

## Agent 工具

`host` 可用时注册与远程相同的一组工具，不换名字：

- `shell_exec`
- `read_file`
- `write_sandbox_file`
- `edit_sandbox_file`
- `list_sandbox_files`

`work_dir` 必须是本机工作区内的真实路径；默认 cwd 就是工作区根，不是 `/workspace`。host 豁免 skills 开关：即使 `SkillsEnabled` 关闭，`shell_exec` 仍然注册。技能安装与技能运行时一期不接。

文件读写由 WeKnora 主进程经路径守卫完成（主进程本身不能进沙盒，否则读不了自己的数据库）；shell 才进 OS 沙盒。两条路径从同一份策略派生。

## 已知不支持

- Windows / Linux 上的 OS 强制隔离（第三、四期）
- 请求批准、完全访问、越界后的审批卡片与放宽重试（第二期）
- 技能安装、技能运行时、域名白名单、CPU/内存配额
- 交互式 PTY、图形桌面、快照、pause/resume、空闲回收
- 会话 fork 回滚本机文件
- 把 `host` 存成具名沙箱配置并 pin 到会话（一期用解析回落，故意不 pin）

## 故障排查

| 现象 | 原因与处理 |
| --- | --- |
| 新对话页没有「选择项目」，对话也没有 shell/文件工具 | 本机不是可用的 macOS Seatbelt 环境。Windows/Linux 上 Lite 不会注册 host。`GET /system/capabilities` 里 `settings.sandbox.host` 为 `platform_unsupported`。 |
| macOS 上同样没有入口 | `/usr/bin/sandbox-exec` 缺失或不可执行。`Available()` 失败则不注册能力，**不会**改成裸跑。确认该二进制存在后重启 Lite。 |
| 命令失败，stderr 末尾有 `[sandbox] denied by workspace policy` | 内核按策略拒绝了这次执行。写家目录、读工作区以外的家目录内容、改项目里的 `.git`、默认断网下的 `curl` 都会走到这里。网络拒绝常见文案是 `Could not resolve host`（curl 退出码 6），不是 `Operation not permitted`。 |
| 装在家目录里的工具链报 `Operation not permitted` | 家目录整体不可读，只重新开出了常见工具链目录。把它加进 `core.homeReadableNames`。 |
| 单次命令输出被截断，末尾是 `...[truncated N more bytes]...` | 单条流超过 1 MiB。上限在本机侧，防止刷屏命令把桌面进程的内存吃光；需要完整输出就重定向到工作区文件再读。 |
| `list_sandbox_files` 只返回 500 个文件 | 列目录在 Walk 时就截断，避免把 `node_modules` 整棵树装进内存。缩小路径或提高 `max_entries`（上限仍是 500）。 |
| 文件工具报 `path is outside the permitted workspace` | 路径落在工作区之外，或命中拒绝读列表。不要用 `/workspace/...`。 |
| 选过的项目突然变成临时工作区 | 该目录已不在本机批准列表里。已有会话不会改写 `host_workspace_dir`，但解析时视为未绑定，回落到自动会话目录。 |
| 项目目录被删掉之后对话失败 | 解析项目路径需要目录仍在磁盘上。把目录放回去，或开新对话改用自动工作区。 |
| 配了 E2B / Cube 却走到了本机目录 | 具名远程配置优先。要测 host，使用**没有**绑定远程沙箱配置的智能体。 |
| 模型一直写 `/workspace` | 当前会话走的是远程后端，或旧提示词残留。host 会话的工具描述是「当前工作目录」和真实路径。 |

启动日志：macOS 上可用时会打 `[sandbox] host backend enabled`。其它平台不注册 host，不会把命令送到无隔离的本机进程。

## 相关文档

- [Lite 与标准版区别](./LITE.md)
- [沙箱协议接入说明](./sandbox-protocol.md)
- [Docker 沙箱后端](./sandbox-docker-backend.md)
