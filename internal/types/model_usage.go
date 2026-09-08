package types

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// CallType classifies the kind of model invocation a ModelUsage row records.
// It also fixes the meaning of the universal token counters (see the comment
// on InputTokens / OutputTokens / TotalTokens).
type CallType string

const (
	CallTypeChat      CallType = "chat"      // Chat completion (knowledge QA, agent round, summary, …)
	CallTypeEmbedding CallType = "embedding" // Text embedding (document or query)
	CallTypeRerank    CallType = "rerank"    // Cross-encoder reranking
)

// TokenProvenance describes the trust source of the token counts in a row.
//
// Logical / provider / WeKnora-embedding-cache / provider-prompt-cache are
// *dimensions*, and they already live in separate counter columns on the same
// row (one logical invocation per row). Provenance is the orthogonal question
// "how trustworthy are the token numbers", so it is modelled as its own axis:
//
//   - provider_reported: the token counts are verbatim what the provider's
//     usage block returned.
//   - derived: the counts were computed from other reported values (e.g.
//     total = input + output) rather than returned directly.
//   - estimated: the counts were estimated (tokenizer / heuristics), not
//     observed from the provider.
//   - unreported: the provider could have reported but did not (all token
//     counters are NULL).
//   - unsupported: the model/provider path cannot report token accounting.
//
// A provider's NULL is never fabricated into 0, and an estimated value is
// never labelled provider_reported. When a row mixes provenances (e.g. input
// provider-reported but total derived), the field records the *least*
// trustworthy value so a reader can never over-trust the data.
type TokenProvenance string

const (
	TokenProvenanceProviderReported TokenProvenance = "provider_reported"
	TokenProvenanceDerived          TokenProvenance = "derived"
	TokenProvenanceEstimated        TokenProvenance = "estimated"
	TokenProvenanceUnreported       TokenProvenance = "unreported"
	TokenProvenanceUnsupported      TokenProvenance = "unsupported"
)

// UsageStatus is the normalized terminal state of a logical model invocation.
// Only the outcome is stored; the underlying error text is deliberately
// excluded because it may embed prompt or secret material.
//
// cancelled and timeout map to context.Canceled and context.DeadlineExceeded
// respectively — the only two outcomes Go can classify reliably. A provider
// that times out without surfacing a context error is classified as error,
// never fabricated into timeout.
type UsageStatus string

const (
	UsageStatusSuccess   UsageStatus = "success"
	UsageStatusError     UsageStatus = "error"
	UsageStatusCancelled UsageStatus = "cancelled"
	UsageStatusTimeout   UsageStatus = "timeout"
)

// EmbeddingCacheStatus describes the WeKnora embedding cache outcome for an
// embedding call. It is fully independent of provider-side prompt caching
// (PromptCacheStatus): the two caches are separate systems with separate
// counters and must never be mixed.
type EmbeddingCacheStatus string

const (
	EmbeddingCacheStatusDisabled EmbeddingCacheStatus = "disabled" // Cache not configured or bypassed
	EmbeddingCacheStatusFullHit  EmbeddingCacheStatus = "full_hit" // Every input served from cache
	EmbeddingCacheStatusPartial  EmbeddingCacheStatus = "partial"  // Some inputs served, rest sent to provider
	EmbeddingCacheStatusMiss     EmbeddingCacheStatus = "miss"     // No inputs served from cache
)

