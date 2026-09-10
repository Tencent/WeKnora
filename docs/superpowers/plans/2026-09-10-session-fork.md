# 会话分叉（Session Fork）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让用户从任意一条历史 user 消息分叉出一个新会话，新会话继承分叉点之前的对话历史与沙箱状态（含依赖环境），两条分支之后各自独立演进。

**Architecture:** 每轮 agent turn 结束时在沙箱内对 `/workspace` 打一个 git commit，把 `{sandbox_id, commit_sha}` 存进该条 assistant 消息；fork 时对源沙箱调 `CreateSnapshot` 拿到一个可当 `TemplateID` 用的快照 ID，只写数据库不建沙箱；新会话第一次用到沙箱时从该快照建沙箱，再 `git reset --hard` 把 `/workspace` 回退到分叉点。快照负责"整个文件系统"，git 负责"精确回到哪一轮"。

**Tech Stack:** Go 1.x + Gin + GORM（Postgres 主 / SQLite 测试）、`internal/sandbox` 的 `RemoteSnapshotManager`（E2B / Cube / Docker 三后端均已实现）、Vue 3 + TypeScript + TDesign + Vite。

**设计文档：** `docs/superpowers/specs/2026-09-10-session-fork-design.md`。本计划中每个任务的"设计依据"字段指向对应章节。

## Global Constraints

- 迁移编号从 `000093` 开始（当前最大为 `000092_mcp_metadata`）。每个迁移必须同时提供 `.up.sql` 和 `.down.sql`，并同步更新 `migrations/sqlite/000000_init.up.sql` 基线。
- Go 测试用 `github.com/stretchr/testify/require`，数据库测试用 `gorm.io/driver/sqlite` 的 `:memory:` + `db.AutoMigrate(...)`，参照 `internal/application/repository/session_test.go:15-38`。
- 前端测试框架是 Node 内置 `node:test` + `node:assert/strict`，通过 `tsx --test` 运行，测试文件命名 `src/**/*.test.ts`。**不是 vitest。**
- 前端 UI 库是 TDesign Vue Next（`<t-icon>` / `<t-button>` / `<t-alert>`）。
- 沙箱内 git 用户固定为 `user.email=agent@weknora.local`、`user.name=WeKnora Agent`。
- 沙箱内 git 仓库路径固定为 `/workspace`，`.gitignore` 内容固定为 `input/\noutput/\n`。
- git commit message 格式固定为 `turn:<assistantMessageID>`。
- fork 降级原因码取值只有四个：`NO_CHECKPOINT`、`SANDBOX_REPLACED`、`SANDBOX_GONE`、`SNAPSHOT_UNSUPPORTED`。`degraded=false` 时 `reason` 为空字符串。
- Docker 后端的 fork 快照命名空间固定为 `weknora-fork`，与技能快照的 `weknora-skill`（`internal/sandbox/docker_snapshot.go:17`）严格分开。
- 一切与 git / 快照相关的失败都是 best-effort：记 warn 日志、降级，**绝不阻断聊天主流程**。唯一例外是 §4.5 的 bootstrap，它必须全有或全无。
- 每完成一个 task 提交一次。commit message 用 `feat:` / `fix:` / `test:` / `chore:` 前缀，正文中文。

---

## File Structure

**新建：**

| 文件 | 职责 |
|---|---|
| `migrations/versioned/000093_session_fork.up.sql` / `.down.sql` | 四个字段的迁移 |
| `internal/types/session_fork.go` | `SandboxCheckpoint` / `ForkBootstrap` 两个 JSONB 类型 |
| `internal/application/service/workspace_checkpointer.go` | 在沙箱内执行 git init + commit，返回 sha |
| `internal/application/service/workspace_checkpointer_test.go` | 上者的单测 |
| `internal/application/service/session_fork.go` | fork 的判定链、快照、消息复制 |
| `internal/application/service/session_fork_test.go` | 上者的单测 |
| `internal/application/service/fork_bootstrapper.go` | 新沙箱首次创建后的 reset + artifacts 还原 + 消费 |
| `internal/application/service/fork_bootstrapper_test.go` | 上者的单测 |
| `internal/application/service/fork_snapshot_reaper.go` | 孤儿快照 GC |
| `internal/application/service/fork_snapshot_reaper_test.go` | 上者的单测 |
| `internal/handler/session/fork.go` | `POST /sessions/:session_id/fork` |
| `internal/handler/session/fork_test.go` | 上者的单测 |
| `internal/sandbox/session_bootstrapper.go` | `SessionBootstrapper` 接口（供 lifecycle 调用） |
| `internal/sandbox/session_bootstrapper_test.go` | lifecycle 集成该接口的测试 |
| `frontend/src/views/chat/forkPoint.ts` | 纯函数：判断某条 user 消息能否分叉、算出前置 checkpoint |
| `frontend/src/views/chat/forkPoint.test.ts` | 上者的单测 |

**修改：**

| 文件 | 改动 |
|---|---|
| `docker/Dockerfile.sandbox:40-50` | apt 清单加 `git` |
| `internal/types/session.go:75-139` | Session 加三字段 |
| `internal/types/message.go:253-324` | Message 加 `SandboxCheckpoint` |
| `internal/application/repository/message.go:50-58` | 分页排序补次级键 |
| `internal/application/repository/message.go` | 新增 `ListMessagesBySessionUpTo` |
| `internal/application/repository/session.go` | 新增 `UpdateForkBootstrap` / `ListUnconsumedForks` |
| `internal/handler/session/agent_stream_handler.go:23-108,650-731` | 注入并调用 checkpointer |
| `internal/handler/session/handler.go:19-105` | Handler 加 `forkService` |
| `internal/handler/session/helpers.go:300-315` | 传 checkpointer 给 stream handler |
| `internal/sandbox/session_manager.go:98-198` | `SessionBoundManagerConfig` 加 `Bootstrapper` |
| `internal/sandbox/session_lifecycle.go:39-92,436-521` | lifecycle 持有并调用 bootstrapper |
| `internal/sandbox/tenant_resolver.go` | `TenantSandboxResolverDeps` 透传 Bootstrapper |
| `internal/container/container.go:307-312` | 注册新 service |
| `internal/router/routes_chat.go:52-98` | 注册 fork 路由 |
| `frontend/src/api/chat/index.ts` | 加 `forkSession` |
| `frontend/src/views/chat/components/usermsg.vue` | 加分叉按钮 |
| `frontend/src/views/chat/index.vue:100-128,282-292` | 处理分叉事件、降级提示 |
| `frontend/src/components/Input-field.vue:2509-2515` | `defineExpose` 加 `prefill` |
| `frontend/src/components/SessionSidebarRow.vue:14-20` | 来源角标 |

---

## Task 1: 数据模型与迁移

**设计依据：** spec §2.1、§2.2、§2.3

**Files:**
- Create: `internal/types/session_fork.go`
- Create: `migrations/versioned/000093_session_fork.up.sql`
- Create: `migrations/versioned/000093_session_fork.down.sql`
- Create: `internal/types/session_fork_test.go`
- Modify: `internal/types/session.go`（在 `SandboxConfigID` 字段之后、`CreatedAt` 之前插入三字段）
- Modify: `internal/types/message.go`（在 `UsedMemories` 字段之后、`CreatedAt` 之前插入一字段）
- Modify: `migrations/sqlite/000000_init.up.sql`（sessions 与 messages 表基线）

**Interfaces:**
- Produces: `types.SandboxCheckpoint{SandboxID, CommitSHA string; CommittedAt time.Time}`，`*types.SandboxCheckpoint` 实现 `sql.Scanner`，值类型实现 `driver.Valuer`
- Produces: `types.ForkBootstrap{SnapshotID, CommitSHA, SourceSandboxID string; CreatedAt time.Time; ConsumedAt *time.Time}`，同上
- Produces: `types.Session.ParentSessionID string`、`types.Session.ForkedFromMessageID string`、`types.Session.ForkBootstrap *types.ForkBootstrap`
- Produces: `types.Message.SandboxCheckpoint *types.SandboxCheckpoint`

- [ ] **Step 1: 写失败的测试**

创建 `internal/types/session_fork_test.go`：

```go
package types

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestSandboxCheckpointRoundTrips(t *testing.T) {
	at := time.Date(2026, 9, 10, 9, 59, 0, 0, time.UTC)
	original := SandboxCheckpoint{SandboxID: "sbx-1", CommitSHA: "abc123", CommittedAt: at}

	raw, err := original.Value()
	require.NoError(t, err)

	var decoded SandboxCheckpoint
	require.NoError(t, decoded.Scan(raw))
	require.Equal(t, original.SandboxID, decoded.SandboxID)
	require.Equal(t, original.CommitSHA, decoded.CommitSHA)
	require.True(t, original.CommittedAt.Equal(decoded.CommittedAt))
}

func TestSandboxCheckpointScanNilYieldsZeroValue(t *testing.T) {
	var decoded SandboxCheckpoint
	require.NoError(t, decoded.Scan(nil))
	require.Equal(t, SandboxCheckpoint{}, decoded)
}

func TestSandboxCheckpointScanRejectsGarbage(t *testing.T) {
	var decoded SandboxCheckpoint
	require.Error(t, decoded.Scan([]byte("not json")))
}

func TestForkBootstrapConsumedReportsState(t *testing.T) {
	pending := ForkBootstrap{SnapshotID: "snap-1", CommitSHA: "abc123"}
	require.False(t, pending.Consumed())

	at := time.Now().UTC()
	done := ForkBootstrap{SnapshotID: "snap-1", CommitSHA: "abc123", ConsumedAt: &at}
	require.True(t, done.Consumed())
}

func TestForkBootstrapRoundTripsConsumedAt(t *testing.T) {
	at := time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC)
	original := ForkBootstrap{
		SnapshotID:      "snap-1",
		CommitSHA:       "abc123",
		SourceSandboxID: "sbx-1",
		CreatedAt:       at,
		ConsumedAt:      &at,
	}

	raw, err := original.Value()
	require.NoError(t, err)

	var decoded ForkBootstrap
	require.NoError(t, decoded.Scan(raw))
	require.Equal(t, original.SnapshotID, decoded.SnapshotID)
	require.NotNil(t, decoded.ConsumedAt)
	require.True(t, at.Equal(*decoded.ConsumedAt))
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /data/workspace/WeKnora && go test ./internal/types/ -run 'SandboxCheckpoint|ForkBootstrap' -v`
Expected: FAIL，编译错误 `undefined: SandboxCheckpoint`

- [ ] **Step 3: 实现类型**

创建 `internal/types/session_fork.go`：

```go
package types

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"
)

// SandboxCheckpoint records the git commit an agent turn produced inside the
// session sandbox's /workspace. It is written on assistant messages only.
//
// SandboxID is stored alongside CommitSHA because commit hashes are only
// meaningful within one sandbox's repository. When a session's sandbox is
// reaped and rebuilt mid-conversation the in-sandbox git history restarts from
// zero, so every checkpoint written before the rebuild becomes unreachable.
// Comparing SandboxID against the session's current binding detects that
// purely from the database, without probing the sandbox.
type SandboxCheckpoint struct {
	SandboxID   string    `json:"sandbox_id"`
	CommitSHA   string    `json:"commit_sha"`
	CommittedAt time.Time `json:"committed_at"`
}

// Value implements the driver.Valuer interface for database serialization.
func (c SandboxCheckpoint) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface for database deserialization.
func (c *SandboxCheckpoint) Scan(value any) error {
	if value == nil {
		*c = SandboxCheckpoint{}
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return errors.New("types: cannot scan sandbox checkpoint from unsupported type")
	}
	if len(b) == 0 {
		*c = SandboxCheckpoint{}
		return nil
	}
	return json.Unmarshal(b, c)
}

// ForkBootstrap carries the one-shot instructions a forked session needs the
// first time it provisions a sandbox: boot from SnapshotID instead of the
// config's template, then roll /workspace back to CommitSHA.
//
// It is consumed exactly once. After ConsumedAt is set the forked session is
// indistinguishable from an ordinary session — matching the durability
// semantics every session already has, where a reaped sandbox loses
// /workspace.
type ForkBootstrap struct {
	SnapshotID      string     `json:"snapshot_id"`
	CommitSHA       string     `json:"commit_sha"`
	SourceSandboxID string     `json:"source_sandbox_id"`
	CreatedAt       time.Time  `json:"created_at"`
	ConsumedAt      *time.Time `json:"consumed_at,omitempty"`
}

// Consumed reports whether the bootstrap has already been applied.
func (f ForkBootstrap) Consumed() bool {
	return f.ConsumedAt != nil
}

// Value implements the driver.Valuer interface for database serialization.
func (f ForkBootstrap) Value() (driver.Value, error) {
	return json.Marshal(f)
}

// Scan implements the sql.Scanner interface for database deserialization.
func (f *ForkBootstrap) Scan(value any) error {
	if value == nil {
		*f = ForkBootstrap{}
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return errors.New("types: cannot scan fork bootstrap from unsupported type")
	}
	if len(b) == 0 {
		*f = ForkBootstrap{}
		return nil
	}
	return json.Unmarshal(b, f)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd /data/workspace/WeKnora && go test ./internal/types/ -run 'SandboxCheckpoint|ForkBootstrap' -v`
Expected: PASS，5 个测试全绿

- [ ] **Step 5: 给 Session 和 Message 挂上字段**

在 `internal/types/session.go` 的 `SandboxConfigID` 字段之后插入：

```go
	// ParentSessionID names the session this one was forked from. Empty for
	// ordinary sessions. Deliberately not a foreign key: the parent may be
	// deleted while the branch lives on, and a branch must not cascade away
	// with it. A dangling value simply renders as an ordinary session.
	ParentSessionID string `json:"parent_session_id,omitempty" gorm:"type:varchar(36);index"`

	// ForkedFromMessageID is the user message, IN THE PARENT SESSION, that the
	// fork branched at. Messages strictly before it were copied here.
	ForkedFromMessageID string `json:"forked_from_message_id,omitempty" gorm:"type:varchar(36)"`

	// ForkBootstrap holds the one-shot sandbox provisioning instructions for a
	// forked session. Nil for ordinary sessions and for forks that have
	// already provisioned. See types.ForkBootstrap.
	ForkBootstrap *ForkBootstrap `json:"-" gorm:"type:jsonb;column:fork_bootstrap"`
```

在 `internal/types/message.go` 的 `UsedMemories` 字段之后插入：

```go
	// SandboxCheckpoint is the git commit this assistant turn produced in the
	// session sandbox's /workspace. Nil for user messages, for turns that ran
	// without a sandbox, and for turns whose commit failed (best-effort — a
	// failed checkpoint must never block the reply). A message without a
	// checkpoint cannot serve as a fork point with sandbox state.
	SandboxCheckpoint *SandboxCheckpoint `json:"sandbox_checkpoint,omitempty" gorm:"type:jsonb;column:sandbox_checkpoint"`
```

- [ ] **Step 6: 写迁移**

创建 `migrations/versioned/000093_session_fork.up.sql`：

```sql
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS parent_session_id VARCHAR(36);
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS forked_from_message_id VARCHAR(36);
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS fork_bootstrap JSONB;

CREATE INDEX IF NOT EXISTS idx_sessions_parent_session_id
    ON sessions (parent_session_id)
    WHERE parent_session_id IS NOT NULL;

-- Drives the orphan-snapshot reaper, which scans for forks whose bootstrap was
-- never consumed. Partial so the index stays tiny: the overwhelming majority of
-- sessions have no bootstrap at all.
CREATE INDEX IF NOT EXISTS idx_sessions_unconsumed_fork
    ON sessions ((fork_bootstrap ->> 'snapshot_id'))
    WHERE fork_bootstrap IS NOT NULL
      AND fork_bootstrap ->> 'consumed_at' IS NULL;

ALTER TABLE messages ADD COLUMN IF NOT EXISTS sandbox_checkpoint JSONB;
```

创建 `migrations/versioned/000093_session_fork.down.sql`：

```sql
DROP INDEX IF EXISTS idx_sessions_unconsumed_fork;
DROP INDEX IF EXISTS idx_sessions_parent_session_id;

ALTER TABLE sessions DROP COLUMN IF EXISTS fork_bootstrap;
ALTER TABLE sessions DROP COLUMN IF EXISTS forked_from_message_id;
ALTER TABLE sessions DROP COLUMN IF EXISTS parent_session_id;

ALTER TABLE messages DROP COLUMN IF EXISTS sandbox_checkpoint;
```

- [ ] **Step 7: 更新 SQLite 基线**

在 `migrations/sqlite/000000_init.up.sql` 的 `sessions` 建表语句里，`sandbox_config_id` 之后加：

```sql
    parent_session_id TEXT,
    forked_from_message_id TEXT,
    fork_bootstrap TEXT,
```

在同文件的 `messages` 建表语句末尾（`deleted_at` 之前）加：

```sql
    sandbox_checkpoint TEXT,
```

- [ ] **Step 8: 运行全量类型与仓储测试**

Run: `cd /data/workspace/WeKnora && go build ./... && go test ./internal/types/... ./internal/application/repository/... 2>&1 | tail -30`
Expected: 全部 PASS（新字段是可空的，现有测试不受影响）

- [ ] **Step 9: 提交**

```bash
cd /data/workspace/WeKnora
git add internal/types/session_fork.go internal/types/session_fork_test.go \
        internal/types/session.go internal/types/message.go \
        migrations/versioned/000093_session_fork.up.sql \
        migrations/versioned/000093_session_fork.down.sql \
        migrations/sqlite/000000_init.up.sql
git commit -m "feat: 会话分叉的数据模型与迁移

sessions 加 parent_session_id / forked_from_message_id / fork_bootstrap，
messages 加 sandbox_checkpoint。checkpoint 里同时存 sandbox_id，因为
commit sha 只在单个沙箱的仓库里有意义。"
```

---

## Task 2: 消息排序稳定化与"截至某条消息"查询

**设计依据：** spec §2.4

fork 依赖一个稳定的复制边界。`GetMessagesBySession` 的裸 `ORDER BY created_at ASC` 没有次级排序键，同毫秒消息的顺序在数据库层面不确定。

**Files:**
- Modify: `internal/application/repository/message.go:50-58`（补次级键）
- Modify: `internal/application/repository/message.go`（新增 `ListMessagesBySessionUpTo`）
- Modify: `internal/types/interfaces/`（`MessageRepository` 接口加方法；用 grep 定位该接口文件）
- Create: `internal/application/repository/message_fork_boundary_test.go`

**Interfaces:**
- Consumes: Task 1 的 `types.Message.SandboxCheckpoint`
- Produces: `MessageRepository.ListMessagesBySessionUpTo(ctx context.Context, sessionID string, boundary time.Time, boundaryID string) ([]*types.Message, error)` — 返回**严格早于** `(boundary, boundaryID)` 的全部消息，按 `(created_at, id)` 升序

- [ ] **Step 1: 写失败的测试**

创建 `internal/application/repository/message_fork_boundary_test.go`：

