package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/agent/intentgate"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// fakeVerdictWriter 是 engine 接缝测试的测试缝：实现
// intentgate.VerdictWriter，记录每次 Write 的记录供断言。
type fakeVerdictWriter struct {
	mu      sync.Mutex
	records []*types.VerdictRecord
}

func (f *fakeVerdictWriter) Write(rec *types.VerdictRecord) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records = append(f.records, rec)
}

func (f *fakeVerdictWriter) Close(context.Context) error { return nil }

func (f *fakeVerdictWriter) written() []*types.VerdictRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*types.VerdictRecord(nil), f.records...)
}

// TestIntentGateVerdictEnqueuedWithFields 验收 [unit]：判定成功后 verdict
// 被异步入队落库，记录字段齐全且与本次工具调用对得上；observe 语义下
// 工具仍被执行（双向断言：该记的记下 + 不该拦的放行）。
func TestIntentGateVerdictEnqueuedWithFields(t *testing.T) {
	engine, executed := intentGateTestEngine(t)
	engine.SetIntentGate(&fakeGate{verdict: intentgate.Verdict{
		Action:   intentgate.ActionDeny,
		PolicyID: "pol-1",
		Reason:   "会话历史无删除意图",
		Layer:    intentgate.LayerRule,
	}})
	writer := &fakeVerdictWriter{}
	engine.SetIntentVerdictWriter(writer)

	toolCall := runGatedToolCall(engine)

	// observe 语义：deny 不拦截，工具照常执行成功。
	if *executed != 1 || toolCall.Result == nil || !toolCall.Result.Success {
		t.Fatalf("observe deny must not block execution: executed=%d result=%+v", *executed, toolCall.Result)
	}
	// verdict 被入队：恰好一条，字段与调用一一对应。
	records := writer.written()
	if len(records) != 1 {
		t.Fatalf("writer must receive exactly 1 record, got %d", len(records))
	}
	rec := records[0]
	if rec.SessionID != "session-1" {
		t.Fatalf("session_id = %q, want session-1", rec.SessionID)
	}
	if rec.ToolCallID != "call-1" {
		t.Fatalf("tool_call_id = %q, want call-1", rec.ToolCallID)
	}
	if rec.AssistantMessageID != "msg-1" {
		t.Fatalf("assistant_message_id = %q, want msg-1", rec.AssistantMessageID)
	}
	if rec.ToolName != "search_knowledge" {
		t.Fatalf("tool_name = %q, want search_knowledge", rec.ToolName)
	}
	if rec.Verdict != types.VerdictActionDeny {
		t.Fatalf("verdict = %q, want deny", rec.Verdict)
	}
	if rec.Layer != types.VerdictLayerRule {
		t.Fatalf("layer = %q, want rule", rec.Layer)
	}
	if rec.ModeAtDecision != types.VerdictModeObserve {
		t.Fatalf("mode_at_decision = %q, want observe（spike 阶段一切判定均为 observe）", rec.ModeAtDecision)
	}
	if rec.PolicyID == nil || *rec.PolicyID != "pol-1" {
		t.Fatalf("policy_id = %v, want pol-1", rec.PolicyID)
	}
	if rec.Reason != "会话历史无删除意图" {
		t.Fatalf("reason = %q", rec.Reason)
	}
	if len(rec.ArgsDigest) != 64 {
		t.Fatalf("args_digest = %q, want sha256 hex（原始参数不落库）", rec.ArgsDigest)
	}
	if rec.LatencyMs < 0 {
		t.Fatalf("latency_ms = %d, must be >= 0", rec.LatencyMs)
	}
	if rec.HumanOverride != types.HumanOverrideNone {
		t.Fatalf("human_override = %q, want none", rec.HumanOverride)
	}
}

// TestIntentGateAllowEnqueuedAsBaseline 验收：无策略命中的 allow 判定
// 落库为 layer=baseline、policy_id=NULL（设计 §6.2 兜底判定）。
func TestIntentGateAllowEnqueuedAsBaseline(t *testing.T) {
	engine, executed := intentGateTestEngine(t)
	engine.SetIntentGate(&fakeGate{verdict: intentgate.Verdict{Action: intentgate.ActionAllow}})
	writer := &fakeVerdictWriter{}
	engine.SetIntentVerdictWriter(writer)

	runGatedToolCall(engine)

	if *executed != 1 {
		t.Fatalf("allow must execute, executed=%d", *executed)
	}
	records := writer.written()
	if len(records) != 1 {
		t.Fatalf("writer must receive exactly 1 record, got %d", len(records))
	}
	rec := records[0]
	if rec.Verdict != types.VerdictActionAllow {
		t.Fatalf("verdict = %q, want allow", rec.Verdict)
	}
	if rec.Layer != types.VerdictLayerBaseline {
		t.Fatalf("layer = %q, want baseline（无策略命中的兜底判定）", rec.Layer)
	}
	if rec.PolicyID != nil {
		t.Fatalf("policy_id = %v, want NULL", *rec.PolicyID)
	}
}

