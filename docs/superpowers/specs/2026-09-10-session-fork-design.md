# 会话分叉（Session Fork）设计文档

> 日期：2026-09-10
> 状态：设计已确认，待转实施计划
>
> 目标：让一个会话可以从任意一条历史 user 消息处分叉出一个新会话，新会话继承分叉点之前的对话历史**与沙箱状态**，两条分支之后各自独立演进、互不影响。

---

## 一、结论与整体架构

### 1.1 一句话方案

**每轮 agent turn 结束时在沙箱内对 `/workspace` 打一个 git commit；fork 时对源沙箱打一个 provider 快照；新会话惰性地从该快照建沙箱，再用 `git reset --hard` 把 `/workspace` 回退到分叉点。**

快照和 git 各管一半，这是整个方案的核心：

| 层 | 负责 | 为什么不能只用另一个 |
|---|---|---|
| provider 快照 | 复制**整个文件系统**（含 `pip install` / `apt install` 装到 `/usr/local`、site-packages 的依赖环境） | git 只纳管 `/workspace`，管不到依赖环境 |
| git | 精确回到**哪一轮** | 快照只能捕获"现在"，无法捕获"第 2 轮时刻" |

### 1.2 完整链路

```
每轮 agent turn 结束（handleComplete，artifact 收集之前）
  └─ 沙箱内 git -C /workspace add -A && git commit --allow-empty
     └─ 把 {sandbox_id, commit_sha} 写进这条 assistant 消息的 sandbox_checkpoint

用户在第 N 条 user 消息上点「分叉」
  ├─ 找到 N 之前最后一条 assistant 消息的 sandbox_checkpoint
  ├─ 校验源会话无活跃 turn（快照会暂停源沙箱）
  ├─ CreateSnapshot(源沙箱)                    ← 只打快照，不建沙箱
  └─ 事务内建新 session：复制 N 之前的消息
       + 记录 (parent_session_id, forked_from_message_id, fork_bootstrap)
     前端跳转新会话，把第 N 条 user 消息预填进输入框

新会话第一次真的用到沙箱（惰性，在 WithLifecycleLock 内）
  ├─ Create(TemplateID = snapshot_id)          ← 整个文件系统含依赖环境
  ├─ 写 binding
  ├─ git -C /workspace reset --hard <sha> && git clean -fdx
  ├─ 还原分叉点之前各消息的 artifacts 到 /workspace/output
  ├─ 标记 fork_bootstrap.consumed_at + 删除快照
  └─ （/workspace/input 由现有 stageSessionAttachments 自动重建，无需新代码）
```

### 1.3 关键设计决策一览

| 决策 | 选择 | 理由 |
|---|---|---|
| fork 产物形态 | 新建独立 session | 侧边栏并列展示，两分支各自独立沙箱；不改造成消息树，避免动整个消息读写链路 |
| 状态保真度 | 文件内容 + 依赖环境，不要求进程/内存态 | 依赖环境是用户真正在意的；内存态与 `git reset` 存在语义冲突（进程看到的文件会在脚下被换掉） |
| 依赖环境的时间点 | 分叉**时刻**而非分叉**点**（接受偏差） | 换来零额外快照成本 |
| fork 时机 | 立即打快照 + 惰性建沙箱 | 锁住状态不怕源沙箱消失；不为"只是想看看"的 fork 付沙箱钱 |
| 快照寿命 | 一次性消费，建完沙箱即删 | 与现有会话的耐久性语义一致，不新增不一致；GC 简单 |
| git 纳管范围 | `.gitignore` 掉 `input/` 和 `output/` | 避免 `.git` 被几百 MB 生成物撑爆 |
| 源沙箱不可用 | 降级不报错 | Docker idle 直接 kill、纯聊天会话压根没建过沙箱，这是常态不是边缘情况 |
| 触发入口 | user 消息上的「分叉」按钮，内容预填输入框供编辑重发 | 分叉点天然对齐上一轮的 git commit |
| commit 失败 | best-effort，静默降级 | 与 artifact 收集现有语义一致，辅助功能不得拖垮主流程 |

