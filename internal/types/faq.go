package types

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/longbridgeapp/opencc"
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

// Sanitize 对元数据进行基础清理（去除首尾空白、去重），保留原始内容
// 用于 DB 存储，不做语义归一化
func (m *FAQChunkMetadata) Sanitize() {
	if m == nil {
		return
	}
	m.StandardQuestion = strings.TrimSpace(m.StandardQuestion)
	m.SimilarQuestions = SanitizeStrings(m.SimilarQuestions)
	m.NegativeQuestions = SanitizeStrings(m.NegativeQuestions)
	m.Answers = SanitizeStrings(m.Answers)
	if m.Version <= 0 {
		m.Version = 1
	}
}

// Normalize 返回归一化后的副本，用于 Hash 计算和向量索引
// 原始数据不变，返回新的归一化副本
func (m *FAQChunkMetadata) Normalize() *FAQChunkMetadata {
	if m == nil {
		return nil
	}
	return &FAQChunkMetadata{
		StandardQuestion:  NormalizeQuestion(m.StandardQuestion),
		SimilarQuestions:  normalizeQuestionStrings(m.SimilarQuestions),
		NegativeQuestions: normalizeQuestionStrings(m.NegativeQuestions),
		Answers:           SanitizeStrings(m.Answers), // 答案只做基础清理
		AnswerStrategy:    m.AnswerStrategy,
		Version:           m.Version,
		Source:            m.Source,
	}
}