```go
package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newMessageRepositoryForForkTest(t *testing.T) (*messageRepository, *gorm.DB) {
	t.Helper()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Message{}))

	return &messageRepository{db: db}, db
}

func seedMessage(t *testing.T, db *gorm.DB, id, sessionID, role string, at time.Time) {
	t.Helper()
	require.NoError(t, db.Create(&types.Message{
		ID:        id,
		SessionID: sessionID,
		Role:      role,
		CreatedAt: at,
	}).Error)
}

// Same-millisecond messages must come back in a deterministic order, otherwise
// a fork boundary drawn at one of them is not reproducible.
func TestGetMessagesBySessionBreaksTimestampTiesById(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)

	seedMessage(t, db, "m-c", "s1", "assistant", at)
	seedMessage(t, db, "m-a", "s1", "user", at)
	seedMessage(t, db, "m-b", "s1", "assistant", at)

	got, err := repo.GetMessagesBySession(ctx, "s1", 1, 10)
	require.NoError(t, err)
	require.Equal(t, []string{"m-a", "m-b", "m-c"}, messageIDs(got))
}

func TestListMessagesBySessionUpToExcludesBoundaryAndLater(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)

	seedMessage(t, db, "m1", "s1", "user", base)
	seedMessage(t, db, "m2", "s1", "assistant", base.Add(time.Second))
	seedMessage(t, db, "m3", "s1", "user", base.Add(2*time.Second))
	seedMessage(t, db, "m4", "s1", "assistant", base.Add(3*time.Second))
	seedMessage(t, db, "other", "s2", "user", base)

	got, err := repo.ListMessagesBySessionUpTo(ctx, "s1", base.Add(2*time.Second), "m3")
	require.NoError(t, err)
	require.Equal(t, []string{"m1", "m2"}, messageIDs(got))
}

// The boundary is a composite (created_at, id) cursor, so a message sharing the
// boundary's timestamp is included only when its ID sorts before the boundary's.
func TestListMessagesBySessionUpToUsesCompositeCursor(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := context.Background()
	at := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)

	seedMessage(t, db, "m-a", "s1", "user", at)
	seedMessage(t, db, "m-b", "s1", "assistant", at)
	seedMessage(t, db, "m-c", "s1", "user", at)

	got, err := repo.ListMessagesBySessionUpTo(ctx, "s1", at, "m-c")
	require.NoError(t, err)
	require.Equal(t, []string{"m-a", "m-b"}, messageIDs(got))
}

func TestListMessagesBySessionUpToSkipsSoftDeleted(t *testing.T) {
	repo, db := newMessageRepositoryForForkTest(t)
	ctx := context.Background()
	base := time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)

	seedMessage(t, db, "m1", "s1", "user", base)
	seedMessage(t, db, "m2", "s1", "assistant", base.Add(time.Second))
	seedMessage(t, db, "boundary", "s1", "user", base.Add(2*time.Second))
	require.NoError(t, db.Delete(&types.Message{}, "id = ?", "m2").Error)

	got, err := repo.ListMessagesBySessionUpTo(ctx, "s1", base.Add(2*time.Second), "boundary")
	require.NoError(t, err)
	require.Equal(t, []string{"m1"}, messageIDs(got))
}

func messageIDs(messages []*types.Message) []string {
	ids := make([]string, 0, len(messages))
	for _, m := range messages {
		ids = append(ids, m.ID)
	}
	return ids
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /data/workspace/WeKnora && go test ./internal/application/repository/ -run 'ForkBoundary|MessagesBySessionUpTo|TiesById' -v`
Expected: FAIL，`repo.ListMessagesBySessionUpTo undefined`

- [ ] **Step 3: 补次级排序键**

把 `internal/application/repository/message.go:50-58` 改为：

```go
// GetMessagesBySession retrieves all messages for a session with pagination.
//
// The secondary sort on id is not cosmetic: messages created in the same
// millisecond would otherwise come back in an order the database is free to
// vary between calls, which makes a fork boundary drawn at one of them
// irreproducible.
func (r *messageRepository) GetMessagesBySession(
	ctx context.Context, sessionID string, page int, pageSize int,
) ([]*types.Message, error) {
	var messages []*types.Message
	if err := r.db.WithContext(ctx).Where("session_id = ?", sessionID).
		Order("created_at ASC, id ASC").
		Offset((page - 1) * pageSize).Limit(pageSize).Find(&messages).Error; err != nil {
		return nil, err
	}
	return messages, nil
}
```

- [ ] **Step 4: 实现 `ListMessagesBySessionUpTo`**

在 `internal/application/repository/message.go` 的 `ListMessagesBySessionAfterTime` 之后追加：

```go
// ListMessagesBySessionUpTo returns every message of a session that sorts
// strictly before the (boundary, boundaryID) cursor, oldest first.
//
// The cursor is composite rather than a bare timestamp because a fork boundary
// must be reproducible: two messages written in the same millisecond are
// ordered by ID, and the caller's boundary message must be excluded regardless
// of how many peers share its timestamp.
func (r *messageRepository) ListMessagesBySessionUpTo(
	ctx context.Context, sessionID string, boundary time.Time, boundaryID string,
) ([]*types.Message, error) {
	var messages []*types.Message
	if err := r.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Where("created_at < ? OR (created_at = ? AND id < ?)", boundary, boundary, boundaryID).
		Order("created_at ASC, id ASC").
		Find(&messages).Error; err != nil {
		return nil, err
	}
	return messages, nil
}
```

- [ ] **Step 5: 把方法加进接口**

Run: `cd /data/workspace/WeKnora && grep -rn "ListMessagesBySessionAfterTime" internal/types/interfaces/`

在同一个接口定义里，`ListMessagesBySessionAfterTime` 之后加：

```go
	// ListMessagesBySessionUpTo returns every message sorting strictly before
	// the (boundary, boundaryID) composite cursor, oldest first. Used by
	// session fork to copy the history preceding a fork point.
	ListMessagesBySessionUpTo(
		ctx context.Context, sessionID string, boundary time.Time, boundaryID string,
	) ([]*types.Message, error)
```

- [ ] **Step 6: 运行测试确认通过**

Run: `cd /data/workspace/WeKnora && go build ./... && go test ./internal/application/repository/ -run 'ForkBoundary|MessagesBySessionUpTo|TiesById' -v`
Expected: PASS，4 个测试全绿

- [ ] **Step 7: 跑仓储层全量回归**

Run: `cd /data/workspace/WeKnora && go test ./internal/application/repository/... 2>&1 | tail -20`
Expected: ok，无回归

- [ ] **Step 8: 提交**

```bash
cd /data/workspace/WeKnora
git add internal/application/repository/message.go \
        internal/application/repository/message_fork_boundary_test.go \
        internal/types/interfaces/
git commit -m "feat: 消息排序补次级键并新增按复合游标截取历史

分页 SQL 原本只按 created_at 排序，同毫秒消息顺序不确定，会让 fork 的
复制边界不可复现。补 id 作为次级键，并新增 ListMessagesBySessionUpTo
按 (created_at, id) 复合游标截取。"
```

---

## Task 3: 沙箱镜像安装 git

**设计依据：** spec §3.1

**Files:**
- Modify: `docker/Dockerfile.sandbox:40-50`

**Interfaces:**
- Produces: 沙箱镜像内可用的 `/usr/bin/git`

- [ ] **Step 1: 改 Dockerfile**

把 `docker/Dockerfile.sandbox` 第 35-50 行的注释与 apt 块改为：

```dockerfile
# Install minimal CLI tools (bash/grep/sed/coreutils/findutils already in slim
# image). `file` is not in slim; models reach for it after a script-type error.
# curl is not optional: the sandbox connectivity check probes egress with it,
# and skills that fetch a URL expect it to be there. The compression tools are
# here because skill bundles and their assets arrive archived. git backs the
# per-turn /workspace checkpoints that session fork rolls back to.
RUN apt-get update && apt-get install -y --no-install-recommends \
    jq \
    curl \
    ca-certificates \
    file \
    zip \
    unzip \
    bzip2 \
    xz-utils \
    zstd \
    git \
    && rm -rf /var/lib/apt/lists/* /var/cache/apt/*
```

- [ ] **Step 2: 构建镜像**

Run: `cd /data/workspace/WeKnora && docker build -f docker/Dockerfile.sandbox --target sandbox -t weknora-sandbox:fork-test .`
Expected: 构建成功

- [ ] **Step 3: 验证 git 可用且能在 /workspace 上工作**

Run:
```bash
docker run --rm weknora-sandbox:fork-test sh -c '
  set -e
  git --version
  git config --global safe.directory /workspace
  git init -q /workspace
  git -C /workspace config user.email agent@weknora.local
  git -C /workspace config user.name "WeKnora Agent"
  printf "input/\noutput/\n" > /workspace/.gitignore
  echo hello > /workspace/a.txt
  echo ignored > /workspace/output/skip.txt
  git -C /workspace add -A
  git -C /workspace commit -q --allow-empty -m turn:test
  git -C /workspace rev-parse HEAD
  echo "--- tracked files ---"
  git -C /workspace ls-files
'
```
Expected: 打印 git 版本、一个 40 位 sha；`ls-files` 只列出 `.gitignore` 和 `a.txt`，**不含** `output/skip.txt`

- [ ] **Step 4: 验证 root 操作 user 属主的 /workspace（safe.directory 场景）**

Run:
```bash
docker run --rm weknora-sandbox:fork-test sh -c '
  chown -R user:user /workspace
  git init -q /workspace
  git -C /workspace status >/dev/null 2>&1 && echo "UNEXPECTED: no dubious-ownership error" || echo "confirmed: dubious ownership blocks without safe.directory"
  git config --global safe.directory /workspace
  git -C /workspace status >/dev/null && echo "confirmed: safe.directory unblocks it"
'
```
Expected: 先打印 `confirmed: dubious ownership blocks without safe.directory`，再打印 `confirmed: safe.directory unblocks it`。这验证了 spec §3.3 的判断。

> 若第一行反而打印 `UNEXPECTED`，说明该 git 版本在 root 下不触发 dubious ownership 检查。此时 `safe.directory` 仍应保留（它是无害的幂等操作，且换个 git 版本或换个执行用户就会需要），但可以在 Task 4 的实现注释里记录这一观察。

- [ ] **Step 5: 检查镜像体积增量**

Run: `docker images weknora-sandbox:fork-test --format '{{.Size}}'`
Expected: 记录数值。相对未装 git 的版本预期增加 40-60 MB，超过 150 MB 需回头检查是否误装了 recommends。

- [ ] **Step 6: 提交**

```bash
cd /data/workspace/WeKnora
git add docker/Dockerfile.sandbox
git commit -m "feat: 沙箱镜像安装 git

会话分叉依赖每轮对 /workspace 打 git commit。git 装在 runtime 层，
sandbox 与 cube 两个 target 自动继承。"
```

---

## Task 4: WorkspaceCheckpointer

**设计依据：** spec §3.2、§3.3、§3.6、§3.7

在沙箱内执行 git 初始化与 commit，返回 sha。结构完全对照 `ArtifactCollector`（`internal/application/service/artifact_collector.go:83-161`）：窄接口 + 可注入 fake + best-effort。

**Files:**
- Create: `internal/application/service/workspace_checkpointer.go`
- Create: `internal/application/service/workspace_checkpointer_test.go`

**Interfaces:**
- Consumes: Task 1 的 `types.SandboxCheckpoint`；`sandbox.ExecuteResult`（见 `internal/sandbox/capabilities.go:20-32`）
- Produces:
  - `service.SandboxShellRunner` 接口：`ExecShellCommand(ctx context.Context, sessionID, command, workDir string, timeout time.Duration, env map[string]string) (*sandbox.ExecuteResult, error)`
  - `service.WorkspaceCheckpointer` 结构体
  - `service.NewWorkspaceCheckpointer(runner SandboxShellRunner) *WorkspaceCheckpointer`
  - `(*WorkspaceCheckpointer).Checkpoint(ctx context.Context, sessionID, sandboxID, messageID string) *types.SandboxCheckpoint` — 永不返回 error，失败返回 nil

- [ ] **Step 1: 写失败的测试**

创建 `internal/application/service/workspace_checkpointer_test.go`：

```go
package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/stretchr/testify/require"
)

type fakeShellRunner struct {
	calls   []string
	result  *sandbox.ExecuteResult
	err     error
	timeout time.Duration
	workDir string
}

func (f *fakeShellRunner) ExecShellCommand(
	_ context.Context, _ string, command, workDir string,
	timeout time.Duration, _ map[string]string,
) (*sandbox.ExecuteResult, error) {
	f.calls = append(f.calls, command)
	f.timeout = timeout
	f.workDir = workDir
	return f.result, f.err
}

func TestCheckpointReturnsShaFromLastStdoutLine(t *testing.T) {
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{
		ExitCode: 0,
		Stdout:   "Initialized empty Git repository\n1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b\n",
	}}
	cp := NewWorkspaceCheckpointer(runner)

	got := cp.Checkpoint(context.Background(), "s1", "sbx-1", "msg-1")

	require.NotNil(t, got)
	require.Equal(t, "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b", got.CommitSHA)
	require.Equal(t, "sbx-1", got.SandboxID)
	require.False(t, got.CommittedAt.IsZero())
}

func TestCheckpointScriptShape(t *testing.T) {
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{
		ExitCode: 0, Stdout: strings.Repeat("a", 40) + "\n",
	}}
	cp := NewWorkspaceCheckpointer(runner)

	cp.Checkpoint(context.Background(), "s1", "sbx-1", "msg-42")

	require.Len(t, runner.calls, 1)
	script := runner.calls[0]

	// safe.directory must precede the rev-parse probe: the probe itself is
	// what dubious-ownership blocks.
	require.Less(t,
		strings.Index(script, "safe.directory"),
		strings.Index(script, "rev-parse --git-dir"),
	)
	// Plain assignment, not --add: safe.directory is multi-valued and --add
	// would append a duplicate line to ~/.gitconfig on every single turn.
	require.NotContains(t, script, "--add safe.directory")
	require.Contains(t, script, "--allow-empty")
	require.Contains(t, script, "turn:msg-42")
	require.Contains(t, script, "printf 'input/\\noutput/\\n'")
	require.Equal(t, workspaceCheckpointTimeout, runner.timeout)
	require.Equal(t, sandbox.SessionWorkspaceRoot, runner.workDir)
}

func TestCheckpointReturnsNilWhenExecFails(t *testing.T) {
	runner := &fakeShellRunner{err: errors.New("sandbox unreachable")}
	cp := NewWorkspaceCheckpointer(runner)

	require.Nil(t, cp.Checkpoint(context.Background(), "s1", "sbx-1", "msg-1"))
}

func TestCheckpointReturnsNilOnNonZeroExit(t *testing.T) {
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{
		ExitCode: 128, Stderr: "fatal: not a git repository",
	}}
	cp := NewWorkspaceCheckpointer(runner)

	require.Nil(t, cp.Checkpoint(context.Background(), "s1", "sbx-1", "msg-1"))
}

func TestCheckpointReturnsNilWhenStdoutIsNotASha(t *testing.T) {
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{
		ExitCode: 0, Stdout: "warning: something odd\n",
	}}
	cp := NewWorkspaceCheckpointer(runner)

	require.Nil(t, cp.Checkpoint(context.Background(), "s1", "sbx-1", "msg-1"))
}

func TestCheckpointReturnsNilWhenRunnerMissing(t *testing.T) {
	require.Nil(t, NewWorkspaceCheckpointer(nil).Checkpoint(
		context.Background(), "s1", "sbx-1", "msg-1"))

	var nilCheckpointer *WorkspaceCheckpointer
	require.Nil(t, nilCheckpointer.Checkpoint(context.Background(), "s1", "sbx-1", "msg-1"))
}

func TestCheckpointReturnsNilWithoutSandboxID(t *testing.T) {
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{
		ExitCode: 0, Stdout: strings.Repeat("a", 40) + "\n",
	}}
	cp := NewWorkspaceCheckpointer(runner)

	// No bound sandbox means there is nothing to check point, and a checkpoint
	// without a sandbox ID could never be validated at fork time.
	require.Nil(t, cp.Checkpoint(context.Background(), "s1", "", "msg-1"))
	require.Empty(t, runner.calls)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /data/workspace/WeKnora && go test ./internal/application/service/ -run 'TestCheckpoint' -v`
Expected: FAIL，`undefined: NewWorkspaceCheckpointer`

- [ ] **Step 3: 实现**

创建 `internal/application/service/workspace_checkpointer.go`：

```go
// Package service - per-turn /workspace git checkpoints.
//
// WorkspaceCheckpointer commits the session sandbox's /workspace at the end of
// every agent turn so session fork can later roll a forked sandbox back to a
// specific turn. It mirrors ArtifactCollector's shape deliberately: a narrow
// injectable interface for the sandbox side, and strict best-effort semantics
// so an auxiliary feature can never break a reply.
//
// Contract:
//   - Never returns an error. A failed checkpoint yields nil, the message
//     simply carries no checkpoint, and that fork point degrades.
//   - Never lazy-creates a sandbox: an empty sandboxID short-circuits.
//   - Idempotent inside the sandbox: the script initialises the repository
//     only when absent, so pause/resume and sandbox rebuilds self-heal.
package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// SandboxShellRunner is the narrow subset of sandbox behaviour the
// checkpointer needs. *sandbox.SessionBoundManager satisfies it in production;
// tests use a fake to assert on the emitted script without a real sandbox.
type SandboxShellRunner interface {
	ExecShellCommand(
		ctx context.Context,
		sessionID, command, workDir string,
		timeout time.Duration,
		env map[string]string,
	) (*sandbox.ExecuteResult, error)
}

// workspaceCheckpointTimeout bounds the git round-trip. `git add -A` walks the
// whole working tree, so this is generous relative to a normal commit but
// still short enough that a wedged sandbox cannot stall the completion path.
const workspaceCheckpointTimeout = 30 * time.Second

// gitSHAPattern matches a full 40-character hex object name. The script's last
// stdout line must look like one; anything else means git printed a warning or
// the script diverged, and we refuse to record a checkpoint we cannot trust.
var gitSHAPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// WorkspaceCheckpointer commits /workspace after each agent turn.
type WorkspaceCheckpointer struct {
	runner SandboxShellRunner
}

// NewWorkspaceCheckpointer wires a checkpointer. A nil runner yields a
// checkpointer that degrades to "no checkpoint", matching the graceful
// degradation contract used by ArtifactCollector.
func NewWorkspaceCheckpointer(runner SandboxShellRunner) *WorkspaceCheckpointer {
	return &WorkspaceCheckpointer{runner: runner}
}

// checkpointScript renders the idempotent init-and-commit script.
//
// Ordering matters in two places:
//
//   - `safe.directory` comes first. /workspace is owned by uid 1000 while
//     shell_exec runs as root by default, and git refuses to touch a
//     dubiously-owned repository — including from the rev-parse probe below.
//     Plain assignment rather than `--add`: safe.directory is a multi-valued
//     key, so --add would append one duplicate line to ~/.gitconfig per turn.
//
//   - `--allow-empty` is mandatory. A turn that only answered a question
//     without touching a file would otherwise produce no commit, leaving that
//     message with no checkpoint and making it unusable as a fork point. With
//     it, every turn has exactly one checkpoint and fork boundaries are always
//     resolvable.
func checkpointScript(workspace, messageID string) string {
	return fmt.Sprintf(`set -e