### 1.4 为什么不需要给 `RemoteSandboxClient` 加 Fork/Clone

初看应该用 E2B 的原生 fork（`POST /sandboxes/{id}/fork`，完整内存 checkpoint），但**不需要**：

1. 我们明确不要求内存态（§1.3），E2B fork 的核心卖点用不上；
2. Cube 的 `Clone` 本身就是 `snapshot + Create(templateID=snapshotID)` 的编排，不承诺内存态；
3. Docker 完全没有 fork/clone；
4. **`RemoteSnapshotManager` 已在三个后端全部实现**，且 `RemoteSnapshotRef.ID` 的契约就是可以直接回填 `TemplateID`：

```514:520:/data/workspace/WeKnora/internal/sandbox/remote_client.go
// RemoteSnapshotRef identifies one provider-side snapshot. ID can be passed
// straight back as RemoteCreateRequest.TemplateID: Cube and E2B store
// snapshots as templates, and Docker stores them as local image tags.
type RemoteSnapshotRef struct {
	ID    string
	Names []string
}
```

`SnapshotManagerFrom(client)` 还自带"类型断言 + `SupportsSnapshots` 能力位"的双重校验（`remote_client.go:546-558`），拿不到就降级而不是失败。

**结论：走快照路径，三后端一套代码，零新增 provider API 面。**

---

## 二、数据模型

### 2.1 `sessions` 新增字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `parent_session_id` | `VARCHAR(36)` NULL | 来源会话 ID，用于侧边栏展示分叉关系 |
| `forked_from_message_id` | `VARCHAR(36)` NULL | 分叉点那条 user 消息在**源会话**中的 ID |
| `fork_bootstrap` | `JSONB` NULL | 一次性引导信息，见下 |

`fork_bootstrap` 结构：

```json
{
  "snapshot_id":       "<provider snapshot / template ID>",
  "commit_sha":        "<git sha at fork point>",
  "source_sandbox_id": "<snapshot 来源沙箱 ID，用于审计>",
  "created_at":        "2026-09-10T10:00:00Z",
  "consumed_at":       null
}
```

`consumed_at` 非空即表示已引导完毕，此后该会话与普通会话完全一致。

`parent_session_id` 与 `forked_from_message_id` **不做外键约束**：源会话可能先于分支被删除，分支不应因此级联消失。展示时按 ID 查不到就退化为普通会话。这与仓库现状一致——`messages.session_id -> sessions.id` 本身也没有外键。

### 2.2 `messages` 新增字段

| 字段 | 类型 | 说明 |
|---|---|---|
| `sandbox_checkpoint` | `JSONB` NULL | 只写在 assistant 消息上 |

```json
{
  "sandbox_id":   "<写这个 commit 时绑定的沙箱 ID>",
  "commit_sha":   "<git sha>",
  "committed_at": "2026-09-10T09:59:00Z"
}
```

**`sandbox_id` 必须和 `commit_sha` 存在一起。** 会话中途沙箱被回收重建时（stale 重建、provider 回收、Docker idle kill），沙箱内的 git 历史会从零开始，旧 sha 在新仓库里根本不存在。存了 `sandbox_id` 就能纯靠数据库比对判定"这个分叉点不可达"，不必去沙箱里试探一次再失败。

### 2.3 迁移

`migrations/versioned/` 下新增一对 `NNNNNN_session_fork.up.sql` / `.down.sql`（编号接在当前最大编号之后），同步更新 `migrations/sqlite/000000_init.up.sql` 基线。

### 2.4 顺手修掉的排序隐患

