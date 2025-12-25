package types

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// FAQChunkMetadata 定义 FAQ 条目在 Chunk.Metadata 中的结构
type FAQChunkMetadata struct {
	StandardQuestion  string         `json:"standard_question"`
	SimilarQuestions  []string       `json:"similar_questions,omitempty"`
	NegativeQuestions []string       `json:"negative_questions,omitempty"`
	Answers           []string       `json:"answers,omitempty"`
	AnswerStrategy    AnswerStrategy `json:"answer_strategy,omitempty"`
	Version           int            `json:"version,omitempty"`
	Source            string         `json:"source,omitempty"`
}

// GeneratedQuestion 表示AI生成的单个问题
type GeneratedQuestion struct {
	ID       string `json:"id"`       // 唯一标识，用于构造 source_id
	Question string `json:"question"` // 问题内容
}

// DocumentChunkMetadata 定义文档 Chunk 的元数据结构
// 用于存储AI生成的问题等增强信息
type DocumentChunkMetadata struct {
	// GeneratedQuestions 存储AI为该Chunk生成的相关问题
	// 这些问题会被独立索引以提高召回率
	GeneratedQuestions []GeneratedQuestion `json:"generated_questions,omitempty"`
}

// GetQuestionStrings 返回问题内容字符串列表（兼容旧代码）
func (m *DocumentChunkMetadata) GetQuestionStrings() []string {
	if m == nil || len(m.GeneratedQuestions) == 0 {
		return nil
	}
	result := make([]string, len(m.GeneratedQuestions))
	for i, q := range m.GeneratedQuestions {
		result[i] = q.Question
	}
	return result
}

