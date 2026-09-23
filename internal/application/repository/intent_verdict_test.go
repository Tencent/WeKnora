package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newIntentVerdictTestRepo(t *testing.T) interfaces.IntentVerdictRepository {
	t.Helper()
	dsn := "file:" + uuid.NewString() + "?mode=memory&cache=shared&_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&types.VerdictRecord{}))
	return NewIntentVerdictRepository(db)
}

func verdictInput(sessionID string) types.VerdictRecordInput {
	return types.VerdictRecordInput{
		TenantID:           1,
		SessionID:          sessionID,
		AssistantMessageID: uuid.NewString(),
		ToolCallID:         "call_" + uuid.NewString()[:8],
		PolicyID:           uuid.NewString(),
		PolicyVersion:      3,
		ToolName:           "web_search",
		Args:               json.RawMessage(`{"query":"intent gate"}`),
		Layer:              types.VerdictLayerRule,
		Verdict:            types.VerdictActionAllow,
		Reason:             "spike.test: ok",
		ModeAtDecision:     types.VerdictModeObserve,
		LatencyMs:          1,
	}
}

func mustVerdictRecord(t *testing.T, in types.VerdictRecordInput) *types.VerdictRecord {
	t.Helper()
	rec, err := types.NewVerdictRecord(in)
	require.NoError(t, err)
	return rec
}

// ---------- types.NewVerdictRecord: digest & 脱敏 ----------

func TestNewVerdictRecordDigestsArgs(t *testing.T) {
	in := verdictInput("s1")
	// 乱序键：digest 必须对规范化后的 JSON 计算，键序不影响结果。
	in.Args = json.RawMessage(`{"b":2,"a":1}`)
	rec := mustVerdictRecord(t, in)

	canonical, err := json.Marshal(map[string]any{"a": float64(1), "b": float64(2)})
	require.NoError(t, err)
	sum := sha256.Sum256(canonical)
	require.Equal(t, hex.EncodeToString(sum[:]), rec.ArgsDigest)

	require.NotEmpty(t, rec.ID)
	require.Equal(t, types.HumanOverrideNone, rec.HumanOverride)
	require.False(t, rec.CreatedAt.IsZero())
	require.Equal(t, time.UTC, rec.CreatedAt.Location())
}

func TestNewVerdictRecordBaselinePolicyIsNull(t *testing.T) {
	in := verdictInput("s1")
	in.PolicyID = ""
	in.PolicyVersion = 0
	rec := mustVerdictRecord(t, in)
	require.Nil(t, rec.PolicyID, "policy_id 空 = 兜底判定（设计 §6.2），必须落 NULL")
	require.Nil(t, rec.PolicyVersion)
}

// TestNewVerdictRecordJudgeModel：judge 模型归属透传（T61，issue #24）。
// 规则层/baseline 判定输入 JudgeModel 为空串，落库即空串（judge_model
// 列默认 ''）；judge 层判定由 LLMJudge 填模型 ID。
func TestNewVerdictRecordJudgeModel(t *testing.T) {
	in := verdictInput("s1")
	rec := mustVerdictRecord(t, in)
	require.Equal(t, "", rec.JudgeModel, "规则层判定无 judge 模型归属")

	in = verdictInput("s1")
	in.Layer = types.VerdictLayerJudge
	in.JudgeModel = "builtin-kimi-coding"
	rec = mustVerdictRecord(t, in)
	require.Equal(t, "builtin-kimi-coding", rec.JudgeModel)
}

// TestNewVerdictRecordRedactsSensitiveArgs 是验收标准「args_digest 脱敏」的
// 核心断言：database_query 这类敏感工具的原始参数（SQL 原文）绝不落库，
// 整条记录任何字段都不得出现原文，只存 digest。
func TestNewVerdictRecordRedactsSensitiveArgs(t *testing.T) {
	const sqlText = "DELETE FROM users WHERE tenant_id = 42"
	require.True(t, types.SensitiveArgsTools["database_query"],
		"database_query 必须在敏感参数工具清单内（与 act.go toolHintSensitiveArgs 对齐）")

	in := verdictInput("s1")
	in.ToolName = "database_query"
	in.Args = json.RawMessage(fmt.Sprintf(`{"sql":%q}`, sqlText))
	rec := mustVerdictRecord(t, in)

	blob, err := json.Marshal(rec)
	require.NoError(t, err)
	require.NotContains(t, string(blob), sqlText, "敏感参数原文不得出现在记录任何字段")
	require.NotContains(t, string(blob), "DELETE FROM", "SQL 语句片段不得落库")
	require.Len(t, rec.ArgsDigest, 64, "敏感参数只存 sha256 hex digest")
}