```50:58:/data/workspace/WeKnora/internal/application/repository/message.go
func (r *messageRepository) GetMessagesBySession(
	ctx context.Context, sessionID string, page int, pageSize int,
) ([]*types.Message, error) {
	var messages []*types.Message
	if err := r.db.WithContext(ctx).Where("session_id = ?", sessionID).Order("created_at ASC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&messages).Error; err != nil {
		return nil, err
	}
	return messages, nil
}
```

裸 `ORDER BY created_at ASC` 没有次级排序键，同毫秒消息的顺序在数据库层面不确定。`GetRecentMessagesBySession` 和 `GetMessagesBySessionBeforeTime`（`:62-107`）在内存里补了"同时间 user 优先"的兜底，但分页 SQL 没有。

fork 依赖一个**稳定的复制边界**，所以：

- 分页 SQL 补 `, id ASC` 作为次级键；
- fork 的复制边界用 `(created_at, id)` 复合比较，而非单纯时间戳。

这是既有缺陷，但 fork 会把它从"偶发的展示顺序抖动"放大成"分叉边界错位"，属于该功能必须一并处理的范围。

---

## 三、git 层

### 3.1 镜像

`docker/Dockerfile.sandbox:40-50` 的 apt 清单加 `git`：

```
jq curl ca-certificates file zip unzip bzip2 xz-utils zstd git
```

该层在 `runtime` stage，`sandbox` 与 `cube` 两个 target 自动继承。镜像体积增加约 40 MB，可忽略。

### 3.2 仓库初始化：惰性 + 幂等

**不在镜像里预置 `.git`，也不在沙箱启动时初始化**，而是与第一次 commit 合并成同一条 shell 命令。收益：

- 从没用过沙箱的纯聊天会话零成本；
- pause/resume、沙箱重建后自动自愈，无需额外的恢复钩子；
- 三后端同一条命令，不碰各自不同的启动机制（E2B start-cmd / Cube entrypoint / Docker CMD）。

```sh
set -e
git config --global safe.directory /workspace
git -C /workspace rev-parse --git-dir >/dev/null 2>&1 || {
  git init -q /workspace
  git -C /workspace config user.email agent@weknora.local
  git -C /workspace config user.name  'WeKnora Agent'
  printf 'input/\noutput/\n' > /workspace/.gitignore
}
git -C /workspace add -A
git -C /workspace commit -q --allow-empty -m "turn:<messageID>"
git -C /workspace rev-parse HEAD
```

取最后一行 stdout 作为 sha。

### 3.3 三个必须注意的细节

**`--allow-empty` 不是可选项。** 没有它，"这一轮 agent 只是回答了个问题、没碰任何文件"就不会产生 commit，`sandbox_checkpoint` 为空，从那里分叉就会落空。有了它，**每一轮都必然有一个 checkpoint**，fork 边界永远可解析。

**`safe.directory`。** `shell_exec` 默认以 root 执行，而 `/workspace` 属主是 `user`（`Dockerfile.sandbox:68-69`），git 会以 "dubious ownership" 拒绝操作。所以 `git config --global safe.directory /workspace` 放在脚本第一行，在 `rev-parse` 探测之前——探测本身就会被 dubious ownership 拦下。

用不带 `--add` 的赋值形式：`safe.directory` 是多值键，`--add` 每轮追加一次，几十轮之后 `~/.gitconfig` 里会堆出几十行重复配置。这里只有一个路径，直接赋值即幂等。

这行必须每次都执行、而不能只放在 `git init` 的初始化分支里：`--global` 写的是**执行用户**的 `~/.gitconfig`，而同一个仓库可能先后被 root 和 `user` 操作。

**`.gitignore` 只挡 `input/` 和 `output/`。** 恢复时用 `git clean -fdx`，其中 `-x` 会连被 ignore 的文件一起删——这正是我们要的：fork 后 input/output 都由还原逻辑重建，不能残留源会话在分叉点之后产生的内容。

### 3.4 hook 位置

`AgentEngine.executeLoop` 有一个 exactly-once 的完成事件保证：