// DocumentMetadata 解析 Chunk 中的文档元数据
func (c *Chunk) DocumentMetadata() (*DocumentChunkMetadata, error) {
	if c == nil || len(c.Metadata) == 0 {
		return nil, nil
	}
	var meta DocumentChunkMetadata
	if err := json.Unmarshal(c.Metadata, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}

// SetDocumentMetadata 设置 Chunk 的文档元数据
func (c *Chunk) SetDocumentMetadata(meta *DocumentChunkMetadata) error {
	if c == nil {
		return nil
	}
	if meta == nil {
		c.Metadata = nil
		return nil
	}
	bytes, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	c.Metadata = JSON(bytes)
	return nil
}

// Normalize 清理空白与重复项
func (m *FAQChunkMetadata) Normalize() {
	if m == nil {
		return
	}
	m.StandardQuestion = strings.TrimSpace(m.StandardQuestion)
	m.SimilarQuestions = normalizeStrings(m.SimilarQuestions)
	m.NegativeQuestions = normalizeStrings(m.NegativeQuestions)
	m.Answers = normalizeStrings(m.Answers)
	if m.Version <= 0 {
		m.Version = 1
	}
}

// FAQMetadata 解析 Chunk 中的 FAQ 元数据
func (c *Chunk) FAQMetadata() (*FAQChunkMetadata, error) {
	if c == nil || len(c.Metadata) == 0 {
		return nil, nil
	}
	var meta FAQChunkMetadata
	if err := json.Unmarshal(c.Metadata, &meta); err != nil {
		return nil, err
	}
	meta.Normalize()
	return &meta, nil
}

// SetFAQMetadata 设置 Chunk 的 FAQ 元数据
func (c *Chunk) SetFAQMetadata(meta *FAQChunkMetadata) error {
	if c == nil {
		return nil
	}
	if meta == nil {
		c.Metadata = nil
		c.ContentHash = ""
		return nil
	}
	meta.Normalize()
	bytes, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	c.Metadata = JSON(bytes)
	// 计算并设置 ContentHash
	c.ContentHash = CalculateFAQContentHash(meta)
	return nil
}

// CalculateFAQContentHash 计算 FAQ 内容的 hash 值
// hash 基于：标准问 + 相似问（排序后）+ 反例（排序后）+ 答案（排序后）
// 用于快速匹配和去重
func CalculateFAQContentHash(meta *FAQChunkMetadata) string {
	if meta == nil {
		return ""
	}

	// 创建副本并标准化
	normalized := *meta
	normalized.Normalize()

	// 对数组进行排序（确保相同内容产生相同 hash）
	similarQuestions := make([]string, len(normalized.SimilarQuestions))
	copy(similarQuestions, normalized.SimilarQuestions)
	sort.Strings(similarQuestions)

	negativeQuestions := make([]string, len(normalized.NegativeQuestions))
	copy(negativeQuestions, normalized.NegativeQuestions)
	sort.Strings(negativeQuestions)

	answers := make([]string, len(normalized.Answers))
	copy(answers, normalized.Answers)
	sort.Strings(answers)

	// 构建用于 hash 的字符串：标准问 + 相似问 + 反例 + 答案
	var builder strings.Builder
	builder.WriteString(normalized.StandardQuestion)
	builder.WriteString("|")
	builder.WriteString(strings.Join(similarQuestions, ","))
	builder.WriteString("|")
	builder.WriteString(strings.Join(negativeQuestions, ","))
	builder.WriteString("|")
	builder.WriteString(strings.Join(answers, ","))

	// 计算 SHA256 hash
	hash := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(hash[:])
}

// AnswerStrategy 定义答案返回策略
type AnswerStrategy string

const (
	// AnswerStrategyAll 返回所有答案
	AnswerStrategyAll AnswerStrategy = "all"
	// AnswerStrategyRandom 随机返回一个答案
	AnswerStrategyRandom AnswerStrategy = "random"
)

// FAQEntry 表示返回给前端的 FAQ 条目
type FAQEntry struct {
	ID                string         `json:"id"`
	ChunkID           string         `json:"chunk_id"`
	KnowledgeID       string         `json:"knowledge_id"`
	KnowledgeBaseID   string         `json:"knowledge_base_id"`
	TagID             string         `json:"tag_id"`
	TagName           string         `json:"tag_name"`
	IsEnabled         bool           `json:"is_enabled"`
	IsRecommended     bool           `json:"is_recommended"`
	StandardQuestion  string         `json:"standard_question"`
	SimilarQuestions  []string       `json:"similar_questions"`
	NegativeQuestions []string       `json:"negative_questions"`
	Answers           []string       `json:"answers"`
	AnswerStrategy    AnswerStrategy `json:"answer_strategy"`
	IndexMode         FAQIndexMode   `json:"index_mode"`
	UpdatedAt         time.Time      `json:"updated_at"`
	CreatedAt         time.Time      `json:"created_at"`
	Score             float64        `json:"score,omitempty"`
	MatchType         MatchType      `json:"match_type,omitempty"`
	ChunkType         ChunkType      `json:"chunk_type"`
}

// FAQEntryPayload 用于创建/更新 FAQ 条目的 payload
type FAQEntryPayload struct {
	StandardQuestion  string          `json:"standard_question"    binding:"required"`
	SimilarQuestions  []string        `json:"similar_questions"`
	NegativeQuestions []string        `json:"negative_questions"`
	Answers           []string        `json:"answers"              binding:"required"`
	AnswerStrategy    *AnswerStrategy `json:"answer_strategy,omitempty"`
	TagID             string          `json:"tag_id"`
	TagName           string          `json:"tag_name"`
	IsEnabled         *bool           `json:"is_enabled,omitempty"`
	IsRecommended     *bool           `json:"is_recommended,omitempty"`
}

const (
	FAQBatchModeAppend  = "append"
	FAQBatchModeReplace = "replace"
)

// FAQBatchUpsertPayload 批量导入 FAQ 条目
type FAQBatchUpsertPayload struct {
	Entries     []FAQEntryPayload `json:"entries"      binding:"required"`
	Mode        string            `json:"mode"         binding:"oneof=append replace"`
	KnowledgeID string            `json:"knowledge_id"`
}

// FAQSearchRequest FAQ检索请求参数
type FAQSearchRequest struct {
	QueryText       string  `json:"query_text"       binding:"required"`
	VectorThreshold float64 `json:"vector_threshold"`
	MatchCount      int     `json:"match_count"`
}

// UntaggedTagID is the special tag ID representing uncategorized entries
const UntaggedTagID = "__untagged__"

// FAQEntryFieldsUpdate 单个FAQ条目的字段更新
type FAQEntryFieldsUpdate struct {
	IsEnabled     *bool   `json:"is_enabled,omitempty"`
	IsRecommended *bool   `json:"is_recommended,omitempty"`
	TagID         *string `json:"tag_id,omitempty"`
	// 后续可扩展更多字段
}

// FAQEntryFieldsBatchUpdate 批量更新FAQ条目字段的请求
// 支持两种模式：
// 1. 按条目ID更新：使用 ByID 字段
// 2. 按Tag更新：使用 ByTag 字段，将该Tag下所有条目应用相同的更新
type FAQEntryFieldsBatchUpdate struct {
	// ByID 按条目ID更新，key为条目ID
	ByID map[string]FAQEntryFieldsUpdate `json:"by_id,omitempty"`
	// ByTag 按Tag批量更新，key为TagID（__untagged__表示未分类）
	ByTag map[string]FAQEntryFieldsUpdate `json:"by_tag,omitempty"`
	// ExcludeIDs 在ByTag操作中需要排除的ID列表
	ExcludeIDs []string `json:"exclude_ids,omitempty"`
}

// FAQImportTaskStatus 导入任务状态
type FAQImportTaskStatus string

const (
	// FAQImportStatusPending represents the pending status of the FAQ import task
	FAQImportStatusPending FAQImportTaskStatus = "pending"
	// FAQImportStatusProcessing represents the processing status of the FAQ import task
	FAQImportStatusProcessing FAQImportTaskStatus = "processing"
	// FAQImportStatusCompleted represents the completed status of the FAQ import task
	FAQImportStatusCompleted FAQImportTaskStatus = "completed"
	// FAQImportStatusFailed represents the failed status of the FAQ import task
	FAQImportStatusFailed FAQImportTaskStatus = "failed"
)

// FAQImportProgress represents the progress of an FAQ import task stored in Redis
type FAQImportProgress struct {
	TaskID      string              `json:"task_id"`       // UUID for the import task
	KBID        string              `json:"kb_id"`         // Knowledge Base ID
	KnowledgeID string              `json:"knowledge_id"`  // FAQ Knowledge ID
	Status      FAQImportTaskStatus `json:"status"`        // Task status
	Progress    int                 `json:"progress"`      // 0-100 percentage
	Total       int                 `json:"total"`         // Total entries to import
	Processed   int                 `json:"processed"`     // Entries processed so far
	Message     string              `json:"message"`       // Status message
	Error       string              `json:"error"`         // Error message if failed
	CreatedAt   int64               `json:"created_at"`    // Task creation timestamp
	UpdatedAt   int64               `json:"updated_at"`    // Last update timestamp
}

// FAQImportMetadata 存储在Knowledge.Metadata中的FAQ导入任务信息
// Deprecated: Use FAQImportProgress with Redis storage instead
type FAQImportMetadata struct {
	ImportProgress  int `json:"import_progress"` // 0-100
	ImportTotal     int `json:"import_total"`
	ImportProcessed int `json:"import_processed"`
}

// ToJSON converts the metadata to JSON type.
func (m *FAQImportMetadata) ToJSON() (JSON, error) {
	if m == nil {
		return nil, nil
	}
	bytes, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	return JSON(bytes), nil
}

// ParseFAQImportMetadata parses FAQ import metadata from Knowledge.
func ParseFAQImportMetadata(k *Knowledge) (*FAQImportMetadata, error) {
	if k == nil || len(k.Metadata) == 0 {
		return nil, nil
	}
	var metadata FAQImportMetadata
	if err := json.Unmarshal(k.Metadata, &metadata); err != nil {
		return nil, err
	}
	return &metadata, nil
}

func normalizeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	dedup := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			continue
		}
		if _, exists := seen[trimmed]; exists {
			continue
		}
		seen[trimmed] = struct{}{}
		dedup = append(dedup, trimmed)
	}
	if len(dedup) == 0 {
		return nil
	}
	return dedup
}