git config --global safe.directory %[1]s
git -C %[1]s rev-parse --git-dir >/dev/null 2>&1 || {
  git init -q %[1]s
  git -C %[1]s config user.email agent@weknora.local
  git -C %[1]s config user.name 'WeKnora Agent'
  printf 'input/\noutput/\n' > %[1]s/.gitignore
}
git -C %[1]s add -A
git -C %[1]s commit -q --allow-empty -m 'turn:%[2]s'
git -C %[1]s rev-parse HEAD`, workspace, messageID)
}

// Checkpoint commits /workspace and returns the resulting checkpoint, or nil
// when anything at all went wrong. It never returns an error by design: the
// caller runs on the turn-completion path and must not be blocked here.
func (c *WorkspaceCheckpointer) Checkpoint(
	ctx context.Context, sessionID, sandboxID, messageID string,
) *types.SandboxCheckpoint {
	if c == nil || c.runner == nil {
		return nil
	}
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(messageID) == "" {
		return nil
	}
	// No bound sandbox means there is nothing to snapshot, and a checkpoint
	// without a sandbox ID could never be validated at fork time anyway.
	if strings.TrimSpace(sandboxID) == "" {
		return nil
	}

	result, err := c.runner.ExecShellCommand(
		ctx,
		sessionID,
		checkpointScript(sandbox.SessionWorkspaceRoot, messageID),
		sandbox.SessionWorkspaceRoot,
		workspaceCheckpointTimeout,
		nil,
	)
	if err != nil {
		logger.Warnf(ctx, "[WorkspaceCheckpointer] exec failed session=%s message=%s: %v",
			sessionID, messageID, err)
		return nil
	}
	if result == nil {
		logger.Warnf(ctx, "[WorkspaceCheckpointer] nil result session=%s message=%s",
			sessionID, messageID)
		return nil
	}
	if result.ExitCode != 0 {
		logger.Warnf(ctx,
			"[WorkspaceCheckpointer] git exited %d session=%s message=%s stderr=%s",
			result.ExitCode, sessionID, messageID, truncateForLog(result.Stderr))
		return nil
	}

	sha := lastNonEmptyLine(result.Stdout)
	if !gitSHAPattern.MatchString(sha) {
		logger.Warnf(ctx,
			"[WorkspaceCheckpointer] unexpected stdout tail session=%s message=%s tail=%q",
			sessionID, messageID, truncateForLog(sha))
		return nil
	}

	return &types.SandboxCheckpoint{
		SandboxID:   sandboxID,
		CommitSHA:   sha,
		CommittedAt: time.Now().UTC(),
	}
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

func truncateForLog(s string) string {
	const limit = 256
	s = strings.TrimSpace(s)
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "…"
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd /data/workspace/WeKnora && go test ./internal/application/service/ -run 'TestCheckpoint' -v`
Expected: PASS，7 个测试全绿

> 若 `truncateForLog` 与该包内已有同名函数冲突，改名为 `truncateCheckpointLog` 并同步更新两处调用。先用 `grep -rn "func truncateForLog" internal/application/service/` 确认。

- [ ] **Step 5: 提交**

```bash
cd /data/workspace/WeKnora
git add internal/application/service/workspace_checkpointer.go \
        internal/application/service/workspace_checkpointer_test.go
git commit -m "feat: WorkspaceCheckpointer，每轮对沙箱 /workspace 打 git commit

结构对照 ArtifactCollector：窄接口 + 可注入 fake + 严格 best-effort。
safe.directory 用赋值而非 --add，避免每轮往 ~/.gitconfig 追加重复行；
--allow-empty 保证没碰文件的轮次也有 checkpoint，fork 边界永远可解析。"
```

---

## Task 5: 把 checkpointer 接到 turn 完成路径

**设计依据：** spec §3.4、§3.5、§3.6

**Files:**
- Modify: `internal/handler/session/agent_stream_handler.go`（结构体字段、构造函数、`handleComplete`）
- Modify: `internal/handler/session/helpers.go:300-315`
- Modify: `internal/handler/session/handler.go:19-105`
- Modify: `internal/container/container.go`（约 307-312 行附近）
- Create: `internal/handler/session/workspace_checkpoint_completion_test.go`

**Interfaces:**
- Consumes: Task 4 的 `service.WorkspaceCheckpointer`、`(*WorkspaceCheckpointer).Checkpoint`
- Consumes: Task 1 的 `types.Message.SandboxCheckpoint`
- Produces: `NewAgentStreamHandler` 增加两个尾部参数 `checkpointer *service.WorkspaceCheckpointer, sandboxIDLookup SandboxIDLookup`
- Produces: `session.SandboxIDLookup` 接口：`BoundSandboxID(ctx context.Context, sessionID string) (string, bool)`

> `BoundSandboxID` 需要一个"只读、不惰性创建沙箱"的查询。先用
> `grep -rn "lookupSessionHandle\|func (m \*SessionBoundManager)" internal/sandbox/session_manager.go | head -40`
> 确认是否已有可导出的等价方法；`lookupSessionHandle`（`session_manager.go:474`）是未导出的正确语义（有绑定才返回，不创建）。若无导出版本，在 `internal/sandbox/session_manager.go` 上加一个薄封装：
>
> ```go
> // BoundSandboxID returns the ID of the sandbox currently bound to sessionID.
> // It never provisions: a session with no live binding reports ok=false, which
> // callers treat as "nothing to check point".
> func (m *SessionBoundManager) BoundSandboxID(
> 	ctx context.Context, sessionID string,
> ) (string, bool) {
> 	handle, ok, err := m.lookupSessionHandle(ctx, sessionID)
> 	if err != nil || !ok || handle == nil {
> 		return "", false
> 	}
> 	return handle.ID(), true
> }
> ```

- [ ] **Step 1: 写失败的测试**

创建 `internal/handler/session/workspace_checkpoint_completion_test.go`。参照现有 `internal/handler/session/artifact_completion_test.go:30-45` 的构造方式：

```go
package session

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type stubShellRunner struct {
	stdout   string
	exitCode int
	err      error
	calls    int
}

func (s *stubShellRunner) ExecShellCommand(
	_ context.Context, _ string, _, _ string, _ time.Duration, _ map[string]string,
) (*sandbox.ExecuteResult, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return &sandbox.ExecuteResult{ExitCode: s.exitCode, Stdout: s.stdout}, nil
}

type stubSandboxIDLookup struct {
	id string
	ok bool
}

func (s stubSandboxIDLookup) BoundSandboxID(context.Context, string) (string, bool) {
	return s.id, s.ok
}

func newCheckpointHandler(
	t *testing.T, runner service.SandboxShellRunner, lookup SandboxIDLookup,
) (*AgentStreamHandler, *types.Message) {
	t.Helper()
	message := &types.Message{ID: "m1", SessionID: "s1", Role: "assistant"}
	h := NewAgentStreamHandler(
		context.Background(), "s1", "m1", "req1", 1, time.Now(),
		message, nil, event.NewEventBus(), nil,
		service.NewWorkspaceCheckpointer(runner), lookup,
	)
	return h, message
}

func completeEvent() event.Event {
	return event.Event{Data: event.AgentCompleteData{MessageID: "m1", FinalAnswer: "done"}}
}

func TestHandleCompleteRecordsCheckpoint(t *testing.T) {
	runner := &stubShellRunner{stdout: strings.Repeat("a", 40) + "\n"}
	h, message := newCheckpointHandler(t, runner, stubSandboxIDLookup{id: "sbx-1", ok: true})

	require.NoError(t, h.handleComplete(context.Background(), completeEvent()))

	require.Equal(t, 1, runner.calls)
	require.NotNil(t, message.SandboxCheckpoint)
	require.Equal(t, "sbx-1", message.SandboxCheckpoint.SandboxID)
	require.Equal(t, strings.Repeat("a", 40), message.SandboxCheckpoint.CommitSHA)
}

// A failed checkpoint must never disturb the reply: the message still
// completes, it just carries no checkpoint and cannot serve as a fork point.
func TestHandleCompleteSurvivesCheckpointFailure(t *testing.T) {
	runner := &stubShellRunner{err: errors.New("sandbox unreachable")}
	h, message := newCheckpointHandler(t, runner, stubSandboxIDLookup{id: "sbx-1", ok: true})

	require.NoError(t, h.handleComplete(context.Background(), completeEvent()))

	require.Nil(t, message.SandboxCheckpoint)
	require.True(t, message.IsCompleted)
}

func TestHandleCompleteSkipsCheckpointWithoutBoundSandbox(t *testing.T) {
	runner := &stubShellRunner{stdout: strings.Repeat("a", 40) + "\n"}
	h, message := newCheckpointHandler(t, runner, stubSandboxIDLookup{ok: false})

	require.NoError(t, h.handleComplete(context.Background(), completeEvent()))

	require.Zero(t, runner.calls)
	require.Nil(t, message.SandboxCheckpoint)
}

func TestHandleCompleteWorksWithoutCheckpointerWired(t *testing.T) {
	message := &types.Message{ID: "m1", SessionID: "s1", Role: "assistant"}
	h := NewAgentStreamHandler(
		context.Background(), "s1", "m1", "req1", 1, time.Now(),
		message, nil, event.NewEventBus(), nil, nil, nil,
	)

	require.NoError(t, h.handleComplete(context.Background(), completeEvent()))
	require.Nil(t, message.SandboxCheckpoint)
}
```

> 构造 `event.Event` / `event.AgentCompleteData` 与 `event.NewEventBus()` 的确切写法以 `internal/handler/session/artifact_completion_test.go` 现有代码为准；若字段名不同，照抄该文件的用法。若 `streamManager` 传 nil 会 panic，同样照抄该文件的处理。

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /data/workspace/WeKnora && go test ./internal/handler/session/ -run 'TestHandleComplete.*Checkpoint|TestHandleCompleteSurvives|TestHandleCompleteWorksWithout' -v`
Expected: FAIL，`NewAgentStreamHandler` 参数数量不匹配

- [ ] **Step 3: 扩展 AgentStreamHandler**

在 `internal/handler/session/agent_stream_handler.go` 的 import 之后、`AgentStreamHandler` 定义之前插入：

```go
// SandboxIDLookup reports the sandbox currently bound to a session without
// provisioning one. A session with no live sandbox reports ok=false, which the
// completion path treats as "nothing to check point".
type SandboxIDLookup interface {
	BoundSandboxID(ctx context.Context, sessionID string) (string, bool)
}
```

在 `AgentStreamHandler` 结构体的 `artifactCollector` 字段之后加：

```go
	// checkpointer commits the sandbox's /workspace at the end of the turn so
	// session fork can roll a forked sandbox back to this exact message. Nil
	// when the deployment has no sandbox backend; handleComplete checks.
	checkpointer *service.WorkspaceCheckpointer

	// sandboxIDLookup resolves the session's currently bound sandbox ID. The
	// ID is stored next to the commit SHA because a SHA is only meaningful
	// within one sandbox's git repository.
	sandboxIDLookup SandboxIDLookup
```

在 `NewAgentStreamHandler` 的参数列表末尾加两个参数并在返回的结构体里赋值：

```go
func NewAgentStreamHandler(
	ctx context.Context,
	sessionID, assistantMessageID, requestID string,
	tenantID uint64,
	receivedAt time.Time,
	assistantMessage *types.Message,
	streamManager interfaces.StreamManager,
	eventBus *event.EventBus,
	artifactCollector *service.ArtifactCollector,
	checkpointer *service.WorkspaceCheckpointer,
	sandboxIDLookup SandboxIDLookup,
) *AgentStreamHandler {
	return &AgentStreamHandler{
		// ... 现有字段保持不变 ...
		artifactCollector:  artifactCollector,
		checkpointer:       checkpointer,
		sandboxIDLookup:    sandboxIDLookup,
		knowledgeRefs:      make([]*types.SearchResult, 0),
		eventStartTimes:    make(map[string]time.Time),
	}
}
```

- [ ] **Step 4: 在 handleComplete 里调用**

在 `internal/handler/session/agent_stream_handler.go` 的 `handleComplete` 中，紧挨在 artifact 收集那段（`var previous types.MessageArtifacts` 之前）插入：

```go
		// Check point /workspace before draining artifacts. Every tool has
		// finished, and the turn's sandbox lease is still held, so the sandbox
		// is guaranteed to still be here.
		//
		// This runs on every exit path, including cancelled and errored turns.
		// Skipping unsuccessful turns would fold their file changes into the
		// NEXT turn's commit, so forking at that next turn would silently pick
		// up the cancelled turn's edits.
		//
		// Best-effort throughout: a nil checkpoint just means this message
		// cannot serve as a fork point.
		if h.checkpointer != nil && h.sandboxIDLookup != nil {
			checkpointCtx := context.WithoutCancel(h.ctx)
			if sandboxID, ok := h.sandboxIDLookup.BoundSandboxID(checkpointCtx, h.sessionID); ok {
				h.assistantMessage.SandboxCheckpoint = h.checkpointer.Checkpoint(
					checkpointCtx, h.sessionID, sandboxID, h.assistantMessageID,
				)
			}
		}
```

- [ ] **Step 5: 更新调用点**

`internal/handler/session/helpers.go:300-315` 的 `setupStreamHandler` 改为：

```go
func (h *Handler) setupStreamHandler(
	ctx context.Context,
	sessionID, assistantMessageID, requestID string,
	tenantID uint64,
	receivedAt time.Time,
	assistantMessage *types.Message,
	eventBus *event.EventBus,
) *AgentStreamHandler {
	streamHandler := NewAgentStreamHandler(
		ctx, sessionID, assistantMessageID, requestID, tenantID, receivedAt,
		assistantMessage, h.streamManager, eventBus, h.artifactCollector,
		h.workspaceCheckpointer, h.sandboxIDLookup,
	)
	streamHandler.Subscribe()
	return streamHandler
}
```

`internal/handler/session/handler.go` 的 `Handler` 结构体在 `artifactCollector` 之后加两个字段，`NewHandler` 参数列表在 `artifactCollector` 之后加对应两个参数并赋值：

```go
	// workspaceCheckpointer commits the sandbox /workspace at the end of each
	// agent turn so session fork can roll back to a specific message. May be
	// nil when the deployment has no sandbox backend.
	workspaceCheckpointer *service.WorkspaceCheckpointer
	// sandboxIDLookup resolves a session's bound sandbox without provisioning.
	sandboxIDLookup SandboxIDLookup
```

修复 `internal/handler/session/artifact_completion_test.go:36-37` 的调用：在末尾补 `, nil, nil`。

- [ ] **Step 6: 容器接线**

在 `internal/container/container.go` 的 `service.NewArtifactCollectorFromSandboxManager` 注册（约 312 行）之后加：

```go
	// WorkspaceCheckpointer commits the sandbox /workspace after each agent
	// turn so session fork can roll a forked sandbox back to a given message.
	// Degrades to nil-safe no-ops when the deployment has no sandbox backend.
	must(container.Provide(func(mgr sandbox.Manager) *service.WorkspaceCheckpointer {
		runner, ok := mgr.(service.SandboxShellRunner)
		if !ok {
			return service.NewWorkspaceCheckpointer(nil)
		}
		return service.NewWorkspaceCheckpointer(runner)
	}))
	must(container.Provide(func(mgr sandbox.Manager) session.SandboxIDLookup {
		lookup, ok := mgr.(session.SandboxIDLookup)
		if !ok {
			return nil
		}
		return lookup
	}))
```

> 若 `internal/container/container.go` 尚未 import `internal/handler/session`，会引入循环依赖风险。先执行
> `grep -n "handler/session" internal/container/container.go`。若没有该 import，把 `SandboxIDLookup` 接口改定义在 `internal/application/service` 包里（与 `SandboxShellRunner` 并列），`session` 包引用 `service.SandboxIDLookup`。**优先采用后者**，它不引入新的包依赖方向。

- [ ] **Step 7: 运行测试确认通过**

Run: `cd /data/workspace/WeKnora && go build ./... && go test ./internal/handler/session/ -run 'Checkpoint|Complete' -v`
Expected: PASS，包括原有的 artifact 完成测试

- [ ] **Step 8: 全量回归**

Run: `cd /data/workspace/WeKnora && go test ./internal/... 2>&1 | grep -v "^ok\|no test files" | head -30`
Expected: 无 FAIL 输出

- [ ] **Step 9: 提交**

```bash
cd /data/workspace/WeKnora
git add internal/handler/session/ internal/container/container.go internal/sandbox/session_manager.go
git commit -m "feat: turn 完成时记录 /workspace 的 git checkpoint

插在 handleComplete 里 artifact 收集之前：此时所有 tool 已结束、turn
lease 未释放，沙箱一定还在。取消和报错的轮次也照常 commit，否则那轮的
文件改动会被并进下一轮的 commit。"
```

---

## Task 6: SessionForkService

**设计依据：** spec §4.2、§4.3、§4.4

**Files:**
- Create: `internal/application/service/session_fork.go`
- Create: `internal/application/service/session_fork_test.go`
- Modify: `internal/application/repository/session.go`（新增两个方法）
- Modify: `internal/types/interfaces/`（`SessionRepository` 加方法）

**Interfaces:**
- Consumes: Task 1 的 `types.ForkBootstrap` / `types.SandboxCheckpoint`；Task 2 的 `ListMessagesBySessionUpTo`
- Produces:
  - `service.ForkDegradeReason` 字符串类型，常量 `ForkDegradeNoCheckpoint = "NO_CHECKPOINT"`、`ForkDegradeSandboxReplaced = "SANDBOX_REPLACED"`、`ForkDegradeSandboxGone = "SANDBOX_GONE"`、`ForkDegradeSnapshotUnsupported = "SNAPSHOT_UNSUPPORTED"`
  - `service.ForkResult{SessionID string; Degraded bool; Reason ForkDegradeReason}`
  - `service.ErrForkSourceBusy`（哨兵 error，handler 映射为 409）
  - `service.SessionForkSandboxPort` 接口：`BoundSandboxID(ctx, sessionID) (string, bool)`、`HasActiveTurn(ctx, sessionID) (bool, error)`、`CreateForkSnapshot(ctx, sessionID, name string) (string, error)`
  - `service.NewSessionForkService(sessions, messages, sandboxPort) *SessionForkService`
  - `(*SessionForkService).Fork(ctx context.Context, tenantID uint64, userID, sourceSessionID, messageID, title string) (*ForkResult, error)`
- Produces: `SessionRepository.UpdateForkBootstrap(ctx, sessionID string, b *types.ForkBootstrap) error`
- Produces: `SessionRepository.ListUnconsumedForks(ctx context.Context, olderThan time.Time) ([]*types.Session, error)`

- [ ] **Step 1: 写失败的测试**

创建 `internal/application/service/session_fork_test.go`：

```go
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type fakeForkSandboxPort struct {
	boundID        string
	bound          bool
	activeTurn     bool
	activeTurnErr  error
	snapshotID     string
	snapshotErr    error
	snapshotCalls  int
	snapshotNameIn string
}

func (f *fakeForkSandboxPort) BoundSandboxID(context.Context, string) (string, bool) {
	return f.boundID, f.bound
}

func (f *fakeForkSandboxPort) HasActiveTurn(context.Context, string) (bool, error) {
	return f.activeTurn, f.activeTurnErr
}

func (f *fakeForkSandboxPort) CreateForkSnapshot(_ context.Context, _, name string) (string, error) {
	f.snapshotCalls++
	f.snapshotNameIn = name
	return f.snapshotID, f.snapshotErr
}

// --- fixture helpers -------------------------------------------------------

var forkBase = time.Date(2026, 9, 10, 9, 0, 0, 0, time.UTC)

func checkpointedTurn(userID, assistantID, sandboxID, sha string, offset time.Duration) []*types.Message {
	return []*types.Message{
		{ID: userID, SessionID: "src", Role: "user", CreatedAt: forkBase.Add(offset)},
		{
			ID: assistantID, SessionID: "src", Role: "assistant",
			CreatedAt: forkBase.Add(offset + time.Second),
			SandboxCheckpoint: &types.SandboxCheckpoint{
				SandboxID: sandboxID, CommitSHA: sha, CommittedAt: forkBase.Add(offset + time.Second),
			},
		},
	}
}

func newForkFixture(t *testing.T, port *fakeForkSandboxPort, messages []*types.Message) (
	*SessionForkService, *fakeSessionStore, *fakeMessageStore,
) {
	t.Helper()
	sessions := newFakeSessionStore(&types.Session{
		ID: "src", TenantID: 1, UserID: "u1", Title: "原会话", SandboxConfigID: "cfg-1",
	})
	msgs := newFakeMessageStore(messages)
	return NewSessionForkService(sessions, msgs, port), sessions, msgs
}