func TestNewVerdictRecordRejectsUnknownEnums(t *testing.T) {
	base := verdictInput("s1")

	bad := base
	bad.Layer = "magic"
	_, err := types.NewVerdictRecord(bad)
	require.Error(t, err, "未知 layer 必须拒绝，防止脏数据进入 verdict 链路")

	bad = base
	bad.Verdict = "maybe"
	_, err = types.NewVerdictRecord(bad)
	require.Error(t, err)

	bad = base
	bad.ModeAtDecision = "yolo"
	_, err = types.NewVerdictRecord(bad)
	require.Error(t, err)

	bad = base
	bad.ModeAtDecision = ""
	rec, err := types.NewVerdictRecord(bad)
	require.NoError(t, err, "mode 缺省应为 observe（新策略一律 Observe 起步）")
	require.Equal(t, types.VerdictModeObserve, rec.ModeAtDecision)
}

// ---------- repository CRUD ----------

func TestIntentVerdictCreateAndListBySession(t *testing.T) {
	repo := newIntentVerdictTestRepo(t)
	ctx := context.Background()

	base := time.Date(2026, 9, 21, 10, 0, 0, 0, time.UTC)
	r1 := mustVerdictRecord(t, verdictInput("sess-a"))
	r1.CreatedAt = base
	r2 := mustVerdictRecord(t, verdictInput("sess-a"))
	r2.CreatedAt = base.Add(time.Second)
	r3 := mustVerdictRecord(t, verdictInput("sess-b"))
	r3.CreatedAt = base.Add(2 * time.Second)
	for _, r := range []*types.VerdictRecord{r1, r2, r3} {
		require.NoError(t, repo.Create(ctx, r))
	}

	rows, err := repo.ListBySession(ctx, 1, "sess-a", 0)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.Equal(t, r1.ID, rows[0].ID, "按 created_at 升序（会话日志按时间读）")
	require.Equal(t, r2.ID, rows[1].ID)
	require.Equal(t, "web_search", rows[0].ToolName)
	require.Equal(t, r1.ArgsDigest, rows[0].ArgsDigest)
	require.NotNil(t, rows[0].PolicyID)

	// 租户隔离：其他租户看不到 sess-a 的 verdict。
	rows, err = repo.ListBySession(ctx, 2, "sess-a", 0)
	require.NoError(t, err)
	require.Empty(t, rows)

	// limit 生效。
	rows, err = repo.ListBySession(ctx, 1, "sess-a", 1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

func TestIntentVerdictListByPolicy(t *testing.T) {
	repo := newIntentVerdictTestRepo(t)
	ctx := context.Background()

	policyID := uuid.NewString()
	in1 := verdictInput("s1")
	in1.PolicyID = policyID
	in1.PolicyVersion = 1
	r1 := mustVerdictRecord(t, in1)
	in2 := verdictInput("s1")
	in2.PolicyID = policyID
	in2.PolicyVersion = 2
	r2 := mustVerdictRecord(t, in2)
	other := mustVerdictRecord(t, verdictInput("s1")) // 别的策略
	inBaseline := verdictInput("s1")
	inBaseline.PolicyID = ""
	inBaseline.PolicyVersion = 0
	baseline := mustVerdictRecord(t, inBaseline)
	for _, r := range []*types.VerdictRecord{r1, r2, other, baseline} {
		require.NoError(t, repo.Create(ctx, r))
	}

	rows, err := repo.ListByPolicy(ctx, 1, policyID, 0)
	require.NoError(t, err)
	require.Len(t, rows, 2, "同一策略的 v1/v2 判定都应可查（版本对账，设计 §3.2）")

	// 租户隔离。
	rows, err = repo.ListByPolicy(ctx, 2, policyID, 0)
	require.NoError(t, err)
	require.Empty(t, rows)
}

// TestIntentVerdictListByJudgeModel 验收 [unit]（T61，issue #24）：
// 按 judge 模型分层过滤——空串取规则层/baseline 判定，具体模型 ID 只取
// 该 judge 产出的行；租户隔离与 limit 同时生效。
func TestIntentVerdictListByJudgeModel(t *testing.T) {
	repo := newIntentVerdictTestRepo(t)
	ctx := context.Background()

	base := time.Date(2026, 9, 23, 10, 0, 0, 0, time.UTC)
	mk := func(model, session string, at time.Time) *types.VerdictRecord {
		in := verdictInput(session)
		in.Layer = types.VerdictLayerJudge
		in.JudgeModel = model
		r := mustVerdictRecord(t, in)
		r.CreatedAt = at
		return r
	}
	rKimi := mk("builtin-kimi-coding", "sess-kimi", base)
	rKimi2 := mk("builtin-kimi-coding", "sess-kimi", base.Add(time.Second))
	rOther := mk("builtin-qwen", "sess-qwen", base.Add(2*time.Second))
	inRule := verdictInput("sess-rule")
	rRule := mustVerdictRecord(t, inRule) // JudgeModel 缺省 = 规则层
	rRule.CreatedAt = base.Add(3 * time.Second)
	for _, r := range []*types.VerdictRecord{rKimi, rKimi2, rOther, rRule} {
		require.NoError(t, repo.Create(ctx, r))
	}

	rows, err := repo.ListByJudgeModel(ctx, 1, "builtin-kimi-coding", 0)
	require.NoError(t, err)
	require.Len(t, rows, 2, "只取该 judge 模型产出的行")
	require.Equal(t, rKimi.ID, rows[0].ID, "created_at 升序（导出语料的稳定顺序）")
	require.Equal(t, rKimi2.ID, rows[1].ID)
	require.Equal(t, "builtin-kimi-coding", rows[0].JudgeModel)

	rows, err = repo.ListByJudgeModel(ctx, 1, "", 0)
	require.NoError(t, err)
	require.Len(t, rows, 1, "空 judge_model = 规则层/baseline 判定")
	require.Equal(t, rRule.ID, rows[0].ID)

	// 租户隔离 + limit。
	rows, err = repo.ListByJudgeModel(ctx, 2, "builtin-kimi-coding", 0)
	require.NoError(t, err)
	require.Empty(t, rows)
	rows, err = repo.ListByJudgeModel(ctx, 1, "builtin-kimi-coding", 1)
	require.NoError(t, err)
	require.Len(t, rows, 1)
}

func TestIntentVerdictGetAndUpdateHumanOverride(t *testing.T) {
	repo := newIntentVerdictTestRepo(t)
	ctx := context.Background()

	rec := mustVerdictRecord(t, verdictInput("s1"))
	require.NoError(t, repo.Create(ctx, rec))

	got, err := repo.GetByID(ctx, 1, rec.ID)
	require.NoError(t, err)
	require.Equal(t, types.HumanOverrideNone, got.HumanOverride)
	require.Equal(t, rec.Reason, got.Reason)

	require.NoError(t, repo.UpdateHumanOverride(ctx, 1, rec.ID, types.HumanOverrideModified))
	got, err = repo.GetByID(ctx, 1, rec.ID)
	require.NoError(t, err)
	require.Equal(t, types.HumanOverrideModified, got.HumanOverride)

	require.Error(t, repo.UpdateHumanOverride(ctx, 1, rec.ID, "bogus"),
		"human_override 是枚举字段，未知值必须拒绝")
	require.Error(t, repo.UpdateHumanOverride(ctx, 1, uuid.NewString(), types.HumanOverrideApproved),
		"更新不存在的记录必须报错而不是静默成功")

	// 租户隔离：跨租户 get 不得命中。
	_, err = repo.GetByID(ctx, 2, rec.ID)
	require.Error(t, err)
}

func TestIntentVerdictDelete(t *testing.T) {
	repo := newIntentVerdictTestRepo(t)
	ctx := context.Background()

	rec := mustVerdictRecord(t, verdictInput("s1"))
	require.NoError(t, repo.Create(ctx, rec))
	require.NoError(t, repo.Delete(ctx, 1, rec.ID))

	_, err := repo.GetByID(ctx, 1, rec.ID)
	require.True(t, errors.Is(err, ErrIntentVerdictNotFound), "删除后 GetByID 应返回 not found，实际: %v", err)

	// 跨租户删除不得命中。
	rec2 := mustVerdictRecord(t, verdictInput("s1"))
	require.NoError(t, repo.Create(ctx, rec2))
	require.Error(t, repo.Delete(ctx, 2, rec2.ID))
	_, err = repo.GetByID(ctx, 1, rec2.ID)
	require.NoError(t, err)
}

func TestIntentVerdictCreateValidation(t *testing.T) {
	repo := newIntentVerdictTestRepo(t)
	ctx := context.Background()

	require.Error(t, repo.Create(ctx, nil))

	rec := mustVerdictRecord(t, verdictInput("s1"))
	rec.SessionID = ""
	require.Error(t, repo.Create(ctx, rec), "缺 session_id 的行无法回链会话，必须拒绝")

	rec = mustVerdictRecord(t, verdictInput("s1"))
	rec.Verdict = ""
	require.Error(t, repo.Create(ctx, rec))
}
