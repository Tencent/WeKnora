package knowledge

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
)

type semanticIdentityFakeChat struct {
	response *types.ChatResponse
	err      error
	chatFn   func(context.Context) (*types.ChatResponse, error)
	messages []chat.Message
	options  *chat.ChatOptions
	calls    int
}

type semanticIdentityFakeCompleter struct {
	response string
	err      error
	prompt   string
}

func (f *semanticIdentityFakeCompleter) Complete(_ context.Context, prompt string) (string, error) {
	f.prompt = prompt
	return f.response, f.err
}

func (f *semanticIdentityFakeChat) Chat(ctx context.Context, messages []chat.Message, options *chat.ChatOptions) (*types.ChatResponse, error) {
	f.calls++
	f.messages = messages
	f.options = options
	if f.chatFn != nil {
		return f.chatFn(ctx)
	}
	return f.response, f.err
}

func (*semanticIdentityFakeChat) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, errors.New("not used")
}

func (*semanticIdentityFakeChat) GetModelName() string { return "semantic-test" }
func (*semanticIdentityFakeChat) GetModelID() string   { return "semantic-test" }

func semanticIdentityCandidates() (IdentityCandidate, IdentityCandidate) {
	return IdentityCandidate{
		KnowledgeType: TypeConcept, Title: "个人知识库五关标准", Aliases: []string{"五关标准"},
		CoreContent: "个人知识库需要通过五项质量检查。",
		StructureFields: map[string]string{
			"definition": "用于判断个人知识库质量的一组标准",
			"components": "可检索、可验证、可维护、可连接、可行动",
		},
	}, IdentityCandidate{
		KnowledgeType: TypeConcept, Title: "个人知识库五大条件", Aliases: []string{"五项条件"},
		CoreContent: "用五个条件评估个人知识库是否真正可用。",
		StructureFields: map[string]string{
			"definition": "评估个人知识库质量的条件体系",
			"components": "能找到、能核验、能更新、能关联、能执行",
		},
	}
}

func TestModelSemanticIdentityAdapterAcceptsStructuredDecisions(t *testing.T) {
	left, right := semanticIdentityCandidates()
	for _, test := range []struct {
		name     string
		decision string
	}{
		{name: "same object", decision: "same_object"},
		{name: "different object", decision: "different_object"},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := &semanticIdentityFakeChat{response: &types.ChatResponse{Content: `{"decision":"` + test.decision + `","confidence":0.93,"reason":"定义与适用范围已完成比较","conflict_fields":[]}`}}
			adapter := NewModelSemanticIdentityAdapter(model)
			assessment, err := adapter.Compare(context.Background(), left, right)
			if err != nil {
				t.Fatalf("Compare returned error: %v", err)
			}
			if assessment.Decision != test.decision || assessment.Confidence != 0.93 {
				t.Fatalf("assessment = %+v", assessment)
			}
			if model.options == nil || len(model.options.Format) == 0 || model.options.Temperature != 0 {
				t.Fatalf("model options do not enforce structured deterministic output: %+v", model.options)
			}
			var payload map[string]any
			if len(model.messages) != 2 || json.Unmarshal([]byte(model.messages[1].Content), &payload) != nil {
				t.Fatalf("candidate payload is not valid JSON: %+v", model.messages)
			}
			if !strings.Contains(model.messages[1].Content, "components") || !strings.Contains(model.messages[1].Content, "五关标准") {
				t.Fatalf("identity-defining fields are missing from prompt: %s", model.messages[1].Content)
			}
		})
	}
}

func TestModelSemanticIdentityAdapterRejectsMalformedResponse(t *testing.T) {
	left, right := semanticIdentityCandidates()
	for _, content := range []string{
		"```json\n{\"decision\":\"same_object\",\"confidence\":0.9,\"reason\":\"same\",\"conflict_fields\":[]}\n```",
		`{"decision":"same_object","confidence":0.9,"reason":"same","conflict_fields":[],"extra":true}`,
		`{"decision":"merge","confidence":0.9,"reason":"same","conflict_fields":[]}`,
		`{"decision":"same_object","confidence":0.9,"reason":"","conflict_fields":[]}`,
	} {
		model := &semanticIdentityFakeChat{response: &types.ChatResponse{Content: content}}
		_, err := NewModelSemanticIdentityAdapter(model).Compare(context.Background(), left, right)
		if err == nil {
			t.Fatalf("malformed response was accepted: %s", content)
		}
	}
}

