package intentgate

import (
	"bytes"
	"context"
	"errors"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// fakeVerdictRepo 是 writer 测试的测试缝：实现
// interfaces.IntentVerdictRepository，Create 行为可编程
// （报错 / 延迟 / 阻塞），并记录收到的记录供断言。
type fakeVerdictRepo struct {
	mu      sync.Mutex
	created []*types.VerdictRecord
	err     error
	delay   time.Duration

	// block 非空时 Create 阻塞直至该 channel 关闭（模拟 DB hang）。
	block chan struct{}
	// entered 在 Create 进入时收到一个信号（用于确定 in-flight 状态）。
	entered chan struct{}
}

func (*fakeVerdictRepo) CountByVerdictGrouped(context.Context, uint64, string) (map[string]int64, error) {
	return nil, nil
}
func (*fakeVerdictRepo) ListByTenant(context.Context, uint64, int) ([]*types.VerdictRecord, error) {
	return nil, nil
}

func (f *fakeVerdictRepo) Create(_ context.Context, rec *types.VerdictRecord) error {
	if f.entered != nil {
		select {
		case f.entered <- struct{}{}:
		default:
		}
	}
	if f.block != nil {
		<-f.block
	}
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.created = append(f.created, rec)
	return nil
}

func (f *fakeVerdictRepo) createdRecords() []*types.VerdictRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*types.VerdictRecord(nil), f.created...)
}

// 其余接口方法本票用不到，留桩。
func (f *fakeVerdictRepo) GetByID(context.Context, uint64, string) (*types.VerdictRecord, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeVerdictRepo) ListBySession(context.Context, uint64, string, int) ([]*types.VerdictRecord, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeVerdictRepo) ListByPolicy(context.Context, uint64, string, int) ([]*types.VerdictRecord, error) {
	return nil, errors.New("not implemented")
}
func (f *fakeVerdictRepo) UpdateHumanOverride(context.Context, uint64, string, string) error {
	return errors.New("not implemented")
}
func (f *fakeVerdictRepo) Delete(context.Context, uint64, string) error {
	return errors.New("not implemented")
}

func mustRecord(t *testing.T, sessionID, verdict string) *types.VerdictRecord {
	t.Helper()
	rec, err := types.NewVerdictRecord(types.VerdictRecordInput{
		TenantID: 1, SessionID: sessionID, ToolCallID: "call-1",
		ToolName: "web_search", Layer: types.VerdictLayerRule,
		Verdict: verdict, ModeAtDecision: types.VerdictModeObserve,
	})
	if err != nil {
		t.Fatalf("NewVerdictRecord: %v", err)
	}
	return rec
}

func closeWriter(t *testing.T, w *AsyncVerdictWriter) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := w.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestAsyncVerdictWriterPersistsInOrder 验收：写入的记录全部落库且保持顺序。
func TestAsyncVerdictWriterPersistsInOrder(t *testing.T) {
	repo := &fakeVerdictRepo{}
	w := NewAsyncVerdictWriter(repo)
	for _, id := range []string{"s1", "s2", "s3"} {
		w.Write(mustRecord(t, id, types.VerdictActionAllow))
	}
	closeWriter(t, w)

	got := repo.createdRecords()
	if len(got) != 3 {
		t.Fatalf("persisted %d records, want 3", len(got))
	}
	for i, id := range []string{"s1", "s2", "s3"} {
		if got[i].SessionID != id {
			t.Fatalf("record %d session_id = %q, want %q（顺序必须保持）", i, got[i].SessionID, id)
		}
	}
	if w.Written() != 3 || w.Dropped() != 0 || w.Failed() != 0 {
		t.Fatalf("counters written=%d dropped=%d failed=%d, want 3/0/0",
			w.Written(), w.Dropped(), w.Failed())
	}
}

