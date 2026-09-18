package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A client-supplied origin is a hint inside the resolved scope; it must not
// widen retrieval to a base the agent was not given.
func TestQuestionOriginInScope(t *testing.T) {
	ctx := context.Background()
	scope := []string{"kb-a", "kb-b"}

	got := questionOriginInScope(ctx, &types.QuestionOrigin{KnowledgeBaseID: " kb-b ", KnowledgeID: " doc-1 "}, scope)
	require.NotNil(t, got)
	assert.Equal(t, types.QuestionOrigin{KnowledgeBaseID: "kb-b", KnowledgeID: "doc-1"}, *got)

	assert.Nil(t, questionOriginInScope(ctx, &types.QuestionOrigin{KnowledgeBaseID: "kb-other"}, scope))
	assert.Nil(t, questionOriginInScope(ctx, &types.QuestionOrigin{KnowledgeBaseID: "kb-a"}, nil))
	assert.Nil(t, questionOriginInScope(ctx, &types.QuestionOrigin{}, scope))
	assert.Nil(t, questionOriginInScope(ctx, nil, scope))
}

func TestResolveQuestionOriginInfoKeepsOnlyDocumentsOfTheOriginBase(t *testing.T) {
	svc := &agentService{knowledgeService: &tagTargetKnowledgeService{knowledges: []*types.Knowledge{
		{ID: "doc-in", KnowledgeBaseID: "kb-a", Title: "Corners"},
		{ID: "doc-elsewhere", KnowledgeBaseID: "kb-b", Title: "Other"},
	}}}
	kbInfos := []*agent.KnowledgeBaseInfo{{ID: "kb-a", Name: "TEST"}}
	ctx := context.Background()
	originOf := func(knowledgeID string) *types.QuestionOrigin {
		return &types.QuestionOrigin{KnowledgeBaseID: "kb-a", KnowledgeID: knowledgeID}
	}

	info := svc.resolveQuestionOriginInfo(ctx, originOf("doc-in"), kbInfos)
	require.NotNil(t, info)
	assert.Equal(t, "TEST", info.KnowledgeBaseName)
	require.NotNil(t, info.Document)
	assert.Equal(t, "Corners", info.Document.Title)

	info = svc.resolveQuestionOriginInfo(ctx, originOf("doc-elsewhere"), kbInfos)
	require.NotNil(t, info)
	assert.Nil(t, info.Document, "a document from another base must be dropped")

	info = svc.resolveQuestionOriginInfo(ctx, originOf("missing"), kbInfos)
	require.NotNil(t, info)
	assert.Nil(t, info.Document)

	assert.Nil(t, svc.resolveQuestionOriginInfo(ctx, nil, kbInfos))
}
