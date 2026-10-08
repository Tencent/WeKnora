package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A client-supplied origin is a hint inside this turn's search targets. It
// must survive document- and tag-only scopes, whose bases never appear in the
// resolved KB list, and must never point outside the targets.
func TestQuestionOriginInTargets(t *testing.T) {
	ctx := context.Background()
	origin := func(kbID, docID string) *types.QuestionOrigin {
		return &types.QuestionOrigin{KnowledgeBaseID: kbID, KnowledgeID: docID}
	}
	wholeBase := types.SearchTargets{{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-a"}}
	// Selected files, or a document-KB tag scope resolved to its documents.
	documents := types.SearchTargets{{
		Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-a", KnowledgeIDs: []string{"doc-1", "doc-2"},
	}}
	// An FAQ tag scope filters chunks inside the base; documents cannot be checked.
	faqTags := types.SearchTargets{{
		Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-a", TagIDs: []string{"tag-1"},
	}}

	for _, tc := range []struct {
		name    string
		origin  *types.QuestionOrigin
		targets types.SearchTargets
		want    *types.QuestionOrigin
	}{
		{"whole base keeps document", origin(" kb-a ", " doc-9 "), wholeBase, origin("kb-a", "doc-9")},
		{"document scope keeps listed document", origin("kb-a", "doc-2"), documents, origin("kb-a", "doc-2")},
		{"document scope drops unlisted document", origin("kb-a", "doc-9"), documents, origin("kb-a", "")},
		{"tag-filtered base keeps base only", origin("kb-a", "doc-1"), faqTags, origin("kb-a", "")},
		{"base outside targets", origin("kb-other", "doc-1"), wholeBase, nil},
		{"no targets", origin("kb-a", ""), nil, nil},
		{"empty base", origin("", "doc-1"), wholeBase, nil},
		{"no origin", nil, wholeBase, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, questionOriginInTargets(ctx, tc.origin, tc.targets))
		})
	}
}

// An agent that retrieves only on @mention treats a picked suggestion from
// one of its bases as selecting that base, and nothing outside its scope.
func TestResolveKnowledgeBasesAnchorsOnQuestionOriginWhenRetrievingOnlyOnMention(t *testing.T) {
	svc := &sessionService{}
	request := func(originKB string) *types.QARequest {
		req := &types.QARequest{
			Session: &types.Session{TenantID: 10000},
			CustomAgent: &types.CustomAgent{TenantID: 10000, Config: types.CustomAgentConfig{
				KBSelectionMode:             "selected",
				KnowledgeBases:              []string{"kb-a", "kb-b"},
				RetrieveKBOnlyWhenMentioned: true,
			}},
		}
		if originKB != "" {
			req.QuestionOrigin = &types.QuestionOrigin{KnowledgeBaseID: originKB, KnowledgeID: "doc-1"}
		}
		return req
	}

	kbIDs, knowledgeIDs, err := svc.resolveKnowledgeBases(context.Background(), request("kb-b"))
	require.NoError(t, err)
	assert.Equal(t, []string{"kb-b"}, kbIDs)
	assert.Empty(t, knowledgeIDs)

	kbIDs, _, err = svc.resolveKnowledgeBases(context.Background(), request("kb-outside"))
	require.NoError(t, err)
	assert.Empty(t, kbIDs, "an origin outside the agent's bases must not enable retrieval")

	kbIDs, _, err = svc.resolveKnowledgeBases(context.Background(), request(""))
	require.NoError(t, err)
	assert.Empty(t, kbIDs)
}

type questionOriginKBService struct {
	interfaces.KnowledgeBaseService
	kbs map[string]*types.KnowledgeBase
}

func (s *questionOriginKBService) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	if kb := s.kbs[id]; kb != nil {
		return kb, nil
	}
	return nil, errors.New("not found")
}

func TestResolveQuestionOriginInfo(t *testing.T) {
	svc := &agentService{
		knowledgeService: &tagTargetKnowledgeService{knowledges: []*types.Knowledge{
			{ID: "doc-in", KnowledgeBaseID: "kb-a", Title: "Corners"},
			{ID: "doc-elsewhere", KnowledgeBaseID: "kb-b", Title: "Other"},
			{ID: "doc-c", KnowledgeBaseID: "kb-c", Title: "File"},
		}},
		knowledgeBaseService: &questionOriginKBService{kbs: map[string]*types.KnowledgeBase{
			"kb-c": {ID: "kb-c", Name: "Files"},
		}},
	}
	targets := types.SearchTargets{
		{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-a"},
		{Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-c", KnowledgeIDs: []string{"doc-c"}},
	}
	kbInfos := []*agent.KnowledgeBaseInfo{{ID: "kb-a", Name: "TEST"}}
	ctx := context.Background()
	resolve := func(kbID, docID string) *agent.QuestionOriginInfo {
		return svc.resolveQuestionOriginInfo(ctx, &types.QuestionOrigin{KnowledgeBaseID: kbID, KnowledgeID: docID},
			targets, kbInfos)
	}

	info := resolve("kb-a", "doc-in")
	require.NotNil(t, info)
	assert.Equal(t, "TEST", info.KnowledgeBaseName)
	require.NotNil(t, info.Document)
	assert.Equal(t, "Corners", info.Document.Title)

	info = resolve("kb-a", "doc-elsewhere")
	require.NotNil(t, info)
	assert.Nil(t, info.Document, "a document from another base must be dropped")

	info = resolve("kb-a", "missing")
	require.NotNil(t, info)
	assert.Nil(t, info.Document)

	// A base reached through a file scope is not in kbInfos when other bases
	// are selected; its name is looked up instead of rendered empty.
	info = resolve("kb-c", "doc-c")
	require.NotNil(t, info)
	assert.Equal(t, "Files", info.KnowledgeBaseName)
	require.NotNil(t, info.Document)

	assert.Nil(t, resolve("kb-b", "doc-elsewhere"), "a base outside the search targets must be dropped")
	assert.Nil(t, svc.resolveQuestionOriginInfo(ctx, nil, targets, kbInfos))
}
