package types

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	TemporaryDocumentStatusUploaded   = "uploaded"
	TemporaryDocumentStatusProcessing = "processing"
	TemporaryDocumentStatusReady      = "ready"
	TemporaryDocumentStatusFailed     = "failed"

	// MaxTemporaryAttachmentsPerMessage is the maximum number of pre-uploaded
	// temporary attachment IDs a single QA turn may reference.
	MaxTemporaryAttachmentsPerMessage = 5
)

// TemporaryDocument is a session-scoped, expiring document used by chat.
// Parsed artifacts are retained separately from the source file so a question
// can select only the useful parts without parsing the upload again.
type TemporaryDocument struct {
	ID                string         `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID          uint64         `json:"tenant_id" gorm:"not null;index"`
	SessionID         string         `json:"session_id" gorm:"type:varchar(36);not null;index"`
	ResourceRef       string         `json:"-" gorm:"type:text;not null"`
	FileName          string         `json:"file_name" gorm:"type:varchar(1024);not null"`
	FileType          string         `json:"file_type" gorm:"type:varchar(32);not null"`
	MimeType          string         `json:"mime_type" gorm:"type:varchar(255);not null;default:''"`
	FileSize          int64          `json:"file_size" gorm:"not null"`
	Status            string         `json:"status" gorm:"type:varchar(16);not null;index"`
	Content           string         `json:"-" gorm:"type:text"`
	Chunks            JSON           `json:"-" gorm:"type:jsonb;not null;default:'[]'"`
	ImageRefs         JSON           `json:"image_refs,omitempty" gorm:"type:jsonb;not null;default:'[]'"`
	Metadata          JSON           `json:"metadata,omitempty" gorm:"type:jsonb;not null;default:'{}'"`
	ProcessingOptions JSON           `json:"-" gorm:"type:jsonb;not null;default:'{}'"`
	TokenCount        int            `json:"token_count" gorm:"not null;default:0"`
	ChunkCount        int            `json:"chunk_count" gorm:"not null;default:0"`
	ErrorMessage      string         `json:"error_message,omitempty" gorm:"type:text"`
	ExpiresAt         time.Time      `json:"expires_at" gorm:"not null;index"`
	StartedAt         *time.Time     `json:"started_at,omitempty"`
	ReadyAt           *time.Time     `json:"ready_at,omitempty"`
	CreatedAt         time.Time      `json:"created_at"`
	UpdatedAt         time.Time      `json:"updated_at"`
	DeletedAt         gorm.DeletedAt `json:"-" gorm:"index"`
}

func (TemporaryDocument) TableName() string { return "temporary_documents" }

func (d *TemporaryDocument) BeforeCreate(_ *gorm.DB) error {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	if d.Status == "" {
		d.Status = TemporaryDocumentStatusUploaded
	}
	if len(d.Chunks) == 0 {
		d.Chunks = JSON(`[]`)
	}
	if len(d.ImageRefs) == 0 {
		d.ImageRefs = JSON(`[]`)
	}
	if len(d.Metadata) == 0 {
		d.Metadata = JSON(`{}`)
	}
	if len(d.ProcessingOptions) == 0 {
		d.ProcessingOptions = JSON(`{}`)
	}
	return nil
}

type TemporaryDocumentChunk struct {
	Seq           int    `json:"seq"`
	Content       string `json:"content"`
	ContextHeader string `json:"context_header,omitempty"`
	Start         int    `json:"start"`
	End           int    `json:"end"`
	TokenCount    int    `json:"token_count"`
}

type TemporaryDocumentImage struct {
	OriginalRef string `json:"original_ref,omitempty"`
	URL         string `json:"url"`
	MimeType    string `json:"mime_type,omitempty"`
}

type TemporaryDocumentTaskPayload struct {
	TenantID   uint64 `json:"tenant_id"`
	DocumentID string `json:"document_id"`
}

type TemporaryDocumentCreateOptions struct {
	ASRModelID   string `json:"asr_model_id,omitempty"`
	ParserEngine string `json:"parser_engine,omitempty"`
	// VLMModelID enables image understanding (caption/OCR) during async parse.
	// Images use it to produce real text content; scanned/image-only documents
	// use it as an OCR fallback when ImageUnderstanding is on.
	VLMModelID string `json:"vlm_model_id,omitempty"`
	// ImageUnderstanding turns on the VLM OCR fallback for image-only /
	// scanned documents whose extracted text falls below a threshold.
	ImageUnderstanding bool `json:"image_understanding,omitempty"`
	// OCRMaxPages overrides the global VLM OCR page cap for this document.
	// 0 uses the global default (WEKNORA_CHAT_ATTACHMENT_OCR_MAX_PAGES).
	OCRMaxPages int `json:"ocr_max_pages,omitempty"`
}

type TemporaryDocumentPromptResult struct {
	Attachments MessageAttachments
	ImageURLs   []string
}
