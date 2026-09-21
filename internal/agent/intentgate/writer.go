// VerdictWriter：verdict 的异步落库接缝（设计 §7 Gate.verdicts、§6.2
// intent_verdicts 表）。
//
// 核心约束：工具调用热路径绝不因落库而阻塞或失败。观测面永远
// fail-open（设计 §9「observe 模式永远 fail-open」扩展到观测面本身）：
//   - Write 只做一次非阻塞入队，纳秒级返回；
//   - 队列满（DB hang/慢）→ 丢弃并记 warn，计 Dropped；
//   - repo 报错 → 记 warn，计 Failed，worker 继续处理后续记录；
//   - Close 尽力排空队列，受调用方 ctx 约束。
package intentgate

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// VerdictWriter 是 intent_verdict 落库接缝。实现必须保证 Write 非阻塞、
// 永不返回错误——它是观测面，不是判定链路的一部分。
type VerdictWriter interface {
	// Write 异步入队一条判定记录，立即返回。队列满或已关闭时丢弃
	// （fail-open），调用方无法也无须感知落库结果。
	Write(rec *types.VerdictRecord)
	// Close 停止接收新记录，并尽力在 ctx 期限内排空队列后退出 worker。
	// 返回 ctx 错误表示仍有记录未落库。
	Close(ctx context.Context) error
}

// 默认队列容量与单条写入超时。队列容量按突发流量估：agent 一轮并发
// 工具调用上限是个位数，1024 足以吸收分钟级的 DB 抖动。
const (
	defaultVerdictQueueSize   = 1024
	defaultVerdictWriteTimout = 5 * time.Second
)

// WriterOption 调整 AsyncVerdictWriter 参数（测试用小队列/短超时）。
type WriterOption func(*AsyncVerdictWriter)

// WithQueueSize 覆盖队列容量。
func WithQueueSize(n int) WriterOption {
	return func(w *AsyncVerdictWriter) {
		if n > 0 {
			w.queue = make(chan *types.VerdictRecord, n)
		}
	}
}

// WithWriteTimeout 覆盖单条落库超时。
func WithWriteTimeout(d time.Duration) WriterOption {
	return func(w *AsyncVerdictWriter) {
		if d > 0 {
			w.writeTimeout = d
		}
	}
}

// AsyncVerdictWriter 以单 worker + 缓冲队列异步落库。单 worker 保证
// 写入顺序与判定顺序一致（verdict 表按时间序对账，设计 §3.2）。
type AsyncVerdictWriter struct {
	repo         interfaces.IntentVerdictRepository
	queue        chan *types.VerdictRecord
	writeTimeout time.Duration

	// quit 通知 worker 排空后退出；done 在 worker 退出时关闭。
	quit chan struct{}
	done chan struct{}

	// writeMu 串行化「closed 检查 + 入队」，保证 Close 设置 closed 之前
	// 已通过检查的 Write 一定已经入队，Close 后的 drain 不会漏掉它们。
	writeMu sync.Mutex
	closed  atomic.Bool

	written atomic.Int64
	dropped atomic.Int64
	failed  atomic.Int64
}

// NewAsyncVerdictWriter 创建一个异步 VerdictWriter 并启动 worker。
func NewAsyncVerdictWriter(repo interfaces.IntentVerdictRepository, opts ...WriterOption) *AsyncVerdictWriter {
	w := &AsyncVerdictWriter{
		repo:         repo,
		queue:        make(chan *types.VerdictRecord, defaultVerdictQueueSize),
		writeTimeout: defaultVerdictWriteTimout,
		quit:         make(chan struct{}),
		done:         make(chan struct{}),
	}
	for _, opt := range opts {
		opt(w)
	}
	go w.loop()
	return w
}

// Write 实现 VerdictWriter。nil 记录直接忽略。
func (w *AsyncVerdictWriter) Write(rec *types.VerdictRecord) {
	if rec == nil {
		return
	}
	w.writeMu.Lock()
	defer w.writeMu.Unlock()
	if w.closed.Load() {
		w.dropped.Add(1)
		return
	}
	select {
	case w.queue <- rec:
	default:
		// 队列满 = DB 慢于判定流量。观测面 fail-open：丢一条日志数据
		// 不可接受，阻塞一次工具调用更不可接受。
		w.dropped.Add(1)
		logger.Warnf(context.Background(),
			"[IntentGate] verdict writer queue full (dropped=%d), dropping verdict for session=%s tool=%s",
			w.dropped.Load(), rec.SessionID, rec.ToolName)
	}
}

// Close 实现 VerdictWriter。
func (w *AsyncVerdictWriter) Close(ctx context.Context) error {
	w.writeMu.Lock()
	alreadyClosed := w.closed.Swap(true)
	w.writeMu.Unlock()
	if alreadyClosed {
		return nil
	}
	close(w.quit)
	select {
	case <-w.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Written / Dropped / Failed 是观测计数（供测试与指标断言）。
func (w *AsyncVerdictWriter) Written() int64 { return w.written.Load() }
func (w *AsyncVerdictWriter) Dropped() int64 { return w.dropped.Load() }
func (w *AsyncVerdictWriter) Failed() int64  { return w.failed.Load() }

func (w *AsyncVerdictWriter) loop() {
	defer close(w.done)
	for {
		select {
		case rec := <-w.queue:
			w.persist(rec)
		case <-w.quit:
			w.drain()
			return
		}
	}
}

// drain 在收到 quit 后排空队列。队列中不会再有新记录：Close 在发 quit
// 之前已置 closed，且所有此前通过检查的 Write 都已入队（writeMu 串行化）。
func (w *AsyncVerdictWriter) drain() {
	for {
		select {
		case rec := <-w.queue:
			w.persist(rec)
		default:
			return
		}
	}
}

// persist 用独立于请求的新 ctx 落库：调用方的请求 ctx 可能已取消，
// 观测数据不应随请求生命周期夭折。失败只记日志、计 Failed，永不外传。
func (w *AsyncVerdictWriter) persist(rec *types.VerdictRecord) {
	ctx, cancel := context.WithTimeout(context.Background(), w.writeTimeout)
	defer cancel()
	if err := w.repo.Create(ctx, rec); err != nil {
		w.failed.Add(1)
		logger.Warnf(ctx,
			"[IntentGate] verdict persist failed (fail-open, failed=%d): session=%s tool=%s verdict=%s: %v",
			w.failed.Load(), rec.SessionID, rec.ToolName, rec.Verdict, err)
		return
	}
	w.written.Add(1)
}
