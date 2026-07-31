package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type wikiRepairKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *wikiRepairKBService) GetKnowledgeBaseByID(_ context.Context, _ string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

func TestResolveWikiRepairModelIDRequiresKBConfig(t *testing.T) {
	kbSvc := &wikiRepairKBService{
		kb: &types.KnowledgeBase{ID: "kb-1", WikiConfig: &types.WikiConfig{}},
	}
	modelSvc := &stubModelService{modelsByID: map[string]*types.Model{}}

	modelID, err := ResolveWikiRepairModelID(context.Background(), kbSvc, modelSvc, "kb-1")

	require.Error(t, err)
	assert.Empty(t, modelID)
	assert.Contains(t, err.Error(), "not configured")
}

func TestResolveWikiRepairModelIDUsesConfiguredChatModel(t *testing.T) {
	kbSvc := &wikiRepairKBService{
		kb: &types.KnowledgeBase{
			ID: "kb-1",
			WikiConfig: &types.WikiConfig{
				RepairModelID: "repair-chat",
			},
		},
	}
	modelSvc := &stubModelService{
		modelsByID: map[string]*types.Model{
			"repair-chat": {ID: "repair-chat", Type: types.ModelTypeKnowledgeQA},
		},
	}

	modelID, err := ResolveWikiRepairModelID(context.Background(), kbSvc, modelSvc, "kb-1")

	require.NoError(t, err)
	assert.Equal(t, "repair-chat", modelID)
}

func TestResolveChatModelIDUsesWikiRepairModelForBuiltinFixer(t *testing.T) {
	svc := &sessionService{
		knowledgeBaseService: &wikiRepairKBService{
			kb: &types.KnowledgeBase{
				ID: "kb-1",
				WikiConfig: &types.WikiConfig{
					RepairModelID: "repair-chat",
				},
			},
		},
		modelService: &stubModelService{
			modelsByID: map[string]*types.Model{
				"repair-chat": {ID: "repair-chat", Type: types.ModelTypeKnowledgeQA},
			},
		},
	}
	req := &types.QARequest{
		Session: &types.Session{},
		CustomAgent: &types.CustomAgent{
			ID: types.BuiltinWikiFixerID,
		},
	}

	modelID, err := svc.resolveChatModelID(context.Background(), req, []string{"kb-1"}, nil)

	require.NoError(t, err)
	assert.Equal(t, "repair-chat", modelID)
}