// --- tests -----------------------------------------------------------------

func TestForkHappyPathTakesSnapshotAndCopiesHistory(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-1"}
	svc, sessions, _ := newForkFixture(t, port, append(turn, forkPoint))

	got, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.NoError(t, err)
	require.False(t, got.Degraded)
	require.Empty(t, got.Reason)
	require.Equal(t, 1, port.snapshotCalls)

	created := sessions.created
	require.NotNil(t, created)
	require.Equal(t, "src", created.ParentSessionID)
	require.Equal(t, "u-msg-2", created.ForkedFromMessageID)
	require.Equal(t, "cfg-1", created.SandboxConfigID, "必须继承 sandbox_config_id，否则可能落到别的 backend")
	require.Equal(t, "原会话（分支）", created.Title)
	require.NotNil(t, created.ForkBootstrap)
	require.Equal(t, "snap-1", created.ForkBootstrap.SnapshotID)
	require.Equal(t, "sha1", created.ForkBootstrap.CommitSHA)
	require.Equal(t, "sbx-1", created.ForkBootstrap.SourceSandboxID)
	require.False(t, created.ForkBootstrap.Consumed())

	// 只复制分叉点之前的消息，且都换了新 ID
	require.Equal(t, []string{"u-msg-1", "a-msg-1"}, sessions.copiedFromIDs())
	for _, m := range sessions.copiedMessages {
		require.NotEmpty(t, m.ID)
		require.Equal(t, created.ID, m.SessionID)
	}
}

func TestForkUsesProvidedTitle(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-1"}
	svc, sessions, _ := newForkFixture(t, port, append(turn, forkPoint))

	_, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "我的分支")

	require.NoError(t, err)
	require.Equal(t, "我的分支", sessions.created.Title)
}

// 分叉点是第一条 user 消息：前面本就无产物，全新沙箱才是正确结果，不算降级。
func TestForkAtFirstUserMessageIsNotDegraded(t *testing.T) {
	first := &types.Message{ID: "u-msg-1", SessionID: "src", Role: "user", CreatedAt: forkBase}
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-1"}
	svc, sessions, _ := newForkFixture(t, port, []*types.Message{first})

	got, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-1", "")

	require.NoError(t, err)
	require.False(t, got.Degraded)
	require.Empty(t, got.Reason)
	require.Zero(t, port.snapshotCalls, "没有前置产物就不该浪费一个快照")
	require.Nil(t, sessions.created.ForkBootstrap)
	require.Empty(t, sessions.copiedFromIDs())
}

func TestForkDegradesWhenPrecedingTurnHasNoCheckpoint(t *testing.T) {
	messages := []*types.Message{
		{ID: "u-msg-1", SessionID: "src", Role: "user", CreatedAt: forkBase},
		{ID: "a-msg-1", SessionID: "src", Role: "assistant", CreatedAt: forkBase.Add(time.Second)},
		{ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second)},
	}
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-1"}
	svc, sessions, _ := newForkFixture(t, port, messages)

	got, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.NoError(t, err)
	require.True(t, got.Degraded)
	require.Equal(t, ForkDegradeNoCheckpoint, got.Reason)
	require.Zero(t, port.snapshotCalls)
	require.Nil(t, sessions.created.ForkBootstrap)
	require.Equal(t, []string{"u-msg-1", "a-msg-1"}, sessions.copiedFromIDs(), "降级也要复制消息")
}

// 会话中途换过沙箱：旧 sha 在新沙箱的仓库里根本不存在。
func TestForkDegradesWhenSandboxWasReplaced(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-OLD", "sha1", 0)
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := &fakeForkSandboxPort{boundID: "sbx-NEW", bound: true, snapshotID: "snap-1"}
	svc, _, _ := newForkFixture(t, port, append(turn, forkPoint))

	got, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.NoError(t, err)
	require.True(t, got.Degraded)
	require.Equal(t, ForkDegradeSandboxReplaced, got.Reason)
	require.Zero(t, port.snapshotCalls)
}

func TestForkDegradesWhenSandboxGone(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := &fakeForkSandboxPort{bound: false}
	svc, _, _ := newForkFixture(t, port, append(turn, forkPoint))

	got, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.NoError(t, err)
	require.True(t, got.Degraded)
	require.Equal(t, ForkDegradeSandboxGone, got.Reason)
}

func TestForkDegradesWhenSnapshotFails(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := &fakeForkSandboxPort{
		boundID: "sbx-1", bound: true, snapshotErr: errors.New("provider does not support snapshots"),
	}
	svc, sessions, _ := newForkFixture(t, port, append(turn, forkPoint))

	got, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.NoError(t, err, "快照失败是降级，不是报错")
	require.True(t, got.Degraded)
	require.Equal(t, ForkDegradeSnapshotUnsupported, got.Reason)
	require.Nil(t, sessions.created.ForkBootstrap)
}

// 源沙箱正在跑 agent 时打快照会暂停它、打断执行中的工具。
func TestForkRefusesWhileSourceTurnIsActive(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	forkPoint := &types.Message{
		ID: "u-msg-2", SessionID: "src", Role: "user", CreatedAt: forkBase.Add(10 * time.Second),
	}
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, activeTurn: true, snapshotID: "snap-1"}
	svc, sessions, _ := newForkFixture(t, port, append(turn, forkPoint))

	_, err := svc.Fork(context.Background(), 1, "u1", "src", "u-msg-2", "")

	require.ErrorIs(t, err, ErrForkSourceBusy)
	require.Zero(t, port.snapshotCalls)
	require.Nil(t, sessions.created, "拒绝时不得留下半个会话")
}

func TestForkRejectsAssistantMessageAsForkPoint(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-1"}
	svc, _, _ := newForkFixture(t, port, turn)

	_, err := svc.Fork(context.Background(), 1, "u1", "src", "a-msg-1", "")

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrForkSourceBusy)
}

func TestForkRejectsForeignSession(t *testing.T) {
	turn := checkpointedTurn("u-msg-1", "a-msg-1", "sbx-1", "sha1", 0)
	port := &fakeForkSandboxPort{boundID: "sbx-1", bound: true, snapshotID: "snap-1"}
	svc, _, _ := newForkFixture(t, port, turn)

	_, err := svc.Fork(context.Background(), 1, "someone-else", "src", "u-msg-1", "")

	require.Error(t, err)
}
```

> `fakeSessionStore` / `fakeMessageStore` 需要你在同文件里实现，接口就是 `SessionForkService` 依赖的那两个仓储端口（见 Step 3 的 `forkSessionStore` / `forkMessageStore`）。`fakeSessionStore` 需暴露 `created *types.Session`、`copiedMessages []*types.Message`，以及 `copiedFromIDs()` 返回复制来源的原始 ID 列表（在 fake 的 `CopyMessages` 里记录入参）。

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /data/workspace/WeKnora && go test ./internal/application/service/ -run 'TestFork' -v`
Expected: FAIL，`undefined: NewSessionForkService`

- [ ] **Step 3: 实现**

创建 `internal/application/service/session_fork.go`：

```go
// Package service - session fork.
//
// Fork copies a session's history up to a chosen user message into a brand new
// session, and arranges for that new session's first sandbox to boot from a
// snapshot of the source sandbox rolled back to the fork point.
//
// The whole operation is cheap and synchronous: it writes the database and
// takes one provider snapshot. No sandbox is created here — provisioning stays
// lazy, exactly as it is for ordinary sessions.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
)

// ForkDegradeReason explains why a fork could not carry the source sandbox's
// state over. A degraded fork still succeeds — it just starts from a brand new
// sandbox — because the alternative (refusing) would block the common cases:
// Docker kills idle containers outright, and plain chat sessions never had a
// sandbox to begin with.
type ForkDegradeReason string

const (
	// ForkDegradeNoCheckpoint: the turn preceding the fork point never
	// produced a git checkpoint (commit failed, or it ran without a sandbox).
	ForkDegradeNoCheckpoint ForkDegradeReason = "NO_CHECKPOINT"

	// ForkDegradeSandboxReplaced: the checkpoint belongs to a sandbox the
	// session no longer uses. In-sandbox git history restarts from zero on
	// every rebuild, so that SHA is unreachable from the current sandbox.
	ForkDegradeSandboxReplaced ForkDegradeReason = "SANDBOX_REPLACED"

	// ForkDegradeSandboxGone: the source session has no live sandbox to
	// snapshot.
	ForkDegradeSandboxGone ForkDegradeReason = "SANDBOX_GONE"

	// ForkDegradeSnapshotUnsupported: snapshotting failed or the backend does
	// not support it.
	ForkDegradeSnapshotUnsupported ForkDegradeReason = "SNAPSHOT_UNSUPPORTED"
)

// ErrForkSourceBusy reports that the source session has an agent turn in
// flight. Snapshotting pauses the source sandbox on every backend, which would
// interrupt a tool mid-execution, so the fork is refused rather than queued.
var ErrForkSourceBusy = errors.New("session fork: source session has an active turn")

// ForkResult is what the HTTP layer renders.
type ForkResult struct {
	SessionID string            `json:"session_id"`
	Degraded  bool              `json:"degraded"`
	Reason    ForkDegradeReason `json:"reason,omitempty"`
}

// SessionForkSandboxPort is the narrow sandbox surface fork needs. It is an
// interface so the fork logic is testable without a provider.
type SessionForkSandboxPort interface {
	// BoundSandboxID returns the session's currently bound sandbox without
	// provisioning one.
	BoundSandboxID(ctx context.Context, sessionID string) (string, bool)

	// HasActiveTurn reports whether an agent turn currently holds the
	// session's sandbox lease.
	HasActiveTurn(ctx context.Context, sessionID string) (bool, error)

	// CreateForkSnapshot snapshots the session's live sandbox and returns a
	// snapshot ID usable as a create-time template.
	CreateForkSnapshot(ctx context.Context, sessionID, name string) (string, error)
}

type forkSessionStore interface {
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.Session, error)
	// CreateForked persists the new session and the copied messages in one
	// transaction. Copied messages arrive with fresh IDs already assigned and
	// SessionID pointing at the new session.
	CreateForked(ctx context.Context, session *types.Session, messages []*types.Message) error
}

type forkMessageStore interface {
	GetMessage(ctx context.Context, sessionID, messageID string) (*types.Message, error)
	ListMessagesBySessionUpTo(
		ctx context.Context, sessionID string, boundary time.Time, boundaryID string,
	) ([]*types.Message, error)
}

// SessionForkService implements the fork decision chain.
type SessionForkService struct {
	sessions forkSessionStore
	messages forkMessageStore
	sandbox  SessionForkSandboxPort
}

// NewSessionForkService wires the service. A nil sandbox port makes every fork
// degrade, which is the correct behaviour for a deployment without sandboxes.
func NewSessionForkService(
	sessions forkSessionStore,
	messages forkMessageStore,
	sandboxPort SessionForkSandboxPort,
) *SessionForkService {
	return &SessionForkService{sessions: sessions, messages: messages, sandbox: sandboxPort}
}

// Fork branches sourceSessionID at messageID.
//
// Errors are reserved for conditions the user must act on: a bad request, a
// session they do not own, or a source that is busy. Everything about the
// sandbox degrades instead, and is reported through ForkResult.
func (s *SessionForkService) Fork(
	ctx context.Context,
	tenantID uint64,
	userID string,
	sourceSessionID string,
	messageID string,
	title string,
) (*ForkResult, error) {
	source, err := s.sessions.GetByID(ctx, tenantID, sourceSessionID)
	if err != nil {
		return nil, fmt.Errorf("session fork: load source session: %w", err)
	}
	if source == nil {
		return nil, errors.New("session fork: source session not found")
	}
	if source.UserID != "" && source.UserID != userID {
		return nil, errors.New("session fork: source session belongs to another user")
	}

	forkPoint, err := s.messages.GetMessage(ctx, sourceSessionID, messageID)
	if err != nil {
		return nil, fmt.Errorf("session fork: load fork point: %w", err)
	}
	if forkPoint == nil {
		return nil, errors.New("session fork: fork point message not found")
	}
	if forkPoint.Role != "user" {
		return nil, errors.New("session fork: fork point must be a user message")
	}

	// Refuse before doing anything observable: snapshotting pauses the source
	// sandbox, and interrupting a running tool is worse than making the user
	// wait for the turn to finish.
	if s.sandbox != nil {
		busy, turnErr := s.sandbox.HasActiveTurn(ctx, sourceSessionID)
		if turnErr != nil {
			return nil, fmt.Errorf("session fork: check source turn state: %w", turnErr)
		}
		if busy {
			return nil, ErrForkSourceBusy
		}
	}

	history, err := s.messages.ListMessagesBySessionUpTo(
		ctx, sourceSessionID, forkPoint.CreatedAt, forkPoint.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("session fork: load history: %w", err)
	}

	bootstrap, reason := s.prepareBootstrap(ctx, sourceSessionID, history)

	newSession := &types.Session{
		ID:                  uuid.New().String(),
		TenantID:            source.TenantID,
		UserID:              source.UserID,
		Title:               forkTitle(title, source.Title),
		Description:         source.Description,
		LastRequestState:    source.LastRequestState,
		SandboxConfigID:     source.SandboxConfigID,
		ParentSessionID:     source.ID,
		ForkedFromMessageID: forkPoint.ID,
		ForkBootstrap:       bootstrap,
	}

	copied := copyMessagesInto(newSession.ID, history)
	if err := s.sessions.CreateForked(ctx, newSession, copied); err != nil {
		// The snapshot is now an orphan. The reaper collects it; deleting it
		// here would need another failure path of its own.
		return nil, fmt.Errorf("session fork: persist forked session: %w", err)
	}

	logger.Infof(ctx,
		"[SessionFork] source=%s fork_point=%s new=%s messages=%d degraded=%v reason=%s",
		sourceSessionID, forkPoint.ID, newSession.ID, len(copied), reason != "", reason)

	return &ForkResult{
		SessionID: newSession.ID,
		Degraded:  reason != "",
		Reason:    reason,
	}, nil
}

// prepareBootstrap runs the decision chain from the design doc §4.2 and, when
// every condition holds, takes the snapshot.
//
// A nil bootstrap with an empty reason means "no sandbox state was needed":
// forking at the very first user message has no prior output to carry, so a
// brand new sandbox is the correct result rather than a degradation.
func (s *SessionForkService) prepareBootstrap(
	ctx context.Context, sourceSessionID string, history []*types.Message,
) (*types.ForkBootstrap, ForkDegradeReason) {
	checkpoint := latestCheckpoint(history)
	if checkpoint == nil {
		if !hasAssistantMessage(history) {
			// Forking at the first user message.
			return nil, ""
		}
		return nil, ForkDegradeNoCheckpoint
	}
	if s.sandbox == nil {
		return nil, ForkDegradeSandboxGone
	}

	currentID, ok := s.sandbox.BoundSandboxID(ctx, sourceSessionID)
	if !ok || currentID == "" {
		return nil, ForkDegradeSandboxGone
	}
	if currentID != checkpoint.SandboxID {
		// The session swapped sandboxes after this checkpoint was written, so
		// the SHA does not exist in the current sandbox's repository.
		return nil, ForkDegradeSandboxReplaced
	}

	snapshotID, err := s.sandbox.CreateForkSnapshot(
		ctx, sourceSessionID, forkSnapshotName(sourceSessionID),
	)
	if err != nil || snapshotID == "" {
		logger.Warnf(ctx, "[SessionFork] snapshot failed source=%s: %v", sourceSessionID, err)
		return nil, ForkDegradeSnapshotUnsupported
	}

	return &types.ForkBootstrap{
		SnapshotID:      snapshotID,
		CommitSHA:       checkpoint.CommitSHA,
		SourceSandboxID: checkpoint.SandboxID,
		CreatedAt:       time.Now().UTC(),
	}, ""
}

// latestCheckpoint returns the checkpoint of the last assistant message in
// history, or nil when that message has none.
func latestCheckpoint(history []*types.Message) *types.SandboxCheckpoint {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != "assistant" {
			continue
		}
		return history[i].SandboxCheckpoint
	}
	return nil
}

func hasAssistantMessage(history []*types.Message) bool {
	for _, m := range history {
		if m.Role == "assistant" {
			return true
		}
	}
	return false
}

// copyMessagesInto clones history into the new session.
//
// Every copy gets a fresh primary key but keeps its original CreatedAt so the
// branch reads with the same timeline as its parent. RequestID is carried over
// as-is: it pairs user with assistant messages, and every query that uses it is
// already scoped by session_id, so reuse across sessions is harmless.
//
// Copies deliberately keep their Artifacts, which point at the same object
// storage blobs as the parent's. Deleting a session does not delete artifact
// blobs today, so sharing is safe — but any future blob GC must reference-count
// before it can be turned on.
func copyMessagesInto(newSessionID string, history []*types.Message) []*types.Message {
	copies := make([]*types.Message, 0, len(history))
	for _, src := range history {
		clone := *src
		clone.ID = uuid.New().String()
		clone.SessionID = newSessionID
		// The parent already indexed these into the chat-history knowledge
		// base. Clearing the link stops the copy from being re-indexed, which
		// would both duplicate retrieval hits and burn embedding quota.
		clone.KnowledgeID = ""
		copies = append(copies, &clone)
	}
	return copies
}

func forkTitle(requested, sourceTitle string) string {
	if requested != "" {
		return requested
	}
	return sourceTitle + "（分支）"
}

func forkSnapshotName(sourceSessionID string) string {
	return fmt.Sprintf("fork-%s-%d", sourceSessionID, time.Now().UnixMilli())
}
```

- [ ] **Step 4: 实现仓储侧的 CreateForked 与 fork_bootstrap 更新**

在 `internal/application/repository/session.go` 追加：

```go
// CreateForked persists a forked session and its copied history atomically.
// A partially written fork would show up in the sidebar with a truncated or
// empty conversation, so both halves must land together.
func (r *sessionRepository) CreateForked(
	ctx context.Context, session *types.Session, messages []*types.Message,
) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		session.CreatedAt = now
		session.UpdatedAt = now
		// Session.BeforeCreate would overwrite the ID the fork service already
		// assigned (the copied messages point at it), so write the row with
		// the hook skipped.
		if err := tx.Session(&gorm.Session{SkipHooks: true}).Create(session).Error; err != nil {
			return err
		}
		if len(messages) == 0 {
			return nil
		}
		return tx.Session(&gorm.Session{SkipHooks: true}).
			CreateInBatches(messages, 100).Error
	})
}

// UpdateForkBootstrap overwrites a session's fork bootstrap. Passing nil clears
// it, which is how a failed or abandoned bootstrap is retired.
func (r *sessionRepository) UpdateForkBootstrap(
	ctx context.Context, sessionID string, b *types.ForkBootstrap,
) error {
	return r.db.WithContext(ctx).Model(&types.Session{}).
		Where("id = ?", sessionID).
		Update("fork_bootstrap", b).Error
}

// ListUnconsumedForks returns forked sessions whose bootstrap was created
// before olderThan and never consumed. It drives the orphan-snapshot reaper.
func (r *sessionRepository) ListUnconsumedForks(
	ctx context.Context, olderThan time.Time,
) ([]*types.Session, error) {
	var sessions []*types.Session
	if err := r.db.WithContext(ctx).
		Where("fork_bootstrap IS NOT NULL").
		Find(&sessions).Error; err != nil {
		return nil, err
	}
	pending := make([]*types.Session, 0, len(sessions))
	for _, s := range sessions {
		if s.ForkBootstrap == nil || s.ForkBootstrap.Consumed() {
			continue
		}
		if s.ForkBootstrap.CreatedAt.After(olderThan) {
			continue
		}
		pending = append(pending, s)
	}
	return pending, nil
}
```

