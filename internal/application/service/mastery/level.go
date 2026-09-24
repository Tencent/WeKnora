package mastery

import (
	"math"
	"time"
)

// ViewSlice is one calendar day of view evidence for a node. The aggregate keeps
// the lumped count/duration for recency and the saturation verdict, and these
// slices for decay.
//
// Why both: a lumped total carries a single "last viewed" timestamp, so a
// five-second glance today refreshes a month of accumulated reading — the same
// overstatement per-signal decay exists to remove, one level further down.
// Slicing by day makes each day's reading pay its own decay.
type ViewSlice struct {
	Day      time.Time // the bucket's calendar day (server-local midnight)
	Views    int       // counted views that day (the dedup window's folding applied)
	Duration int64     // effective seconds that day (never folded)
}

// SpreadSlice is one calendar day of neighbour-spread credit for a node: seconds
// of reading time that leaked in from adjacent pages. Like view evidence it is
// kept per day so each day pays its own decay, and a day-multiple never lets a
// fresh neighbour visit refresh a month of warmth.
type SpreadSlice struct {
	Day     time.Time
	Seconds int64 // credited source seconds (undiscounted; SpreadFactor applies at read time)
}

// Evidence is the per-node aggregation of the three behavior signals. Each
// signal carries its own recency so they decay independently: a whole-score
// decay would let "one faint visit today" refresh "three citations from a month
// ago", overstating how fresh the node really is.
type Evidence struct {
	Citations    int       // citation count (capped by CitationCap)
	LastCitedAt  time.Time // most recent citation
	Views        int       // effective view count
	Duration     int64     // cumulative effective viewing seconds
	LastViewedAt time.Time // most recent effective view
	Likes        float64   // like credit (sum of credited allocations, kept fractional)
	LastLikedAt  time.Time // most recent like
	// ViewSlices is the per-day breakdown of the view signal, used for decay.
	// Empty means "this node has no daily buckets", and Level falls back to
	// decaying the lumped row by LastViewedAt.
	ViewSlices []ViewSlice
	// SpreadSlices is the per-day neighbour warmth credited by reading adjacent
	// pages. It is never lumped (there is no fallback row), so an empty slice list
	// simply means "no neighbour activity around this node".
	SpreadSlices []SpreadSlice
}

// LastActive returns the most recent interaction across all three signals. It
// drives the "recently active" ripple and the profile display; it no longer
// drives the decay — each signal now decays on its own clock.
func (e Evidence) LastActive() time.Time {
	latest := e.LastCitedAt
	if e.LastViewedAt.After(latest) {
		latest = e.LastViewedAt
	}
	if e.LastLikedAt.After(latest) {
		latest = e.LastLikedAt
	}
	return latest
}

// Level maps an evidence aggregate onto the 0..100 water level (ten tiers):
//
//	0           no evidence
//	10/20/30    contact
//	40/50/60    familiar
//	70/80/90    mastered (high-evidence active)
//	100         evidence saturated
//
// The score is the sum of capped citation, view and like contributions, each
// decayed on its own clock, then mapped onto tiers with a linear subdivision
// inside each tier. This is deliberately a deterministic, pure function (no
// model, no hidden state) so it can be unit-tested and replayed.
func Level(cfg Config, e Evidence, now time.Time) int {
	if e.Citations == 0 && e.Views == 0 && e.Likes == 0 &&
		len(e.ViewSlices) == 0 && len(e.SpreadSlices) == 0 {
		return 0
	}

	cite := math.Min(float64(e.Citations), cfg.CitationCap) * cfg.CitationWeight
	view := e.rawViewScore(cfg)
	like := e.Likes * cfg.LikeWeight

	// Saturation is one-way: once a node has accumulated enough evidence it
	// stays filled. Letting it decay would demote a page the user genuinely
	// studied just because they were busy for a while, which reads as
	// "what I learned did not count".
	//
	// The test runs on the un-decayed total and counts own evidence only: neighbour
	// warmth may lift a node, but it must never be able to declare one saturated.
	if cite+view+like >= cfg.SatScore {
		return 100
	}

	score := cite*decayFactor(cfg, now, e.LastCitedAt) +
		e.decayedViewScore(cfg, now) +
		like*decayFactor(cfg, now, e.LastLikedAt) +
		e.decayedSpreadScore(cfg, now)

	level := scoreToLevel(cfg, score)
	// 100 is reserved for the person's own evidence (the short-circuit above).
	// Borrowed warmth can carry a node past the saturation line when the person's
	// own evidence is nearly there, but it can neither saturate a node by itself
	// nor, alone, lift one out of the familiar tier (see decayedSpreadScore's cap).
	if level == 100 {
		level = 90
	}
	return level
}