```443:461:/data/workspace/WeKnora/internal/agent/engine.go
	// Guarantee exactly-one EventAgentComplete emission on every exit path
	// (normal finish, ctx cancel observed at the loop head, or iteration error
	// bubbling up while ctx was cancelled).
	completionEmitted := false
	emitCompletion := func() {
		if completionEmitted {
			return
		}
		completionEmitted = true
		e.emitCompletionEvent(context.WithoutCancel(ctx), state, sessionID, messageID, startTime)
	}
	defer emitCompletion()
```

其同步消费者是 `AgentStreamHandler.handleComplete`（`internal/handler/session/agent_stream_handler.go:650`）。commit 插在**artifact 收集之前**（当前约 `:690`）。此时：

- 所有 tool 已执行完毕；
- 本轮的 sandbox turn lease 仍未释放，沙箱一定还在；
- 该处已经在持 `h.mu` 的状态下做沙箱 I/O（artifact 收集），加一次 exec 是同构的，不引入新的并发形态；
- commit 结果可以写进同一条 `complete` SSE 事件与同一次消息落库。

因为 `output/` 被 ignore，commit 与 artifact 收集的先后其实不影响结果；选在之前是为了让"本轮的工作产物"这个语义更清晰。

**不要**放在 `sessionService.AgentQA` 中 `engine.Execute` 返回之后：完成事件已在 `Execute` 内的 defer 同步派发完毕，那时客户端可能已经收到 `complete`。也**不要**放在单轮 ReAct iteration 结束处（`engine.go:770-779`），那只是一次工具循环，不是一个对话 turn。

### 3.5 取消与报错的轮次也照常 commit

完成事件在所有退出路径（自然结束、达到迭代上限、取消、工具后 LLM 失败降级）都会触发一次。如果只对成功轮次 commit，被取消那轮改动的文件会被并进下一轮的 commit——从下一轮分叉就会额外拿到上一轮被取消的变更。**统一 commit，不区分结果。**

### 3.6 失败策略：best-effort

commit 失败（磁盘满、git 缺失、沙箱瞬时不可达、工作区有超大文件导致超时）时：

- 记 warn 日志；
- 该消息的 `sandbox_checkpoint` 留空；
- 主流程不受任何影响；
- 前端对没有 checkpoint 的位置不展示「分叉」按钮（见 §5.2）。

与 `handleComplete` 中 artifact 收集现有的 best-effort 语义完全一致（`agent_stream_handler.go:690-695` 的注释即为先例）。

### 3.7 性能

`git add -A` 每轮扫一遍工作区。与已经在同一位置运行的 artifact 收集是一个量级，可接受。exec 超时建议 30s，超时即按失败处理。

---

## 四、Fork 执行

### 4.1 接口

```
POST /api/v1/sessions/:id/fork
body:     { "message_id": "<分叉点 user 消息 ID>", "title": "<可选，新会话标题>" }
response: { "session_id": "<新会话 ID>", "degraded": bool, "reason": "<降级原因码>" }
```

`degraded=false` 时 `reason` 为空字符串。「分叉点是第一条 user 消息」这种情况也是 `degraded=false`——前面本就没有产物，全新沙箱就是正确结果，不是降级。

注册在 authed 组，与 `internal/router/routes_chat.go:52-79` 的其他会话操作并列。

**全程不建沙箱**，只做数据库操作 + 一次 `CreateSnapshot`，所以响应很快（快照本身在 E2B/Cube 上是秒级）。

### 4.2 判定顺序

任一条不满足即降级为「全新沙箱 + 仅还原 artifacts」，**返回 200 并置 `degraded=true`，不报错**：