// ModelUsage is a call-level record of one logical model invocation. A single
// logical call may fan out to N provider requests (provider_requests) and M
// provider inputs/pairs, and carries the WeKnora embedding-cache and provider
// prompt-cache breakdowns in dedicated counters.
//
// Counters that a provider does not report are stored as NULL, never forged to
// zero. Monetary cost remains a separate derived model_usage_cost fact.
type ModelUsage struct {
	ID string `gorm:"type:varchar(36);primaryKey"`
	//nolint:lll // Composite GORM indexes must remain together in the field tag.
	TenantID uint64 `gorm:"not null;index:idx_model_usage_tenant_created,priority:1;index:idx_model_usage_tenant_model_created,priority:1;index:idx_model_usage_tenant_evaluation_created,priority:1"`
	// ModelTenantID is the tenant that owns the model config / credential, as
	// opposed to TenantID which is the business / evaluation caller. The two
	// differ for cross-tenant shared models.
	ModelTenantID uint64 `gorm:"column:model_tenant_id;not null"`
	// EvaluationRunID attributes the row to a run-level evaluation when set;
	// ordinary business calls leave it NULL. The repository enforces that a
	// non-NULL run belongs to TenantID.
	//nolint:lll // Composite GORM indexes must remain together in the field tag.
	EvaluationRunID *string `gorm:"type:varchar(36);index:idx_model_usage_tenant_evaluation_created,priority:2;index:idx_model_usage_evaluation_run"`

	// Model identity snapshot at call time. Never dereferenced later, so a
	// model renamed or deleted after the call leaves the row self-describing.
	// ResolvedProvider is the resolved provider.ProviderName (from the config
	// Provider or BaseURL detection), not the raw config string.
	ModelID          string `gorm:"type:varchar(64);not null;index:idx_model_usage_tenant_model_created,priority:2"`
	ModelName        string `gorm:"type:varchar(255);not null"`
	ModelType        string `gorm:"type:varchar(32);not null"`
	ModelSource      string `gorm:"type:varchar(32);not null"`
	ResolvedProvider string `gorm:"column:resolved_provider;type:varchar(64);not null"`
	// ResolvedModelName is the effective model identity actually placed on the
	// outbound provider request. It is deliberately nullable: legacy rows and
	// provider paths that cannot prove the effective identity remain unknown.
	ResolvedModelName *string `gorm:"column:resolved_model_name;type:varchar(255)"`

	CallType        CallType        `gorm:"type:varchar(16);not null"`
	Purpose         string          `gorm:"type:varchar(128);not null;default:''"`
	Status          UsageStatus     `gorm:"type:varchar(32);not null"`
	TokenProvenance TokenProvenance `gorm:"column:token_provenance;type:varchar(32);not null"`

	// LatencyMS is wall-clock milliseconds of the logical invocation. NULL
	// when the call never completed enough to be measured.
	LatencyMS *int64     `gorm:"column:latency_ms"`
	StartedAt *time.Time `gorm:"column:started_at"`
	//nolint:lll // Composite GORM indexes must remain together in the field tag.
	CreatedAt time.Time `gorm:"not null;index:idx_model_usage_tenant_created,priority:2;index:idx_model_usage_tenant_model_created,priority:3;index:idx_model_usage_tenant_evaluation_created,priority:3"`

	// Universal token counters, interpreted by CallType. NULL when the
	// provider did not report that value.
	//
	//   chat:      input = prompt tokens, output = completion tokens
	//   embedding: input = provider-reported embedding input tokens, output NULL
	//   rerank:    input = provider-reported prompt/input tokens, output NULL
	InputTokens  *int `gorm:"column:input_tokens"`
	OutputTokens *int `gorm:"column:output_tokens"`
	TotalTokens  *int `gorm:"column:total_tokens"`

	// Provider prompt-cache accounting. Status is never inferred backwards
	// from these counters: a "reported zero" is a non-NULL 0 with an explicit
	// miss/hit status, and an unreported value stays NULL.
	PromptCacheStatus *PromptCacheStatus `gorm:"column:prompt_cache_status;type:varchar(32)"`
	CacheReadTokens   *int               `gorm:"column:cache_read_tokens"`
	CacheWriteTokens  *int               `gorm:"column:cache_write_tokens"`
	CacheMissTokens   *int               `gorm:"column:cache_miss_tokens"`

	// Logical and WeKnora embedding-cache counters (embedding / shared).
	LogicalRequests  int `gorm:"column:logical_requests;not null;default:0"`
	EmbeddingInputs  int `gorm:"column:embedding_inputs;not null;default:0"`
	CacheHits        int `gorm:"column:cache_hits;not null;default:0"`
	CacheMisses      int `gorm:"column:cache_misses;not null;default:0"`
	ProviderRequests int `gorm:"column:provider_requests;not null;default:0"`
	ProviderInputs   int `gorm:"column:provider_inputs;not null;default:0"`
	CacheReadErrors  int `gorm:"column:cache_read_errors;not null;default:0"`
	CacheWriteErrors int `gorm:"column:cache_write_errors;not null;default:0"`
	// EmbeddingCacheStatus is NULL when the call type has no embedding cache
	// (chat / rerank) or the cache outcome was not recorded.
	EmbeddingCacheStatus *EmbeddingCacheStatus `gorm:"column:embedding_cache_status;type:varchar(32)"`

	// Rerank counters.
	Queries       int `gorm:"not null;default:0"`
	Documents     int `gorm:"not null;default:0"`
	Pairs         int `gorm:"not null;default:0"`
	ProviderPairs int `gorm:"column:provider_pairs;not null;default:0"`
}