> `ListUnconsumedForks` 故意在 Go 侧过滤而不是写 JSONB 谓词：SQLite 测试库不支持 Postgres 的 `->>` 语法，而未消费的 fork 数量天然很小（正常路径消费后立即清空）。迁移里那个 partial index 仍然有价值——它让 `fork_bootstrap IS NOT NULL` 这一步走索引。

把三个方法加进 `internal/types/interfaces/` 的 `SessionRepository` 接口（用 `grep -rn "CreateSession\|interface" internal/types/interfaces/ | grep -i session` 定位）。

- [ ] **Step 5: 运行测试确认通过**

Run: `cd /data/workspace/WeKnora && go build ./... && go test ./internal/application/service/ -run 'TestFork' -v`
Expected: PASS，10 个测试全绿

- [ ] **Step 6: 提交**

```bash
cd /data/workspace/WeKnora
git add internal/application/service/session_fork.go \
        internal/application/service/session_fork_test.go \
        internal/application/repository/session.go \
        internal/types/interfaces/
git commit -m "feat: SessionForkService，fork 的判定链与消息复制

判定链任一条不满足即降级为全新沙箱而非报错（Docker idle 直接 kill、
纯聊天会话压根没建过沙箱，都是常态）。源会话有活跃 turn 时拒绝，因为
打快照会暂停源沙箱、打断执行中的工具。复制的消息清空 KnowledgeID，
避免重复建 chat-history 索引。"
```

---

## Task 7: Fork HTTP 接口

**设计依据：** spec §4.1、§4.3

**Files:**
- Create: `internal/handler/session/fork.go`
- Create: `internal/handler/session/fork_test.go`
- Modify: `internal/handler/session/handler.go`（加 `forkService` 字段与构造参数）
- Modify: `internal/router/routes_chat.go`（`RegisterSessionRoutes` 内）
- Modify: `internal/container/container.go`（注册 `SessionForkService`）

**Interfaces:**
- Consumes: Task 6 的 `service.SessionForkService`、`service.ForkResult`、`service.ErrForkSourceBusy`
- Produces: `POST /api/v1/sessions/:session_id/fork`

- [ ] **Step 1: 写失败的测试**

创建 `internal/handler/session/fork_test.go`：

```go
package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type stubForker struct {
	result *service.ForkResult
	err    error
	gotMsg string
	gotTit string
}

func (s *stubForker) Fork(
	_ context.Context, _ uint64, _, _, messageID, title string,
) (*service.ForkResult, error) {
	s.gotMsg = messageID
	s.gotTit = title
	return s.result, s.err
}

func performFork(t *testing.T, forker sessionForker, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h := &Handler{forkService: forker}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Params = gin.Params{{Key: "session_id", Value: "src"}}
	c.Request = httptest.NewRequest(http.MethodPost, "/fork", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.ForkSession(c)
	return w
}

func TestForkSessionReturnsNewSessionID(t *testing.T) {
	forker := &stubForker{result: &service.ForkResult{SessionID: "new-1"}}

	w := performFork(t, forker, `{"message_id":"u-2"}`)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, "u-2", forker.gotMsg)

	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	data := body["data"].(map[string]any)
	require.Equal(t, "new-1", data["session_id"])
	require.Equal(t, false, data["degraded"])
}

func TestForkSessionSurfacesDegradeReason(t *testing.T) {
	forker := &stubForker{result: &service.ForkResult{
		SessionID: "new-1", Degraded: true, Reason: service.ForkDegradeSandboxGone,
	}}

	w := performFork(t, forker, `{"message_id":"u-2"}`)

	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	data := body["data"].(map[string]any)
	require.Equal(t, true, data["degraded"])
	require.Equal(t, "SANDBOX_GONE", data["reason"])
}

func TestForkSessionMapsBusySourceTo409(t *testing.T) {
	forker := &stubForker{err: service.ErrForkSourceBusy}

	w := performFork(t, forker, `{"message_id":"u-2"}`)

	require.Equal(t, http.StatusConflict, w.Code)
}

func TestForkSessionRejectsMissingMessageID(t *testing.T) {
	forker := &stubForker{result: &service.ForkResult{SessionID: "new-1"}}

	w := performFork(t, forker, `{}`)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestForkSessionMapsUnknownErrorTo500(t *testing.T) {
	forker := &stubForker{err: errors.New("boom")}

	w := performFork(t, forker, `{"message_id":"u-2"}`)

	require.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestForkSessionPassesTitleThrough(t *testing.T) {
	forker := &stubForker{result: &service.ForkResult{SessionID: "new-1"}}

	performFork(t, forker, `{"message_id":"u-2","title":"我的分支"}`)

	require.Equal(t, "我的分支", forker.gotTit)
}
```

> 断言 JSON 形状前，先看 `internal/handler/session/handler.go` 里 `CreateSession` 用的成功响应封装（`c.JSON(http.StatusOK, gin.H{"data": ...})` 或项目自有的 helper），照它写。上面假设是 `{"data": {...}}`；若不同，把三处 `body["data"]` 改成实际形状。

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /data/workspace/WeKnora && go test ./internal/handler/session/ -run 'TestForkSession' -v`
Expected: FAIL，`h.ForkSession undefined`

- [ ] **Step 3: 实现 handler**

创建 `internal/handler/session/fork.go`：

```go
package session

import (
	stderrors "errors"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

// sessionForker is the fork surface the handler needs. Declaring it here
// rather than depending on *service.SessionForkService keeps the handler
// testable with a stub.
type sessionForker interface {
	Fork(
		ctx context.Context,
		tenantID uint64,
		userID, sourceSessionID, messageID, title string,
	) (*service.ForkResult, error)
}

// ForkSessionRequest is the fork endpoint's body.
type ForkSessionRequest struct {
	// MessageID is the user message to branch at. Messages strictly before it
	// are copied into the new session; this one is not, so the client can
	// prefill it for editing.
	MessageID string `json:"message_id" binding:"required"`
	// Title is optional. Empty falls back to the source title plus a suffix.
	Title string `json:"title"`
}

// ForkSession godoc
// @Summary      分叉会话
// @Description  从指定的用户消息处分叉出一个新会话，继承分叉点之前的历史与沙箱状态
// @Tags         会话
// @Accept       json
// @Produce      json
// @Param        session_id  path      string              true  "源会话 ID"
// @Param        request     body      ForkSessionRequest  true  "分叉请求"
// @Success      200         {object}  map[string]interface{}  "新会话"
// @Failure      400         {object}  errors.AppError         "请求参数错误"
// @Failure      409         {object}  errors.AppError         "源会话正在生成中"
// @Security     Bearer
// @Security     ApiKeyAuth
// @Router       /sessions/{session_id}/fork [post]
func (h *Handler) ForkSession(c *gin.Context) {
	ctx := c.Request.Context()

	sessionID := strings.TrimSpace(c.Param("session_id"))
	if sessionID == "" {
		sessionID = strings.TrimSpace(c.Param("id"))
	}
	if sessionID == "" {
		c.Error(errors.NewBadRequestError("session ID is required"))
		return
	}

	var req ForkSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.Error(errors.NewBadRequestError("message_id is required"))
		return
	}
	if strings.TrimSpace(req.MessageID) == "" {
		c.Error(errors.NewBadRequestError("message_id is required"))
		return
	}

	if h.forkService == nil {
		c.Error(errors.NewBadRequestError("session fork is not available"))
		return
	}

	tenantID, _ := types.TenantIDFromContext(ctx)
	userID := currentUserID(ctx)

	result, err := h.forkService.Fork(
		ctx, tenantID, userID, sessionID,
		strings.TrimSpace(req.MessageID), strings.TrimSpace(req.Title),
	)
	if err != nil {
		// A busy source is a retryable, user-actionable state, not a fault:
		// snapshotting would pause the sandbox and interrupt the running turn.
		if stderrors.Is(err, service.ErrForkSourceBusy) {
			c.JSON(http.StatusConflict, gin.H{
				"success": false,
				"error":   "source session has an active turn",
				"code":    "FORK_SOURCE_BUSY",
			})
			return
		}
		logger.Errorf(ctx, "fork session %s failed: %v", sessionID, err)
		c.Error(errors.NewInternalServerError("fork session failed"))
		return
	}

	c.JSON(http.StatusOK, gin.H{"success": true, "data": result})
}
```

> `currentUserID(ctx)` 与 `errors.NewBadRequestError` / `NewInternalServerError` 的确切名字以本包现有 handler 为准。执行
> `grep -n "UserID\|NewBadRequestError\|NewInternalServerError" internal/handler/session/handler.go | head -20`
> 找到 `CreateSession` 里读取当前用户和构造错误的写法，照抄。

- [ ] **Step 4: 接线**

`internal/handler/session/handler.go` 的 `Handler` 结构体加：

```go
	// forkService branches a session at a chosen user message. May be nil in
	// deployments where fork is not wired; ForkSession checks.
	forkService sessionForker
```

`NewHandler` 参数列表在 `terminalService` 之后加 `forkService *service.SessionForkService`，并在返回体里赋值。注意：字段是接口类型而参数是具体指针类型时，直接赋值会让 nil 指针变成非 nil 接口。所以这样写：

```go
	h := &Handler{ /* ... 现有字段 ... */ }
	if forkService != nil {
		h.forkService = forkService
	}
	return h
```

`internal/router/routes_chat.go` 的 `RegisterSessionRoutes` 里，在 `sessions.POST("/:session_id/stop", handler.StopSession)` 之后加：

```go
		sessions.POST("/:session_id/fork", handler.ForkSession)
```

`internal/container/container.go` 注册：

```go
	must(container.Provide(service.NewSessionForkService))
```

> `NewSessionForkService` 的参数是未导出的接口类型（`forkSessionStore` / `forkMessageStore`），DI 容器无法直接解析。加一个导出的 DI 构造函数到 `internal/application/service/session_fork.go`：
>
> ```go
> // NewSessionForkServiceFromRepos is the DI-friendly constructor. The
> // repository interfaces satisfy the narrow ports structurally.
> func NewSessionForkServiceFromRepos(
> 	sessions interfaces.SessionRepository,
> 	messages interfaces.MessageRepository,
> 	sandboxPort SessionForkSandboxPort,
> ) *SessionForkService {
> 	return NewSessionForkService(sessions, messages, sandboxPort)
> }
> ```
>
> 并注册 `SessionForkSandboxPort` 的实现（由 `sandbox.Manager` 类型断言得到，取不到就提供 nil，fork 会全部降级）。`HasActiveTurn` 与 `CreateForkSnapshot` 需要在 `SessionBoundManager` 上补两个导出方法：`HasActiveTurn` 走 `sessionTurnLeaseStore.TurnState`（`internal/sandbox/session_binding.go:144-149`），`CreateForkSnapshot` 包装已有的 `CreateSnapshot`（`internal/sandbox/session_manager.go:421-436`）。

- [ ] **Step 5: 运行测试确认通过**

Run: `cd /data/workspace/WeKnora && go build ./... && go test ./internal/handler/session/ -run 'TestForkSession' -v`
Expected: PASS，6 个测试全绿

- [ ] **Step 6: 手工冒烟**

Run: 启动服务后
```bash
curl -s -X POST "http://localhost:8080/api/v1/sessions/<真实会话ID>/fork" \
  -H "Authorization: Bearer <token>" -H 'Content-Type: application/json' \
  -d '{"message_id":"<该会话里某条 user 消息ID>"}' | jq
```
Expected: 返回 `{"success":true,"data":{"session_id":"...","degraded":...}}`；侧边栏刷新后能看到新会话，其消息为分叉点之前的历史

- [ ] **Step 7: 提交**

```bash
cd /data/workspace/WeKnora
git add internal/handler/session/fork.go internal/handler/session/fork_test.go \
        internal/handler/session/handler.go internal/router/routes_chat.go \
        internal/container/container.go internal/application/service/session_fork.go \
        internal/sandbox/session_manager.go
git commit -m "feat: POST /sessions/:session_id/fork

同步返回，不建沙箱。源会话有活跃 turn 时返回 409；沙箱相关的一切失败
都降级为 degraded=true 而非报错。"
```

---

## Task 8: 沙箱 bootstrap 钩子

**设计依据：** spec §4.5、§4.6

给 `remoteSessionLifecycle` 加一个可选的 bootstrapper，让它在建沙箱时能覆盖 `TemplateID`，并在写完 binding 后跑一段全有或全无的引导。

**Files:**
- Create: `internal/sandbox/session_bootstrapper.go`
- Create: `internal/sandbox/session_bootstrapper_test.go`
- Modify: `internal/sandbox/session_lifecycle.go:39-92`（结构体与构造函数）
- Modify: `internal/sandbox/session_lifecycle.go:436-521`（`createAndBind`）
- Modify: `internal/sandbox/session_manager.go:98-198`（`SessionBoundManagerConfig` 与构造）
- Modify: `internal/sandbox/tenant_resolver.go`（`TenantSandboxResolverDeps` 透传）

**Interfaces:**
- Produces: `sandbox.SessionBootstrapper` 接口
  - `TemplateOverride(ctx context.Context, key SessionSandboxKey) (string, error)`
  - `AfterCreate(ctx context.Context, key SessionSandboxKey, handle RemoteSandboxHandle) error`
- Produces: `SessionBoundManagerConfig.Bootstrapper SessionBootstrapper`（可选，nil 表示无）

- [ ] **Step 1: 写失败的测试**

创建 `internal/sandbox/session_bootstrapper_test.go`。参照 `internal/sandbox/session_lifecycle_test.go` 现有的 fake client / store 搭建方式（先读该文件，复用其 helper）：

```go
package sandbox

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type recordingBootstrapper struct {
	override      string
	overrideErr   error
	afterErr      error
	afterCalls    int
	afterHandleID string
}

func (b *recordingBootstrapper) TemplateOverride(
	context.Context, SessionSandboxKey,
) (string, error) {
	return b.override, b.overrideErr
}

func (b *recordingBootstrapper) AfterCreate(
	_ context.Context, _ SessionSandboxKey, handle RemoteSandboxHandle,
) error {
	b.afterCalls++
	if handle != nil {
		b.afterHandleID = handle.ID()
	}
	return b.afterErr
}

func TestCreateAndBindUsesTemplateOverride(t *testing.T) {
	// 用 session_lifecycle_test.go 里的 helper 构造 lifecycle 与 fake client。
	// fake client 需记录 Create 收到的 RemoteCreateRequest.TemplateID。
	lc, client, _ := newLifecycleForTest(t)
	lc.bootstrapper = &recordingBootstrapper{override: "snap-1"}

	_, err := lc.Resolve(context.Background(), testSessionKey)

	require.NoError(t, err)
	require.Equal(t, "snap-1", client.lastCreateRequest.TemplateID)
}

func TestCreateAndBindKeepsConfigTemplateWithoutOverride(t *testing.T) {
	lc, client, _ := newLifecycleForTest(t)
	lc.bootstrapper = &recordingBootstrapper{override: ""}

	_, err := lc.Resolve(context.Background(), testSessionKey)

	require.NoError(t, err)
	require.Equal(t, lc.createRequest.TemplateID, client.lastCreateRequest.TemplateID)
}

// 最重要的一条：override 绝不能污染跨 session 复用的 l.createRequest。
func TestTemplateOverrideDoesNotMutateSharedCreateRequest(t *testing.T) {
	lc, _, _ := newLifecycleForTest(t)
	baseTemplate := lc.createRequest.TemplateID
	lc.bootstrapper = &recordingBootstrapper{override: "snap-1"}

	_, err := lc.Resolve(context.Background(), testSessionKey)

	require.NoError(t, err)
	require.Equal(t, baseTemplate, lc.createRequest.TemplateID,
		"共享的 createRequest 被改了，同 config 下其他会话会从别人的快照启动")
}

func TestAfterCreateRunsWithTheNewHandle(t *testing.T) {
	lc, _, _ := newLifecycleForTest(t)
	b := &recordingBootstrapper{override: "snap-1"}
	lc.bootstrapper = b

	handle, err := lc.Resolve(context.Background(), testSessionKey)

	require.NoError(t, err)
	require.Equal(t, 1, b.afterCalls)
	require.Equal(t, handle.ID(), b.afterHandleID)
}

// 全有或全无：引导失败必须销毁沙箱并删掉 binding，否则用户会在一个
// 「文件系统停在分叉时刻而不是分叉点」的沙箱里干活。
func TestAfterCreateFailureDestroysSandboxAndBinding(t *testing.T) {
	lc, client, store := newLifecycleForTest(t)
	lc.bootstrapper = &recordingBootstrapper{override: "snap-1", afterErr: errors.New("git reset failed")}

	_, err := lc.Resolve(context.Background(), testSessionKey)

	require.Error(t, err)
	require.Contains(t, client.deleted, client.lastCreatedID)

	binding, getErr := store.Get(context.Background(), testSessionKey)
	require.NoError(t, getErr)
	require.Nil(t, binding, "引导失败后不得留下 binding")
}

func TestTemplateOverrideErrorAbortsCreate(t *testing.T) {
	lc, client, _ := newLifecycleForTest(t)
	lc.bootstrapper = &recordingBootstrapper{overrideErr: errors.New("db down")}

	_, err := lc.Resolve(context.Background(), testSessionKey)

	require.Error(t, err)
	require.Empty(t, client.lastCreatedID, "读不到 override 就不该建沙箱")
}

func TestNilBootstrapperIsANoOp(t *testing.T) {
	lc, client, _ := newLifecycleForTest(t)
	lc.bootstrapper = nil

	_, err := lc.Resolve(context.Background(), testSessionKey)

	require.NoError(t, err)
	require.Equal(t, lc.createRequest.TemplateID, client.lastCreateRequest.TemplateID)
}
```

> `newLifecycleForTest` / `testSessionKey` / fake client 的 `lastCreateRequest`、`lastCreatedID`、`deleted` 字段需要基于 `internal/sandbox/session_lifecycle_test.go` 与 `internal/sandbox/remote_fake_test.go` 的现有 fake 扩展。先读这两个文件，尽量复用而不是新写一套。

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /data/workspace/WeKnora && go test ./internal/sandbox/ -run 'Bootstrapper|TemplateOverride|AfterCreate' -v`
Expected: FAIL，`lc.bootstrapper undefined`

- [ ] **Step 3: 定义接口**

创建 `internal/sandbox/session_bootstrapper.go`：

```go
package sandbox

import "context"

// SessionBootstrapper customises how ONE session's first sandbox is created.
// It exists for session fork: a forked session boots from a snapshot of its
// parent's sandbox and then rolls /workspace back to the fork point.
//
// It is optional. A nil bootstrapper leaves the lifecycle's behaviour
// unchanged, which is what every ordinary session gets.
//
// The lifecycle calls both methods inside the per-session lifecycle lock, so
// implementations need no locking of their own and cannot race with a
// concurrent resolve of the same session.
type SessionBootstrapper interface {
	// TemplateOverride returns a template ID to create this session's sandbox
	// from instead of the config's. An empty string means "no override".
	//
	// An error aborts the create: an implementation that cannot tell whether
	// an override applies must not be silently treated as "no override",
	// because that would boot a plain sandbox for a session the user expects
	// to carry forked state.
	TemplateOverride(ctx context.Context, key SessionSandboxKey) (string, error)

	// AfterCreate runs once, immediately after the binding is written.
	//
	// Returning an error makes the lifecycle destroy the sandbox and delete
	// the binding. That severity is deliberate: a half-bootstrapped fork looks
	// completely normal but has the wrong baseline, and that silent wrongness
	// is far worse than an honest failure the next resolve can retry from a
	// clean slate.
	AfterCreate(ctx context.Context, key SessionSandboxKey, handle RemoteSandboxHandle) error
}
```

