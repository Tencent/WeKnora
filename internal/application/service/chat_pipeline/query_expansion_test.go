package chatpipeline

import (
	"context"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
)

func TestExtractKeywordsSegmentsChineseQuery(t *testing.T) {
	keywords := extractKeywords("如何配置向量数据库")

	assert.NotContains(t, keywords, "如何配置向量数据库")
	assert.Contains(t, keywords, "配置")
	assert.Contains(t, keywords, "向量")
	assert.Contains(t, keywords, "数据库")
}

func TestTokenizePreservesMixedLanguageBoundaries(t *testing.T) {
	tokens := tokenize("RAG如何配置PostgreSQL")

	assert.Contains(t, tokens, "RAG")
	assert.Contains(t, tokens, "配置")
	assert.Contains(t, tokens, "PostgreSQL")
	assert.NotContains(t, tokens, "RAG如何配置")
}

func TestExtractKeywordsDropsSingleRuneChineseTokens(t *testing.T) {
	keywords := extractKeywords("他来到了网易杭研大厦")

	assert.Contains(t, keywords, "网易")
	assert.Contains(t, keywords, "大厦")
	for _, keyword := range keywords {
		assert.Greater(t, utf8.RuneCountInString(keyword), 1, "unexpected single-rune keyword %q", keyword)
	}
}

func TestExpandQueriesBuildsChineseKeywordVariant(t *testing.T) {
	expansions := (&PluginSearch{}).expandQueries(context.Background(), &types.ChatManage{
		PipelineState: types.PipelineState{RewriteQuery: "如何配置向量数据库"},
	})

	var foundKeywordVariant bool
	for _, expansion := range expansions {
		fields := strings.Fields(expansion)
		if containsToken(fields, "配置") && containsToken(fields, "向量") && containsToken(fields, "数据库") {
			foundKeywordVariant = true
			break
		}
	}

	assert.True(t, foundKeywordVariant, "expected a Chinese keyword expansion with segmented terms, got %v", expansions)
}

func containsToken(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type recordingExpansionKBService struct {
	interfaces.KnowledgeBaseService
	mu     sync.Mutex
	params []types.SearchParams
}

func (s *recordingExpansionKBService) HybridSearch(
	_ context.Context, _ string, params types.SearchParams,
) ([]*types.SearchResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.params = append(s.params, params)
	return nil, nil
}

// Expansion variants only add keyword recall, so they must not re-embed and
// repeat the vector search; with keyword matching off there is nothing to do.
func TestRunQueryExpansionSearchesKeywordsOnly(t *testing.T) {
	kbService := &recordingExpansionKBService{}
	plugin := &PluginSearch{knowledgeBaseService: kbService}
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			EmbeddingTopK: 10,
			SearchTargets: types.SearchTargets{{KnowledgeBaseID: "kb-1", Type: types.SearchTargetTypeKnowledgeBase}},
		},
		PipelineState: types.PipelineState{RewriteQuery: "如何配置向量数据库的索引参数"},
	}

	plugin.runQueryExpansion(context.Background(), cm)
	if len(kbService.params) == 0 {
		t.Fatal("expected expansion searches")
	}
	for _, p := range kbService.params {
		if !p.DisableVectorMatch || p.DisableKeywordsMatch {
			t.Fatalf("expansion search params = %+v, want keyword-only", p)
		}
	}

	kbService.params = nil
	cm.DisableKeywordsMatch = true
	plugin.runQueryExpansion(context.Background(), cm)
	if len(kbService.params) != 0 {
		t.Fatalf("expansion ran %d searches with keyword matching off", len(kbService.params))
	}
}