| # | 条件 | 不满足时 |
|---|---|---|
| 0 | session 归属校验、message 属于该 session、`role == "user"` | **报错** 400/403/404 |
| 1 | 源会话当前**无活跃 turn** | **报错** 409（见 §4.3） |
| 2 | 分叉点不是第一条 user 消息 | 不算降级：前面本就无产物，直接用全新沙箱 |
| 3 | 前置 assistant 消息的 `sandbox_checkpoint` 非空 | 降级 `NO_CHECKPOINT` |
| 4 | `checkpoint.sandbox_id == 当前 binding.sandbox_id` | 降级 `SANDBOX_REPLACED` |
| 5 | binding 存在且沙箱非 terminal | 降级 `SANDBOX_GONE` |
| 6 | `SnapshotManagerFrom(client)` 返回 true | 降级 `SNAPSHOT_UNSUPPORTED` |

全部满足 → `CreateSnapshot(源沙箱, "fork-<newSessionID>")`。快照失败也降级，不报错。

### 4.3 源会话正在跑 agent 时必须拦住

E2B 和 Cube 的 `CreateSnapshot` 都会**暂停源沙箱**（`remote_client.go:524-528` 的接口契约明确写了 "The provider pauses the sandbox while the snapshot is taken"），Docker 的 `ContainerCommit` 同样默认暂停容器（`docker_snapshot.go:25-27`）。在别人 turn 中间打快照会打断正在执行的工具。

现有的 turn lease 正好能查：`sessionTurnLeaseStore.TurnState(ctx, key)`（`session_binding.go:144-149`）返回 `active`。有活跃 turn 就返回 **409**，前端提示「请等本轮回答结束后再分叉」。

### 4.4 消息复制

在一个数据库事务内：

1. 创建新 session：`title` 优先取请求体传入的值，未传则用源标题加 `（分支）` 后缀；`description`、`tenant_id`、`user_id`、`agent_config` 原样继承；
2. **必须继承 `sandbox_config_id`**——否则新会话可能落到另一个 backend，快照就用不了了；
3. 复制 `(created_at, id) < 分叉点` 的全部消息，每条生成**新的 message ID**，其余字段原样带过（含 `created_at`，以保持时间顺序与展示语义）；
4. 写入 `parent_session_id` / `forked_from_message_id` / `fork_bootstrap`。

`request_id` 原样复制。它用于配对 user/assistant 消息，所有查询都带 `session_id`，跨 session 重复无害。

### 4.5 新沙箱的 bootstrap 必须是全有或全无

惰性创建时，在 `WithLifecycleLock` 内按顺序执行：

1. 读 session 的 `fork_bootstrap`，`consumed_at` 为空则进入引导路径；
2. 用 `snapshot_id` 覆盖 `TemplateID` 建沙箱；
3. 写 binding；
4. `git -C /workspace reset --hard <sha> && git -C /workspace clean -fdx`；
5. 还原 artifacts 到 `/workspace/output`（§4.7）；
6. 标记 `consumed_at`，删除快照。

**中间任何一步失败，就销毁这个沙箱、清空 `fork_bootstrap`、删除快照**，下次 resolve 走全新沙箱路径。

不能容忍部分成功：如果建好了沙箱但 `git reset` 失败就放着不管，用户会在一个"文件系统停在分叉**时刻**而不是分叉**点**"的沙箱里干活。这种静默错误比明确降级糟得多——它看起来一切正常，但产物基线是错的，排查时几乎不可能想到这一层。

因为整段在 `WithLifecycleLock` 内，同一 session 的并发 resolve 天然串行，不会出现两个请求都拿同一个快照建两个沙箱。

### 4.6 实现约束：不能污染共享的 createRequest

`remoteSessionLifecycle` 是**按 config 构造、跨 session 复用**的对象，`l.createRequest`（由 `buildSessionCreateRequest` 生成，`session_manager.go:1192-1258`）是共享状态。

**`TemplateID` 的覆盖必须走 per-resolve 的参数传递，绝不能直接改 `l.createRequest`**，否则同一 config 下其他会话的建沙箱行为会被污染——表现为"某个会话 fork 之后，同租户其他新会话莫名其妙从别人的快照启动"，是排查代价极高的故障。