func TestModelSemanticIdentityAdapterFailsClosedOnTimeout(t *testing.T) {
	left, right := semanticIdentityCandidates()
	model := &semanticIdentityFakeChat{chatFn: func(ctx context.Context) (*types.ChatResponse, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	adapter := NewModelSemanticIdentityAdapter(model)
	adapter.timeout = 5 * time.Millisecond
	_, err := adapter.Compare(context.Background(), left, right)
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timeout must fail closed, got %v", err)
	}
}

func TestModelSemanticIdentityAdapterFailsClosedOnLowConfidence(t *testing.T) {
	left, right := semanticIdentityCandidates()
	model := &semanticIdentityFakeChat{response: &types.ChatResponse{Content: `{"decision":"same_object","confidence":0.79,"reason":"可能相同","conflict_fields":[]}`}}
	_, err := NewModelSemanticIdentityAdapter(model).Compare(context.Background(), left, right)
	if err == nil || !strings.Contains(err.Error(), "below required") {
		t.Fatalf("low confidence must fail closed, got %v", err)
	}
}

func TestModelSemanticIdentityAdapterBlocksTypeConflictWithoutModelCall(t *testing.T) {
	left, right := semanticIdentityCandidates()
	right.KnowledgeType = TypeMethodology
	model := &semanticIdentityFakeChat{}
	assessment, err := NewModelSemanticIdentityAdapter(model).Compare(context.Background(), left, right)
	if err != nil || assessment.Decision != "uncertain" || model.calls != 0 {
		t.Fatalf("type conflict = %+v, calls=%d, err=%v", assessment, model.calls, err)
	}
}

func TestCompletionModelSemanticIdentityAdapterUsesStructuredFailClosedContract(t *testing.T) {
	left, right := semanticIdentityCandidates()
	completer := &semanticIdentityFakeCompleter{response: `<think>比较定义、机制和适用范围。</think>
{"decision":"same_object","confidence":0.93,"reason":"定义和机制一致","conflict_fields":[]}`}
	assessment, err := NewCompletionModelSemanticIdentityAdapter(completer).Compare(context.Background(), left, right)
	if err != nil {
		t.Fatalf("Compare returned error: %v", err)
	}
	if assessment.Decision != "same_object" || assessment.Confidence != 0.93 {
		t.Fatalf("assessment = %#v", assessment)
	}
	if !strings.Contains(completer.prompt, "不得通过传递关系") || !strings.Contains(completer.prompt, "candidate_a") {
		t.Fatalf("prompt does not contain identity contract and candidates: %s", completer.prompt)
	}
	if !strings.Contains(completer.prompt, `"confidence":0.00`) || !strings.Contains(completer.prompt, "只能包含以下四个字段") {
		t.Fatalf("prompt does not contain the explicit four-field output contract: %s", completer.prompt)
	}
}

func TestCompletionModelSemanticIdentityAdapterRejectsUnclosedReasoningPrefix(t *testing.T) {
	left, right := semanticIdentityCandidates()
	completer := &semanticIdentityFakeCompleter{response: `<think>推理未闭合
{"decision":"same_object","confidence":0.93,"reason":"定义和机制一致","conflict_fields":[]}`}
	_, err := NewCompletionModelSemanticIdentityAdapter(completer).Compare(context.Background(), left, right)
	if err == nil || !strings.Contains(err.Error(), "decode semantic identity response") {
		t.Fatalf("unclosed reasoning prefix must fail closed, got %v", err)
	}
}

func TestCompletionModelSemanticIdentityAdapterAcceptsCompleteJSONFence(t *testing.T) {
	left, right := semanticIdentityCandidates()
	completer := &semanticIdentityFakeCompleter{response: "```json\n{\"decision\":\"different_object\",\"confidence\":0.91,\"reason\":\"边界不同\",\"conflict_fields\":[]}\n```"}
	assessment, err := NewCompletionModelSemanticIdentityAdapter(completer).Compare(context.Background(), left, right)
	if err != nil || assessment.Decision != "different_object" {
		t.Fatalf("complete JSON fence = %#v, err=%v", assessment, err)
	}
}

func TestCompletionModelSemanticIdentityAdapterAcceptsNarrowResultWrappers(t *testing.T) {
	left, right := semanticIdentityCandidates()
	for _, response := range []string{
		`{"result":"same_object","confidence":0.91,"reasoning":"核心机制一致","conflict_fields":[]}`,
		`{"identity_verdict":"different_object","confidence":0.91,"reason":"适用范围不同","conflict_fields":["scope"]}`,
		`{"result":{"decision":"uncertain","confidence":0.91,"reasoning":"信息不足","conflict_fields":["scope"]}}`,
	} {
		completer := &semanticIdentityFakeCompleter{response: response}
		assessment, err := NewCompletionModelSemanticIdentityAdapter(completer).Compare(context.Background(), left, right)
		if err != nil {
			t.Fatalf("wrapper response was rejected: %s: %v", response, err)
		}
		if assessment.Confidence != 0.91 {
			t.Fatalf("unexpected assessment: %#v", assessment)
		}
	}
}

func TestCompletionModelSemanticIdentityAdapterRejectsWrapperWithMissingContractFields(t *testing.T) {
	left, right := semanticIdentityCandidates()
	completer := &semanticIdentityFakeCompleter{response: `{"result":"same_object","reason":"核心机制一致"}`}
	_, err := NewCompletionModelSemanticIdentityAdapter(completer).Compare(context.Background(), left, right)
	if err == nil {
		t.Fatalf("incomplete wrapper must fail closed, got %v", err)
	}
}

func TestCompletionModelSemanticIdentityAdapterRejectsProseAroundJSONFence(t *testing.T) {
	left, right := semanticIdentityCandidates()
	completer := &semanticIdentityFakeCompleter{response: "结果如下：\n```json\n{\"decision\":\"different_object\",\"confidence\":0.91,\"reason\":\"边界不同\",\"conflict_fields\":[]}\n```"}
	_, err := NewCompletionModelSemanticIdentityAdapter(completer).Compare(context.Background(), left, right)
	if err == nil || !strings.Contains(err.Error(), "decode semantic identity response") {
		t.Fatalf("prose around JSON fence must fail closed, got %v", err)
	}
}

func TestCompletionModelSemanticIdentityAdapterRejectsLowConfidence(t *testing.T) {
	left, right := semanticIdentityCandidates()
	completer := &semanticIdentityFakeCompleter{response: `{"decision":"same_object","confidence":0.70,"reason":"信息不足","conflict_fields":[]}`}
	_, err := NewCompletionModelSemanticIdentityAdapter(completer).Compare(context.Background(), left, right)
	if err == nil || !strings.Contains(err.Error(), "below required") {
		t.Fatalf("low-confidence response must fail closed, got %v", err)
	}
}
