package mastery

import (
	"math"
	"time"
)

// Config holds the tunable thresholds for the water-level computation. Every
// value here is a configurable constant (per the implementation contract); none
// may be hard-coded in call sites.
type Config struct {
	// Citation evidence is the weakest signal (system-side association), so it
	// saturates quickly and is capped.
	CitationCap    float64
	CitationWeight float64

	// View evidence is the most direct signal of active contact.
	ViewWeight     float64
	DurationWeight float64 // per-second contribution of effective viewing

	// Like evidence is an explicit confirmation, already split across sources.
	LikeWeight float64

	// SpreadFactor is the fraction of a reader's viewing time credited to the
	// pages adjacent to the one being read ("neighbour warmth"). Only time spreads:
	// a view that registered a count after a few seconds of glancing turns into a
	// few seconds of credit, i.e. nothing at scoring time. At 0.2 one point of
	// warmth costs 300 seconds of reading on the source page.
	SpreadFactor float64
	// SpreadCap bounds the decayed neighbour-warmth contribution, so a node
	// surrounded by activity reads as "familiar" at most on borrowed evidence and
	// never as mastered — the gold tiers and 100 stay reserved for the person's own
	// evidence. At the default it caps warmth at 50%, mid-familiar.
	SpreadCap float64

	// Recent-activity decay: the water level falls toward zero when the user
	// stops interacting for a while.
	HalfLifeDays float64

	// DecayFloor bounds how far a signal may decay. A node the user stopped
	// touching fades toward "dormant" but never back to "never seen" — the
	// history stays visible, and a month away does not look the same as never
	// having been there (design doc §六). It also keeps users from feeling
	// punished for a short absence.
	DecayFloor float64

	// FreshnessDays is the "recently active" window used only for the ripple
	// animation: a node touched within this many days keeps its water surface
	// moving. It does NOT affect the water level itself (that is HalfLifeDays).
	FreshnessDays float64

	// Score thresholds mapping the combined score onto the ten water-level
	// tiers (0 / 10..30 / 40..60 / 70..90 / 100).
	TouchScore    float64
	FamiliarScore float64
	MasteredScore float64
	SatScore      float64
}

// ColdHorizon is the age beyond which every signal's decay factor has already
// reached DecayFloor: exp(-d/HalfLifeDays) < DecayFloor. Evidence older than this
// scores identically whether it is kept day by day or summed into one bucket,
// which is what lets the daily-bucket read be folded instead of loading every
// visited page × day.
//
// Returns 0 for a degenerate configuration (no floor, or no half-life), meaning
// "do not fold": the caller must then keep every bucket.
func (c Config) ColdHorizon() time.Duration {
	if c.HalfLifeDays <= 0 || c.DecayFloor <= 0 || c.DecayFloor >= 1 {
		return 0
	}
	days := math.Log(1/c.DecayFloor) * c.HalfLifeDays
	// 加一天余量：日桶按日历日存，边界当天的实际年龄一定比日期差更大，
	// 留出余量才不会把因子尚高于下限的桶折进去。
	return time.Duration((days + 1) * float64(24*time.Hour))
}

// DefaultConfig returns the parameter set this feature ships with.
//
// The values are derived, not guessed. They satisfy five consistency
// constraints — view ≥ like > citation in evidence strength; a single capped
// view stays below saturation; one citation lands exactly on the contact tier;
// citation count is capped so one document cannot dominate a page; borrowed
// neighbour warmth tops out inside the familiar tier (SpreadCap < MasteredScore)
// — plus three product rules: per-signal decay, a decay floor, and one-way
// saturation.
func DefaultConfig() Config {
	return Config{
		CitationCap:    3,
		CitationWeight: 1,
		ViewWeight:     2,
		DurationWeight: 1.0 / 60, // one point per 60s of effective viewing
		LikeWeight:     2,
		SpreadFactor:   0.2, // 邻居拿到本次阅读时长的 20%（1 分 = 300 秒来源页阅读）
		SpreadCap:      6,   // 单靠邻居最多到 50%（熟悉档中段）——掌握档留给自己的证据
		HalfLifeDays:   30,
		// 0.35: even a long-dormant node keeps roughly a third of its evidence,
		// so it reads as "dormant" rather than "never touched".
		DecayFloor:    0.35,
		FreshnessDays: 7,
		TouchScore:    1,
		FamiliarScore: 4,
		MasteredScore: 8,
		SatScore:      12,
	}
}