// TestIntentGatePolicyVerdictCarriesVersionAndMode 验收 [unit]（issue #13）：
// 策略驱动的 verdict 落库时 policy_id / policy_version / mode_at_decision
// 全部来自策略（不再是 spike 阶段的硬编码 observe 与空版本）；
// mode=observe 的 deny 只记录不拦截——工具照常执行成功。
func TestIntentGatePolicyVerdictCarriesVersionAndMode(t *testing.T) {
	engine, executed := intentGateTestEngine(t)
	engine.SetIntentGate(&fakeGate{verdict: intentgate.Verdict{
		Action:        intentgate.ActionDeny,
		PolicyID:      "pol-t23",
		PolicyVersion: 3,
		Mode:          types.VerdictModeObserve,
		Reason:        "违反策略约束「单笔退款不得超过 75」",
		Layer:         intentgate.LayerRule,
	}})
	writer := &fakeVerdictWriter{}
	engine.SetIntentVerdictWriter(writer)

	toolCall := runGatedToolCall(engine)

	// observe 语义：只记录不拦截，工具照常执行成功。
	if *executed != 1 || toolCall.Result == nil || !toolCall.Result.Success {
		t.Fatalf("observe-mode policy deny must not block execution: executed=%d result=%+v",
			*executed, toolCall.Result)
	}
	records := writer.written()
	if len(records) != 1 {
		t.Fatalf("writer must receive exactly 1 record, got %d", len(records))
	}
	rec := records[0]
	if rec.PolicyID == nil || *rec.PolicyID != "pol-t23" {
		t.Fatalf("policy_id = %v, want pol-t23", rec.PolicyID)
	}
	if rec.PolicyVersion == nil || *rec.PolicyVersion != 3 {
		t.Fatalf("policy_version = %v, want 3", rec.PolicyVersion)
	}
	if rec.ModeAtDecision != types.VerdictModeObserve {
		t.Fatalf("mode_at_decision = %q, want observe", rec.ModeAtDecision)
	}
	if rec.Layer != types.VerdictLayerRule {
		t.Fatalf("layer = %q, want rule", rec.Layer)
	}
}

// TestIntentGatePersistFailureKeepsToolCallAlive 验收 [unit]：落库失败
// 不影响 verdict 返回与工具执行（fail-open 于观测面）——用真
// AsyncVerdictWriter + 必失败的 repo，走完整异步入队路径。
func TestIntentGatePersistFailureKeepsToolCallAlive(t *testing.T) {
	engine, executed := intentGateTestEngine(t)
	engine.SetIntentGate(&fakeGate{verdict: intentgate.Verdict{
		Action: intentgate.ActionDeny, Layer: intentgate.LayerRule, Reason: "r",
	}})
	failingRepo := &alwaysFailVerdictRepo{err: errors.New("db is down")}
	writer := intentgate.NewAsyncVerdictWriter(failingRepo)
	engine.SetIntentVerdictWriter(writer)

	toolCall := runGatedToolCall(engine)

	if *executed != 1 || toolCall.Result == nil || !toolCall.Result.Success {
		t.Fatalf("persist failure must not affect the tool call: executed=%d result=%+v",
			*executed, toolCall.Result)
	}
	ctx := context.Background()
	if err := writer.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if writer.Failed() != 1 || writer.Written() != 0 {
		t.Fatalf("counters written=%d failed=%d, want 0/1", writer.Written(), writer.Failed())
	}
}

// alwaysFailVerdictRepo 是 Create 必失败的 repository 桩。
func (*alwaysFailVerdictRepo) CountByVerdictGrouped(context.Context, uint64, string) (map[string]int64, error) {
	return nil, nil
}
func (*alwaysFailVerdictRepo) ListByTenant(context.Context, uint64, int) ([]*types.VerdictRecord, error) {
	return nil, nil
}
func (*alwaysFailVerdictRepo) UpdateHumanOverrideByToolCallID(context.Context, uint64, string, string) error {
	return nil
}

type alwaysFailVerdictRepo struct{ err error }

