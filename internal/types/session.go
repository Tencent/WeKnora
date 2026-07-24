package types

import (
	"database/sql/driver"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// FallbackStrategy represents the fallback strategy type
type FallbackStrategy string

const (
	FallbackStrategyFixed FallbackStrategy = "fixed" // Fixed response
	FallbackStrategyModel FallbackStrategy = "model" // Model fallback response
)

// SummaryConfig represents the summary configuration for a session
type SummaryConfig struct {
	// Max tokens
	MaxTokens int `json:"max_tokens"`
	// Repeat penalty
	RepeatPenalty float64 `json:"repeat_penalty"`
	// TopK
	TopK int `json:"top_k"`
	// TopP
	TopP float64 `json:"top_p"`
	// Frequency penalty
	FrequencyPenalty float64 `json:"frequency_penalty"`
	// Presence penalty
	PresencePenalty float64 `json:"presence_penalty"`
	// Prompt
	Prompt string `json:"prompt"`
	// Context template
	ContextTemplate string `json:"context_template"`
	// No match prefix
	NoMatchPrefix string `json:"no_match_prefix"`
	// Temperature
	Temperature float64 `json:"temperature"`
	// Seed
	Seed int `json:"seed"`
	// Max completion tokens
	MaxCompletionTokens int `json:"max_completion_tokens"`
	// Thinking - whether to enable thinking mode
	Thinking *bool `json:"thinking"`
}

// ContextCompressionStrategy represents the strategy for context compression
type ContextCompressionStrategy string

const (
	// ContextCompressionSlidingWindow keeps the most recent N messages
	ContextCompressionSlidingWindow ContextCompressionStrategy = "sliding_window"
	// ContextCompressionSmart uses LLM to summarize old messages
	ContextCompressionSmart ContextCompressionStrategy = "smart"
)

// ContextConfig configures LLM context management
// This is separate from message storage and manages token limits
type ContextConfig struct {
	// Maximum tokens allowed in LLM context
	MaxTokens int `json:"max_tokens"`
	// Compression strategy: "sliding_window" or "smart"
	CompressionStrategy ContextCompressionStrategy `json:"compression_strategy"`
	// For sliding_window: number of messages to keep
	// For smart: number of recent messages to keep uncompressed
	RecentMessageCount int `json:"recent_message_count"`
	// Summarize threshold: number of messages before summarization
	SummarizeThreshold int `json:"summarize_threshold"`
}

// Session represents the session
type Session struct {
	// ID
	ID string `json:"id"          gorm:"type:varchar(36);primaryKey"`
	// Title
	Title string `json:"title"`
	// Description
	Description string `json:"description"`
	// Workspace ID
	TenantID uint64 `json:"tenant_id"   gorm:"index"`
	// UserID is the owner scope for this session. WeKnora user UUIDs, API
	// external-user principals, and embed visitor principals all use this column.
	UserID string `json:"user_id,omitempty" gorm:"type:varchar(512);index"`
	// IsPinned indicates whether the session is pinned in the list.
	IsPinned bool `json:"is_pinned" gorm:"default:false"`
	// PinnedAt records when the session was pinned; nil when not pinned.
	PinnedAt *time.Time `json:"pinned_at,omitempty"`

	// LastRequestState records the input-bar state used the last time this
	// session sent a question (agent, model, KB scope, web search, MCPs).
	// Persisted on every successful POST to /knowledge-chat or /agent-chat so
	// that reopening the session can restore the original request context to
	// the chat UI. Stored in the legacy sessions.agent_config JSONB column to
	// avoid a new migration; the shape used today is `SessionLastRequestState`.
	LastRequestState *SessionLastRequestState `json:"last_request_state,omitempty" gorm:"column:agent_config;type:jsonb"`

	// // Strategy configuration
	// KnowledgeBaseID   string              `json:"knowledge_base_id"`                    // 关联的知识库ID
	// MaxRounds         int                 `json:"max_rounds"`                           // 多轮保持轮数
	// EnableRewrite     bool                `json:"enable_rewrite"`                       // 多轮改写开关
	// FallbackStrategy  FallbackStrategy    `json:"fallback_strategy"`                    // 兜底策略
	// FallbackResponse  string              `json:"fallback_response"`                    // 固定回复内容
	// EmbeddingTopK     int                 `json:"embedding_top_k"`                      // 向量召回TopK
	// KeywordThreshold  float64             `json:"keyword_threshold"`                    // 关键词召回阈值
	// VectorThreshold   float64             `json:"vector_threshold"`                     // 向量召回阈值
	// RerankModelID     string              `json:"rerank_model_id"`                      // 排序模型ID
	// RerankTopK        int                 `json:"rerank_top_k"`                         // 排序TopK
	// RerankThreshold   float64             `json:"rerank_threshold"`                     // 排序阈值
	// SummaryModelID    string              `json:"summary_model_id"`                     // 总结模型ID
	// SummaryParameters *SummaryConfig      `json:"summary_parameters" gorm:"type:json"`  // 总结模型参数
	// AgentConfig       *SessionAgentConfig `json:"agent_config"       gorm:"type:jsonb"` // Agent 配置（会话级别，仅存储enabled和knowledge_bases）
	// ContextConfig     *ContextConfig      `json:"context_config"     gorm:"type:jsonb"` // 上下文管理配置（可选）

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`

	// IMPlatform is the originating IM platform (e.g. "feishu", "wecom") when
	// this session is bound to an IM channel. It is not stored on the sessions
	// table (it lives in im_channel_sessions) and is populated on read so the
	// Web console can classify a session's origin folder without a list query.
	IMPlatform string `json:"im_platform,omitempty" gorm:"-"`

	// Association relationship, not stored in the database
	Messages []Message `json:"-" gorm:"foreignKey:SessionID"`
}

func (s *Session) BeforeCreate(tx *gorm.DB) (err error) {
	s.ID = uuid.New().String()
	return nil
}

// SessionSourceAPI is the source filter that lists every session created via a
// tenant API key across the whole tenant. It is an admin-only view: the service
// layer requires Admin+ and drops the per-user owner scope so a tenant
// Owner/admin can observe API-key traffic that is otherwise isolated per key.
const SessionSourceAPI = "api"

// SessionSourceWeb is the source filter for a user's own Web-console chats.
const SessionSourceWeb = "web"

// SessionListSourceRequiresAdmin reports whether a session-list source filter
// exposes tenant-wide channel traffic (API / IM / embed) in the Web console.
func SessionListSourceRequiresAdmin(source string) bool {
	src := strings.TrimSpace(source)
	if src == "" || strings.EqualFold(src, SessionSourceWeb) {
		return false
	}
	return true
}

// SessionRequiresAdminConsoleRead reports whether a session row is channel-
// managed traffic that non-admin web users must not open from the console.
func SessionRequiresAdminConsoleRead(s *Session, imPlatform string) bool {
	if s == nil {
		return false
	}
	if strings.HasPrefix(s.UserID, SessionOwnerAPITenantKeyPrefix) {
		return true
	}
	if strings.HasPrefix(s.Description, EmbedSessionMarkerPrefix) ||
		strings.HasPrefix(s.UserID, PrincipalEmbedSession+":") {
		return true
	}
	return strings.TrimSpace(imPlatform) != ""
}

// SessionListQuery bundles the parameters for listing sessions.
// UserID empty means "tenant-wide" (used by API-key callers / legacy rows).
// Keyword matches title ILIKE '%keyword%'.
// Source values: "web" (user chats, no IM/embed), "embed" / "embed:{channelID}",
// "api" (all API-key sessions, Admin+ only), or an IM platform name
// (e.g. "feishu", "wechat"). IM and embed sources are also Admin+ only.
// AgentID currently only filters sessions that have an IM channel mapping.
type SessionListQuery struct {
	TenantID uint64
	UserID   string
	Keyword  string
	Source   string
	AgentID  string
	Page     int
	PageSize int
}

// SessionListItem is a session row enriched with its IM origin (when any).
// IM-related fields are populated from the im_channel_sessions table via LEFT JOIN
// and are empty for Web-created sessions.
type SessionListItem struct {
	Session
	IMPlatform  string `json:"im_platform,omitempty"   gorm:"column:im_platform"`
	IMChatID    string `json:"im_chat_id,omitempty"    gorm:"column:im_chat_id"`
	IMThreadID  string `json:"im_thread_id,omitempty"  gorm:"column:im_thread_id"`
	IMUserID    string `json:"im_user_id,omitempty"    gorm:"column:im_user_id"`
	IMAgentID   string `json:"im_agent_id,omitempty"   gorm:"column:im_agent_id"`
	IMChannelID string `json:"im_channel_id,omitempty" gorm:"column:im_channel_id"`
}

// StringArray represents a list of strings
type StringArray []string

// Value implements the driver.Valuer interface, used to convert StringArray to database value
func (c StringArray) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface, used to convert database value to StringArray
func (c *StringArray) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// Value implements the driver.Valuer interface, used to convert SummaryConfig to database value
func (c *SummaryConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface, used to convert database value to SummaryConfig
func (c *SummaryConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}

// SessionLastRequestState captures the user-facing input-bar state at the
// time of the most recent QA request on a session. It is purely a UI memory
// aid — none of the fields here drive backend behaviour. They are echoed back
// to the frontend by GetSession so the chat input can restore the same agent,
// model, KB scope, etc. the user had selected last time.
type SessionLastRequestState struct {
	AgentID          string         `json:"agent_id,omitempty"`
	AgentEnabled     bool           `json:"agent_enabled"`
	ModelID          string         `json:"model_id,omitempty"`
	KnowledgeBaseIDs []string       `json:"knowledge_base_ids,omitempty"`
	KnowledgeIDs     []string       `json:"knowledge_ids,omitempty"`
	TagIDs           []string       `json:"tag_ids,omitempty"`
	MCPServiceIDs    []string       `json:"mcp_service_ids,omitempty"`
	SkillNames       []string       `json:"skill_names,omitempty"`
	MentionedItems   MentionedItems `json:"mentioned_items,omitempty"`
	WebSearchEnabled bool           `json:"web_search_enabled"`
}

// Value implements driver.Valuer for SessionLastRequestState (JSONB).
func (s *SessionLastRequestState) Value() (driver.Value, error) {
	if s == nil {
		return nil, nil
	}
	return json.Marshal(s)
}

// Scan implements sql.Scanner for SessionLastRequestState (JSONB).
// Tolerates legacy values that may not match the current schema by silently
// ignoring unmarshal errors — the stored row predates this struct.
func (s *SessionLastRequestState) Scan(value interface{}) error {
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
	if err := json.Unmarshal(b, s); err != nil {
		// Tolerate legacy shapes from before this column was repurposed.
		return nil
	}
	return nil
}

// Value implements the driver.Valuer interface, used to convert ContextConfig to database value
func (c *ContextConfig) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface, used to convert database value to ContextConfig
func (c *ContextConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}