// decayedSpreadScore is the neighbour-warmth contribution: reading time that
// leaked in from adjacent pages, decayed per day like the view signal and capped
// so a node surrounded by activity cannot look mastered on borrowed evidence
// alone.
//
// The discount runs through DurationWeight, i.e. it reads as "20% of what this
// reading time would have been worth on the page itself". Deliberate, but a real
// coupling: retuning DurationWeight rescales neighbour warmth with it, so the two
// cannot be tuned independently — change SpreadFactor to rebalance warmth alone.
//
// With the default cap, warmth alone tops out at 50%: a node can look "familiar"
// because its neighbours are being read, never "mastered".
func (e Evidence) decayedSpreadScore(cfg Config, now time.Time) float64 {
	if len(e.SpreadSlices) == 0 || cfg.SpreadFactor <= 0 {
		return 0
	}
	var score float64
	for _, s := range e.SpreadSlices {
		score += float64(s.Seconds) * cfg.DurationWeight * cfg.SpreadFactor * decayFactor(cfg, now, s.Day)
	}
	if cfg.SpreadCap > 0 && score > cfg.SpreadCap {
		return cfg.SpreadCap
	}
	return score
}

// rawViewScore is the un-decayed view contribution: the per-day slices when the
// aggregate has them, otherwise the lumped row.
//
// Both are totals of the same events — BumpPageView writes the bucket on every
// view and folds the count exactly like view_count does — so preferring the
// slices does not change the saturation verdict for normal data. It keeps one
// source of truth, and keeps a node whose lumped row went missing from silently
// scoring as "no evidence".
func (e Evidence) rawViewScore(cfg Config) float64 {
	if len(e.ViewSlices) == 0 {
		return viewScore(cfg, e.Views, e.Duration)
	}
	var score float64
	for _, s := range e.ViewSlices {
		score += viewScore(cfg, s.Views, s.Duration)
	}
	return score
}

// decayedViewScore applies decay to view evidence, per day when slices exist.
//
// Decaying the lumped total by LastViewedAt would let today's five-second glance
// refresh a month of accumulated reading, because the lump carries one timestamp
// for all of it. Per-day slices make each day's reading pay its own decay, so
// "read carefully a month ago, glanced at today" stops looking like "read
// carefully today".
//
// With no slices we fall back to the lumped row (history written before the
// daily buckets existed, or a KB whose buckets were never written), which is the
// pre-slice behaviour.
//
// 已知取舍：只要有切片，汇总行就完全不参与评分（仅在零切片时回落）。两侧同源
// ——BumpPageView 在同一次写入里同时更新汇总行与日桶——所以正常数据下两种读法
// 结果一致。但若哪天日桶被单独删除或只丢了一部分，汇总行里的那段历史会被**静默
// 忽略**而不是报错。这里刻意选「桶为准」这条规则，而不是让两个数据源互相补偿：
// 补偿会把数据裂痕掩盖过去，桶为准至少能让裂痕表现为水位偏低这种可察觉的现象。
func (e Evidence) decayedViewScore(cfg Config, now time.Time) float64 {
	if len(e.ViewSlices) == 0 {
		return viewScore(cfg, e.Views, e.Duration) * decayFactor(cfg, now, e.LastViewedAt)
	}
	var score float64
	for _, s := range e.ViewSlices {
		score += viewScore(cfg, s.Views, s.Duration) * decayFactor(cfg, now, s.Day)
	}
	return score
}

// viewScore is one bucket's un-decayed view contribution, shared by the lumped
// row and the per-day slices so the two paths cannot drift apart.
func viewScore(cfg Config, views int, duration int64) float64 {
	return float64(views)*cfg.ViewWeight + float64(duration)*cfg.DurationWeight
}

// decayFactor returns the decay multiplier for one signal. It is floored at
// cfg.DecayFloor so a dormant node keeps a visible trace of what was learned
// instead of collapsing back to "never seen".
func decayFactor(cfg Config, now, last time.Time) float64 {
	if last.IsZero() {
		// 时间戳缺失（脏数据 / 早期行）按最古老处理：一条没有时间的证据
		// 不应该因此永久保鲜，宁可让它在下限处失效。
		return cfg.DecayFloor
	}
	days := now.Sub(last).Hours() / 24
	if days <= 0 {
		return 1
	}
	factor := math.Exp(-days / cfg.HalfLifeDays)
	if factor < cfg.DecayFloor {
		return cfg.DecayFloor
	}
	return factor
}

// scoreToLevel maps a combined score onto the ten tiers, subdividing each tier
// linearly so the three state tiers each contain three internal levels.
//
// The SatScore branch is unreachable from Level, which short-circuits on the
// un-decayed total first and decay can only lower a score. It is kept so the
// mapping stays total for any caller and so the meaning of 100 is stated where
// the tiers are defined.
func scoreToLevel(cfg Config, score float64) int {
	switch {
	case score >= cfg.SatScore:
		return 100
	case score >= cfg.MasteredScore:
		return subdivide(cfg.MasteredScore, cfg.SatScore, 70, score)
	case score >= cfg.FamiliarScore:
		return subdivide(cfg.FamiliarScore, cfg.MasteredScore, 40, score)
	case score >= cfg.TouchScore:
		return subdivide(cfg.TouchScore, cfg.FamiliarScore, 10, score)
	default:
		return 10
	}
}

// subdivide maps score inside [lo, hi) onto water levels [base, base+20),
// rounding to the nearest ten.
func subdivide(lo, hi, base float64, score float64) int {
	span := hi - lo
	if span <= 0 {
		return int(base)
	}
	ratio := (score - lo) / span
	if ratio > 1 {
		ratio = 1
	}
	level := base + ratio*20
	return int(math.Round(level/10) * 10)
}