建议在 `createAndBind` 上加一个可选的 template override 参数，由 resolve 路径按 session 查出后传入。

### 4.7 artifacts 还原

因为 `output/` 被 gitignore、且 `git clean -fdx` 会清掉它，fork 后 `/workspace/output` 是空的。需要把分叉点之前各消息的 `artifacts` 从对象存储下载后写回沙箱。

这是**本方案中唯一没有现成先例的新代码**：现有的 `artifact_collector` 只做"从沙箱收集到对象存储"这个方向，反向没有。

- 输入：复制过来的消息集合中所有 `artifacts` 条目；
- 同名文件按消息时间顺序后写覆盖，与源会话中的最终状态一致；
- 需要总量上限（建议 200 MB）与单文件上限，超限则跳过并记日志，避免一次 fork 拉爆沙箱磁盘和后端带宽。

**`/workspace/input` 不需要新代码。** `GetSessionAttachments` 是按 session 聚合所有消息的 `attachments` 字段（`repository/message.go:364-387`），而 `session_agent_qa.go:170-196` 每轮都会重新 stage 一遍。复制消息即自动带上附件，新会话第一轮自动重建 input。

### 4.8 快照 GC

一次性消费，正常路径在 §4.5 第 6 步删除。此外需要一个定时清理，回收"建了但从没被打开过"的孤儿快照：扫描 `fork_bootstrap.consumed_at IS NULL AND created_at < now() - 7d` 的 session，删快照并清空 `fork_bootstrap`。

**Docker 后端要用独立命名空间。** 现在 `dockerSkillSnapshotRepo = "weknora-skill"`，且 `ListTemplates` 会隐藏该前缀以免管理员误选（`docker_snapshot.go:12-20`）。fork 快照应使用 `weknora-fork/` 前缀与独立 label，否则会和技能镜像的回收逻辑互相误删。`DeleteSnapshot` 的 `PruneChildren: true` 语义（`docker_snapshot.go:74-81`）对 fork 快照同样适用。

---

## 五、前端

### 5.1 分叉入口

user 消息 hover 时出现「分叉」按钮 → 调 `POST .../fork` → 跳转新会话 → 把该条 user 消息的内容预填进输入框供编辑后重发。

涉及 `frontend/src/views/chat/index.vue`（消息列表与输入框）与 `frontend/src/components/chat/usermsg.vue`（消息操作区）。

### 5.2 按钮的显隐

只在能解析出分叉点时展示。前端已经能拿到消息列表，`sandbox_checkpoint` 随消息一起返回即可本地判断，无需额外请求：

- 分叉点是第一条 user 消息 → 展示（走全新沙箱，正常路径）；
- 前置 assistant 消息有 checkpoint → 展示；
- 前置 assistant 消息无 checkpoint → 仍展示，但 tooltip 提示"将创建全新沙箱环境"。

### 5.3 降级与告知

接口返回 `degraded=true` 时，在新会话顶部显示一条一次性提示，按 `reason` 映射文案：

| reason | 文案 |
|---|---|
| `NO_CHECKPOINT` | 未能复制沙箱环境（该轮未创建检查点），已为分支创建全新环境 |
| `SANDBOX_REPLACED` | 原会话中途更换过沙箱，未能复制环境，已创建全新环境 |
| `SANDBOX_GONE` | 原会话沙箱已回收，未能复制环境，已创建全新环境 |
| `SNAPSHOT_UNSUPPORTED` | 当前沙箱后端不支持环境复制，已创建全新环境 |

即使成功，也应在分支会话上标注**「依赖环境沿用了分叉操作时的最新状态」**——见 §6.1，这是本方案已知且已接受的语义偏差，必须让用户可见，否则排查时无从下手。

### 5.4 侧边栏