// SanitizeStrings 对字符串列表进行基础清理（TrimSpace + 去重）
func SanitizeStrings(values []string) []string {
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

// FAQMetadata 解析 Chunk 中的 FAQ 元数据
// 返回原始数据（仅做基础清理）
func (c *Chunk) FAQMetadata() (*FAQChunkMetadata, error) {
	if c == nil || len(c.Metadata) == 0 {
		return nil, nil
	}
	var meta FAQChunkMetadata
	if err := json.Unmarshal(c.Metadata, &meta); err != nil {
		return nil, err
	}
	meta.Sanitize() // 只做基础清理，保留原始内容
	return &meta, nil
}

// SetFAQMetadata 设置 Chunk 的 FAQ 元数据
// DB 存储原始数据，ContentHash 基于归一化数据计算
func (c *Chunk) SetFAQMetadata(meta *FAQChunkMetadata) error {
	if c == nil {
		return nil
	}
	if meta == nil {
		c.Metadata = nil
		c.ContentHash = ""
		return nil
	}
	// 基础清理后存储到 DB（保留原始内容）
	meta.Sanitize()
	bytes, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	c.Metadata = JSON(bytes)
	// ContentHash 基于归一化后的数据计算，用于去重匹配
	normalized := meta.Normalize()
	c.ContentHash = CalculateFAQContentHash(normalized)
	return nil
}

// CalculateFAQContentHash 计算 FAQ 内容的 hash 值
// hash 基于：标准问 + 相似问（排序后）+ 反例（排序后）+ 答案（排序后）
// 用于快速匹配和去重
func CalculateFAQContentHash(meta *FAQChunkMetadata) string {
	if meta == nil {
		return ""
	}

	// Normalize() returns a new copy; the old code discarded the return value.
	normalized := meta.Normalize()
	if normalized == nil {
		return ""
	}

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
	ID                int64          `json:"id"`
	ChunkID           string         `json:"chunk_id"`
	KnowledgeID       string         `json:"knowledge_id"`
	KnowledgeBaseID   string         `json:"knowledge_base_id"`
	TagID             int64          `json:"tag_id"`
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
	// MatchedQuestion is the actual question text that was matched in FAQ search
	// Could be the standard question or one of the similar questions
	MatchedQuestion string `json:"matched_question,omitempty"`
}

// FAQEntryPayload 用于创建/更新 FAQ 条目的 payload
type FAQEntryPayload struct {
	// ID 可选，用于数据迁移时指定 seq_id（必须小于自增起始值 100000000）
	ID                *int64          `json:"id,omitempty"`
	StandardQuestion  string          `json:"standard_question"    binding:"required"`
	SimilarQuestions  []string        `json:"similar_questions"`
	NegativeQuestions []string        `json:"negative_questions"`
	Answers           []string        `json:"answers"`
	AnswerStrategy    *AnswerStrategy `json:"answer_strategy,omitempty"`
	TagID             int64           `json:"tag_id"`
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
	TaskID      string            `json:"task_id"` // 可选，如果不传则自动生成UUID
	DryRun      bool              `json:"dry_run"` // 仅验证，不实际导入
}

// FAQFailedEntry 表示导入/验证失败的条目
type FAQFailedEntry struct {
	Index             int      `json:"index"`                        // 条目在批次中的索引（从0开始）
	Reason            string   `json:"reason"`                       // 失败原因
	IsPartialFailure  bool     `json:"is_partial_failure,omitempty"` // 是否为部分失败（相似问/反例被移除，但整条仍可导入）
	TagName           string   `json:"tag_name,omitempty"`           // 分类
	StandardQuestion  string   `json:"standard_question"`            // 标准问题
	SimilarQuestions  []string `json:"similar_questions,omitempty"`  // 相似问题
	NegativeQuestions []string `json:"negative_questions,omitempty"` // 反例问题
	Answers           []string `json:"answers,omitempty"`            // 答案
	AnswerAll         bool     `json:"answer_all,omitempty"`         // 是否全部回复
	IsDisabled        bool     `json:"is_disabled,omitempty"`        // 是否停用
	// 部分失败详情（当 IsPartialFailure 为 true 时）
	RemovedSimilarQuestions  []string `json:"removed_similar_questions,omitempty"`  // 被移除的相似问及原因
	RemovedNegativeQuestions []string `json:"removed_negative_questions,omitempty"` // 被移除的反例及原因
}

// FAQSuccessEntry 表示导入成功的条目简单信息
type FAQSuccessEntry struct {
	Index            int    `json:"index"`              // 条目在批次中的索引（从0开始）
	SeqID            int64  `json:"seq_id"`             // 导入后的条目序列ID
	TagID            int64  `json:"tag_id,omitempty"`   // 分类ID（seq_id）
	TagName          string `json:"tag_name,omitempty"` // 分类名称
	StandardQuestion string `json:"standard_question"`  // 标准问题
}

// FAQDryRunResult 表示 dry_run 模式的验证结果
type FAQDryRunResult struct {
	TaskID        string           `json:"task_id,omitempty"` // 异步任务ID（异步模式时返回）
	Total         int              `json:"total"`             // 总条目数
	SuccessCount  int              `json:"success_count"`     // 验证通过的条目数
	FailedCount   int              `json:"failed_count"`      // 验证失败的条目数
	FailedEntries []FAQFailedEntry `json:"failed_entries"`    // 失败条目详情
}

// FAQSearchRequest FAQ检索请求参数
type FAQSearchRequest struct {
	QueryText            string  `json:"query_text"             binding:"required"`
	VectorThreshold      float64 `json:"vector_threshold"`
	MatchCount           int     `json:"match_count"`
	FirstPriorityTagIDs  []int64 `json:"first_priority_tag_ids"`  // 第一优先级标签ID列表，限定命中范围，优先级最高
	SecondPriorityTagIDs []int64 `json:"second_priority_tag_ids"` // 第二优先级标签ID列表，限定命中范围，优先级低于第一优先级
	OnlyRecommended      bool    `json:"only_recommended"`        // 是否仅返回推荐的条目
}

// UntaggedTagName is the default tag name for entries without a tag
const UntaggedTagName = "未分类"

// FAQEntryFieldsUpdate 单个FAQ条目的字段更新
type FAQEntryFieldsUpdate struct {
	IsEnabled     *bool  `json:"is_enabled,omitempty"`
	IsRecommended *bool  `json:"is_recommended,omitempty"`
	TagID         *int64 `json:"tag_id,omitempty"`
	// 后续可扩展更多字段
}

// FAQEntryFieldsBatchUpdate 批量更新FAQ条目字段的请求
// 支持两种模式：
// 1. 按条目ID更新：使用 ByID 字段
// 2. 按Tag更新：使用 ByTag 字段，将该Tag下所有条目应用相同的更新
type FAQEntryFieldsBatchUpdate struct {
	// ByID 按条目ID更新，key为条目ID (seq_id)
	ByID map[int64]FAQEntryFieldsUpdate `json:"by_id,omitempty"`
	// ByTag 按Tag批量更新，key为TagID (seq_id)
	ByTag map[int64]FAQEntryFieldsUpdate `json:"by_tag,omitempty"`
	// ExcludeIDs 在ByTag操作中需要排除的ID列表 (seq_id)
	ExcludeIDs []int64 `json:"exclude_ids,omitempty"`
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
// When Status is "completed", the result fields (SkippedCount, ImportMode, ImportedAt, DisplayStatus, ProcessingTime) are populated.
type FAQImportProgress struct {
	TaskID             string              `json:"task_id"`                        // UUID for the import task
	KBID               string              `json:"kb_id"`                          // Knowledge Base ID
	KnowledgeID        string              `json:"knowledge_id"`                   // FAQ Knowledge ID
	Status             FAQImportTaskStatus `json:"status"`                         // Task status
	Progress           int                 `json:"progress"`                       // 0-100 percentage
	Total              int                 `json:"total"`                          // Total entries to import
	Processed          int                 `json:"processed"`                      // Entries processed so far
	SuccessCount       int                 `json:"success_count"`                  // 完全成功的条目数（不包含部分成功/部分失败）
	FailedCount        int                 `json:"failed_count"`                   // 失败的条目数
	PartialFailedCount int                 `json:"partial_failed_count,omitempty"` // 部分失败的条目数（相似问/反例被移除）
	SkippedCount       int                 `json:"skipped_count,omitempty"`        // 跳过的条目数（如重复等）
	FailedEntries      []FAQFailedEntry    `json:"failed_entries,omitempty"`       // 失败条目详情（少量时直接返回）
	FailedEntriesURL   string              `json:"failed_entries_url,omitempty"`   // 失败条目CSV下载URL（大量时返回URL）
	SuccessEntries     []FAQSuccessEntry   `json:"success_entries,omitempty"`      // 成功条目简单信息（少量时直接返回）
	ValidEntryIndices  []int               `json:"valid_entry_indices,omitempty"`  // 验证通过的条目索引（用于重试时跳过验证）
	Message            string              `json:"message"`                        // Status message
	Error              string              `json:"error"`                          // Error message if failed
	CreatedAt          int64               `json:"created_at"`                     // Task creation timestamp
	UpdatedAt          int64               `json:"updated_at"`                     // Last update timestamp
	DryRun             bool                `json:"dry_run,omitempty"`              // 是否为 dry run 模式

	// Result fields (populated when Status == "completed")
	ImportMode     string    `json:"import_mode,omitempty"`     // 导入模式：append 或 replace
	ImportedAt     time.Time `json:"imported_at,omitempty"`     // 导入完成时间
	DisplayStatus  string    `json:"display_status,omitempty"`  // 显示状态：open 或 close
	ProcessingTime int64     `json:"processing_time,omitempty"` // 处理耗时（毫秒）
}

// FAQImportMetadata 存储在Knowledge.Metadata中的FAQ导入任务信息
// Deprecated: Use FAQImportProgress with Redis storage instead
type FAQImportMetadata struct {
	ImportProgress  int `json:"import_progress"` // 0-100
	ImportTotal     int `json:"import_total"`
	ImportProcessed int `json:"import_processed"`
}

// FAQImportResult 存储FAQ导入完成后的统计结果
// 这个信息是持久化的，不跟随进度状态，直到下次导入时被替换
type FAQImportResult struct {
	// 导入统计信息
	TotalEntries       int `json:"total_entries"`        // 总条目数
	SuccessCount       int `json:"success_count"`        // 完全成功的条目数（不包含部分成功/部分失败）
	FailedCount        int `json:"failed_count"`         // 完全失败的条目数
	PartialFailedCount int `json:"partial_failed_count"` // 部分失败的条目数（相似问/反例被移除但已导入）
	SkippedCount       int `json:"skipped_count"`        // 跳过的条目数（如重复等）

	// 导入模式和时间信息
	ImportMode string    `json:"import_mode"` // 导入模式：append 或 replace
	ImportedAt time.Time `json:"imported_at"` // 导入完成时间
	TaskID     string    `json:"task_id"`     // 导入任务ID

	// 失败详情URL（失败条目较多时提供下载链接）
	FailedEntriesURL string `json:"failed_entries_url,omitempty"` // 失败条目CSV下载URL

	// 显示控制
	DisplayStatus string `json:"display_status"` // 显示状态：open 或 close

	// 额外统计信息
	ProcessingTime int64 `json:"processing_time"` // 处理耗时（毫秒）
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

// ToJSON converts the import result to JSON type.
func (r *FAQImportResult) ToJSON() (JSON, error) {
	if r == nil {
		return nil, nil
	}
	bytes, err := json.Marshal(r)
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

// normalizeQuestionStrings 对问题列表进行归一化处理
// 包括全角转半角、去除末尾标点、合并空格等，同时去重
func normalizeQuestionStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	dedup := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		normalized := NormalizeQuestion(v)
		if normalized == "" {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		dedup = append(dedup, normalized)
	}
	if len(dedup) == 0 {
		return nil
	}
	return dedup
}

// multiSpaceRegex 用于匹配多个连续空白字符
var multiSpaceRegex = regexp.MustCompile(`\s+`)

// urlRegex 用于匹配 URL
var urlRegex = regexp.MustCompile(`https?://[^\s]+`)

// URLNormMode 定义 URL 归一化模式
type URLNormMode int

const (
	// URLRemove 完全移除 URL
	URLRemove URLNormMode = iota
	// URLPlaceholder 替换为占位符 <URL>
	URLPlaceholder
	// URLKeepDomain 只保留域名
	URLKeepDomain
	// URLKeepDomainAndPath 保留域名和路径
	URLKeepDomainAndPath
)

// t2sConverter 繁体转简体转换器（单例）
var t2sConverter *opencc.OpenCC

func init() {
	var err error
	t2sConverter, err = opencc.New("t2s") // Traditional to Simplified
	if err != nil {
		// 初始化失败时使用空转换器，不影响其他功能
		t2sConverter = nil
	}
}

// NormalizeQuestion 对问题文本进行归一化处理以提高向量匹配命中率
// 处理顺序参考: query = convert_st(trim_url(query.lower().strip().strip("？。，；、：""！?.,;!:'\"")), 1)
// 1. 去除首尾空白
// 2. 移除 URL
// 3. 转小写
// 4. 去除首尾标点
// 5. 繁体转简体
// 6. 全角符号转半角
// 7. 智能空格处理（中文间去空格，英文/数字间保留）
func NormalizeQuestion(q string) string {
	q = strings.TrimSpace(q)
	if q == "" {
		return ""
	}

	// 1. 移除 URL
	q = trimURL(q)

	// 2. 转小写（对英文有效）
	q = strings.ToLower(q)

	// 3. 去除首尾标点符号
	q = strings.Trim(q, `？。，；、：""！?.,;!:'""`)

	// 4. 繁体转简体
	q = toSimplified(q)

	// 5. 全角字符转半角
	q = toHalfWidth(q)

	// 6. 智能空格处理：中文间去空格，英文/数字间保留
	q = normalizeSpaces(q)

	return strings.TrimSpace(q)
}

// normalizeSpaces 智能处理空格
// 规则：
// - 去除中文语境中的多余空格
// - 保留英文/数字之间的必要空格
// 示例：
// - "怎么 绑定 手机" → "怎么绑定手机"
// - "iphone 15 怎么 激活" → "iphone 15怎么激活"
func normalizeSpaces(s string) string {
	// 先合并多个连续空格为单个空格
	s = multiSpaceRegex.ReplaceAllString(s, " ")

	runes := []rune(s)
	if len(runes) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.Grow(len(s))

	for i := 0; i < len(runes); i++ {
		r := runes[i]

		// 如果不是空格，直接写入
		if r != ' ' {
			builder.WriteRune(r)
			continue
		}

		// 处理空格：检查前后字符决定是否保留
		// 获取前一个非空字符
		var prevRune rune
		if i > 0 {
			prevRune = runes[i-1]
		}

		// 获取后一个非空字符
		var nextRune rune
		for j := i + 1; j < len(runes); j++ {
			if runes[j] != ' ' {
				nextRune = runes[j]
				break
			}
		}

		// 只有当前后都是英文字母或数字时才保留空格
		// 其他情况（包括中文）都去除空格
		if isASCIIAlphaNum(prevRune) && isASCIIAlphaNum(nextRune) {
			builder.WriteRune(' ')
		}
		// 否则跳过空格
	}

	return builder.String()
}

// isASCIIAlphaNum 判断是否为 ASCII 字母或数字
func isASCIIAlphaNum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// trimURL 移除字符串中的 URL（使用默认的 URLRemove 模式）
func trimURL(s string) string {
	return NormalizeURL(s, URLRemove)
}

// NormalizeURL 根据指定模式处理文本中的 URL
func NormalizeURL(text string, mode URLNormMode) string {
	return urlRegex.ReplaceAllStringFunc(text, func(raw string) string {
		switch mode {
		case URLRemove:
			return ""
		case URLPlaceholder:
			return "<URL>"
		case URLKeepDomain:
			domain, _ := parseURL(raw)
			if domain != "" {
				return domain
			}
			return "<URL>"
		case URLKeepDomainAndPath:
			domain, path := parseURL(raw)
			if domain != "" {
				return domain + path
			}
			return "<URL>"
		default:
			return "<URL>"
		}
	})
}

// parseURL 解析 URL，返回域名和路径
func parseURL(raw string) (domain, path string) {
	// 移除协议前缀
	u := raw
	if strings.HasPrefix(u, "https://") {
		u = u[8:]
	} else if strings.HasPrefix(u, "http://") {
		u = u[7:]
	}

	// 分离域名和路径
	slashIdx := strings.Index(u, "/")
	if slashIdx == -1 {
		// 没有路径，整个是域名（可能带查询参数）
		queryIdx := strings.Index(u, "?")
		if queryIdx != -1 {
			domain = u[:queryIdx]
		} else {
			domain = u
		}
		return domain, ""
	}

	domain = u[:slashIdx]
	path = u[slashIdx:]

	// 移除查询参数和片段
	if queryIdx := strings.Index(path, "?"); queryIdx != -1 {
		path = path[:queryIdx]
	}
	if fragIdx := strings.Index(path, "#"); fragIdx != -1 {
		path = path[:fragIdx]
	}

	return domain, path
}

// toSimplified 繁体中文转简体中文
func toSimplified(s string) string {
	if t2sConverter == nil {
		return s
	}
	result, err := t2sConverter.Convert(s)
	if err != nil {
		return s
	}
	return result
}

// toHalfWidth 将全角字符转换为半角字符
// 主要处理：全角空格、全角ASCII字符（包括标点符号）
func toHalfWidth(s string) string {
	var builder strings.Builder
	builder.Grow(len(s))

	for _, r := range s {
		switch {
		// 全角空格 -> 半角空格
		case r == '\u3000':
			builder.WriteRune(' ')
		// 全角ASCII字符 (！到～，范围 0xFF01-0xFF5E) -> 半角 (0x0021-0x007E)
		case r >= 0xFF01 && r <= 0xFF5E:
			builder.WriteRune(r - 0xFF01 + 0x21)
		// 其他字符保持不变
		default:
			builder.WriteRune(r)
		}
	}

	return builder.String()
}

// NormalizeQueryText 对搜索查询文本进行归一化处理
// 与 NormalizeQuestion 相同的处理逻辑，用于搜索时对查询文本进行归一化
func NormalizeQueryText(q string) string {
	return NormalizeQuestion(q)
}

// IsChineseChar 判断是否为中文字符
func IsChineseChar(r rune) bool {
	return unicode.Is(unicode.Han, r)
}
