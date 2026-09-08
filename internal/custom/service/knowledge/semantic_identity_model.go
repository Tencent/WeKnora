package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
)

const (
	defaultSemanticIdentityTimeout       = 20 * time.Second
	defaultSemanticIdentityMinConfidence = 0.80
)

var semanticIdentitySchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "decision": {"type": "string", "enum": ["same_object", "different_object", "uncertain"]},
    "confidence": {"type": "number", "minimum": 0, "maximum": 1},
    "reason": {"type": "string", "minLength": 1},
    "conflict_fields": {"type": "array", "items": {"type": "string", "minLength": 1}, "uniqueItems": true}
  },
  "required": ["decision", "confidence", "reason", "conflict_fields"]
}`)

const semanticIdentitySystemPrompt = `你是 WeKnora 的知识对象身份判定器。判断候选 A 与候选 B 是否指向完全相同的知识对象。

标题和别名只用于召回，不能单独决定合并。必须比较核心内容、适用范围和结构字段：
- 实体：同一人物、组织、产品、技术、行业或地点；
- 概念：定义、适用范围、核心构成或机制一致；
- 方法论：目标、输入、关键步骤或判断逻辑、输出属于同一套方法；
- 案例：时间背景、参与对象和事件过程属于同一事件；
- 洞察：核心判断、方向、适用条件和限定一致。

