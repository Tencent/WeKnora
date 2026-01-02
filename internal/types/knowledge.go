package types

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	// KnowledgeTypeManual 수동 지식 유형을 나타냅니다
	KnowledgeTypeManual = "manual"
	// KnowledgeTypeFAQ FAQ 지식 유형을 나타냅니다
	KnowledgeTypeFAQ = "faq"
)

// 지식 파싱 상태 상수
const (
	// ParseStatusPending 지식이 처리 대기 중임을 나타냅니다
	ParseStatusPending = "pending"
	// ParseStatusProcessing 지식이 처리 중임을 나타냅니다
	ParseStatusProcessing = "processing"
	// ParseStatusCompleted 지식이 성공적으로 처리되었음을 나타냅니다
	ParseStatusCompleted = "completed"
	// ParseStatusFailed 지식 처리가 실패했음을 나타냅니다
	ParseStatusFailed = "failed"
	// ParseStatusDeleting 지식이 삭제 중임을 나타냅니다 (비동기 작업 충돌 방지에 사용)
	ParseStatusDeleting = "deleting"
)

// 비동기 요약 생성을 위한 요약 상태 상수
const (
	// SummaryStatusNone 요약 작업이 필요 없음을 나타냅니다
	SummaryStatusNone = "none"
	// SummaryStatusPending 요약 작업이 처리 대기 중임을 나타냅니다
	SummaryStatusPending = "pending"
	// SummaryStatusProcessing 요약이 생성 중임을 나타냅니다
	SummaryStatusProcessing = "processing"
	// SummaryStatusCompleted 요약이 성공적으로 생성되었음을 나타냅니다
	SummaryStatusCompleted = "completed"
	// SummaryStatusFailed 요약 생성이 실패했음을 나타냅니다
	SummaryStatusFailed = "failed"
)

// ManualKnowledgeFormat 수동 지식의 형식을 나타냅니다
const (
	ManualKnowledgeFormatMarkdown = "markdown"
	ManualKnowledgeStatusDraft    = "draft"
	ManualKnowledgeStatusPublish  = "publish"
)

// Knowledge 시스템의 지식 엔티티를 나타냅니다.
// 지식 소스, 처리 상태 및 해당되는 경우 실제 파일에 대한 참조에 대한 메타데이터를 포함합니다.
type Knowledge struct {
	// 지식의 고유 식별자
	ID string `json:"id"                 gorm:"type:varchar(36);primaryKey"`
	// 테넌트 ID
	TenantID uint64 `json:"tenant_id"`
	// 지식베이스 ID
	KnowledgeBaseID string `json:"knowledge_base_id"`
	// 지식베이스 내 분류를 위한 선택적 태그 ID
	TagID string `json:"tag_id"             gorm:"type:varchar(36);index"`
	// 지식 유형
	Type string `json:"type"`
	// 지식 제목
	Title string `json:"title"`
	// 지식 설명
	Description string `json:"description"`
	// 지식 소스
	Source string `json:"source"`
	// 지식 파싱 상태
	ParseStatus string `json:"parse_status"`
	// 비동기 요약 생성을 위한 요약 상태
	SummaryStatus string `json:"summary_status"     gorm:"type:varchar(32);default:none"`
	// 지식 활성화 상태
	EnableStatus string `json:"enable_status"`
	// 임베딩 모델 ID
	EmbeddingModelID string `json:"embedding_model_id"`
	// 지식 파일 이름
	FileName string `json:"file_name"`
	// 지식 파일 유형
	FileType string `json:"file_type"`
	// 지식 파일 크기
	FileSize int64 `json:"file_size"`
	// 지식 파일 해시
	FileHash string `json:"file_hash"`
	// 지식 파일 경로
	FilePath string `json:"file_path"`
	// 지식 저장소 크기
	StorageSize int64 `json:"storage_size"`
	// 지식 메타데이터
	Metadata JSON `json:"metadata"           gorm:"type:json"`
	// 지식 생성 시간
	CreatedAt time.Time `json:"created_at"`
	// 지식 마지막 업데이트 시간
	UpdatedAt time.Time `json:"updated_at"`
	// 지식 처리 시간
	ProcessedAt *time.Time `json:"processed_at"`
	// 지식 오류 메시지
	ErrorMessage string `json:"error_message"`
	// 지식 삭제 시간
	DeletedAt gorm.DeletedAt `json:"deleted_at"         gorm:"index"`
	// 지식베이스 이름 (데이터베이스에 저장되지 않음, 쿼리 시 채워짐)
	KnowledgeBaseName string `json:"knowledge_base_name" gorm:"-"`
}