- [ ] **Step 4: 接进 lifecycle**

`internal/sandbox/session_lifecycle.go` 的 `remoteSessionLifecycle` 结构体加字段：

```go
	// bootstrapper customises the first create of individual sessions
	// (session fork). Optional; nil keeps the default behaviour.
	bootstrapper SessionBootstrapper
```

`newRemoteSessionLifecycle` 加一个尾部参数 `bootstrapper SessionBootstrapper` 并赋值（允许 nil，不校验）。

把 `createAndBind` 开头改为：

```go
func (l *remoteSessionLifecycle) createAndBind(
	ctx context.Context,
	key SessionSandboxKey,
) (RemoteSandboxHandle, error) {
	// request is a VALUE copy of l.createRequest, and every mutation below
	// must stay on the copy. l.createRequest is shared by every session this
	// config serves, so writing the override onto it would make unrelated
	// sessions boot from another session's fork snapshot — a failure that
	// reproduces only under specific interleavings and is brutal to diagnose.
	request := l.createRequest
	request.Metadata = nil
	if l.client.Capabilities().SupportsMetadata {
		request.Metadata = cloneMetadata(l.createRequest.Metadata)
		if request.Metadata == nil {
			request.Metadata = make(map[string]string)
		}
		for metadataKey, value := range l.metadata(key) {
			request.Metadata[metadataKey] = value
		}
	}
	request.EnvVars = cloneMetadata(l.createRequest.EnvVars)

	if l.bootstrapper != nil {
		override, err := l.bootstrapper.TemplateOverride(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("resolve session template override: %w", err)
		}
		if strings.TrimSpace(override) != "" {
			request.TemplateID = override
		}
	}

	handle, err := l.client.Create(ctx, request)
	// ... 后续保持不变 ...
```

在同函数中 `if created { return handle, nil }` 那一处，改为先跑 bootstrap：

```go
	if created {
		if err := l.runBootstrap(ctx, key, handle); err != nil {
			return nil, err
		}
		return handle, nil
	}
```

在 `createAndBind` 之后追加：

```go
// runBootstrap applies the session bootstrapper to a freshly bound sandbox.
//
// Failure is all-or-nothing: the sandbox is destroyed and the binding deleted
// so the next resolve starts from a clean slate. Leaving a half-bootstrapped
// sandbox in place would hand the user an environment that looks right and is
// not.
func (l *remoteSessionLifecycle) runBootstrap(
	ctx context.Context, key SessionSandboxKey, handle RemoteSandboxHandle,
) error {
	if l.bootstrapper == nil {
		return nil
	}
	bootstrapErr := l.bootstrapper.AfterCreate(ctx, key, handle)
	if bootstrapErr == nil {
		return nil
	}
	// Use a cancellation-immune context: the caller may already be gone, and
	// abandoning the rollback would leak both a sandbox and a wrong binding.
	cleanupCtx := context.WithoutCancel(ctx)
	if _, delErr := l.bindings.DeleteIfMatch(
		cleanupCtx, key, l.client.Provider(), handle.ID(),
	); delErr != nil {
		return errors.Join(bootstrapErr, fmt.Errorf("delete binding after failed bootstrap: %w", delErr))
	}
	return errors.Join(bootstrapErr, l.cleanupCreated(cleanupCtx, handle))
}
```

- [ ] **Step 5: 透传到构造链**

`internal/sandbox/session_manager.go` 的 `SessionBoundManagerConfig` 加：

```go
	// Bootstrapper customises the first sandbox create of individual sessions
	// (session fork). Optional: nil is the ordinary path.
	Bootstrapper SessionBootstrapper
```

`NewSessionBoundManager` 里把它传给 `newRemoteSessionLifecycle`。

`internal/sandbox/tenant_resolver.go` 的 `TenantSandboxResolverDeps` 加同名字段，并在它构造每租户 `SessionBoundManagerConfig` 的地方透传。用 `grep -n "SessionBoundManagerConfig{" internal/sandbox/tenant_resolver.go` 定位。

- [ ] **Step 6: 运行测试确认通过**

Run: `cd /data/workspace/WeKnora && go build ./... && go test ./internal/sandbox/ -run 'Bootstrapper|TemplateOverride|AfterCreate' -v`
Expected: PASS，7 个测试全绿

- [ ] **Step 7: 沙箱包全量回归**

Run: `cd /data/workspace/WeKnora && go test ./internal/sandbox/... 2>&1 | tail -20`
Expected: ok，无回归（新参数一路是 nil）

- [ ] **Step 8: 提交**

```bash
cd /data/workspace/WeKnora
git add internal/sandbox/
git commit -m "feat: 沙箱生命周期支持 per-session bootstrap 钩子

TemplateOverride 让 fork 出的会话从快照建沙箱，AfterCreate 在 binding
写入后做全有或全无的引导。override 只改 createAndBind 内的值拷贝，
绝不碰跨 session 共享的 l.createRequest。"
```

---

## Task 9: ForkBootstrapper 实现

**设计依据：** spec §4.5、§4.7

**Files:**
- Create: `internal/application/service/fork_bootstrapper.go`
- Create: `internal/application/service/fork_bootstrapper_test.go`
- Modify: `internal/container/container.go`（注册并接到 sandbox 构造链）

**Interfaces:**
- Consumes: Task 8 的 `sandbox.SessionBootstrapper`；Task 4 的 `service.SandboxShellRunner`；Task 6 的 `SessionRepository.UpdateForkBootstrap`
- Produces: `service.ForkBootstrapper`，实现 `sandbox.SessionBootstrapper`
- Produces: `service.NewForkBootstrapper(sessions, messages, runner, fileService, snapshots, writer) *ForkBootstrapper`
- Produces: `service.ForkSnapshotDeleter` 接口：`DeleteSnapshot(ctx context.Context, snapshotID string) error`
- Produces: `service.SandboxWorkspaceWriter` 接口：`WriteSessionWorkspaceFile(ctx context.Context, sessionID, filePath string, content []byte) error`

- [ ] **Step 1: 写失败的测试**

创建 `internal/application/service/fork_bootstrapper_test.go`：

```go
package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type fakeSnapshotDeleter struct {
	deleted []string
	err     error
}

func (f *fakeSnapshotDeleter) DeleteSnapshot(_ context.Context, id string) error {
	f.deleted = append(f.deleted, id)
	return f.err
}

type fakeWorkspaceWriter struct {
	written map[string][]byte
	err     error
}

func (f *fakeWorkspaceWriter) WriteSessionWorkspaceFile(
	_ context.Context, _, path string, content []byte,
) error {
	if f.err != nil {
		return f.err
	}
	if f.written == nil {
		f.written = map[string][]byte{}
	}
	f.written[path] = content
	return nil
}

func pendingForkSession() *types.Session {
	return &types.Session{
		ID: "fork-1", TenantID: 1,
		ForkBootstrap: &types.ForkBootstrap{
			SnapshotID: "snap-1", CommitSHA: "abc123",
			SourceSandboxID: "sbx-1", CreatedAt: time.Now().UTC(),
		},
	}
}

func forkKey() sandbox.SessionSandboxKey {
	return sandbox.SessionSandboxKey{TenantID: 1, SessionID: "fork-1"}
}

func TestTemplateOverrideReturnsSnapshotForPendingFork(t *testing.T) {
	sessions := newFakeSessionStore(pendingForkSession())
	b := NewForkBootstrapper(sessions, nil, nil, nil, nil, nil)

	got, err := b.TemplateOverride(context.Background(), forkKey())

	require.NoError(t, err)
	require.Equal(t, "snap-1", got)
}

func TestTemplateOverrideIsEmptyForOrdinarySession(t *testing.T) {
	sessions := newFakeSessionStore(&types.Session{ID: "fork-1", TenantID: 1})
	b := NewForkBootstrapper(sessions, nil, nil, nil, nil, nil)

	got, err := b.TemplateOverride(context.Background(), forkKey())

	require.NoError(t, err)
	require.Empty(t, got)
}

func TestTemplateOverrideIsEmptyForConsumedFork(t *testing.T) {
	s := pendingForkSession()
	at := time.Now().UTC()
	s.ForkBootstrap.ConsumedAt = &at
	sessions := newFakeSessionStore(s)
	b := NewForkBootstrapper(sessions, nil, nil, nil, nil, nil)

	got, err := b.TemplateOverride(context.Background(), forkKey())

	require.NoError(t, err)
	require.Empty(t, got)
}

func TestTemplateOverridePropagatesLookupError(t *testing.T) {
	sessions := newFakeSessionStore(nil)
	sessions.getErr = errors.New("db down")
	b := NewForkBootstrapper(sessions, nil, nil, nil, nil, nil)

	_, err := b.TemplateOverride(context.Background(), forkKey())

	require.Error(t, err)
}

func TestAfterCreateResetsWorkspaceThenConsumesAndDeletesSnapshot(t *testing.T) {
	sessions := newFakeSessionStore(pendingForkSession())
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{ExitCode: 0}}
	snapshots := &fakeSnapshotDeleter{}
	b := NewForkBootstrapper(sessions, newFakeMessageStore(nil), runner, nil, snapshots, &fakeWorkspaceWriter{})

	require.NoError(t, b.AfterCreate(context.Background(), forkKey(), fakeHandle{id: "sbx-2"}))

	require.Len(t, runner.calls, 1)
	script := runner.calls[0]
	require.Contains(t, script, "reset --hard abc123")
	require.Contains(t, script, "clean -fdx")

	require.Equal(t, []string{"snap-1"}, snapshots.deleted)
	require.NotNil(t, sessions.updatedBootstrap)
	require.True(t, sessions.updatedBootstrap.Consumed())
}

func TestAfterCreateIsNoOpForOrdinarySession(t *testing.T) {
	sessions := newFakeSessionStore(&types.Session{ID: "fork-1", TenantID: 1})
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{ExitCode: 0}}
	b := NewForkBootstrapper(sessions, newFakeMessageStore(nil), runner, nil, &fakeSnapshotDeleter{}, &fakeWorkspaceWriter{})

	require.NoError(t, b.AfterCreate(context.Background(), forkKey(), fakeHandle{id: "sbx-2"}))
	require.Empty(t, runner.calls)
}

// 全有或全无：reset 失败必须返回 error，让 lifecycle 销毁这个沙箱。
func TestAfterCreateFailsWhenResetFails(t *testing.T) {
	sessions := newFakeSessionStore(pendingForkSession())
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{
		ExitCode: 128, Stderr: "fatal: bad object abc123",
	}}
	snapshots := &fakeSnapshotDeleter{}
	b := NewForkBootstrapper(sessions, newFakeMessageStore(nil), runner, nil, snapshots, &fakeWorkspaceWriter{})

	err := b.AfterCreate(context.Background(), forkKey(), fakeHandle{id: "sbx-2"})

	require.Error(t, err)
	// 引导失败时清空 bootstrap 并回收快照：下次 resolve 走全新沙箱路径。
	require.Equal(t, []string{"snap-1"}, snapshots.deleted)
	require.True(t, sessions.bootstrapCleared)
}

func TestAfterCreateRestoresArtifactsIntoOutputDir(t *testing.T) {
	sessions := newFakeSessionStore(pendingForkSession())
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{ExitCode: 0}}
	writer := &fakeWorkspaceWriter{}
	messages := newFakeMessageStore([]*types.Message{{
		ID: "m1", SessionID: "fork-1", Role: "assistant",
		Artifacts: types.MessageArtifacts{{FileName: "report.pdf", SourcePath: "/workspace/output/report.pdf"}},
	}})
	files := &fakeArtifactFileService{content: []byte("PDF")}
	b := NewForkBootstrapper(sessions, messages, runner, files, &fakeSnapshotDeleter{}, writer)

	require.NoError(t, b.AfterCreate(context.Background(), forkKey(), fakeHandle{id: "sbx-2"}))

	require.Equal(t, []byte("PDF"), writer.written["/workspace/output/report.pdf"])
}

// artifacts 还原是尽力而为的收尾步骤，不该让整个 fork 失败。
func TestAfterCreateSucceedsWhenArtifactRestoreFails(t *testing.T) {
	sessions := newFakeSessionStore(pendingForkSession())
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{ExitCode: 0}}
	messages := newFakeMessageStore([]*types.Message{{
		ID: "m1", SessionID: "fork-1", Role: "assistant",
		Artifacts: types.MessageArtifacts{{FileName: "report.pdf", SourcePath: "/workspace/output/report.pdf"}},
	}})
	writer := &fakeWorkspaceWriter{err: errors.New("disk full")}
	b := NewForkBootstrapper(sessions, messages, runner, &fakeArtifactFileService{content: []byte("PDF")},
		&fakeSnapshotDeleter{}, writer)

	require.NoError(t, b.AfterCreate(context.Background(), forkKey(), fakeHandle{id: "sbx-2"}))
	require.True(t, sessions.updatedBootstrap.Consumed())
}

func TestAfterCreateSkipsOversizedArtifactRestore(t *testing.T) {
	sessions := newFakeSessionStore(pendingForkSession())
	runner := &fakeShellRunner{result: &sandbox.ExecuteResult{ExitCode: 0}}
	writer := &fakeWorkspaceWriter{}
	big := strings.Repeat("x", 1024)
	artifacts := types.MessageArtifacts{}
	for i := 0; i < 3; i++ {
		artifacts = append(artifacts, types.MessageArtifact{
			FileName:   "big.bin",
			SourcePath: "/workspace/output/big" + string(rune('a'+i)) + ".bin",
		})
	}
	messages := newFakeMessageStore([]*types.Message{
		{ID: "m1", SessionID: "fork-1", Role: "assistant", Artifacts: artifacts},
	})
	b := NewForkBootstrapper(sessions, messages, runner, &fakeArtifactFileService{content: []byte(big)},
		&fakeSnapshotDeleter{}, writer)
	b.maxRestoreBytes = 1500 // 只放得下第一个

	require.NoError(t, b.AfterCreate(context.Background(), forkKey(), fakeHandle{id: "sbx-2"}))
	require.Len(t, writer.written, 1)
}
```

> `fakeHandle`、`fakeArtifactFileService`、`newFakeMessageStore`、`newFakeSessionStore` 若在 Task 6 已建，扩展它们（`fakeSessionStore` 需加 `getErr`、`updatedBootstrap`、`bootstrapCleared`）。`types.MessageArtifact` 的实际字段名以 `internal/types/message.go` 为准，用 `grep -n "type MessageArtifact struct" -A 15 internal/types/message.go` 核对后再落笔。读取 artifact 内容的 file service 方法名同样先 grep `internal/application/service/artifact_collector.go` 里保存时用的那个的反向操作。

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /data/workspace/WeKnora && go test ./internal/application/service/ -run 'TestTemplateOverride|TestAfterCreate' -v`
Expected: FAIL，`undefined: NewForkBootstrapper`

- [ ] **Step 3: 实现**

创建 `internal/application/service/fork_bootstrapper.go`：

```go
// Package service - forked-session sandbox bootstrap.
//
// ForkBootstrapper implements sandbox.SessionBootstrapper for forked sessions:
// it points the first create at the fork snapshot, then rolls /workspace back
// to the fork point and restores that point's artifacts.
//
// It is strictly all-or-nothing. Handing back a sandbox whose filesystem sits
// at the fork MOMENT rather than the fork POINT would look completely normal
// while silently working from the wrong baseline — worse than an honest
// failure the next resolve retries cleanly.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// ForkSnapshotDeleter retires a consumed or abandoned fork snapshot.
type ForkSnapshotDeleter interface {
	DeleteSnapshot(ctx context.Context, snapshotID string) error
}

// SandboxWorkspaceWriter writes a file into a session's /workspace.
type SandboxWorkspaceWriter interface {
	WriteSessionWorkspaceFile(ctx context.Context, sessionID, filePath string, content []byte) error
}

type forkBootstrapSessionStore interface {
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.Session, error)
	UpdateForkBootstrap(ctx context.Context, sessionID string, b *types.ForkBootstrap) error
}

type forkBootstrapMessageStore interface {
	GetMessagesBySession(ctx context.Context, sessionID string, page, pageSize int) ([]*types.Message, error)
}

// forkResetTimeout bounds the git rollback. clean -fdx may delete a large
// output tree, so it is more generous than the per-turn checkpoint.
const forkResetTimeout = 60 * time.Second

// defaultForkRestoreBudget caps the total bytes pushed back into a forked
// sandbox's output directory. A fork must not be able to fill the sandbox disk
// or saturate the backend on a single click.
const defaultForkRestoreBudget int64 = 200 * 1024 * 1024

// ForkBootstrapper provisions forked sessions' first sandbox.
type ForkBootstrapper struct {
	sessions        forkBootstrapSessionStore
	messages        forkBootstrapMessageStore
	runner          SandboxShellRunner
	files           interfaces.FileService
	snapshots       ForkSnapshotDeleter
	writer          SandboxWorkspaceWriter
	maxRestoreBytes int64
}

// NewForkBootstrapper wires the bootstrapper.
func NewForkBootstrapper(
	sessions forkBootstrapSessionStore,
	messages forkBootstrapMessageStore,
	runner SandboxShellRunner,
	files interfaces.FileService,
	snapshots ForkSnapshotDeleter,
	writer SandboxWorkspaceWriter,
) *ForkBootstrapper {
	return &ForkBootstrapper{
		sessions:        sessions,
		messages:        messages,
		runner:          runner,
		files:           files,
		snapshots:       snapshots,
		writer:          writer,
		maxRestoreBytes: defaultForkRestoreBudget,
	}
}

// TemplateOverride returns the fork snapshot for a session whose bootstrap is
// still pending.
func (b *ForkBootstrapper) TemplateOverride(
	ctx context.Context, key sandbox.SessionSandboxKey,
) (string, error) {
	pending, err := b.pendingBootstrap(ctx, key)
	if err != nil {
		return "", err
	}
	if pending == nil {
		return "", nil
	}
	return pending.SnapshotID, nil
}

// AfterCreate rolls the new sandbox back to the fork point.
func (b *ForkBootstrapper) AfterCreate(
	ctx context.Context, key sandbox.SessionSandboxKey, _ sandbox.RemoteSandboxHandle,
) error {
	pending, err := b.pendingBootstrap(ctx, key)
	if err != nil {
		return err
	}
	if pending == nil {
		return nil
	}

	if err := b.resetWorkspace(ctx, key.SessionID, pending.CommitSHA); err != nil {
		// Retire the bootstrap so the next resolve provisions an ordinary
		// sandbox instead of retrying a rollback that just failed.
		b.abandon(ctx, key.SessionID, pending)
		return err
	}

	// Artifacts are gitignored, so clean -fdx just emptied the output tree.
	// Restoring is best-effort: an incomplete output directory is a cosmetic
	// loss, while failing here would throw away a correctly rolled-back
	// filesystem.
	b.restoreArtifacts(ctx, key.SessionID)

	now := time.Now().UTC()
	consumed := *pending
	consumed.ConsumedAt = &now
	if err := b.sessions.UpdateForkBootstrap(ctx, key.SessionID, &consumed); err != nil {
		return fmt.Errorf("mark fork bootstrap consumed: %w", err)
	}
	b.deleteSnapshot(ctx, pending.SnapshotID)
	return nil
}

