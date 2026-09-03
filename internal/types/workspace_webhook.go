package types

import (
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

const (
	WorkspaceWebhookSpecVersion = "1"

	EventKnowledgeCreated        = "knowledge.created"
	EventKnowledgeParseCompleted = "knowledge.parse_completed"
	EventKnowledgeParseFailed    = "knowledge.parse_failed"
	EventKnowledgeDeleted        = "knowledge.deleted"
	EventKnowledgeBatchDeleted   = "knowledge.batch_deleted"
	EventKBCreated               = "kb.created"
	EventKBDeleted               = "kb.deleted"
	EventRBACMemberAdded         = "rbac.member_added"
	EventRBACMemberRemoved       = "rbac.member_removed"
	EventWebhookTest             = "webhook.test"

	WebhookOutboxPending    = "pending"
	WebhookOutboxProcessed  = "processed"
	WebhookOutboxFailed     = "failed"
	WebhookDeliveryPending  = "pending"
	WebhookDeliverySuccess  = "success"
	WebhookDeliveryFailed   = "failed"

	WebhookUserAgent             = "WeKnora-Workspace-Webhook/1.0"
	WebhookDownloadTicketHeader  = "X-WeKnora-Download-Ticket"
	WebhookDownloadPathPrefix    = "/api/v1/files/knowledge-download/"
	WebhookBatchDeletedChunkSize = 100
	WebhookMaxEndpointsPerTenant = 5
	WebhookMinSecretLength       = 16
	WebhookInFlightLimit         = 20
	WebhookOutboxInsertRetries   = 3
	WebhookOutboxSweepMaxAttempts = 50
	WebhookDeliveryKeepPerEndpoint = 50

	// Webhook subscription index (Emit gate): Redis SET of subscribed types.
	WebhookSubRedisKeyPrefix   = "weknora:webhook:sub:"
	WebhookSubEmptyMarker      = "__none__"
	WebhookSubPositiveTTL      = 30 * time.Minute
	WebhookSubNegativeTTL      = 5 * time.Minute

	KnowledgeTypeFile    = "file"
	KnowledgeTypeURL     = "url"
	KnowledgeTypeFileURL = "file_url"
	KnowledgeTypePassage = "passage"
)

// P0WebhookEventTypes is the subscribe-able catalog shown in settings.
func P0WebhookEventTypes() []string {
	return []string{
		EventKnowledgeCreated,
		EventKnowledgeParseCompleted,
		EventKnowledgeParseFailed,
		EventKnowledgeDeleted,
		EventKnowledgeBatchDeleted,
		EventKBCreated,
		EventKBDeleted,
		EventRBACMemberAdded,
		EventRBACMemberRemoved,
	}
}

// WorkspaceWebhookEnvelope is the outbound JSON body. Field set is fixed; empty
// values stay present so receivers can switch only on top-level type.
type WorkspaceWebhookEnvelope struct {
	SpecVersion string          `json:"spec_version"`
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Time        string          `json:"time"`
	TenantID    uint64          `json:"tenant_id"`
	ActorUserID string          `json:"actor_user_id"`
	RequestID   string          `json:"request_id"`
	Data        json.RawMessage `json:"data"`
}

type WebhookKnowledgeData struct {
	Resource         string              `json:"resource"`
	KnowledgeID      string              `json:"knowledge_id"`
	KnowledgeBaseID  string              `json:"knowledge_base_id"`
	OwnerTenantID    uint64              `json:"owner_tenant_id"`
	Title            string              `json:"title"`
	KnowledgeType    string              `json:"knowledge_type"`
	Source           string              `json:"source"`
	FileType         string              `json:"file_type"`
	ParseStatus      string              `json:"parse_status"`
	EnableStatus     string              `json:"enable_status"`
	FolderPath       string              `json:"folder_path"`
	ErrorMessage     string              `json:"error_message"`
	Deleted          bool                `json:"deleted"`
	KnowledgeIDs     []string            `json:"knowledge_ids"`
	Count            int                 `json:"count"`
	TotalCount       int                 `json:"total_count"`
	BatchIndex       int                 `json:"batch_index"`
	BatchTotal       int                 `json:"batch_total"`
	DeleteBatchID    string              `json:"delete_batch_id"`
	Truncated        bool                `json:"truncated"`
	Download         WebhookDownloadInfo `json:"download"`
}

type WebhookDownloadInfo struct {
	Available        bool   `json:"available"`
	Reason           string `json:"reason"`
	HTTPMethod       string `json:"http_method"`
	Path             string `json:"path"`
	Ticket           string `json:"ticket"`
	TicketExpiresAt  string `json:"ticket_expires_at"`
	TicketHeader     string `json:"ticket_header"`
}

type WebhookKBData struct {
	Resource            string `json:"resource"`
	KnowledgeBaseID     string `json:"knowledge_base_id"`
	Name                string `json:"name"`
	Visibility          string `json:"visibility"`
	CascadeKnowledge    bool   `json:"cascade_knowledge"`
	KnowledgeCount      int64  `json:"knowledge_count"`
	UnavailableToTenant bool   `json:"unavailable_to_tenant"`
	ShareID             string `json:"share_id"`
	SourceTenantID      uint64 `json:"source_tenant_id"`
}

type WebhookMemberData struct {
	Resource string `json:"resource"`
	UserID   string `json:"user_id"`
	Role     string `json:"role"`
	Reason   string `json:"reason"`
	Email    string `json:"email"`
}

type WebhookTestData struct {
	Resource string `json:"resource"`
	OK       bool   `json:"ok"`
}

type WorkspaceEvent struct {
	Type        string
	TenantID    uint64
	ActorUserID string
	RequestID   string
	Data        any
}

type TenantWebhookEndpoint struct {
	ID          string         `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID    uint64         `json:"tenant_id"`
	Name        string         `json:"name" gorm:"type:varchar(64);not null;default:''"`
	URL         string         `json:"url" gorm:"type:varchar(512);not null"`
	SecretEnc   string         `json:"-" gorm:"column:secret_enc;type:text;not null;default:''"`
	Events      WebhookEvents  `json:"events" gorm:"type:jsonb;serializer:json"`
	Enabled     bool           `json:"enabled" gorm:"not null;default:true"`
	Description string         `json:"description" gorm:"type:varchar(256);not null;default:''"`
	CreatedBy   string         `json:"created_by" gorm:"type:varchar(36);not null;default:''"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `json:"deleted_at,omitempty" gorm:"index"`
}

func (TenantWebhookEndpoint) TableName() string { return "tenant_webhook_endpoints" }

type WebhookEvents []string

func (e WebhookEvents) Contains(eventType string) bool {
	for _, item := range e {
		if item == eventType {
			return true
		}
	}
	return false
}

type TenantWebhookOutbox struct {
	ID            int64           `json:"id" gorm:"primaryKey"`
	EventID       string          `json:"event_id" gorm:"type:varchar(64);not null"`
	EventType     string          `json:"event_type" gorm:"type:varchar(64);not null"`
	OwnerTenantID uint64          `json:"owner_tenant_id"`
	Payload       json.RawMessage `json:"payload" gorm:"type:jsonb;not null"`
	Status        string          `json:"status" gorm:"type:varchar(16);not null;default:pending"`
	Attempts      int             `json:"attempts" gorm:"not null;default:0"`
	LastError     string          `json:"last_error" gorm:"type:varchar(512);not null;default:''"`
	CreatedAt     time.Time       `json:"created_at"`
	ProcessedAt   *time.Time      `json:"processed_at"`
}

func (TenantWebhookOutbox) TableName() string { return "tenant_webhook_outbox" }

type TenantWebhookDelivery struct {
	ID         int64      `json:"id" gorm:"primaryKey"`
	DeliveryID string     `json:"delivery_id" gorm:"type:varchar(64);not null"`
	EndpointID string     `json:"endpoint_id" gorm:"type:varchar(36);not null"`
	TenantID   uint64     `json:"tenant_id"`
	EventID    string     `json:"event_id" gorm:"type:varchar(64);not null"`
	EventType  string     `json:"event_type" gorm:"type:varchar(64);not null"`
	Status     string     `json:"status" gorm:"type:varchar(16);not null"`
	HTTPStatus int        `json:"http_status" gorm:"not null;default:0"`
	Attempts   int        `json:"attempts" gorm:"not null;default:0"`
	Error      string     `json:"error" gorm:"type:varchar(512);not null;default:''"`
	DurationMs int        `json:"duration_ms" gorm:"not null;default:0"`
	CreatedAt  time.Time  `json:"created_at"`
	FinishedAt *time.Time `json:"finished_at"`
}

func (TenantWebhookDelivery) TableName() string { return "tenant_webhook_deliveries" }

type WebhookDeliverPayload struct {
	EventID    string `json:"event_id"`
	EndpointID string `json:"endpoint_id"`
	TenantID   uint64 `json:"tenant_id"`
	DeliveryID string `json:"delivery_id"`
}

type WebhookEndpointPublic struct {
	ID          string    `json:"id"`
	TenantID    uint64    `json:"tenant_id"`
	Name        string    `json:"name"`
	URL         string    `json:"url"`
	Events      []string  `json:"events"`
	Enabled     bool      `json:"enabled"`
	Description string    `json:"description"`
	HasSecret   bool      `json:"has_secret"`
	CreatedBy   string    `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (e *TenantWebhookEndpoint) Public() WebhookEndpointPublic {
	if e == nil {
		return WebhookEndpointPublic{}
	}
	events := append([]string(nil), e.Events...)
	return WebhookEndpointPublic{
		ID:          e.ID,
		TenantID:    e.TenantID,
		Name:        e.Name,
		URL:         e.URL,
		Events:      events,
		Enabled:     e.Enabled,
		Description: e.Description,
		HasSecret:   e.SecretEnc != "",
		CreatedBy:   e.CreatedBy,
		CreatedAt:   e.CreatedAt,
		UpdatedAt:   e.UpdatedAt,
	}
}

func KnowledgeNeedsDownloadTicket(knowledgeType, filePath string, deleted bool) bool {
	if deleted || filePath == "" {
		return false
	}
	return knowledgeType == KnowledgeTypeFile || knowledgeType == KnowledgeTypeFileURL
}
