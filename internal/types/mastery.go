package types

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// Mastery event kinds used by the daily aggregation bucket.
const (
	MasteryEventCitation = "citation"
	MasteryEventView     = "view"
	MasteryEventLike     = "like"
)

// MasteryMaxPageViewSeconds caps a single effective page-view duration. The
// server clamps larger client-supplied values so a hung tab cannot inflate the
// water level toward saturation with one stray event.
//
// 300s is chosen against the scoring constants: one capped view is worth
// ViewWeight(2) + 300*DurationWeight(1/60) = 7, which stays below
// SatScore(12), so saturation always requires repeat visits instead of one
// long idle tab. Keep in sync with PAGE_VIEW_MAX_SECONDS in the wiki graph
// frontend (frontend/src/views/knowledge/wiki/WikiBrowser.vue).
const MasteryMaxPageViewSeconds = 300

// MasteryPageViewDayStart returns the start of the server-local calendar day that
// t falls in: the boundary of same-page view de-duplication.
//
// The window is a calendar day rather than a rolling duration, so one rule holds
// in all three places that record a repetition — the folded page-view row, the
// daily bucket (which caps its own count at one per day), and the exposure log. A
// rolling 24-hour window drifted from the day-keyed bucket: a visit arriving less
// than 24 hours after the previous counted one, but on a new date, folded in the
// row while the bucket still recorded a repetition, so the profile could show
// "5 views" on a node whose score had already saturated.
//
// What the rule blocks: count farming. Reopening a page six times (~30 seconds of
// clicking) walks the score to 12.5 through ViewWeight alone and saturates the
// node permanently, so counts fold — while a second, genuinely long read on the
// same day still counts through its duration. The cheapest path to six
// repetitions is therefore "come back on six different days", which is also what
// the tier semantics claim: a mastered page is one revisited over time.
//
// Known and accepted edge: two views straddling local midnight count twice.
func MasteryPageViewDayStart(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// Mastery state tiers. The mastery "water level" is a 0..100 percentage that
// maps onto these tiers; the tier labels are UI affordances, not a claim about
// a user's cognitive ability (see the design doc).
const (
	MasteryTierNone     = 0 // 暂无证据
	MasteryTierTouch    = 1 // 接触
	MasteryTierFamiliar = 2 // 熟悉
	MasteryTierMastered = 3 // 掌握（= 高证据活跃）
)

// MemoryCitation records how often a person's answers cited a document, for the
// knowledge-guidance overlay. It is deliberately a separate ledger from
// MemoryDocAffinity (which the reranker keeps reading), so deleting a person's
// learning profile never disturbs retrieval personalization.
type MemoryCitation struct {
	ID              string    `json:"id" gorm:"primaryKey;type:varchar(36)"`
	TenantID        uint64    `json:"tenant_id" gorm:"column:tenant_id;not null;uniqueIndex:idx_mastery_citation_scope,priority:1"`
	SubjectID       string    `json:"subject_id" gorm:"column:subject_id;type:varchar(512);not null;uniqueIndex:idx_mastery_citation_scope,priority:2"`
	KnowledgeID     string    `json:"knowledge_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_mastery_citation_scope,priority:3"`
	KnowledgeBaseID string    `json:"knowledge_base_id" gorm:"type:varchar(36);not null;default:''"`
	CiteCount       int       `json:"cite_count" gorm:"column:cite_count;not null;default:0"`
	LastCitedAt     time.Time `json:"last_cited_at" gorm:"column:last_cited_at"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (MemoryCitation) TableName() string { return "memory_citations" }

// MemoryPageView records how often, and for how long, a person actively viewed a
// Wiki page. total_duration is the cumulative *effective* viewing duration in
// seconds (front-end cleans misfires, tab-switch and hang time).
type MemoryPageView struct {
	ID              string    `json:"id" gorm:"primaryKey;type:varchar(36)"`
	TenantID        uint64    `json:"tenant_id" gorm:"column:tenant_id;not null;uniqueIndex:idx_mastery_view_scope,priority:1"`
	SubjectID       string    `json:"subject_id" gorm:"column:subject_id;type:varchar(512);not null;uniqueIndex:idx_mastery_view_scope,priority:2"`
	KnowledgeBaseID string    `json:"knowledge_base_id" gorm:"type:varchar(36);not null;default:'';uniqueIndex:idx_mastery_view_scope,priority:3"`
	Slug            string    `json:"slug" gorm:"type:varchar(255);not null;uniqueIndex:idx_mastery_view_scope,priority:4"`
	ViewCount       int       `json:"view_count" gorm:"column:view_count;not null;default:0"`
	TotalDuration   int64     `json:"total_duration" gorm:"column:total_duration;not null;default:0"`
	LastViewAt      time.Time `json:"last_view_at" gorm:"column:last_view_at"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (MemoryPageView) TableName() string { return "memory_page_views" }

// LikeAllocation is the per-source credit of one answer like. A like approves
// the whole answer, not each cited document, so the total gain is capped
// sub-linearly and split across citations by position; each allocation keeps its
// credited weight so a cancel can roll back exactly.
type LikeAllocation struct {
	KnowledgeID      string  `json:"knowledge_id"`
	KnowledgeBaseID  string  `json:"knowledge_base_id"`
	CitationPosition int     `json:"citation_position"`
	CreditedWeight   float64 `json:"credited_weight"`
}

// LikeAllocations is the stored, immutable split of one like across sources.
type LikeAllocations []LikeAllocation

func (a LikeAllocations) Value() (driver.Value, error) {
	if a == nil {
		return "[]", nil
	}
	data, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

func (a *LikeAllocations) Scan(value interface{}) error {
	if value == nil {
		*a = nil
		return nil
	}
	var data []byte
	switch v := value.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return fmt.Errorf("unsupported type for LikeAllocations: %T", value)
	}
	if len(data) == 0 {
		*a = nil
		return nil
	}
	return json.Unmarshal(data, a)
}

// MemoryAnswerLike is one like on an AI answer, bound to (subject, message) so
// it can be revoked. Allocations is the immutable split across the cited docs.
type MemoryAnswerLike struct {
	ID          string          `json:"id" gorm:"primaryKey;type:varchar(36)"`
	TenantID    uint64          `json:"tenant_id" gorm:"column:tenant_id;not null;uniqueIndex:idx_mastery_like_scope,priority:1"`
	SubjectID   string          `json:"subject_id" gorm:"column:subject_id;type:varchar(512);not null;uniqueIndex:idx_mastery_like_scope,priority:2"`
	MessageID   string          `json:"message_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_mastery_like_scope,priority:3"`
	Allocations LikeAllocations `json:"allocations" gorm:"type:jsonb;not null"`
	LikedAt     time.Time       `json:"liked_at" gorm:"column:liked_at"`
	CancelledAt *time.Time      `json:"cancelled_at" gorm:"column:cancelled_at"`
}

func (MemoryAnswerLike) TableName() string { return "memory_answer_likes" }

// Guide strategies for the exposure log.
const (
	GuideStrategyPPRBoundary = "ppr_boundary"
	GuideStrategyFadedReview = "faded_review"
)

// MemoryGuideExposure records one candidate shown to a person by the guidance
// view, for evaluation and future strategy learning. clicked_at is a click;
// qualified_view_at marks a click that reached the effective-view threshold
// (the reward signal). They are distinct so misfires are not counted as success.
type MemoryGuideExposure struct {
	ID        string `json:"id" gorm:"primaryKey;type:varchar(36)"`
	TenantID  uint64 `json:"tenant_id" gorm:"column:tenant_id;not null;index:idx_mastery_exposure_scope,priority:1;index:idx_mastery_exposure_candidate,priority:1"`
	SubjectID string `json:"subject_id" gorm:"column:subject_id;type:varchar(512);not null;index:idx_mastery_exposure_scope,priority:2;index:idx_mastery_exposure_candidate,priority:2"`
	// KnowledgeBaseID + CandidateSlug are the pair every "mark qualified" lookup
	// filters on (once per recorded page view); idx_mastery_exposure_candidate
	// covers it, see migration 000095 / Lite 000016.
	KnowledgeBaseID string     `json:"knowledge_base_id" gorm:"type:varchar(36);not null;default:'';index:idx_mastery_exposure_candidate,priority:3"`
	SessionID       string     `json:"session_id" gorm:"type:varchar(36)"`
	TriggerSlug     string     `json:"trigger_slug" gorm:"type:varchar(255)"`
	CandidateSlug   string     `json:"candidate_slug" gorm:"type:varchar(255);index:idx_mastery_exposure_candidate,priority:4"`
	Strategy        string     `json:"strategy" gorm:"type:varchar(32)"`
	Rank            int        `json:"rank"`
	ShownAt         time.Time  `json:"shown_at" gorm:"column:shown_at;index:idx_mastery_exposure_scope,priority:3"`
	ClickedAt       *time.Time `json:"clicked_at" gorm:"column:clicked_at"`
	QualifiedViewAt *time.Time `json:"qualified_view_at" gorm:"column:qualified_view_at"`
}

func (MemoryGuideExposure) TableName() string { return "memory_guide_exposures" }

// MemoryMasteryDaily is the daily aggregation bucket (subject × slug × event ×
// date). It is light enough to support time-decay, time-split replay and privacy
// deletion without a full event log.
type MemoryMasteryDaily struct {
	ID              string    `json:"id" gorm:"primaryKey;type:varchar(36)"`
	TenantID        uint64    `json:"tenant_id" gorm:"column:tenant_id;not null;uniqueIndex:idx_mastery_daily_scope,priority:1"`
	SubjectID       string    `json:"subject_id" gorm:"column:subject_id;type:varchar(512);not null;uniqueIndex:idx_mastery_daily_scope,priority:2"`
	KnowledgeBaseID string    `json:"knowledge_base_id" gorm:"type:varchar(36);not null;default:'';uniqueIndex:idx_mastery_daily_scope,priority:3"`
	Slug            string    `json:"slug" gorm:"type:varchar(255);not null;uniqueIndex:idx_mastery_daily_scope,priority:4"`
	EventType       string    `json:"event_type" gorm:"column:event_type;type:varchar(16);not null;uniqueIndex:idx_mastery_daily_scope,priority:5"`
	EventDate       string    `json:"event_date" gorm:"column:event_date;type:varchar(10);not null;uniqueIndex:idx_mastery_daily_scope,priority:6"`
	EventCount      int       `json:"event_count" gorm:"column:event_count;not null;default:0"`
	DurationSum     int64     `json:"duration_sum" gorm:"column:duration_sum;not null;default:0"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (MemoryMasteryDaily) TableName() string { return "memory_mastery_daily" }

// MemorySpreadView is the neighbour-spread ledger: viewing one page credits the
// pages linked to it with a fraction of the reader's time, so a node the person
// has not opened yet still picks up context from the pages around it.
//
// It is deliberately its own table rather than another event_type in
// memory_mastery_daily: that table is the behavioural ledger the time-split
// replay reads back ("what the person did, when"), while spread is derived,
// borrowed evidence ("what the neighbourhood implies"). Folding it in would make
// every future replay consumer count borrowed warmth as user behaviour, and would
// force each of them to filter the spread event type back out forever.
//
// Only reading *time* spreads, which is why the column stores seconds rather than
// a count — a view that merely registered a count (a few seconds of glancing)
// becomes a few seconds of credit, i.e. nothing. SpreadSeconds holds the source
// page's reading seconds undiscounted; the discount and the cap are scoring
// constants applied when the water level is computed, so they can be retuned
// without rewriting history.
type MemorySpreadView struct {
	ID              string    `json:"id" gorm:"primaryKey;type:varchar(36)"`
	TenantID        uint64    `json:"tenant_id" gorm:"column:tenant_id;not null;uniqueIndex:idx_mastery_spread_scope,priority:1"`
	SubjectID       string    `json:"subject_id" gorm:"column:subject_id;type:varchar(512);not null;uniqueIndex:idx_mastery_spread_scope,priority:2"`
	KnowledgeBaseID string    `json:"knowledge_base_id" gorm:"type:varchar(36);not null;default:'';uniqueIndex:idx_mastery_spread_scope,priority:3"`
	Slug            string    `json:"slug" gorm:"type:varchar(255);not null;uniqueIndex:idx_mastery_spread_scope,priority:4"`
	EventDate       string    `json:"event_date" gorm:"column:event_date;type:varchar(10);not null;uniqueIndex:idx_mastery_spread_scope,priority:5"`
	SpreadSeconds   int64     `json:"spread_seconds" gorm:"column:spread_seconds;not null;default:0"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (MemorySpreadView) TableName() string { return "memory_spread_views" }

// MasteryDailyBucket is one row of a per-day ledger read: how much of one signal
// landed on one page on one calendar day. Both daily reads (view time and
// neighbour spread) return this shape, so the water-level aggregation has a single
// assembly path and the two ledgers cannot drift apart in shape or folding rule.
//
// EventDate is empty on folded (cold) rows: buckets older than the decay horizon
// are summed per slug, and every day they cover already decays at DecayFloor, so
// the caller stamps them with the cutoff day and scores identically. Count is 0 for
// signals that carry no count — neighbour spread is time only.
type MasteryDailyBucket struct {
	Slug string
	// EventDate is the calendar day as stored, "2006-01-02". The repository
	// normalizes the driver's rendering, so callers never see PostgreSQL's
	// "2026-09-10T00:00:00Z" form for the DATE column. Empty on folded rows.
	EventDate string
	Count     int   // folded event count
	Seconds   int64 // effective seconds (viewing time, or credited spread time)
}

// NodeLedger is the per-node slice of the guidance ledgers: the rows that can
// possibly affect one page's water level, and nothing else.
//
// It exists because the page-view endpoint echoes that page's fresh level after
// every effective view, and reaching it through the graph's aggregation meant
// reading the whole knowledge base — every cited document, every viewed page, the
// subject's entire like history and every daily bucket in the KB — to answer about
// one node. That made the cost of reading a page scale with the size of the
// knowledge base and the length of the subject's history.
//
// Buckets arrive in the same folded shape as the KB-wide reads: recent days as one
// row per (slug, day), older days summed per slug with an empty EventDate. Callers
// stamp both from the same cutoff day, so they never need to be told apart.
type NodeLedger struct {
	// Citations are the rows of the documents this page is built from.
	Citations []*MemoryCitation
	// Likes are the subject's active likes, NOT narrowed to this node: the like
	// snapshot is keyed by message and its document scope lives inside the
	// allocations JSON, so there is no column to filter on. It is the one read here
	// a single node cannot narrow — and the smallest of them, which is why the
	// narrowing that matters (the daily buckets) still happens.
	Likes         []*MemoryAnswerLike
	View          *MemoryPageView // nil when the page has never been viewed
	ViewBuckets   []MasteryDailyBucket
	SpreadBuckets []MasteryDailyBucket
}

// MasteryNodeDetail is the per-node evidence breakdown surfaced by the profile
// endpoint, so a user can see exactly which signals formed a node's water level.
type MasteryNodeDetail struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	// PageType is the wiki page type, used by the export to group nodes into
	// the same collapsible buckets the wiki index uses.
	PageType string `json:"page_type"`
	Level    int    `json:"level"`
	// Tier is the coarse state label: none / touch / familiar / mastered.
	Tier      string  `json:"tier"`
	Citations int     `json:"citations"`
	Views     int     `json:"views"`
	Duration  int64   `json:"duration"`
	Likes     float64 `json:"likes"`
	// Spread is the neighbour-warmth contribution from adjacent pages, already
	// decayed and capped, so the breakdown explains the water level rather than
	// only the person's own actions.
	Spread     float64   `json:"spread"`
	LastActive time.Time `json:"last_active"`
}

// MasteryNodeState bundles a node's water level with its recency, so the graph
// overlay can render both accumulation (height) and activity (ripple) without
// the frontend doing its own clock math.
type MasteryNodeState struct {
	Level          int       `json:"level"`
	LastActive     time.Time `json:"last_active"`
	RecentlyActive bool      `json:"recently_active"`
}

// MasteryTierLabel maps a water level onto its coarse state label.
func MasteryTierLabel(level int) string {
	switch {
	case level >= 70:
		return "mastered"
	case level >= 40:
		return "familiar"
	case level >= 10:
		return "touch"
	default:
		return "none"
	}
}