只有身份特征兼容且没有事实冲突时输出 same_object；明确是两个对象时输出 different_object；信息不足、类型冲突、范围冲突、事实冲突或边界不清时输出 uncertain。不得通过传递关系推断同一性。conflict_fields 填写发生冲突或不足的字段名，没有则返回空数组。只输出符合 JSON Schema 的 JSON。`

// ModelSemanticIdentityAdapter delegates final identity adjudication to the
// chat model already configured for the active WeKnora Agent session.
type ModelSemanticIdentityAdapter struct {
	model         chat.Chat
	timeout       time.Duration
	minConfidence float64
}

func NewModelSemanticIdentityAdapter(model chat.Chat) *ModelSemanticIdentityAdapter {
	return &ModelSemanticIdentityAdapter{
		model:         model,
		timeout:       defaultSemanticIdentityTimeout,
		minConfidence: defaultSemanticIdentityMinConfidence,
	}
}

type semanticIdentityPromptCandidate struct {
	KnowledgeType   KnowledgeType     `json:"knowledge_type"`
	EntitySubType   string            `json:"entity_sub_type,omitempty"`
	Title           string            `json:"title"`
	Aliases         []string          `json:"aliases,omitempty"`
	CoreContent     string            `json:"core_content"`
	StructureFields map[string]string `json:"structure_fields"`
}

func (a *ModelSemanticIdentityAdapter) Compare(
	ctx context.Context,
	left IdentityCandidate,
	right IdentityCandidate,
) (SemanticIdentityAssessment, error) {
	if a == nil || a.model == nil {
		return SemanticIdentityAssessment{}, fmt.Errorf("semantic identity model is not configured")
	}
	if left.KnowledgeType != right.KnowledgeType {
		return SemanticIdentityAssessment{
			Decision: "uncertain", Confidence: 1,
			Reason: "knowledge objects have different top-level types", ConflictFields: []string{"knowledge_type"},
		}, nil
	}
	if left.KnowledgeType == TypeEntity && left.EntitySubType != right.EntitySubType {
		return SemanticIdentityAssessment{
			Decision: "uncertain", Confidence: 1,
			Reason: "entities have different subtypes", ConflictFields: []string{"entity_sub_type"},
		}, nil
	}

	payload, err := json.Marshal(struct {
		CandidateA semanticIdentityPromptCandidate `json:"candidate_a"`
		CandidateB semanticIdentityPromptCandidate `json:"candidate_b"`
	}{
		CandidateA: semanticPromptCandidate(left),
		CandidateB: semanticPromptCandidate(right),
	})
	if err != nil {
		return SemanticIdentityAssessment{}, fmt.Errorf("encode semantic identity candidates: %w", err)
	}

	timeout := a.timeout
	if timeout <= 0 {
		timeout = defaultSemanticIdentityTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	thinking := false
	response, err := a.model.Chat(callCtx, []chat.Message{
		{Role: "system", Content: semanticIdentitySystemPrompt},
		{Role: "user", Content: string(payload)},
	}, &chat.ChatOptions{
		Temperature:         0,
		MaxCompletionTokens: 600,
		Thinking:            &thinking,
		Format:              semanticIdentitySchema,
	})
	if err != nil {
		return SemanticIdentityAssessment{}, fmt.Errorf("semantic identity model call: %w", err)
	}
	if response == nil {
		return SemanticIdentityAssessment{}, fmt.Errorf("semantic identity model returned no response")
	}
	if strings.EqualFold(strings.TrimSpace(response.FinishReason), "length") {
		return SemanticIdentityAssessment{}, fmt.Errorf("semantic identity model response was truncated")
	}

	assessment, err := parseSemanticIdentityAssessment(response.Content)
	if err != nil {
		return SemanticIdentityAssessment{}, err
	}
	minimum := a.minConfidence
	if minimum <= 0 || minimum > 1 {
		minimum = defaultSemanticIdentityMinConfidence
	}
	if assessment.Confidence < minimum {
		return SemanticIdentityAssessment{}, fmt.Errorf(
			"semantic identity confidence %.2f is below required %.2f", assessment.Confidence, minimum,
		)
	}
	return assessment, nil
}

func semanticPromptCandidate(candidate IdentityCandidate) semanticIdentityPromptCandidate {
	return semanticIdentityPromptCandidate{
		KnowledgeType:   candidate.KnowledgeType,
		EntitySubType:   strings.TrimSpace(candidate.EntitySubType),
		Title:           CanonicalKnowledgeTitle(candidate.Title),
		Aliases:         cleanStrings(candidate.Aliases),
		CoreContent:     strings.TrimSpace(candidate.CoreContent),
		StructureFields: cloneStructureFields(candidate.StructureFields),
	}
}

func parseSemanticIdentityAssessment(content string) (SemanticIdentityAssessment, error) {
	var assessment SemanticIdentityAssessment
	decoder := json.NewDecoder(strings.NewReader(strings.TrimSpace(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&assessment); err != nil {
		return SemanticIdentityAssessment{}, fmt.Errorf("decode semantic identity response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return SemanticIdentityAssessment{}, fmt.Errorf("decode semantic identity response: trailing content")
	}
	assessment.Decision = strings.TrimSpace(assessment.Decision)
	assessment.Reason = strings.TrimSpace(assessment.Reason)
	switch assessment.Decision {
	case "same_object", "different_object", "uncertain":
	default:
		return SemanticIdentityAssessment{}, fmt.Errorf("semantic identity response has invalid decision %q", assessment.Decision)
	}
	if assessment.Confidence < 0 || assessment.Confidence > 1 {
		return SemanticIdentityAssessment{}, fmt.Errorf("semantic identity response confidence must be between 0 and 1")
	}
	if assessment.Reason == "" {
		return SemanticIdentityAssessment{}, fmt.Errorf("semantic identity response reason is required")
	}
	seen := make(map[string]struct{}, len(assessment.ConflictFields))
	for index, field := range assessment.ConflictFields {
		field = strings.TrimSpace(field)
		if field == "" {
			return SemanticIdentityAssessment{}, fmt.Errorf("semantic identity response conflict_fields[%d] is empty", index)
		}
		if _, exists := seen[field]; exists {
			return SemanticIdentityAssessment{}, fmt.Errorf("semantic identity response conflict field %q is duplicated", field)
		}
		seen[field] = struct{}{}
		assessment.ConflictFields[index] = field
	}
	return assessment, nil
}