// TestAsyncVerdictWriterRepoErrorFailsOpen 验收 [unit]：落库失败不影响
// 判定链路——Write 不返回错误、不 panic、不阻塞；worker 不因单次失败退出，
// 后续写入仍被处理；失败计入 Failed 并记 warn 日志。
func TestAsyncVerdictWriterRepoErrorFailsOpen(t *testing.T) {
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	defer logger.SetOutput(os.Stdout)

	repo := &fakeVerdictRepo{err: errors.New("db is down")}
	w := NewAsyncVerdictWriter(repo)

	done := make(chan struct{})
	go func() {
		w.Write(mustRecord(t, "s1", types.VerdictActionDeny))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Write must not block even when the repository is failing")
	}

	closeWriter(t, w)
	if w.Failed() != 1 || w.Written() != 0 {
		t.Fatalf("counters written=%d failed=%d, want 0/1", w.Written(), w.Failed())
	}
	if !bytes.Contains(buf.Bytes(), []byte("db is down")) {
		t.Fatalf("repo error must be logged, got:\n%s", buf.String())
	}
	// worker 存活：恢复后的写入仍应成功。
	repo.mu.Lock()
	repo.err = nil
	repo.mu.Unlock()
	w2 := NewAsyncVerdictWriter(repo)
	w2.Write(mustRecord(t, "s2", types.VerdictActionAllow))
	closeWriter(t, w2)
	if len(repo.createdRecords()) != 1 {
		t.Fatalf("repo must still accept writes after a failure, got %d", len(repo.createdRecords()))
	}
}

// TestAsyncVerdictWriterFullQueueDropsWithoutBlocking 验收：DB hang 时
// 队列满即丢弃并 warn，Write 永不阻塞工具调用热路径。
func TestAsyncVerdictWriterFullQueueDropsWithoutBlocking(t *testing.T) {
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	defer logger.SetOutput(os.Stdout)

	block := make(chan struct{})
	repo := &fakeVerdictRepo{block: block, entered: make(chan struct{}, 1)}
	w := NewAsyncVerdictWriter(repo, WithQueueSize(2))

	// 等 worker 拿起第一条并阻塞在 Create 里，此后队列状态确定：
	// in-flight 1 条 + 队列 2 条，其余全部丢弃。
	w.Write(mustRecord(t, "s0", types.VerdictActionAllow))
	<-repo.entered

	const total = 50
	start := time.Now()
	for i := 0; i < total-1; i++ {
		w.Write(mustRecord(t, "sx", types.VerdictActionAllow))
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("49 Writes against a hung DB took %v; Write must never block", elapsed)
	}
	// in-flight(1) + queued(2) = 3 条被接收，47 条丢弃。
	if got := w.Dropped(); got != int64(total-3) {
		t.Fatalf("dropped = %d, want %d", got, total-3)
	}
	if !bytes.Contains(buf.Bytes(), []byte("queue full")) {
		t.Fatalf("queue-full drop must be logged, got:\n%s", buf.String())
	}

	close(block)
	closeWriter(t, w)
	if w.Written() != 3 {
		t.Fatalf("written = %d, want 3（in-flight + queued 在 Close 时排空）", w.Written())
	}
}

// TestAsyncVerdictWriterCloseDrains 验收：Close 排空队列里未写完的记录。
func TestAsyncVerdictWriterCloseDrains(t *testing.T) {
	repo := &fakeVerdictRepo{delay: 5 * time.Millisecond}
	w := NewAsyncVerdictWriter(repo)
	const total = 20
	for i := 0; i < total; i++ {
		w.Write(mustRecord(t, "s", types.VerdictActionAllow))
	}
	closeWriter(t, w)
	if got := len(repo.createdRecords()); got != total {
		t.Fatalf("Close must drain pending records: persisted %d, want %d", got, total)
	}
}