func (r *alwaysFailVerdictRepo) Create(context.Context, *types.VerdictRecord) error { return r.err }
func (r *alwaysFailVerdictRepo) GetByID(context.Context, uint64, string) (*types.VerdictRecord, error) {
	return nil, r.err
}
func (r *alwaysFailVerdictRepo) ListBySession(context.Context, uint64, string, int) ([]*types.VerdictRecord, error) {
	return nil, r.err
}
func (r *alwaysFailVerdictRepo) ListByPolicy(context.Context, uint64, string, int) ([]*types.VerdictRecord, error) {
	return nil, r.err
}
func (r *alwaysFailVerdictRepo) UpdateHumanOverride(context.Context, uint64, string, string) error {
	return r.err
}
func (r *alwaysFailVerdictRepo) Delete(context.Context, uint64, string) error { return r.err }

// TestIntentGateErrorSkipsWriter 验收：Evaluate 出错（fail-open）时没有
// verdict 可记，writer 不得被调用。
func TestIntentGateErrorSkipsWriter(t *testing.T) {
	engine, executed := intentGateTestEngine(t)
	engine.SetIntentGate(&fakeGate{err: errors.New("policy store unavailable")})
	writer := &fakeVerdictWriter{}
	engine.SetIntentVerdictWriter(writer)

	toolCall := runGatedToolCall(engine)

	if *executed != 1 || toolCall.Result == nil || !toolCall.Result.Success {
		t.Fatalf("gate error must fail open, executed=%d", *executed)
	}
	if got := len(writer.written()); got != 0 {
		t.Fatalf("no verdict on gate error => no record, writer got %d", got)
	}
}

// TestIntentGateNilWriterKeepsBehavior 验收：装了 Gate 但没装 writer 时
// 行为与 T02 接缝一致（观测面是可选件）。
func TestIntentGateNilWriterKeepsBehavior(t *testing.T) {
	engine, executed := intentGateTestEngine(t)
	engine.SetIntentGate(&fakeGate{verdict: intentgate.Verdict{Action: intentgate.ActionAllow}})

	toolCall := runGatedToolCall(engine)

	if *executed != 1 || toolCall.Result == nil || !toolCall.Result.Success {
		t.Fatalf("nil writer must keep behavior, executed=%d", *executed)
	}
}

