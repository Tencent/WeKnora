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

只有身份特征兼容且没有事实冲突时输出 same_object；明确是两个对象时输出 different_object；信息不足、类型冲突、范围冲突、事实冲突或边界不清时输出 uncertain。不得通过传递关系推断同一性。conflict_fields 填写发生冲突或不足的字段名，没有则返回空数组。

输出格式是唯一允许的内容，必须且只能包含以下四个字段，不得使用 result、identity、verdict、reasoning、explanation 等替代字段，也不得输出 Markdown 或解释文字：
{"decision":"same_object|different_object|uncertain","confidence":0.00,"reason":"非空判定依据","conflict_fields":[]}
其中 confidence 必须是 0 到 1 的数字；reason 必须非空；conflict_fields 必须是字符串数组。`

// ModelSemanticIdentityAdapter delegates final identity adjudication to the
// chat model already configured for the active WeKnora Agent session.
type ModelSemanticIdentityAdapter struct {
	model         chat.Chat
	timeout       time.Duration
	minConfidence float64
}

type SemanticIdentityCompleter interface {
	Complete(context.Context, string) (string, error)
}

type SemanticIdentitySystemCompleter interface {
	CompleteWithSystem(context.Context, string, string) (string, error)
}

type CompletionModelSemanticIdentityAdapter struct {
	completer     SemanticIdentityCompleter
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

func NewCompletionModelSemanticIdentityAdapter(completer SemanticIdentityCompleter) *CompletionModelSemanticIdentityAdapter {
	return &CompletionModelSemanticIdentityAdapter{
		completer: completer, timeout: defaultSemanticIdentityTimeout, minConfidence: defaultSemanticIdentityMinConfidence,
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

func (a *CompletionModelSemanticIdentityAdapter) Compare(
	ctx context.Context,
	left IdentityCandidate,
	right IdentityCandidate,
) (SemanticIdentityAssessment, error) {
	if a == nil || a.completer == nil {
		return SemanticIdentityAssessment{}, fmt.Errorf("semantic identity model is not configured")
	}
	if assessment, handled := semanticIdentityTypeGuard(left, right); handled {
		return assessment, nil
	}
	payload, err := semanticIdentityPayload(left, right)
	if err != nil {
		return SemanticIdentityAssessment{}, err
	}
	timeout := a.timeout
	if timeout <= 0 {
		timeout = defaultSemanticIdentityTimeout
	}
	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var content string
	if systemCompleter, ok := a.completer.(SemanticIdentitySystemCompleter); ok {
		content, err = systemCompleter.CompleteWithSystem(callCtx, semanticIdentitySystemPrompt, "候选对象：\n"+string(payload))
	} else {
		content, err = a.completer.Complete(callCtx, semanticIdentitySystemPrompt+"\n\n候选对象：\n"+string(payload))
	}
	if err != nil {
		return SemanticIdentityAssessment{}, fmt.Errorf("semantic identity model call: %w", err)
	}
	assessment, err := parseSemanticIdentityAssessmentCompatible(content)
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

func semanticIdentityTypeGuard(left, right IdentityCandidate) (SemanticIdentityAssessment, bool) {
	if left.KnowledgeType != right.KnowledgeType {
		return SemanticIdentityAssessment{
			Decision: "uncertain", Confidence: 1,
			Reason: "knowledge objects have different top-level types", ConflictFields: []string{"knowledge_type"},
		}, true
	}
	if left.KnowledgeType == TypeEntity && left.EntitySubType != right.EntitySubType {
		return SemanticIdentityAssessment{
			Decision: "uncertain", Confidence: 1,
			Reason: "entities have different subtypes", ConflictFields: []string{"entity_sub_type"},
		}, true
	}
	return SemanticIdentityAssessment{}, false
}

func semanticIdentityPayload(left, right IdentityCandidate) ([]byte, error) {
	payload, err := json.Marshal(struct {
		CandidateA semanticIdentityPromptCandidate `json:"candidate_a"`
		CandidateB semanticIdentityPromptCandidate `json:"candidate_b"`
	}{
		CandidateA: semanticPromptCandidate(left),
		CandidateB: semanticPromptCandidate(right),
	})
	if err != nil {
		return nil, fmt.Errorf("encode semantic identity candidates: %w", err)
	}
	return payload, nil
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
	decoder := json.NewDecoder(strings.NewReader(stripSemanticIdentityReasoning(content)))
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

// parseSemanticIdentityAssessmentCompatible accepts the exact contract first,
// then a deliberately narrow set of equivalent result wrappers observed from
// completion models. It never invents missing confidence, reason, or fields.
func parseSemanticIdentityAssessmentCompatible(content string) (SemanticIdentityAssessment, error) {
	content = stripSemanticIdentityEnvelope(content)
	assessment, err := parseSemanticIdentityAssessment(content)
	if err == nil {
		return assessment, nil
	}

	var values map[string]json.RawMessage
	if decodeErr := json.Unmarshal([]byte(content), &values); decodeErr != nil {
		return SemanticIdentityAssessment{}, err
	}
	if rawResult, ok := values["result"]; ok && len(rawResult) > 0 && rawResult[0] == '{' {
		var nested map[string]json.RawMessage
		if nestedErr := json.Unmarshal(rawResult, &nested); nestedErr == nil {
			delete(values, "result")
			for key, value := range nested {
				if _, exists := values[key]; exists {
					return SemanticIdentityAssessment{}, err
				}
				values[key] = value
			}
		}
	}

	decisionKey := ""
	for _, key := range []string{"decision", "result", "identity", "verdict", "judgment", "comparison_result", "identity_verdict", "identity_judgment"} {
		if _, ok := values[key]; ok {
			if decisionKey != "" {
				return SemanticIdentityAssessment{}, err
			}
			decisionKey = key
		}
	}
	if decisionKey == "" {
		for _, key := range []string{"same_object", "different_object", "uncertain"} {
			if raw, ok := values[key]; ok && strings.TrimSpace(string(raw)) == "true" {
				if decisionKey != "" {
					return SemanticIdentityAssessment{}, err
				}
				decisionKey = key
			}
		}
	}
	if decisionKey == "" {
		return SemanticIdentityAssessment{}, err
	}
	if _, exists := values["reason"]; !exists {
		for _, key := range []string{"reasoning", "explanation", "basis"} {
			if raw, ok := values[key]; ok {
				values["reason"] = raw
				delete(values, key)
				break
			}
		}
	}
	allowed := map[string]struct{}{
		"confidence": {}, "reason": {}, "conflict_fields": {}, decisionKey: {},
	}
	for key := range values {
		if _, ok := allowed[key]; !ok {
			return SemanticIdentityAssessment{}, err
		}
	}
	decision := decisionKey
	if decisionKey != "same_object" && decisionKey != "different_object" && decisionKey != "uncertain" {
		if err := json.Unmarshal(values[decisionKey], &decision); err != nil {
			return SemanticIdentityAssessment{}, err
		}
	}
	values["decision"] = json.RawMessage(fmt.Sprintf("%q", decision))
	if decisionKey != "decision" {
		delete(values, decisionKey)
	}
	canonical, marshalErr := json.Marshal(values)
	if marshalErr != nil {
		return SemanticIdentityAssessment{}, marshalErr
	}
	return parseSemanticIdentityAssessment(string(canonical))
}

func stripSemanticIdentityEnvelope(content string) string {
	content = stripSemanticIdentityReasoning(content)
	if strings.HasPrefix(content, "```json") && strings.HasSuffix(content, "```") {
		content = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(content, "```json"), "```"))
	}
	return content
}

func stripSemanticIdentityReasoning(content string) string {
	content = strings.TrimSpace(content)
	for {
		matched := false
		for _, tag := range []string{"think", "analysis"} {
			if !strings.HasPrefix(strings.ToLower(content), "<"+tag+">") {
				continue
			}
			closingTag := "</" + tag + ">"
			closingIndex := strings.Index(strings.ToLower(content), closingTag)
			if closingIndex < 0 {
				return content
			}
			content = strings.TrimSpace(content[closingIndex+len(closingTag):])
			matched = true
			break
		}
		if !matched {
			return content
		}
	}
}