分叉出来的会话展示一个来源角标，点击回到父会话。会话列表现有分桶逻辑在 `frontend/src/components/menu.vue:300-343`，`parent_session_id` 随会话列表返回即可，不改分桶结构。

---

## 六、风险与已知取舍

### 6.1 【已接受】依赖环境是分叉时刻的状态，不是分叉点的状态

会话进行到第 4 轮，从第 2 轮分叉：快照复制的是**第 4 轮时刻**的整个文件系统，`git reset` 只把 `/workspace` 拉回第 2 轮。结果是分支的 `/workspace` 停在第 2 轮，但 `/usr/local`、site-packages、apt 包都是第 4 轮的。

- 常见情形（第 3 轮装了 pandas）：分支里多一个包，无害；
- 恶劣情形（第 3 轮做了 `pip uninstall`、改坏 `/etc` 配置、删了系统文件）：分支会继承这个破坏。

概率低，但排查时极易困惑。缓解：§5.3 的 UI 标注 + 在 fork 的审计日志里记录 `source_sandbox_id` 与快照时刻。

### 6.2 【必须处理】沙箱重建会切断 checkpoint 链

会话中途沙箱被回收重建（stale 重建、provider 回收、Docker idle kill）时，沙箱内 git 历史从零开始。重建之前的所有 checkpoint 都变得不可达。

处理：`sandbox_checkpoint.sandbox_id` 与当前 binding 比对，不匹配即降级（§4.2 第 4 条）。这是纯数据库判定，不需要探测沙箱。

### 6.3 【必须处理】共享的 `l.createRequest`

见 §4.6。这是本方案最容易写错、且故障表现最隐蔽的一处。

### 6.4 【必须处理】Docker 快照命名空间冲突

见 §4.8。

### 6.5 【需留意】artifact blob 的共享引用

复制的消息中 `artifacts` 指向对象存储的**同一批 blob**，父子会话共享。当前删除会话不物理删除 blob（`service/session.go:472-536`），所以现在是安全的。但**未来若给 blob 加 GC，必须先做引用计数**，否则删掉父会话会打穿所有分支的历史产物。应在 `MessageArtifacts` 相关代码处留注释。

### 6.6 【需留意】chat-history 知识库索引不能重复建

`qa.go:1439-1460` 在消息完成后会异步建 chat-history 知识库索引。fork 复制过来的历史消息**不得重复建索引**——它们在父会话里已经建过，重复会污染检索结果并浪费 embedding 配额。复制路径必须绕开这条链路。

### 6.7 【需留意】`.git` 体积

已通过 gitignore `input/` 和 `output/` 大幅缓解。但长会话中若 agent 在 `/workspace` 根目录反复重写大文件（如反复生成同一个 CSV），`.git` 仍会线性增长。

暂不处理，先观察。若成为问题，后续可加：定期 `git gc --aggressive`，或超过 N 轮后 squash 掉最早的 commit（会让很早的分叉点失效，需在 UI 上反映）。

### 6.8 【需留意】快照的存储与配额

一次性消费已经把稳态存储压到接近零。但短时间内大量 fork（用户连点、或脚本化调用）会同时存在多个未消费快照。建议加每租户"未消费快照数"上限（如 10），超限时拒绝新 fork 并提示。

### 6.9 【不处理】跨后端 fork

源会话在 Cube、目标想在 E2B——不支持。新会话强制继承 `sandbox_config_id`（§4.4 第 2 条）。

---

## 七、复用的现有设施

