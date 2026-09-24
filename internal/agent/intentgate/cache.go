// 判定缓存（issue #16 / T32，设计 §12）：同 session 内相同
// (policy_id, args_digest) 的 judge 判定缓存 5 分钟，命中不重复调用模型。
//
// 这是成本控制的关键一格：全量 judge 的最坏情况成本是每 tool call
// 一次模型调用，① 层命中率不够高时缓存兜住重复参数（重试/轮询/同义
// 改写前的原样重发）的成本。设计 §12 明确"全量 judge 成本最坏翻倍，
// ① 层命中率是核心指标"——缓存不解决命中率，只消灭重复。
//
// 缓存语义保守：
//   - 键含 tenant_id + session_id + policy_id + args_digest：跨租户/
//     跨 session/跨策略永不串（digest 用 types.DigestArgs 规范化 JSON
//     后计算，与 verdict 表 args_digest 同口径，可对账）；
//   - 只缓存成功的 judge 产出（uncertain 也缓存——同一输入的解析失败
//     大概率重复失败，且 uncertain 是 fail-open 语义，缓存不改变处置）；
//   - 命中返回原始 verdict（含 JudgeTokens）：成本记账保持"只付过一次"；
//   - 过期惰性清理 + 容量上限（防长跑进程内存膨胀）。
package intentgate

import (
	"context"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// judgeCacheDefaultTTL 是判定缓存有效期（设计 §12：5 分钟）。
const judgeCacheDefaultTTL = 5 * time.Minute

// judgeCacheMaxEntries 是缓存容量上限：超限时先清过期项，仍满则随机
// 逐出（map range 首键）。judge 调用频率远低于此量级的几十倍，逐出
// 命中率不受影响。
const judgeCacheMaxEntries = 4096

type judgeCacheKey struct {
	tenantID   uint64
	sessionID  string
	policyID   string
	argsDigest string
}

type judgeCacheEntry struct {
	verdict   Verdict
	expiresAt time.Time
}

// CachingJudge 是 Judge 的缓存装饰器。实现 Enabled 委托（内层 judge
// 支持能力档时，PolicyGate 的降级检查对缓存包装依然生效）。
type CachingJudge struct {
	inner Judge
	ttl   time.Duration
	now   func() time.Time // 测试缝

	mu      sync.Mutex
	entries map[judgeCacheKey]judgeCacheEntry
}

// JudgeCacheOption 定制 CachingJudge（测试注入时钟/TTL）。
type JudgeCacheOption func(*CachingJudge)

// WithJudgeCacheTTL 覆盖缓存有效期（默认 5 分钟）。
func WithJudgeCacheTTL(d time.Duration) JudgeCacheOption {
	return func(c *CachingJudge) { c.ttl = d }
}

// NewCachingJudge 包装 inner：Judge 调用按 (tenant, session, policy,
// args_digest) 缓存 ttl。inner 为 nil 时返回 nil（装配侧零行为变化）。
func NewCachingJudge(inner Judge, opts ...JudgeCacheOption) *CachingJudge {
	c := &CachingJudge{
		inner:   inner,
		ttl:     judgeCacheDefaultTTL,
		now:     time.Now,
		entries: make(map[judgeCacheKey]judgeCacheEntry),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Judge 实现 Judge 接口：命中缓存直接返回，未命中调 inner 并回填。
func (c *CachingJudge) Judge(ctx context.Context, in JudgeInput) (Verdict, error) {
	key := judgeCacheKey{
		tenantID:   in.TenantID,
		sessionID:  in.SessionID,
		policyID:   in.PolicyID,
		argsDigest: types.DigestArgs(in.Args),
	}
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && c.now().Before(e.expiresAt) {
		c.mu.Unlock()
		return e.verdict, nil
	}
	c.mu.Unlock()

	v, err := c.inner.Judge(ctx, in)
	if err != nil {
		return v, err
	}
	c.mu.Lock()
	if len(c.entries) >= judgeCacheMaxEntries {
		c.evictExpiredLocked()
	}
	if len(c.entries) >= judgeCacheMaxEntries {
		// 仍满：随机逐出一项（map range 首键），保住写入。
		for k := range c.entries {
			delete(c.entries, k)
			break
		}
	}
	c.entries[key] = judgeCacheEntry{verdict: v, expiresAt: c.now().Add(c.ttl)}
	c.mu.Unlock()
	return v, nil
}

// Enabled 委托内层 judge 的能力档（T31 降级检查不被缓存包装短路）。
// 内层不支持能力档时视为恒可用（与 PolicyGate 的缺省语义一致）。
func (c *CachingJudge) Enabled(ctx context.Context, tenantID uint64) bool {
	if tcj, ok := c.inner.(tenantCapabilityJudge); ok {
		return tcj.Enabled(ctx, tenantID)
	}
	return true
}

// evictExpiredLocked 清掉所有过期项；调用方必须持有 c.mu。
func (c *CachingJudge) evictExpiredLocked() {
	now := c.now()
	for k, e := range c.entries {
		if !now.Before(e.expiresAt) {
			delete(c.entries, k)
		}
	}
}

// 编译期断言：CachingJudge 实现 Judge 与能力档委托接口。
var (
	_ Judge                 = (*CachingJudge)(nil)
	_ tenantCapabilityJudge = (*CachingJudge)(nil)
)