func (b *ForkBootstrapper) pendingBootstrap(
	ctx context.Context, key sandbox.SessionSandboxKey,
) (*types.ForkBootstrap, error) {
	if b == nil || b.sessions == nil {
		return nil, nil
	}
	session, err := b.sessions.GetByID(ctx, key.TenantID, key.SessionID)
	if err != nil {
		return nil, fmt.Errorf("load session for fork bootstrap: %w", err)
	}
	if session == nil || session.ForkBootstrap == nil {
		return nil, nil
	}
	if session.ForkBootstrap.Consumed() {
		return nil, nil
	}
	if strings.TrimSpace(session.ForkBootstrap.SnapshotID) == "" {
		return nil, nil
	}
	return session.ForkBootstrap, nil
}

// resetWorkspace rolls /workspace back to sha.
//
// clean -fdx includes ignored files on purpose: input/ and output/ are
// gitignored, and leaving the source session's post-fork-point content in
// place would contradict the rollback. input/ is rebuilt automatically by the
// per-turn attachment staging; output/ is restored below.
func (b *ForkBootstrapper) resetWorkspace(ctx context.Context, sessionID, sha string) error {
	if b.runner == nil {
		return errors.New("fork bootstrap: no shell runner wired")
	}
	script := fmt.Sprintf(`set -e
git config --global safe.directory %[1]s
git -C %[1]s reset --hard %[2]s
git -C %[1]s clean -fdx`, sandbox.SessionWorkspaceRoot, sha)

	result, err := b.runner.ExecShellCommand(
		ctx, sessionID, script, sandbox.SessionWorkspaceRoot, forkResetTimeout, nil,
	)
	if err != nil {
		return fmt.Errorf("fork bootstrap: git reset exec: %w", err)
	}
	if result == nil || result.ExitCode != 0 {
		stderr := ""
		if result != nil {
			stderr = result.Stderr
		}
		return fmt.Errorf("fork bootstrap: git reset to %s failed: %s", sha, stderr)
	}
	return nil
}

func (b *ForkBootstrapper) restoreArtifacts(ctx context.Context, sessionID string) {
	if b.messages == nil || b.files == nil || b.writer == nil {
		return
	}
	messages, err := b.messages.GetMessagesBySession(ctx, sessionID, 1, 1000)
	if err != nil {
		logger.Warnf(ctx, "[ForkBootstrap] load messages for artifact restore failed: %v", err)
		return
	}

	var budget int64
	// Oldest first, so a file rewritten across turns ends up at its latest
	// version — matching what the source session's workspace looked like.
	for _, message := range messages {
		for _, artifact := range message.Artifacts {
			path := strings.TrimSpace(artifact.SourcePath)
			if path == "" || !strings.HasPrefix(path, sandbox.SessionOutputRoot+"/") {
				continue
			}
			content, readErr := readArtifactContent(ctx, b.files, artifact)
			if readErr != nil {
				logger.Warnf(ctx, "[ForkBootstrap] read artifact %s failed: %v", path, readErr)
				continue
			}
			if budget+int64(len(content)) > b.maxRestoreBytes {
				logger.Warnf(ctx,
					"[ForkBootstrap] restore budget %d exhausted, skipping %s (session=%s)",
					b.maxRestoreBytes, path, sessionID)
				return
			}
			if writeErr := b.writer.WriteSessionWorkspaceFile(ctx, sessionID, path, content); writeErr != nil {
				logger.Warnf(ctx, "[ForkBootstrap] write artifact %s failed: %v", path, writeErr)
				continue
			}
			budget += int64(len(content))
		}
	}
}

// abandon retires a bootstrap that could not be applied.
func (b *ForkBootstrapper) abandon(ctx context.Context, sessionID string, pending *types.ForkBootstrap) {
	cleanupCtx := context.WithoutCancel(ctx)
	if err := b.sessions.UpdateForkBootstrap(cleanupCtx, sessionID, nil); err != nil {
		logger.Warnf(cleanupCtx, "[ForkBootstrap] clear bootstrap of %s failed: %v", sessionID, err)
	}
	b.deleteSnapshot(cleanupCtx, pending.SnapshotID)
}

func (b *ForkBootstrapper) deleteSnapshot(ctx context.Context, snapshotID string) {
	if b.snapshots == nil || strings.TrimSpace(snapshotID) == "" {
		return
	}
	if err := b.snapshots.DeleteSnapshot(ctx, snapshotID); err != nil {
		// The reaper collects it later; failing the fork over a leaked
		// snapshot would be a bad trade.
		logger.Warnf(ctx, "[ForkBootstrap] delete snapshot %s failed: %v", snapshotID, err)
	}
}
```

> `readArtifactContent(ctx, files, artifact)` 需要你按 `internal/application/service/artifact_collector.go` 里保存 artifact 时用的 `fileService` 方法写出反向读取。先执行
> `grep -n "fileService\." internal/application/service/artifact_collector.go`
> 找到保存用的方法与它返回的存储标识字段，据此写读取。若 `interfaces.FileService` 没有按存储标识读回的方法，改用 artifact 下载 handler 里的读取路径（`grep -rn "DownloadMessageArtifact" internal/handler/session/`）。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd /data/workspace/WeKnora && go build ./... && go test ./internal/application/service/ -run 'TestTemplateOverride|TestAfterCreate' -v`
Expected: PASS，10 个测试全绿

- [ ] **Step 5: 容器接线**

在 `internal/container/container.go` 注册 `NewForkBootstrapper`，并把它作为 `sandbox.SessionBootstrapper` 传进 `newTenantSandboxResolver` → `TenantSandboxResolverDeps.Bootstrapper`（Task 8 加的字段）。

> 这里有真实的循环依赖风险：`ForkBootstrapper` 需要 `SandboxShellRunner` 与 `SandboxWorkspaceWriter`，两者都由 `sandbox.Manager` 满足，而 manager 又需要 bootstrapper。用间接层打破：给 `ForkBootstrapper` 加 setter
> ```go
> // AttachSandbox wires the sandbox-side dependencies after construction. The
> // sandbox manager needs this bootstrapper at build time, so the two cannot be
> // constructed in one pass.
> func (b *ForkBootstrapper) AttachSandbox(runner SandboxShellRunner, writer SandboxWorkspaceWriter, snapshots ForkSnapshotDeleter) {
> 	b.runner, b.writer, b.snapshots = runner, writer, snapshots
> }
> ```
> 先构造 bootstrapper（sandbox 依赖为 nil），把它传给 resolver，再在 resolver 就绪后回填。`pendingBootstrap` 已经对 nil 依赖安全，`resetWorkspace` 对 nil runner 返回明确错误。

- [ ] **Step 6: 端到端手工验证**

Run: 在有真实 E2B 或 Cube 后端的环境上：
1. 建会话，让 agent 跑 3 轮，每轮写一个不同文件到 `/workspace`，并在第 2 轮 `pip install pandas`
2. 从第 3 轮的 user 消息分叉
3. 打开新会话，让 agent 执行 `ls /workspace && python -c "import pandas; print(pandas.__version__)"`

Expected: `/workspace` 只有前两轮的文件（第 3 轮的不在）；pandas 可导入（依赖环境随快照带过来了）

- [ ] **Step 7: 提交**

```bash
cd /data/workspace/WeKnora
git add internal/application/service/fork_bootstrapper.go \
        internal/application/service/fork_bootstrapper_test.go \
        internal/container/container.go
git commit -m "feat: ForkBootstrapper，新沙箱从快照建好后回退到分叉点

git reset 失败即清空 bootstrap 并回收快照，让 lifecycle 销毁沙箱，
下次 resolve 走全新沙箱路径。artifacts 还原是尽力而为的收尾，不让它
拖垮一个已经正确回退的文件系统。"
```

---

## Task 10: 孤儿快照回收

**设计依据：** spec §4.8、§6.8

**Files:**
- Create: `internal/application/service/fork_snapshot_reaper.go`
- Create: `internal/application/service/fork_snapshot_reaper_test.go`
- Modify: `internal/sandbox/docker_snapshot.go`（fork 快照独立命名空间）
- Modify: `internal/container/container.go`（启动定时任务）

**Interfaces:**
- Consumes: Task 6 的 `SessionRepository.ListUnconsumedForks` / `UpdateForkBootstrap`；Task 9 的 `ForkSnapshotDeleter`
- Produces: `service.ForkSnapshotReaper`、`NewForkSnapshotReaper(sessions, snapshots, retention time.Duration) *ForkSnapshotReaper`
- Produces: `(*ForkSnapshotReaper).ReapOnce(ctx context.Context) (int, error)`
- Produces: `sandbox.DockerForkSnapshotRepo = "weknora-fork"` 常量

- [ ] **Step 1: 写失败的测试**

创建 `internal/application/service/fork_snapshot_reaper_test.go`：

```go
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func agedFork(id string, age time.Duration) *types.Session {
	return &types.Session{
		ID: id, TenantID: 1,
		ForkBootstrap: &types.ForkBootstrap{
			SnapshotID: "snap-" + id,
			CreatedAt:  time.Now().UTC().Add(-age),
		},
	}
}

func TestReapDeletesSnapshotAndClearsBootstrap(t *testing.T) {
	sessions := newFakeSessionStore(nil)
	sessions.unconsumed = []*types.Session{agedFork("f1", 8*24*time.Hour)}
	snapshots := &fakeSnapshotDeleter{}
	reaper := NewForkSnapshotReaper(sessions, snapshots, 7*24*time.Hour)

	n, err := reaper.ReapOnce(context.Background())

	require.NoError(t, err)
	require.Equal(t, 1, n)
	require.Equal(t, []string{"snap-f1"}, snapshots.deleted)
	require.True(t, sessions.bootstrapCleared)
}

func TestReapReportsZeroWhenNothingIsOldEnough(t *testing.T) {
	sessions := newFakeSessionStore(nil)
	sessions.unconsumed = nil // 仓储层已按 retention 过滤
	reaper := NewForkSnapshotReaper(sessions, &fakeSnapshotDeleter{}, 7*24*time.Hour)

	n, err := reaper.ReapOnce(context.Background())

	require.NoError(t, err)
	require.Zero(t, n)
}

// 一个删不掉的快照不该挡住后面的。
func TestReapContinuesAfterOneDeleteFails(t *testing.T) {
	sessions := newFakeSessionStore(nil)
	sessions.unconsumed = []*types.Session{
		agedFork("f1", 8*24*time.Hour),
		agedFork("f2", 9*24*time.Hour),
	}
	snapshots := &fakeSnapshotDeleter{err: errors.New("provider timeout")}
	reaper := NewForkSnapshotReaper(sessions, snapshots, 7*24*time.Hour)

	_, err := reaper.ReapOnce(context.Background())

	require.NoError(t, err, "单个删除失败不该让整轮回收报错")
	require.Len(t, snapshots.deleted, 2, "两个都要尝试")
}

// 删不掉时绝不能清空 bootstrap，否则快照 ID 丢失，快照永久泄漏。
func TestReapKeepsBootstrapWhenDeleteFails(t *testing.T) {
	sessions := newFakeSessionStore(nil)
	sessions.unconsumed = []*types.Session{agedFork("f1", 8*24*time.Hour)}
	snapshots := &fakeSnapshotDeleter{err: errors.New("provider timeout")}
	reaper := NewForkSnapshotReaper(sessions, snapshots, 7*24*time.Hour)

	_, err := reaper.ReapOnce(context.Background())

	require.NoError(t, err)
	require.False(t, sessions.bootstrapCleared)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /data/workspace/WeKnora && go test ./internal/application/service/ -run 'TestReap' -v`
Expected: FAIL，`undefined: NewForkSnapshotReaper`

- [ ] **Step 3: 实现**

创建 `internal/application/service/fork_snapshot_reaper.go`：

```go
// Package service - orphan fork snapshot collection.
//
// A fork takes a provider snapshot immediately but provisions the sandbox
// lazily. A branch the user never opens therefore leaves a snapshot behind
// forever. This reaper collects those.
//
// The happy path does not depend on it: ForkBootstrapper deletes the snapshot
// the moment it is consumed. This exists only for forks that were never
// opened, or whose consumption failed to clean up.
package service

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// DefaultForkSnapshotRetention is how long an unopened fork keeps its
// snapshot. A week is long enough that "I'll get back to that branch on
// Monday" works, and short enough that abandoned forks do not accumulate.
const DefaultForkSnapshotRetention = 7 * 24 * time.Hour

type reaperSessionStore interface {
	ListUnconsumedForks(ctx context.Context, olderThan time.Time) ([]*types.Session, error)
	UpdateForkBootstrap(ctx context.Context, sessionID string, b *types.ForkBootstrap) error
}

// ForkSnapshotReaper deletes snapshots of forks that were never opened.
type ForkSnapshotReaper struct {
	sessions  reaperSessionStore
	snapshots ForkSnapshotDeleter
	retention time.Duration
}

// NewForkSnapshotReaper wires the reaper.
func NewForkSnapshotReaper(
	sessions reaperSessionStore, snapshots ForkSnapshotDeleter, retention time.Duration,
) *ForkSnapshotReaper {
	if retention <= 0 {
		retention = DefaultForkSnapshotRetention
	}
	return &ForkSnapshotReaper{sessions: sessions, snapshots: snapshots, retention: retention}
}

// ReapOnce runs a single collection pass and reports how many snapshots it
// retired.
//
// A per-snapshot delete failure is logged and skipped rather than returned:
// one unreachable snapshot must not stop the pass, and the next run retries it.
func (r *ForkSnapshotReaper) ReapOnce(ctx context.Context) (int, error) {
	if r == nil || r.sessions == nil || r.snapshots == nil {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-r.retention)
	stale, err := r.sessions.ListUnconsumedForks(ctx, cutoff)
	if err != nil {
		return 0, err
	}

	reaped := 0
	for _, session := range stale {
		if session.ForkBootstrap == nil {
			continue
		}
		snapshotID := session.ForkBootstrap.SnapshotID
		if err := r.snapshots.DeleteSnapshot(ctx, snapshotID); err != nil {
			// Clearing the bootstrap now would drop the only record of this
			// snapshot's ID and leak it permanently. Leave it for next time.
			logger.Warnf(ctx, "[ForkSnapshotReaper] delete %s (session=%s) failed: %v",
				snapshotID, session.ID, err)
			continue
		}
		if err := r.sessions.UpdateForkBootstrap(ctx, session.ID, nil); err != nil {
			logger.Warnf(ctx, "[ForkSnapshotReaper] clear bootstrap of %s failed: %v",
				session.ID, err)
			continue
		}
		reaped++
	}
	if reaped > 0 {
		logger.Infof(ctx, "[ForkSnapshotReaper] reaped %d orphan fork snapshot(s)", reaped)
	}
	return reaped, nil
}
```

- [ ] **Step 4: Docker 快照独立命名空间**

在 `internal/sandbox/docker_snapshot.go` 的常量块加：

```go
	// dockerForkSnapshotRepo is the local image namespace for session-fork
	// snapshots. It is deliberately separate from dockerSkillSnapshotRepo: the
	// skill reaper prunes by the skill namespace and labels, and letting fork
	// snapshots share them would have each reaper collecting the other's
	// images.
	dockerForkSnapshotRepo = "weknora-fork"

	dockerForkSnapshotLabel       = "com.weknora.sandbox.fork-snapshot"
	dockerForkSnapshotSourceLabel = "com.weknora.sandbox.fork-snapshot-source"
```

给 `DockerRemoteClient` 加一个 `CreateForkSnapshot`，与 `CreateSnapshot` 同结构但用 fork 的仓库前缀与 label；并确认 `ListTemplates` 对 `weknora-fork` 前缀同样隐藏（用 `grep -n "dockerSkillSnapshotRepo" internal/sandbox/*.go` 找到所有过滤点，逐个补上 fork 前缀）。

> 若 Task 7 的 `SessionForkSandboxPort.CreateForkSnapshot` 在 `SessionBoundManager` 上实现时直接复用了 `CreateSnapshot`，这里要改成分派到新方法：E2B/Cube 无所谓（快照就是 template，没有命名空间概念），Docker 必须走 fork 命名空间。

- [ ] **Step 5: 运行测试确认通过**

Run: `cd /data/workspace/WeKnora && go build ./... && go test ./internal/application/service/ -run 'TestReap' -v && go test ./internal/sandbox/ -run 'Snapshot' -v`
Expected: PASS

- [ ] **Step 6: 启动定时任务**

在 `internal/container/container.go` 中，仿照已有的后台任务注册方式（用 `grep -n "reaper\|Ticker\|go func" internal/container/container.go | head -20` 找到现有模式），每 6 小时跑一次 `ReapOnce`。

- [ ] **Step 7: 提交**

```bash
cd /data/workspace/WeKnora
git add internal/application/service/fork_snapshot_reaper.go \
        internal/application/service/fork_snapshot_reaper_test.go \
        internal/sandbox/docker_snapshot.go internal/container/container.go
git commit -m "feat: 孤儿 fork 快照回收 + Docker 独立命名空间

从没被打开过的分支会永久留下快照，7 天后回收。删除失败时保留
bootstrap，否则快照 ID 丢失会导致永久泄漏。Docker 侧用 weknora-fork
前缀，避免与技能快照的 reaper 互相误删。"
```

---

## Task 11: 前端分叉入口

**设计依据：** spec §5.1、§5.2

**Files:**
- Create: `frontend/src/views/chat/forkPoint.ts`
- Create: `frontend/src/views/chat/forkPoint.test.ts`
- Modify: `frontend/src/api/chat/index.ts`
- Modify: `frontend/src/views/chat/components/usermsg.vue`
- Modify: `frontend/src/views/chat/index.vue`

**Interfaces:**
- Consumes: Task 7 的 `POST /api/v1/sessions/:session_id/fork`
- Produces: `forkSession(sessionId: string, data: { message_id: string; title?: string })`
- Produces: `resolveForkAffordance(messages, messageId): { canFork: boolean; willDegrade: boolean }`
- Produces: `usermsg.vue` emit `fork`（payload 为该消息的 `id`）

- [ ] **Step 1: 写失败的测试**

创建 `frontend/src/views/chat/forkPoint.test.ts`：

```ts
import assert from 'node:assert/strict'
import test from 'node:test'
import { resolveForkAffordance } from './forkPoint'

const checkpoint = { sandbox_id: 'sbx-1', commit_sha: 'abc', committed_at: '2026-09-10T09:00:00Z' }

test('第一条 user 消息可以分叉且不算降级', () => {
  const messages = [
    { id: 'u1', role: 'user' },
    { id: 'a1', role: 'assistant', sandbox_checkpoint: checkpoint },
  ]
  assert.deepEqual(resolveForkAffordance(messages, 'u1'), { canFork: true, willDegrade: false })
})

test('前置 assistant 有 checkpoint 时不降级', () => {
  const messages = [
    { id: 'u1', role: 'user' },
    { id: 'a1', role: 'assistant', sandbox_checkpoint: checkpoint },
    { id: 'u2', role: 'user' },
  ]
  assert.deepEqual(resolveForkAffordance(messages, 'u2'), { canFork: true, willDegrade: false })
})

test('前置 assistant 没有 checkpoint 时仍可分叉但会降级', () => {
  const messages = [
    { id: 'u1', role: 'user' },
    { id: 'a1', role: 'assistant' },
    { id: 'u2', role: 'user' },
  ]
  assert.deepEqual(resolveForkAffordance(messages, 'u2'), { canFork: true, willDegrade: true })
})

test('取最近的一条前置 assistant 消息，而不是更早那条', () => {
  const messages = [
    { id: 'u1', role: 'user' },
    { id: 'a1', role: 'assistant', sandbox_checkpoint: checkpoint },
    { id: 'u2', role: 'user' },
    { id: 'a2', role: 'assistant' },
    { id: 'u3', role: 'user' },
  ]
  assert.deepEqual(resolveForkAffordance(messages, 'u3'), { canFork: true, willDegrade: true })
})

test('assistant 消息不能作为分叉点', () => {
  const messages = [
    { id: 'u1', role: 'user' },
    { id: 'a1', role: 'assistant', sandbox_checkpoint: checkpoint },
  ]
  assert.deepEqual(resolveForkAffordance(messages, 'a1'), { canFork: false, willDegrade: false })
})

test('未知消息 ID 不能分叉', () => {
  assert.deepEqual(
    resolveForkAffordance([{ id: 'u1', role: 'user' }], 'nope'),
    { canFork: false, willDegrade: false },
  )
})

test('未完成的 assistant 消息意味着本轮还在跑，不给分叉', () => {
  const messages = [
    { id: 'u1', role: 'user' },
    { id: 'a1', role: 'assistant', is_completed: false },
  ]
  assert.deepEqual(resolveForkAffordance(messages, 'u1'), { canFork: false, willDegrade: false })
})
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /data/workspace/WeKnora/frontend && npx tsx --test src/views/chat/forkPoint.test.ts`
Expected: FAIL，找不到 `./forkPoint`