func (ModelUsage) TableName() string { return "model_usage" }

func (u *ModelUsage) BeforeCreate(_ *gorm.DB) error {
	if u.ID == "" {
		u.ID = uuid.NewString()
	}
	return nil
}

// Validate enforces the invariants that cannot be expressed (or are better
// not expressed) as database constraints: enum membership, non-negative
// numeric counters, and the cross-field embedding-cache accounting identity.
// It is the single source of truth for semantic validation, called by the
// repository before any write.
func (u *ModelUsage) Validate() error {
	if u.TenantID == 0 {
		return fmt.Errorf("model_usage: tenant_id must be non-zero")
	}
	if u.ModelTenantID == 0 {
		return fmt.Errorf("model_usage: model_tenant_id must be non-zero")
	}
	if u.ModelID == "" {
		return fmt.Errorf("model_usage: model_id must not be empty")
	}
	switch u.CallType {
	case CallTypeChat, CallTypeEmbedding, CallTypeRerank:
	default:
		return fmt.Errorf("model_usage: invalid call_type %q", u.CallType)
	}
	switch u.Status {
	case UsageStatusSuccess, UsageStatusError, UsageStatusCancelled, UsageStatusTimeout:
	default:
		return fmt.Errorf("model_usage: invalid status %q", u.Status)
	}
	switch u.TokenProvenance {
	case TokenProvenanceProviderReported, TokenProvenanceDerived, TokenProvenanceEstimated,
		TokenProvenanceUnreported, TokenProvenanceUnsupported:
	default:
		return fmt.Errorf("model_usage: invalid token_provenance %q", u.TokenProvenance)
	}
	if u.PromptCacheStatus != nil {
		switch *u.PromptCacheStatus {
		case PromptCacheStatusUnsupported, PromptCacheStatusUnreported, PromptCacheStatusMiss, PromptCacheStatusHit:
		default:
			return fmt.Errorf("model_usage: invalid prompt_cache_status %q", *u.PromptCacheStatus)
		}
	}
	if u.EmbeddingCacheStatus != nil {
		switch *u.EmbeddingCacheStatus {
		case EmbeddingCacheStatusDisabled, EmbeddingCacheStatusFullHit,
			EmbeddingCacheStatusPartial, EmbeddingCacheStatusMiss:
		default:
			return fmt.Errorf("model_usage: invalid embedding_cache_status %q", *u.EmbeddingCacheStatus)
		}
	}
	for name, v := range map[string]*int{
		"input_tokens": u.InputTokens, "output_tokens": u.OutputTokens, "total_tokens": u.TotalTokens,
		"cache_read_tokens": u.CacheReadTokens, "cache_write_tokens": u.CacheWriteTokens,
		"cache_miss_tokens": u.CacheMissTokens,
	} {
		if v != nil && *v < 0 {
			return fmt.Errorf("model_usage: %s must not be negative", name)
		}
	}
	for name, v := range map[string]int{
		"logical_requests": u.LogicalRequests, "embedding_inputs": u.EmbeddingInputs,
		"cache_hits": u.CacheHits, "cache_misses": u.CacheMisses,
		"provider_requests": u.ProviderRequests, "provider_inputs": u.ProviderInputs,
		"cache_read_errors": u.CacheReadErrors, "cache_write_errors": u.CacheWriteErrors,
		"queries": u.Queries, "documents": u.Documents,
		"pairs": u.Pairs, "provider_pairs": u.ProviderPairs,
	} {
		if v < 0 {
			return fmt.Errorf("model_usage: %s must not be negative", name)
		}
	}
	if u.LatencyMS != nil && *u.LatencyMS < 0 {
		return fmt.Errorf("model_usage: latency_ms must not be negative")
	}

	// Cross-field embedding-cache identity. Only meaningful when the cache is
	// active: when disabled the counters are zero regardless of input count,
	// and when the outcome was not recorded we cannot assert anything.
	if u.CallType == CallTypeEmbedding &&
		u.EmbeddingCacheStatus != nil && *u.EmbeddingCacheStatus != EmbeddingCacheStatusDisabled {
		if u.CacheHits+u.CacheMisses != u.EmbeddingInputs {
			return fmt.Errorf(
				"model_usage: embedding cache accounting must satisfy "+
					"cache_hits + cache_misses == embedding_inputs (got %d + %d != %d)",
				u.CacheHits, u.CacheMisses, u.EmbeddingInputs,
			)
		}
	}
	return nil
}