// verdictHarnessDB 打开 harness 用的 DB：默认 sqlite 临时文件（lite
// 路径，零依赖）；设 INTENTGATE_PG_DSN 时走 postgres（[cli] 验收可用
// docker exec psql 独立复核）。返回 DB 与"本次 harness 启动时的行数"。
func verdictHarnessDB(t *testing.T) (*gorm.DB, int64) {
	t.Helper()
	var db *gorm.DB
	var err error
	if dsn := os.Getenv("INTENTGATE_PG_DSN"); dsn != "" {
		db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
		t.Logf("[cli-check] using postgres: INTENTGATE_PG_DSN is set")
	} else {
		dsn := "file:" + filepath.Join(t.TempDir(), "verdicts.db")
		db, err = gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	}
	if err != nil {
		t.Fatalf("open harness db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&types.VerdictRecord{}); err != nil {
		t.Fatalf("migrate intent_verdicts: %v", err)
	}
	var before int64
	if err := db.Raw("SELECT count(*) FROM intent_verdicts").Scan(&before).Error; err != nil {
		t.Fatalf("count before: %v", err)
	}
	return db, before
}

// TestVerdictWriterPersistenceHarness 是 [cli] 验收的驱动入口：真
// SpikeGate（非 fake）+ 真 AsyncVerdictWriter + 真 IntentVerdictRepository，
// 各触发一次 deny 与 allow 判定，随后断言并用 SQL 打印
// `SELECT count(*) FROM intent_verdicts` 的前后值与 mode_at_decision：
//
//	go test ./internal/agent/ -run TestVerdictWriterPersistenceHarness -v | grep 'cli-check'
//
// 设 INTENTGATE_PG_DSN 指向 dev postgres 后，可用 docker exec psql 独立复核：
//
//	docker exec WeKnora-postgres-dev psql -U postgres -d WeKnora \
//	  -c "SELECT count(*) FROM intent_verdicts"
//
// 双向断言：deny（shell_exec rm -rf ~ 无删除意图）与 allow（普通检索）
// 两个方向的调用都必须执行成功（observe 不拦截），且各落一行 verdict。
func TestVerdictWriterPersistenceHarness(t *testing.T) {
	db, before := verdictHarnessDB(t)
	repo := repository.NewIntentVerdictRepository(db)
	var _ interfaces.IntentVerdictRepository = repo
	writer := intentgate.NewAsyncVerdictWriter(repo)

	engine := newTestEngine(t, &mockChat{})
	engine.toolRegistry = tools.NewToolRegistry()
	succeed := func(context.Context) *types.ToolResult { return &types.ToolResult{Success: true} }
	engine.toolRegistry.RegisterTool(&orderedTestTool{
		BaseTool: tools.NewBaseTool("shell_exec", "", json.RawMessage(`{"type":"object"}`)),
		run:      succeed,
	})
	engine.toolRegistry.RegisterTool(&orderedTestTool{
		BaseTool: tools.NewBaseTool("search_knowledge", "", json.RawMessage(`{"type":"object"}`)),
		run:      succeed,
	})
	engine.SetIntentGate(intentgate.NewSpikeGate())
	engine.SetIntentVerdictWriter(writer)

	sessionID := "cli-persist-" + uuid.NewString()[:8]

	// deny 方向：spike 规则 3（rm -rf ~ 而会话无删除意图）。
	denyCall := engine.runToolCall(context.Background(), types.LLMToolCall{
		ID:       "cli-deny-1",
		Function: types.FunctionCall{Name: "shell_exec", Arguments: `{"command":"rm -rf ~"}`},
	}, 0, 0, 1, sessionID, "msg-1")
	if denyCall.Result == nil || !denyCall.Result.Success {
		t.Fatalf("observe deny must not block execution, got %+v", denyCall.Result)
	}

	// allow 方向：普通检索不命中任何 spike 规则。
	allowCall := engine.runToolCall(context.Background(), types.LLMToolCall{
		ID:       "cli-allow-1",
		Function: types.FunctionCall{Name: "search_knowledge", Arguments: `{"query":"活动方案"}`},
	}, 1, 0, 1, sessionID, "msg-1")
	if allowCall.Result == nil || !allowCall.Result.Success {
		t.Fatalf("allow verdict must execute successfully, got %+v", allowCall.Result)
	}

	if err := writer.Close(context.Background()); err != nil {
		t.Fatalf("writer Close: %v", err)
	}
	if writer.Written() != 2 || writer.Dropped() != 0 || writer.Failed() != 0 {
		t.Fatalf("counters written=%d dropped=%d failed=%d, want 2/0/0",
			writer.Written(), writer.Dropped(), writer.Failed())
	}

	var after int64
	if err := db.Raw("SELECT count(*) FROM intent_verdicts").Scan(&after).Error; err != nil {
		t.Fatalf("count after: %v", err)
	}
	t.Logf("[cli-check] SELECT count(*) FROM intent_verdicts -> before=%d after=%d", before, after)
	if after-before != 2 {
		t.Fatalf("intent_verdicts must grow by 2, before=%d after=%d", before, after)
	}

	type row struct {
		ToolName       string  `gorm:"column:tool_name"`
		Verdict        string  `gorm:"column:verdict"`
		Layer          string  `gorm:"column:layer"`
		ModeAtDecision string  `gorm:"column:mode_at_decision"`
		PolicyID       *string `gorm:"column:policy_id"`
	}
	var rows []row
	if err := db.Raw(
		"SELECT tool_name, verdict, layer, mode_at_decision, policy_id FROM intent_verdicts WHERE session_id = ? ORDER BY created_at",
		sessionID).Scan(&rows).Error; err != nil {
		t.Fatalf("select rows: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("session must have 2 verdict rows, got %d", len(rows))
	}
	for _, r := range rows {
		t.Logf("[cli-check] tool=%s verdict=%s layer=%s mode_at_decision=%s",
			r.ToolName, r.Verdict, r.Layer, r.ModeAtDecision)
		if r.ModeAtDecision != types.VerdictModeObserve {
			t.Fatalf("mode_at_decision = %q, want observe（spike 阶段）", r.ModeAtDecision)
		}
	}
	byTool := map[string]row{}
	for _, r := range rows {
		byTool[r.ToolName] = r
	}
	if r := byTool["shell_exec"]; r.Verdict != types.VerdictActionDeny || r.Layer != types.VerdictLayerRule {
		t.Fatalf("shell_exec row = %+v, want deny/rule", r)
	}
	if r := byTool["search_knowledge"]; r.Verdict != types.VerdictActionAllow || r.Layer != types.VerdictLayerBaseline {
		t.Fatalf("search_knowledge row = %+v, want allow/baseline", r)
	}
	fmt.Printf("[cli-check] session=%s before=%d after=%d mode_at_decision=observe\n", sessionID, before, after)
}