- [ ] **Step 3: 实现纯函数**

创建 `frontend/src/views/chat/forkPoint.ts`：

```ts
/**
 * Fork affordance rules, kept as a pure function so they can be tested without
 * mounting the chat view.
 *
 * The backend re-derives all of this; this is purely so the UI can show the
 * right button state and tooltip without an extra round trip.
 */

export interface ForkCandidateMessage {
  id?: unknown
  role?: unknown
  is_completed?: unknown
  sandbox_checkpoint?: unknown
}

export interface ForkAffordance {
  /** Whether the fork button should be offered on this message at all. */
  canFork: boolean
  /**
   * Whether forking here will start from a brand new sandbox rather than a
   * copy of the current one. The fork still succeeds; the tooltip just says so.
   */
  willDegrade: boolean
}

const REFUSED: ForkAffordance = { canFork: false, willDegrade: false }

export function resolveForkAffordance(
  messages: ForkCandidateMessage[],
  messageId: string,
): ForkAffordance {
  const index = messages.findIndex((m) => m.id === messageId)
  if (index < 0 || messages[index].role !== 'user') {
    return REFUSED
  }

  // A turn still streaming means the source session holds an active sandbox
  // lease. The backend would answer 409, so do not offer the button.
  if (messages.some((m) => m.role === 'assistant' && m.is_completed === false)) {
    return REFUSED
  }

  // Walk back to the nearest preceding assistant message: that turn's
  // checkpoint is the state a fork here would roll back to.
  for (let i = index - 1; i >= 0; i -= 1) {
    if (messages[i].role !== 'assistant') continue
    return { canFork: true, willDegrade: !messages[i].sandbox_checkpoint }
  }

  // Nothing before this message produced output, so a brand new sandbox is the
  // correct result rather than a degradation.
  return { canFork: true, willDegrade: false }
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd /data/workspace/WeKnora/frontend && npx tsx --test src/views/chat/forkPoint.test.ts`
Expected: PASS，7 个测试全绿

- [ ] **Step 5: 加 API 函数**

在 `frontend/src/api/chat/index.ts` 的 `updateSession` 之后加：

```ts
export async function forkSession(
  session_id: string,
  data: { message_id: string; title?: string },
) {
  return post(`/api/v1/sessions/${session_id}/fork`, data);
}
```

- [ ] **Step 6: 给 usermsg.vue 加分叉按钮**

在 `frontend/src/views/chat/components/usermsg.vue` 的 `.user_msg` 气泡之后加一个 hover 显隐的操作区。该组件目前没有 `defineEmits`，需要新增：

```vue
    <div class="user_msg">
      {{ content }}
    </div>
    <div v-if="canFork" class="user_msg_actions">
      <t-tooltip :content="forkTooltip">
        <t-button
          variant="text"
          size="small"
          class="user_msg_action"
          @click="emit('fork', messageId)"
        >
          <t-icon name="git-branch" />
        </t-button>
      </t-tooltip>
    </div>
```

script 部分新增（与该文件现有 `<script setup>` 的写法保持一致）：

```ts
const props = defineProps<{
  // ... 保留现有 props ...
  messageId: string
  canFork?: boolean
  willDegrade?: boolean
}>()

const emit = defineEmits<{ (e: 'fork', messageId: string): void }>()

const canFork = computed(() => props.canFork === true)
const forkTooltip = computed(() =>
  props.willDegrade
    ? '从这里分叉出新会话（将创建全新沙箱环境）'
    : '从这里分叉出新会话',
)
```

样式加在该文件 `<style>` 块内，跟随现有的下划线命名：

```css
.user_msg_actions {
  display: flex;
  justify-content: flex-end;
  opacity: 0;
  transition: opacity 0.15s ease;
}

.user_msg_container:hover .user_msg_actions {
  opacity: 1;
}
```

> `git-branch` 这个图标名需要核实 TDesign 是否提供。执行
> `grep -rn "t-icon" frontend/src/views/chat/components/botmsg.vue | head` 看现有用法，并到 TDesign 图标表确认。若没有，用 `add-rectangle` 或 `root-list` 等已在项目中出现过的图标替代。

- [ ] **Step 7: 在 chat/index.vue 里接上**

在 `usermsg` 的使用处（`frontend/src/views/chat/index.vue:100-128` 附近）传入新 props 并监听事件：

```vue
    <usermsg
      ...
      :session-id="session_id"
      :message-id="session.id"
      :can-fork="forkAffordanceOf(session.id).canFork"
      :will-degrade="forkAffordanceOf(session.id).willDegrade"
      @fork="handleFork"
    />
```

script 部分加：

```ts
import { resolveForkAffordance } from './forkPoint'
import { forkSession } from '@/api/chat'

function forkAffordanceOf(messageId: string) {
  return resolveForkAffordance(messagesList as any[], messageId)
}
```

`handleFork` 的实现留到 Task 12（需要跳转与预填）。本步先放一个最小可用版本：

```ts
async function handleFork(messageId: string) {
  const res = await forkSession(session_id.value, { message_id: messageId })
  const data = (res as any)?.data
  if (!data?.session_id) return
  router.push(`/platform/chat/${data.session_id}`)
}
```

> 路由路径以该文件里现有的会话跳转写法为准，用 `grep -n "router.push" frontend/src/views/chat/index.vue frontend/src/components/menu.vue | head` 确认。

- [ ] **Step 8: 类型检查与全量前端测试**

Run: `cd /data/workspace/WeKnora/frontend && npm run build 2>&1 | tail -20 && npm test 2>&1 | tail -20`
Expected: 构建通过，测试全绿

- [ ] **Step 9: 提交**

```bash
cd /data/workspace/WeKnora
git add frontend/src/views/chat/forkPoint.ts frontend/src/views/chat/forkPoint.test.ts \
        frontend/src/api/chat/index.ts \
        frontend/src/views/chat/components/usermsg.vue \
        frontend/src/views/chat/index.vue
git commit -m "feat: 前端会话分叉入口

user 消息 hover 出分叉按钮。分叉可用性与是否会降级由纯函数
resolveForkAffordance 从已加载的消息本地推导，不额外请求。"
```

---

## Task 12: 前端预填、降级提示与来源角标

**设计依据：** spec §5.1、§5.3、§5.4

**Files:**
- Modify: `frontend/src/components/Input-field.vue:2509-2515`（`defineExpose` 加 `prefill`）
- Modify: `frontend/src/views/chat/index.vue`（fork 后预填 + 降级横幅）
- Modify: `frontend/src/components/SessionSidebarRow.vue:14-20`（来源角标）
- Create: `frontend/src/views/chat/forkNotice.ts`
- Create: `frontend/src/views/chat/forkNotice.test.ts`

**Interfaces:**
- Consumes: Task 11 的 `forkSession`
- Produces: `Input-field.vue` 的 `prefill(text: string): void`
- Produces: `forkDegradeMessage(reason: string): string`

- [ ] **Step 1: 写失败的测试**

创建 `frontend/src/views/chat/forkNotice.test.ts`：

```ts
import assert from 'node:assert/strict'
import test from 'node:test'
import { forkDegradeMessage } from './forkNotice'

test('每个降级原因码都有对应文案', () => {
  assert.equal(
    forkDegradeMessage('NO_CHECKPOINT'),
    '未能复制沙箱环境（该轮未创建检查点），已为分支创建全新环境',
  )
  assert.equal(
    forkDegradeMessage('SANDBOX_REPLACED'),
    '原会话中途更换过沙箱，未能复制环境，已创建全新环境',
  )
  assert.equal(
    forkDegradeMessage('SANDBOX_GONE'),
    '原会话沙箱已回收，未能复制环境，已创建全新环境',
  )
  assert.equal(
    forkDegradeMessage('SNAPSHOT_UNSUPPORTED'),
    '当前沙箱后端不支持环境复制，已创建全新环境',
  )
})

test('未知原因码退化为通用文案而不是显示原始码', () => {
  assert.equal(forkDegradeMessage('SOMETHING_NEW'), '未能复制沙箱环境，已创建全新环境')
})

test('空原因码返回空串，调用方据此不显示横幅', () => {
  assert.equal(forkDegradeMessage(''), '')
})
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /data/workspace/WeKnora/frontend && npx tsx --test src/views/chat/forkNotice.test.ts`
Expected: FAIL，找不到 `./forkNotice`

- [ ] **Step 3: 实现文案映射**

创建 `frontend/src/views/chat/forkNotice.ts`：

```ts
/**
 * Fork degradation copy.
 *
 * A degraded fork still succeeded — the branch exists and the conversation is
 * intact — so the wording explains what was NOT carried over rather than
 * reading as an error.
 */

const FORK_DEGRADE_COPY: Record<string, string> = {
  NO_CHECKPOINT: '未能复制沙箱环境（该轮未创建检查点），已为分支创建全新环境',
  SANDBOX_REPLACED: '原会话中途更换过沙箱，未能复制环境，已创建全新环境',
  SANDBOX_GONE: '原会话沙箱已回收，未能复制环境，已创建全新环境',
  SNAPSHOT_UNSUPPORTED: '当前沙箱后端不支持环境复制，已创建全新环境',
}

/** Returns the banner text for a degrade reason, or '' when none applies. */
export function forkDegradeMessage(reason: string): string {
  if (!reason) return ''
  return FORK_DEGRADE_COPY[reason] ?? '未能复制沙箱环境，已创建全新环境'
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd /data/workspace/WeKnora/frontend && npx tsx --test src/views/chat/forkNotice.test.ts`
Expected: PASS，3 个测试全绿

- [ ] **Step 5: 给输入框加 prefill**

`frontend/src/components/Input-field.vue` 的 `defineExpose` 改为：

```ts
defineExpose({
  triggerSend(text: string) {
    if (!text.trim()) return;
    query.value = text;
    nextTick(() => createSession(text));
  },
  /**
   * Puts text in the composer WITHOUT sending it. Session fork uses this so
   * the user lands on the branch with the original question ready to edit —
   * the whole point of branching at a user message.
   */
  prefill(text: string) {
    query.value = text;
  }
});
```

- [ ] **Step 6: fork 后跳转、预填、显示降级横幅**

`frontend/src/views/chat/index.vue` 把 Task 11 的 `handleFork` 换成：

```ts
import { forkDegradeMessage } from './forkNotice'

const forkNotice = ref('')

async function handleFork(messageId: string) {
  const source = (messagesList as any[]).find((m) => m.id === messageId)
  if (!source) return

  try {
    const res: any = await forkSession(session_id.value, { message_id: messageId })
    const data = res?.data
    if (!data?.session_id) return

    // Carry the question across the navigation: the branch's message list is
    // reloaded from scratch, so prefill has to happen after the new session's
    // history has mounted.
    pendingForkPrefill.value = String(source.content ?? '')
    forkNotice.value = data.degraded ? forkDegradeMessage(String(data.reason ?? '')) : ''

    await router.push(`/platform/chat/${data.session_id}`)
  } catch (err: any) {
    if (err?.status === 409 || err?.response?.status === 409) {
      MessagePlugin.warning('请等本轮回答结束后再分叉')
      return
    }
    MessagePlugin.error('分叉失败，请重试')
  }
}

const pendingForkPrefill = ref('')

// The branch loads its history like any other session; prefill once that has
// settled so the composer is not clobbered by the mount.
watch(messagesList, () => {
  if (!pendingForkPrefill.value) return
  inputFieldRef.value?.prefill(pendingForkPrefill.value)
  pendingForkPrefill.value = ''
}, { flush: 'post' })
```

在模板消息列表上方加横幅：

```vue
  <t-alert
    v-if="forkNotice"
    theme="warning"
    class="chat-fork-notice"
    :message="forkNotice"
    close
    @close="forkNotice = ''"
  />
```

> `MessagePlugin` 的导入方式、错误对象上状态码的实际字段（`err.status` 还是 `err.response.status`）以该文件现有的错误处理为准。执行
> `grep -n "MessagePlugin\|catch (err" frontend/src/views/chat/index.vue | head -20` 核对后再写。
> `pendingForkPrefill` 跨路由跳转会随组件卸载丢失——若 chat 视图在切换会话时会重新挂载，需要改用 `sessionStorage` 或路由 query 承载。先用 `grep -n ":key=" frontend/src/router` 或直接实测切会话时组件是否重建来确定。

- [ ] **Step 7: 侧边栏来源角标**

`frontend/src/components/SessionSidebarRow.vue:14-20` 的 `.submenu_title` 内，`is_pinned` 图标之后加：

```vue
  <t-tooltip v-if="item.parent_session_id" content="由其他会话分叉而来">
    <t-icon name="git-branch" class="submenu_fork_icon" />
  </t-tooltip>
```

`frontend/src/components/sessionGrouping.ts:17-27` 的 `SessionForGrouping` 加可选字段：

```ts
  parent_session_id?: string
```

样式跟随现有 `.submenu_pin_icon` 的写法加一条 `.submenu_fork_icon`。

- [ ] **Step 8: 前端全量验证**

Run: `cd /data/workspace/WeKnora/frontend && npm run build 2>&1 | tail -20 && npm test 2>&1 | tail -20`
Expected: 构建通过，全部测试绿

- [ ] **Step 9: 端到端手工验收**

1. 开一个会话，跑 3 轮 agent，每轮写一个文件到 `/workspace`
2. 在第 3 轮的 user 消息上点分叉 → 应跳到新会话，输入框预填了该问题，历史只到第 2 轮
3. 改一下问题发出去，让 agent `ls /workspace` → 只应看到前两轮的文件
4. 回到原会话 → 三轮历史完整，`/workspace` 里三个文件都在
5. 源会话正在生成时点分叉 → 应提示"请等本轮回答结束后再分叉"
6. 侧边栏上新会话有分支角标，鼠标悬停显示提示

Expected: 六条全部符合

- [ ] **Step 10: 提交**

```bash
cd /data/workspace/WeKnora
git add frontend/src/views/chat/forkNotice.ts frontend/src/views/chat/forkNotice.test.ts \
        frontend/src/views/chat/index.vue frontend/src/components/Input-field.vue \
        frontend/src/components/SessionSidebarRow.vue frontend/src/components/sessionGrouping.ts
git commit -m "feat: 分叉后预填输入框、降级提示与侧边栏来源角标

Input-field 新增 prefill（只填不发），因为在 user 消息上分叉的意义就是
编辑后重发。降级用 t-alert 横幅告知，409 走 toast 提示等本轮结束。"
```

---

## Self-Review

**1. Spec coverage**

| spec 章节 | 覆盖任务 |
|---|---|
| §1 架构 | 全部 |
| §2.1 sessions 三字段 | Task 1 |
| §2.2 messages checkpoint | Task 1 |
| §2.3 迁移 | Task 1 |
| §2.4 排序稳定化 | Task 2 |
| §3.1 镜像装 git | Task 3 |
| §3.2 惰性幂等初始化 | Task 4 |
| §3.3 allow-empty / safe.directory / gitignore | Task 3 Step 4、Task 4 |
| §3.4 hook 位置 | Task 5 |
| §3.5 取消轮次也 commit | Task 5 Step 4 |
| §3.6 best-effort | Task 4、Task 5 |
| §3.7 性能（30s 超时） | Task 4 |
| §4.1 接口 | Task 7 |
| §4.2 判定顺序 | Task 6 |
| §4.3 活跃 turn 拦截 | Task 6、Task 7 |
| §4.4 消息复制 | Task 6 |
| §4.5 全有或全无 bootstrap | Task 8、Task 9 |
| §4.6 不污染 createRequest | Task 8 Step 4（含专门的回归测试） |
| §4.7 artifacts 还原 | Task 9 |
| §4.8 快照 GC + Docker 命名空间 | Task 10 |
| §5.1 分叉入口 | Task 11 |
| §5.2 按钮显隐 | Task 11 |
| §5.3 降级提示 | Task 12 |
| §5.4 侧边栏 | Task 12 |
| §6.5 artifact blob 共享 | Task 6（`copyMessagesInto` 注释） |
| §6.6 不重复建索引 | Task 6（`copyMessagesInto` 清 `KnowledgeID`） |
| §6.8 每租户未消费快照上限 | **未覆盖** |

**缺口一处：** spec §6.8 建议给每租户加"未消费快照数"上限（如 10），防止连点或脚本化调用短时间内堆出大量快照。当前计划只有 7 天 TTL 的 reaper 兜底。这是纵深防御而非正确性问题，且需要一次额外的仓储查询，建议作为 Task 10 的后续增量单独处理——在 `SessionForkService.Fork` 的活跃 turn 检查之后加一次 `CountUnconsumedForks(tenantID)`，超限返回一个新的哨兵 error 映射为 429。执行本计划时若时间允许，可并入 Task 10。

**2. Placeholder scan**

无 TBD / TODO / "类似 Task N"。有 8 处 `>` 引用块要求实现者先 grep 核实（响应封装形状、`currentUserID` 名称、`MessageArtifact` 字段名、file service 反向读取方法、TDesign 图标名、路由路径、错误状态码字段、chat 视图是否跨会话重建）。这些是本仓库中我无法在不读更多文件的前提下断言的具体名称，每处都给了确切的 grep 命令和判断标准，不是"自行处理"式的空洞指示。

**3. Type consistency**

- `SandboxCheckpoint` / `ForkBootstrap`：Task 1 定义，Task 4/5/6/9/10 一致引用；字段名 `SandboxID`/`CommitSHA`/`CommittedAt`、`SnapshotID`/`CommitSHA`/`SourceSandboxID`/`CreatedAt`/`ConsumedAt` 全文统一。
- `SandboxShellRunner.ExecShellCommand`：Task 4 定义签名，Task 9 复用同一接口，两处 fake 签名一致。
- `BoundSandboxID(ctx, sessionID) (string, bool)`：Task 5 的 `SandboxIDLookup` 与 Task 6 的 `SessionForkSandboxPort` 用的是同一签名，实现上由 `SessionBoundManager` 同一个方法满足。
- `ForkSnapshotDeleter.DeleteSnapshot`：Task 9 定义，Task 10 复用。
- 降级原因码：Task 6 的四个 Go 常量与 Task 12 的四个 TS key 字符串逐一对应。
- `SessionRepository` 新增的三个方法（`CreateForked` / `UpdateForkBootstrap` / `ListUnconsumedForks`）在 Task 6 一次性加齐，Task 9/10 只消费不再新增。

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-09-10-session-fork.md`.**
