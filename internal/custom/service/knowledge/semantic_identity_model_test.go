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