// TestAsyncVerdictWriterCloseHonorsContext 验收：DB 挂死时 Close 受 ctx
// 约束返回错误，而不是无限等待。
func TestAsyncVerdictWriterCloseHonorsContext(t *testing.T) {
	block := make(chan struct{})
	defer close(block) // 放行 worker，避免 goroutine 泄漏
	repo := &fakeVerdictRepo{block: block}
	w := NewAsyncVerdictWriter(repo)
	w.Write(mustRecord(t, "s", types.VerdictActionAllow))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := w.Close(ctx); err == nil {
		t.Fatal("Close against a hung DB must return the ctx error")
	}
}

// TestAsyncVerdictWriterWriteAfterCloseDrops 验收：Close 后的 Write 是
// 安全的 no-op（计 Dropped，不 panic）。
func TestAsyncVerdictWriterWriteAfterCloseDrops(t *testing.T) {
	repo := &fakeVerdictRepo{}
	w := NewAsyncVerdictWriter(repo)
	closeWriter(t, w)
	w.Write(mustRecord(t, "s", types.VerdictActionAllow))
	if w.Dropped() != 1 {
		t.Fatalf("write after close must be counted as dropped, dropped=%d", w.Dropped())
	}
	if len(repo.createdRecords()) != 0 {
		t.Fatal("write after close must not reach the repository")
	}
}

// TestAsyncVerdictWriterWriteLatencyP99 验收 [unit]：异步写入 p99 不增加
// 工具调用延迟——repo 每次写 20ms（慢 DB），Write 的 p99 仍须远低于它；
// 同负载下同步写入的 p99 必然 ≥20ms，作为对照。
func TestAsyncVerdictWriterWriteLatencyP99(t *testing.T) {
	const repoLatency = 10 * time.Millisecond
	const n = 150

	p99 := func(ds []time.Duration) time.Duration {
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		return ds[int(float64(len(ds))*0.99)-1]
	}

	// 异步：Write 只是把记录放进队列。
	repo := &fakeVerdictRepo{delay: repoLatency}
	w := NewAsyncVerdictWriter(repo, WithQueueSize(n*2))
	asyncDs := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		rec := mustRecord(t, "s", types.VerdictActionAllow)
		start := time.Now()
		w.Write(rec)
		asyncDs = append(asyncDs, time.Since(start))
	}
	closeWriter(t, w)

	// 同步对照：直接调 repo。
	syncRepo := &fakeVerdictRepo{delay: repoLatency}
	syncDs := make([]time.Duration, 0, n)
	for i := 0; i < n; i++ {
		rec := mustRecord(t, "s", types.VerdictActionAllow)
		start := time.Now()
		_ = syncRepo.Create(context.Background(), rec)
		syncDs = append(syncDs, time.Since(start))
	}

	asyncP99, syncP99 := p99(asyncDs), p99(syncDs)
	t.Logf("write p99: async=%v sync=%v (repo latency=%v)", asyncP99, syncP99, repoLatency)
	if syncP99 < repoLatency {
		t.Fatalf("sync p99 %v must be >= repo latency %v（对照组 sanity check）", syncP99, repoLatency)
	}
	if asyncP99 > 5*time.Millisecond {
		t.Fatalf("async write p99 %v exceeds 5ms; enqueue must be independent of repo latency %v",
			asyncP99, repoLatency)
	}
}

// BenchmarkAsyncVerdictWriterWrite 基准：慢 repo（1ms/次）下 Write 的
// 单次开销，证明热路径开销与落库耗时无关。
func BenchmarkAsyncVerdictWriterWrite(b *testing.B) {
	repo := &fakeVerdictRepo{delay: time.Millisecond}
	w := NewAsyncVerdictWriter(repo, WithQueueSize(b.N+16))
	rec, err := types.NewVerdictRecord(types.VerdictRecordInput{
		TenantID: 1, SessionID: "bench", ToolName: "web_search",
		Layer: types.VerdictLayerRule, Verdict: types.VerdictActionAllow,
	})
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.Write(rec)
	}
	b.StopTimer()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = w.Close(ctx)
}