// GetMetadata 메타데이터를 map[string]string으로 반환합니다.
func (k *Knowledge) GetMetadata() map[string]string {
	metadata := make(map[string]string)
	metadataMap, err := k.Metadata.Map()
	if err != nil {
		return nil
	}
	for k, v := range metadataMap {
		metadata[k] = fmt.Sprintf("%v", v)
	}
	return metadata
}

// BeforeCreate 훅은 생성되기 전에 새 Knowledge 엔티티에 대한 UUID를 생성합니다.
func (k *Knowledge) BeforeCreate(tx *gorm.DB) (err error) {
	if k.ID == "" {
		k.ID = uuid.New().String()
	}
	return nil
}

// ManualKnowledgeMetadata 수동 마크다운 지식 콘텐츠에 대한 메타데이터를 저장합니다.
type ManualKnowledgeMetadata struct {
	Content   string `json:"content"`
	Format    string `json:"format"`
	Status    string `json:"status"`
	Version   int    `json:"version"`
	UpdatedAt string `json:"updated_at"`
}

// ManualKnowledgePayload 수동 지식 작업을 위한 페이로드를 나타냅니다.
type ManualKnowledgePayload struct {
	Title   string `json:"title"`
	Content string `json:"content"`
	Status  string `json:"status"`
}

// NewManualKnowledgeMetadata 새로운 ManualKnowledgeMetadata 인스턴스를 생성합니다.
func NewManualKnowledgeMetadata(content, status string, version int) *ManualKnowledgeMetadata {
	if version <= 0 {
		version = 1
	}
	return &ManualKnowledgeMetadata{
		Content:   content,
		Format:    ManualKnowledgeFormatMarkdown,
		Status:    status,
		Version:   version,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
}

// ToJSON 메타데이터를 JSON 타입으로 변환합니다.
func (m *ManualKnowledgeMetadata) ToJSON() (JSON, error) {
	if m == nil {
		return nil, nil
	}
	if m.Format == "" {
		m.Format = ManualKnowledgeFormatMarkdown
	}
	if m.Status == "" {
		m.Status = ManualKnowledgeStatusDraft
	}
	if m.Version <= 0 {
		m.Version = 1
	}
	if m.UpdatedAt == "" {
		m.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	bytes, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return JSON(bytes), nil
}

// ManualMetadata 수동 지식 메타데이터를 파싱하여 반환합니다.
func (k *Knowledge) ManualMetadata() (*ManualKnowledgeMetadata, error) {
	if len(k.Metadata) == 0 {
		return nil, nil
	}
	var metadata ManualKnowledgeMetadata
	if err := json.Unmarshal(k.Metadata, &metadata); err != nil {
		return nil, err
	}
	if metadata.Format == "" {
		metadata.Format = ManualKnowledgeFormatMarkdown
	}
	if metadata.Version <= 0 {
		metadata.Version = 1
	}
	return &metadata, nil
}

// SetManualMetadata 지식 인스턴스에 수동 지식 메타데이터를 설정합니다.
func (k *Knowledge) SetManualMetadata(meta *ManualKnowledgeMetadata) error {
	if meta == nil {
		k.Metadata = nil
		return nil
	}
	jsonValue, err := meta.ToJSON()
	if err != nil {
		return err
	}
	k.Metadata = jsonValue
	return nil
}

// IsManual 지식 항목이 수동 마크다운 지식인지 여부를 반환합니다.
func (k *Knowledge) IsManual() bool {
	return k != nil && k.Type == KnowledgeTypeManual
}

// EnsureManualDefaults 수동 지식 항목에 대한 기본값을 설정합니다.
func (k *Knowledge) EnsureManualDefaults() {
	if k == nil {
		return
	}
	if k.Type == "" {
		k.Type = KnowledgeTypeManual
	}
	if k.FileType == "" {
		k.FileType = KnowledgeTypeManual
	}
	if k.Source == "" {
		k.Source = KnowledgeTypeManual
	}
}

// IsDraft 페이로드를 초안으로 저장해야 하는지 여부를 반환합니다.
func (p ManualKnowledgePayload) IsDraft() bool {
	return p.Status == "" || p.Status == ManualKnowledgeStatusDraft
}

// KnowledgeCheckParams 지식이 이미 존재하는지 확인하는 데 사용되는 매개변수를 정의합니다.
type KnowledgeCheckParams struct {
	// 파일 매개변수
	FileName string
	FileSize int64
	FileHash string
	// URL 매개변수
	URL string
	// 텍스트 구절 매개변수
	Passages []string
	// 지식 유형
	Type string
}