| 设施 | 位置 | 复用方式 |
|---|---|---|
| exactly-once 完成事件 | `internal/agent/engine.go:443-461` | commit hook 的语义锚点 |
| 完成事件的同步消费者 | `internal/handler/session/agent_stream_handler.go:650` | commit 的执行位置 |
| artifact 收集的 best-effort 语义 | `agent_stream_handler.go:690-695` | commit 失败策略的先例 |
| 三后端快照能力 | `internal/sandbox/remote_client.go:514-558`、`cube_remote_client.go:860-914`、`e2b_remote_client.go:1018-1119`、`docker_snapshot.go:30-165` | 直接使用，零改动 |
| 快照 ID → TemplateID 契约 | `remote_client.go:514-518` | fork 沙箱的创建方式 |
| 惰性建沙箱 + 生命周期锁 | `internal/sandbox/session_lifecycle.go:94-229` | bootstrap 的执行位置与串行保证 |
| turn lease | `internal/sandbox/session_binding.go:141-149` | 拦截"源会话正在跑"的 fork |
| 沙箱 binding（Redis） | `internal/sandbox/session_binding_redis.go:108-180` | 判定沙箱是否可用、比对 sandbox_id |
| 附件按 session 聚合 + 每轮重新 stage | `repository/message.go:364-387`、`session_agent_qa.go:170-196` | `/workspace/input` 自动重建，无需新代码 |
| `ExecShellCommand` | `internal/sandbox/session_manager.go:708-790` | 执行 git 命令 |
| 会话软删除时清理沙箱 | `service/session.go:670-724` | 分支会话的销毁沿用，无需改动 |
| 沙箱镜像多 target 结构 | `docker/Dockerfile.sandbox` | 加 `git` 到 runtime 层，两 target 自动继承 |

---

## 八、待验证项

建议在动工前打样，这几项会影响架构分叉：

| # | 验证内容 | 决定什么 |
|---|---|---|
| **V1** | 三后端 `CreateSnapshot` 返回的 ID 直接作为 `RemoteCreateRequest.TemplateID` 建沙箱，能否成功，且 `/workspace` 与依赖环境完整 | 整个方案能否成立 |
| **V2** | 快照建出的沙箱，其 binding 的 `TemplateID` 与 config 不一致，会不会被 `session_lifecycle` 误判并重建 | 已初步核实为否（stale 判定按 `ConfigID` 而非 `TemplateID`，见 `InvalidateByConfig`），但需实测确认 |
| **V3** | root 执行 git 操作属主为 `user` 的 `/workspace`，`safe.directory` 的正确配法（`--global` 对哪个用户生效） | §3.3 的具体写法 |
| **V4** | E2B / Cube `CreateSnapshot` 暂停源沙箱的实际时长 | fork 接口的超时设置，以及是否需要做成异步 |
| **V5** | Docker `weknora-fork/` 命名空间与现有 skill snapshot reaper 是否互不干扰 | §4.8 |
| **V6** | 长会话（50+ 轮）下 `.git` 的实际体积与 `git add -A` 耗时 | §6.7 是否需要提前处理 |
| **V7** | 快照建出的 E2B/Cube 沙箱，其 `TrafficAccessToken` 是否正常签发并写入 binding | 数据面能否连通（终端、桌面等功能依赖） |

---

## 九、实施顺序

1. **打样**：V1 / V2 / V3（决定方案能否成立，约 1–2 天）
2. **镜像**：`Dockerfile.sandbox` 加 `git`，重建并推送
3. **数据模型**：`sessions` 三字段 + `messages` 一字段的迁移（Postgres + SQLite），`types` 结构体，排序次级键修复
4. **git checkpoint**：`handleComplete` 中的 commit hook + best-effort 失败处理 + 落库
5. **fork 接口**：判定链 → turn lease 拦截 → `CreateSnapshot` → 事务内复制消息与建会话
6. **沙箱 bootstrap**：`createAndBind` 的 template override（注意 §4.6）→ 全有或全无的引导流程 → artifacts 还原 → 消费标记与快照删除
7. **GC**：孤儿快照定时清理 + Docker 独立命名空间 + 每租户未消费快照上限
8. **前端**：分叉按钮与显隐 → fork 调用与跳转预填 → 降级提示 → 侧边栏来源角标
9. **观测**：fork 的审计日志（who / when / 源会话 / 分叉点 / 快照 ID / 是否降级）
