package types

import (
	"context"
	"database/sql/driver"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Write modes for MemoryConfig.
const (
	// MemoryWriteExplicitOnly records only what the user explicitly asked to
	// remember. No background LLM call is made.
	MemoryWriteExplicitOnly = "explicit_only"
	// MemoryWriteAuto additionally distills memories from the conversation in
	// a background task.
	MemoryWriteAuto = "auto"
)

// Bounds on one on-demand lookup over accounts.
const (
	// MemorySearchMaxEpisodes and MemorySearchDefaultEpisodes bound what a
	// search tool call may pull back.
	//
	// Small numbers for large results: an account runs to a few thousand
	// characters, so three of them is already more than the model will read
	// carefully, and returning ten would bury the one that answered the
	// question.
	MemorySearchMaxEpisodes     = 5
	MemorySearchDefaultEpisodes = 3
	// MemoryInterestMaxItems bounds how many recurring subjects are handed to
	// retrieval conditioning.
	//
	// Interests are not filtered by relevance to the current question — a
	// question about the person ("what am I working on") shares no words with
	// the interest's own text, so relevance would drop exactly the entries
	// that answer it. A long-running user accumulates dozens of them, and a
	// search query is not a place to list all of them, so the cap applies and
	// recurrence decides which ones survive it.
	MemoryInterestMaxItems = 5
)

// MemoryDisabledContextKey marks a request whose agent opted out of memory.
// The agent switch is per-request rather than per-scope, so it travels in the
// context instead of the database: the same user talking to two agents gets
// memory in one conversation and not the other.
//
// Exported because logger.CloneContext rebuilds contexts from an allowlist of
// keys, and the memory write path (extraction, explicit remember, document
// affinity) runs on the far side of one of those rebuilds. A key left
// unexported here would be silently dropped there, leaving an opted-out agent
// unable to read memory while still writing to it.
const MemoryDisabledContextKey ContextKey = "MemoryDisabled"

// WithMemoryDisabled marks the current request as not allowed to read memory.
func WithMemoryDisabled(ctx context.Context) context.Context {
	return context.WithValue(ctx, MemoryDisabledContextKey, true)
}

// MemoryAllowedForAgent reports whether the agent handling this request
// permits memory. Absence of the marker means allowed, so every existing call
// site keeps working and only an explicit opt-out turns memory off.
func MemoryAllowedForAgent(ctx context.Context) bool {
	if ctx == nil {
		return true
	}
	disabled, ok := ctx.Value(MemoryDisabledContextKey).(bool)
	return !(ok && disabled)
}

// ApplyAgentMemoryPreference threads an agent's memory switch into ctx. A nil
// preference inherits the workspace setting.
func ApplyAgentMemoryPreference(ctx context.Context, enabled *bool) context.Context {
	if enabled != nil && !*enabled {
		return WithMemoryDisabled(ctx)
	}
	return ctx
}

// MemorySubject is one memory space: a single principal inside a single
// workspace. Scope is always derived from the request context, never from a
// client-supplied id.
type MemorySubject struct {
	ID string `json:"id" gorm:"primaryKey;type:varchar(36)"`
	// The scope is declared as a unique index on the model, not only in the
	// migration, so EnsureSubject's upsert has a constraint to target on every
	// database the model is auto-migrated onto.
	TenantID uint64 `json:"tenant_id" gorm:"column:tenant_id;not null;uniqueIndex:idx_memory_subjects_scope,priority:1"`
	// SubjectID is Principal.StorageID(), so IM users, embed visitors and API
	// external users each get their own space without needing an account.
	SubjectID string `json:"subject_id" gorm:"type:varchar(512);not null;uniqueIndex:idx_memory_subjects_scope,priority:2"`
	// Enabled is the per-user opt out. The workspace switch lives on
	// Tenant.MemoryConfig and takes precedence over it.
	Enabled         bool       `json:"enabled" gorm:"not null;default:true"`
	LastExtractedAt *time.Time `json:"last_extracted_at" gorm:"column:last_extracted_at"`
	// ExtractCursor is the legacy subject-wide watermark, retained for
	// the upgrade boundary for newly initialized session cursors. It is never
	// advanced by new workers; each session has its own progress row.
	ExtractCursor   *time.Time            `json:"extract_cursor" gorm:"column:extract_cursor"`
	ExtractionState MemoryExtractionState `json:"-" gorm:"column:extraction_state;type:jsonb"`
	// PendingSessions is the legacy queue, imported into indexed progress rows
	// once on the next enqueue or claim. New workers never grow this array.
	PendingSessions MemoryPendingSessions `json:"pending_sessions" gorm:"column:pending_sessions;type:jsonb"`
	// ExtractScheduledAt marks a distillation task as in flight, so concurrent
	// turns enqueue one task rather than one per turn.
	ExtractScheduledAt *time.Time `json:"extract_scheduled_at" gorm:"column:extract_scheduled_at"`
	// ConsolidatedAt is when this subject's memories were last reviewed as a
	// whole rather than one turn at a time. Distillation only ever sees the
	// newest conversation, so nothing else notices that five turns over three
	// weeks have said the same thing five slightly different ways.
	ConsolidatedAt *time.Time `json:"consolidated_at" gorm:"column:consolidated_at"`
	// ForcedConsolidatedAt is when this person last asked for a review
	// themselves. It is a separate clock from ConsolidatedAt on purpose: the
	// daily pass having just run must not refuse someone who presses the
	// button, so the two cannot share one timestamp.
	ForcedConsolidatedAt *time.Time `json:"forced_consolidated_at" gorm:"column:forced_consolidated_at"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
}

// MemoryPendingSessions is the persisted queue of sessions awaiting
// distillation for one subject.
type MemoryPendingSessions []string

// MaxMemoryPendingSessions bounds one processing batch, never the durable queue.
const MaxMemoryPendingSessions = 32

func (p MemoryPendingSessions) Value() (driver.Value, error) {
	if p == nil {
		return json.Marshal([]string{})
	}
	return json.Marshal(p)
}

func (p *MemoryPendingSessions) Scan(value interface{}) error {
	if value == nil {
		*p = nil
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		*p = nil
		return nil
	}
	if len(b) == 0 {
		*p = nil
		return nil
	}
	return json.Unmarshal(b, p)
}

// Append adds a session id without dropping existing work.
func (p MemoryPendingSessions) Append(sessionID string) MemoryPendingSessions {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return p
	}
	for _, existing := range p {
		if existing == sessionID {
			return p
		}
	}
	updated := append(p, sessionID)
	return updated
}

func (MemorySubject) TableName() string { return "memory_subjects" }

// MemoryConfig is the workspace-level memory switch, stored as JSONB on
// tenants. It is deliberately small: everything a workspace admin can decide
// fits in four fields.
type MemoryConfig struct {
	// Enabled defaults to false. Memory retains user statements across
	// sessions, so a workspace admin has to turn it on deliberately.
	Enabled bool `json:"enabled"`
	// WriteMode is MemoryWriteExplicitOnly or MemoryWriteAuto.
	WriteMode string `json:"write_mode"`
	// ExtractModelID is the model used by the background extraction task.
	// Empty means "use the model the conversation itself used", which is what
	// the settings UI promises, so the extraction task must never fail merely
	// because this is blank.
	ExtractModelID string `json:"extract_model_id"`
	// ConsolidateModelID is the model that rewrites the profile injected into
	// every conversation. Empty means use the extraction model.
	//
	// Worth separating from the above because the two phases are not the same
	// job. Describing one conversation faithfully is work a small model does
	// well and does often; deciding which of a person's requests generalize
	// into preferences happens rarely, is read on every future turn, and is
	// the one a weak model gets embarrassingly wrong. Codex splits these for
	// the same reason.
	ConsolidateModelID string `json:"consolidate_model_id"`
	// MaxEpisodes caps how many accounts one subject keeps. 0 means
	// DefaultMemoryMaxEpisodes. Beyond it the least-read accounts are dropped,
	// which is the only automatic forgetting in the system.
	MaxEpisodes int `json:"max_episodes"`
	// ExtractDelaySeconds is how long a finished turn waits before
	// distillation runs. Waiting lets one model call cover the several
	// messages a user usually sends in a row. 0 means the default.
	ExtractDelaySeconds int `json:"extract_delay_seconds"`
	// ExtractMinIntervalSeconds is the floor between two distillation runs for
	// the same person, and exists purely to bound cost. It never drops a turn:
	// a turn arriving inside the interval is queued and picked up by the next
	// run. 0 means the default.
	ExtractMinIntervalSeconds int `json:"extract_min_interval_seconds"`
	// ExtractInstructions are workspace-specific rules appended to the
	// distillation prompt, for policies the product cannot guess ("never record
	// customer names", "always note the environment a question is about").
	ExtractInstructions string `json:"extract_instructions"`
	// InterestThreshold is how many separate accounts must carry the same
	// keyword before it counts as a subject this person keeps returning to and
	// is allowed to shape their retrieval. 0 means the default. Setting it to
	// 1 treats every single question as a standing interest, which is usually
	// too noisy to condition retrieval on.
	InterestThreshold int `json:"interest_threshold"`
	// EmbeddingModelID is the single model used to score memory against a
	// question. It is pinned per workspace: knowledge bases each have their
	// own embedding model, and grabbing whichever one happens to be listed
	// first would mix incomparable vector spaces. Blank means semantic recall
	// is off and matching stays lexical.
	EmbeddingModelID string `json:"embedding_model_id"`
	// VectorRecall adds semantic similarity to memory recall. Nil means on
	// when an embedding model is reachable.
	//
	// Lexical matching alone cannot find a memory the user has re-worded, which
	// is most of them: "回答直接给结论" and "别铺垫那么多" share no tokens. The
	// cost is one embedding call per turn, bounded and degraded to lexical on
	// failure, so the feature never becomes a reason a chat is slow.
	VectorRecall *bool `json:"vector_recall"`
	// RetrievalConditioning lets memory shape retrieval — query rewriting and
	// per-document ranking — rather than only being appended to the answer
	// prompt. This is where memory earns its keep in a knowledge-base product.
	RetrievalConditioning *bool `json:"retrieval_conditioning"`
}

// DefaultMemoryMaxEpisodes is the per-subject account cap a workspace that has
// not chosen one gets. It sits well under MemoryEpisodeMaxPerSubject, which is
// the ceiling any workspace may configure.
const DefaultMemoryMaxEpisodes = 200

// Bounds on how often a keyword has to recur to count as an interest.
const (
	DefaultMemoryInterestThreshold = 3
	MaxMemoryInterestThreshold     = 20
)

// VectorRecallEnabled reports whether recall may use semantic similarity.
func (c *MemoryConfig) VectorRecallEnabled() bool {
	if c == nil || !c.Enabled {
		return false
	}
	return c.VectorRecall == nil || *c.VectorRecall
}

// RetrievalConditioningEnabled reports whether memory may shape retrieval.
// Nil means on: a memory feature that cannot improve retrieval is most of the
// value left on the table in a knowledge-base product.
func (c *MemoryConfig) RetrievalConditioningEnabled() bool {
	if c == nil || !c.Enabled {
		return false
	}
	return c.RetrievalConditioning == nil || *c.RetrievalConditioning
}

// EffectiveInterestThreshold returns the recurrence threshold for a nil config.
func (c *MemoryConfig) EffectiveInterestThreshold() int {
	if c == nil || c.InterestThreshold <= 0 {
		return DefaultMemoryInterestThreshold
	}
	if c.InterestThreshold > MaxMemoryInterestThreshold {
		return MaxMemoryInterestThreshold
	}
	return c.InterestThreshold
}

// MaxMemoryExtractInstructionsRunes bounds the custom prompt so one workspace
// cannot turn every distillation call into a large prompt.
const MaxMemoryExtractInstructionsRunes = 1000

func (c MemoryConfig) Value() (driver.Value, error) { return json.Marshal(c) }

func (c *MemoryConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return nil
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, c)
}

// Normalize applies defaults and rejects unknown write modes.
func (c *MemoryConfig) Normalize() {
	if c == nil {
		return
	}
	if c.WriteMode != MemoryWriteAuto {
		c.WriteMode = MemoryWriteExplicitOnly
	}
	c.ExtractModelID = strings.TrimSpace(c.ExtractModelID)
	c.ConsolidateModelID = strings.TrimSpace(c.ConsolidateModelID)
	c.EmbeddingModelID = strings.TrimSpace(c.EmbeddingModelID)
	if c.MaxEpisodes <= 0 {
		c.MaxEpisodes = DefaultMemoryMaxEpisodes
	}
	if c.MaxEpisodes > MemoryEpisodeMaxPerSubject {
		c.MaxEpisodes = MemoryEpisodeMaxPerSubject
	}
	c.ExtractDelaySeconds = clampSeconds(
		c.ExtractDelaySeconds, DefaultMemoryExtractDelaySeconds,
		MinMemoryExtractDelaySeconds, MaxMemoryExtractDelaySeconds,
	)
	c.ExtractMinIntervalSeconds = clampSeconds(
		c.ExtractMinIntervalSeconds, DefaultMemoryExtractMinIntervalSeconds,
		0, MaxMemoryExtractMinIntervalSeconds,
	)
	if c.InterestThreshold <= 0 {
		c.InterestThreshold = DefaultMemoryInterestThreshold
	}
	if c.InterestThreshold > MaxMemoryInterestThreshold {
		c.InterestThreshold = MaxMemoryInterestThreshold
	}
	c.ExtractInstructions = strings.TrimSpace(c.ExtractInstructions)
	if runes := []rune(c.ExtractInstructions); len(runes) > MaxMemoryExtractInstructionsRunes {
		c.ExtractInstructions = strings.TrimSpace(string(runes[:MaxMemoryExtractInstructionsRunes]))
	}
}

// Bounds for the distillation timers. The lower bound on the delay is not a
// safety rail but a cost one: a delay near zero turns a burst of messages into
// one model call per message.
const (
	DefaultMemoryExtractDelaySeconds       = 90
	MinMemoryExtractDelaySeconds           = 5
	MaxMemoryExtractDelaySeconds           = 3600
	DefaultMemoryExtractMinIntervalSeconds = 300
	MaxMemoryExtractMinIntervalSeconds     = 86400
)

func clampSeconds(value, fallback, minimum, maximum int) int {
	if value <= 0 {
		value = fallback
	}
	if value < minimum {
		value = minimum
	}
	if value > maximum {
		value = maximum
	}
	return value
}

// ExtractDelay is the debounce window for a possibly nil config.
func (c *MemoryConfig) ExtractDelay() time.Duration {
	if c == nil || c.ExtractDelaySeconds <= 0 {
		return time.Duration(DefaultMemoryExtractDelaySeconds) * time.Second
	}
	return time.Duration(c.ExtractDelaySeconds) * time.Second
}

// ExtractMinInterval is the floor between two runs for a possibly nil config.
func (c *MemoryConfig) ExtractMinInterval() time.Duration {
	if c == nil || c.ExtractMinIntervalSeconds <= 0 {
		return time.Duration(DefaultMemoryExtractMinIntervalSeconds) * time.Second
	}
	return time.Duration(c.ExtractMinIntervalSeconds) * time.Second
}

// EffectiveMaxEpisodes returns the account cap for a possibly nil config.
func (c *MemoryConfig) EffectiveMaxEpisodes() int {
	if c == nil || c.MaxEpisodes <= 0 {
		return DefaultMemoryMaxEpisodes
	}
	if c.MaxEpisodes > MemoryEpisodeMaxPerSubject {
		return MemoryEpisodeMaxPerSubject
	}
	return c.MaxEpisodes
}

// AutoExtractEnabled reports whether the background distillation task should
// run. A nil or disabled config never extracts.
func (c *MemoryConfig) AutoExtractEnabled() bool {
	return c != nil && c.Enabled && c.WriteMode == MemoryWriteAuto
}

// MemoryEnabled reports whether the workspace switch is on.
func (c *MemoryConfig) MemoryEnabled() bool {
	return c != nil && c.Enabled
}

// Patterns for material that must never become a long-term note. A memory is
// injected into the system prompt of every later turn, so a credential that
// lands here is not just retained, it is re-sent to a model repeatedly.
//
// The list is deliberately specific rather than clever. The previous attempt at
// this feature matched loosely and mangled ordinary long order numbers while
// still leaving the tail of an ID card in place, which is the worst of both
// outcomes: the user loses correct memories and keeps the sensitive one.
var sensitivePatterns = []*regexp.Regexp{
	// Provider tokens, matched by their documented prefixes.
	regexp.MustCompile(`\bsk-[A-Za-z0-9_\-]{16,}`),
	regexp.MustCompile(`\bsk_(live|test)_[A-Za-z0-9]{16,}`),
	regexp.MustCompile(`\b(ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}`),
	regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9\-]{10,}`),
	regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),
	// A value assigned to something that names itself a secret.
	// The value stops at whitespace or CJK punctuation: Chinese has no spaces,
	// so a greedy \S+ would swallow the rest of the sentence and redact a whole
	// legitimate memory along with the secret.
	regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[_\- ]?key|access[_\- ]?key)\b` +
		`\s*[:=＝：]\s*[^\s，。、；：！？,;]+`),
	// No \b here: Go's word boundary is ASCII-only, so it never matches before
	// a CJK character and would silently disable this rule.
	regexp.MustCompile(`(密码|口令|密钥|秘钥)\s*[:=＝：是为]?\s*[^\s，。、；：！？,;]+`),
	// Mainland China resident ID: anchored on a plausible birth date so long
	// order numbers and other 18-digit strings are not caught.
	regexp.MustCompile(`\b[1-9]\d{5}(19|20)\d{2}(0[1-9]|1[0-2])(0[1-9]|[12]\d|3[01])\d{3}[\dXx]\b`),
	// Bank card numbers, optionally spaced or dashed into groups.
	regexp.MustCompile(`\b\d{4}[ \-]?\d{4}[ \-]?\d{4}[ \-]?\d{2,7}\b`),
	// Mainland China mobile numbers.
	regexp.MustCompile(`\b1[3-9]\d{9}\b`),
}

// sensitiveOpaqueRun finds candidates for the unrecognised-token rule: long
// runs of the characters a key is made of. Whether a candidate is a key is
// decided by isOpaqueToken, not by the match itself.
var sensitiveOpaqueRun = regexp.MustCompile(`\b[A-Za-z0-9_\-]{40,}\b`)

const (
	// opaqueRunMinUnbroken is the longest stretch of letters and digits,
	// uninterrupted by a separator, that marks a candidate as a key.
	opaqueRunMinUnbroken = 24
	// opaqueRunMinDigits is how many digits mark one, for the tokens that are
	// delimited into segments and would otherwise read as words.
	opaqueRunMinDigits = 12
)

// isOpaqueToken reports whether a long run of key characters is a key rather
// than words joined by hyphens.
//
// The rule used to be length alone, and an episode slug is made of exactly
// these characters: lowercase words joined by hyphens, up to
// MemoryEpisodeSlugMaxRunes of them. So every account whose handle ran past
// forty characters had that handle replaced by the placeholder — including in
// the digest's memory index, which is the one place a handle has to survive
// verbatim, because the index is what the rewrite is told to quote back and
// what search_memory resolves. A redacted pointer is not untidy, it is a
// pointer to nothing.
//
// What separates the two is shape, not size. Words are short and separated: a
// key is one long unbroken run, at most with a scheme prefix like ghp_ in
// front of it, or it is delimited but carries the long numeric ids that no
// slug does.
func isOpaqueToken(candidate string) bool {
	longest, current, digits := 0, 0, 0
	for _, r := range candidate {
		if r == '-' || r == '_' {
			current = 0
			continue
		}
		if r >= '0' && r <= '9' {
			digits++
		}
		current++
		if current > longest {
			longest = current
		}
	}
	return longest >= opaqueRunMinUnbroken || digits >= opaqueRunMinDigits
}

// RedactedMemoryPlaceholder replaces removed material. It is visible on purpose:
// a user reading their memory list should be able to tell that something was
// dropped rather than silently mangled.
const RedactedMemoryPlaceholder = "【已隐藏】"

// RedactSensitive removes credentials and identity numbers from a statement.
// The second return value reports whether anything was removed.
func RedactSensitive(content string) (string, bool) {
	redacted := content
	for _, pattern := range sensitivePatterns {
		redacted = pattern.ReplaceAllString(redacted, RedactedMemoryPlaceholder)
	}
	redacted = sensitiveOpaqueRun.ReplaceAllStringFunc(redacted, func(candidate string) string {
		if !isOpaqueToken(candidate) {
			return candidate
		}
		return RedactedMemoryPlaceholder
	})
	return redacted, redacted != content
}

// IsMostlyRedacted reports whether a statement lost so much that keeping it
// would store a placeholder rather than a memory.
func IsMostlyRedacted(content string) bool {
	stripped := strings.ReplaceAll(content, RedactedMemoryPlaceholder, "")
	remaining := len([]rune(strings.TrimSpace(stripped)))
	return remaining < 6
}

// collapseToLine is the shared shape every stored memory string takes: one
// line, single-spaced, inside a rune budget.
func collapseToLine(text string, limit int) string {
	text = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	if runes := []rune(text); len(runes) > limit {
		text = strings.TrimSpace(string(runes[:limit]))
	}
	return text
}

// MemorySettings is the effective, already-merged memory state for one user.
// The UI renders this directly rather than merging a workspace setting with a
// user setting itself, so "why is my memory off" has exactly one answer.
type MemorySettings struct {
	// WorkspaceEnabled is the admin switch on the workspace.
	WorkspaceEnabled bool `json:"workspace_enabled"`
	// UserEnabled is the caller's own opt out. Meaningless while the
	// workspace switch is off, but still reported so the toggle keeps its
	// position when an admin turns the workspace back on.
	UserEnabled bool `json:"user_enabled"`
	// Effective is what actually happens: WorkspaceEnabled && UserEnabled.
	Effective bool `json:"effective"`
	// WriteMode is the workspace write mode.
	WriteMode string `json:"write_mode"`
	// EpisodeCount is how many accounts of past conversations the caller has.
	EpisodeCount int `json:"episode_count"`
	// MaxEpisodes is the cap past which the least-read accounts are dropped.
	MaxEpisodes int `json:"max_episodes"`
}

// Why a review changed nothing. A review that merges nothing is the normal
// case, so "nothing happened" on its own tells the person who asked for it
// neither whether it worked nor whether it is worth asking again.
const (
	// MemoryConsolidationSkipTooFewItems: too few accounts to write a profile
	// worth injecting, so the pass does not spend a call on them.
	MemoryConsolidationSkipTooFewItems = "too_few_items"
	// MemoryConsolidationSkipModelUnavailable: there was material to rewrite
	// from but the model that rewrites the profile could not be reached.
	MemoryConsolidationSkipModelUnavailable = "model_unavailable"
	// MemoryConsolidationSkipTooSoon: this person asked for a review moments
	// ago. Only a review someone requested can report this; the daily pass has
	// its own, much longer interval and simply stays quiet.
	MemoryConsolidationSkipTooSoon = "too_soon"
)

// MemoryConsolidationResult is what a whole-store review did. Zeroes mean the
// store was already tidy, not that the review failed — Skipped says which.
type MemoryConsolidationResult struct {
	Merged  int `json:"merged"`
	Demoted int `json:"demoted"`
	Expired int `json:"expired"`
	// Reviewed is how many active memories the pass looked at.
	Reviewed int `json:"reviewed"`
	// Candidates is how many groups were put in front of the model.
	Candidates int `json:"candidates"`
	// Skipped is why nothing was merged, empty when something was.
	Skipped string `json:"skipped,omitempty"`
}

// UsedMemory is the per-turn record of which memories were injected. It is
// returned to the client so the chat UI can show, and let the user delete,
// exactly what influenced an answer.
type UsedMemory struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Content string `json:"content"`
}

// UsedMemories is the persisted per-message list.
type UsedMemories []UsedMemory

func (u UsedMemories) Value() (driver.Value, error) {
	if u == nil {
		return json.Marshal([]UsedMemory{})
	}
	return json.Marshal(u)
}

func (u *UsedMemories) Scan(value interface{}) error {
	if value == nil {
		*u = make(UsedMemories, 0)
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		*u = make(UsedMemories, 0)
		return nil
	}
	if len(b) == 0 {
		*u = make(UsedMemories, 0)
		return nil
	}
	return json.Unmarshal(b, u)
}

// Kinds of memory contribution reported on a message. These are display
// categories rather than storage kinds: what the chat UI needs to say is where
// a line came from, and "the profile", "your own words" and "a past
// conversation" are the three answers a person can act on.
const (
	UsedMemoryKindDigest  = "digest"
	UsedMemoryKindNote    = "note"
	UsedMemoryKindEpisode = "episode"
)

// UsedMemoriesFromDocuments flattens one turn's memory contribution.
//
// The profile is reported as a single entry rather than line by line. It is
// rewritten as a whole, the user edits it as a whole, and attributing an answer
// to one of its bullets would imply a precision the injection does not have —
// the model saw the entire document.
func UsedMemoriesFromDocuments(
	digest *MemoryDigest, notes []*MemoryNote, episodes []*MemoryEpisode,
) UsedMemories {
	used := make(UsedMemories, 0, 1+len(notes)+len(episodes))
	if digest != nil && strings.TrimSpace(digest.Body) != "" {
		used = append(used, UsedMemory{
			ID:      fmt.Sprintf("digest-%d", digest.Revision),
			Kind:    UsedMemoryKindDigest,
			Content: collapseToLine(MemoryDigestSection(digest.Body, MemoryDigestSectionProfile), 200),
		})
	}
	for _, note := range notes {
		if note == nil {
			continue
		}
		used = append(used, UsedMemory{
			ID: note.ID, Kind: UsedMemoryKindNote, Content: note.Content,
		})
	}
	for _, episode := range episodes {
		if episode == nil {
			continue
		}
		used = append(used, UsedMemory{
			ID: episode.ID, Kind: UsedMemoryKindEpisode, Content: episode.Title,
		})
	}
	return used
}

// Explicit memory directives. Recognizing a fixed set of prefixes keeps the
// explicit-write path deterministic: in the default explicit_only mode nothing
// is ever stored that the user did not literally ask to store, and no model
// call stands between the request and the record.
var explicitMemoryPrefixes = []string{
	"记住：", "记住:", "记住，", "记住,", "记住 ", "记住",
	"请记住：", "请记住:", "请记住，", "请记住,", "请记住 ", "请记住",
	"帮我记住：", "帮我记住:", "帮我记住，", "帮我记住,", "帮我记住 ", "帮我记住",
	"remember that ", "remember: ", "remember, ", "please remember that ",
	"please remember: ", "note that ", "keep in mind that ",
}

// DetectExplicitMemory extracts the statement from a "remember ..." directive.
// It returns ok=false for anything else, including a bare directive with no
// statement after it.
func DetectExplicitMemory(query string) (string, bool) {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return "", false
	}
	lowered := strings.ToLower(trimmed)
	for _, prefix := range explicitMemoryPrefixes {
		if !strings.HasPrefix(lowered, strings.ToLower(prefix)) {
			continue
		}
		statement := SanitizeMemoryNote(strings.TrimSpace(trimmed[len(prefix):]))
		statement = strings.TrimLeft(statement, "：:，, ")
		if len([]rune(statement)) < 2 {
			return "", false
		}
		return statement, true
	}
	return "", false
}

// MergeUsedMemories combines two lists of shown memories, keeping the first
// occurrence of each id.
//
// A memory can influence a turn twice — once by shaping the search and once by
// being quoted in the answer — and the user should see it listed once.
func MergeUsedMemories(existing, additional []UsedMemory) []UsedMemory {
	if len(additional) == 0 {
		return existing
	}
	seen := make(map[string]struct{}, len(existing)+len(additional))
	merged := make([]UsedMemory, 0, len(existing)+len(additional))
	for _, list := range [][]UsedMemory{existing, additional} {
		for _, item := range list {
			if item.ID != "" {
				if _, dup := seen[item.ID]; dup {
					continue
				}
				seen[item.ID] = struct{}{}
			}
			merged = append(merged, item)
		}
	}
	return merged
}

// EncodeEmbedding packs a vector as little-endian float32.
func EncodeEmbedding(vector []float32) []byte {
	if len(vector) == 0 {
		return nil
	}
	out := make([]byte, len(vector)*4)
	for i, value := range vector {
		binary.LittleEndian.PutUint32(out[i*4:], math.Float32bits(value))
	}
	return out
}

// DecodeEmbedding unpacks a vector written by EncodeEmbedding.
func DecodeEmbedding(raw []byte) []float32 {
	if len(raw) < 4 {
		return nil
	}
	out := make([]float32, len(raw)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	return out
}

// FormatEmbeddingLiteral renders a vector the way pgvector parses it, so the
// database can do the distance arithmetic instead of shipping every stored
// vector to the application to be scored there.
func FormatEmbeddingLiteral(vector []float32) string {
	if len(vector) == 0 {
		return ""
	}
	var b strings.Builder
	b.Grow(len(vector) * 8)
	b.WriteByte('[')
	for i, value := range vector {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(value), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}

// CosineSimilarity scores two vectors in [-1, 1]. Mismatched lengths score 0:
// vectors from different models are not comparable, and guessing is worse than
// declining to answer.
func CosineSimilarity(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		normA += x * x
		normB += y * y
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}
