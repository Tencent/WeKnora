package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestParseWikiRelatedSlugs(t *testing.T) {
	got := parseWikiRelatedSlugs("```json\n{\"related\":[\"concept/a\",\"entity/b\",\"concept/a\",\"\"]}\n```")
	if len(got) != 2 || got[0] != "concept/a" || got[1] != "entity/b" {
		t.Fatalf("parseWikiRelatedSlugs = %v", got)
	}
	if parseWikiRelatedSlugs("not json") != nil {
		t.Fatal("garbage should yield nil")
	}
	got = parseWikiRelatedSlugs(`{"related":[{"slug":"concept/a","task_use":"ignored"},"entity/b"]}`)
	if len(got) != 2 || got[0] != "concept/a" || got[1] != "entity/b" {
		t.Fatalf("object slugs = %v", got)
	}
}

func TestFormatLeafCardsForPromptOmitsFolders(t *testing.T) {
	got := formatLeafCardsForPrompt([]types.WikiLeafKeywordCard{
		{
			Slug: "concept/root-crack", Title: "叶根断裂",
			Aliases: types.StringArray{"根部开裂", "leaf root crack"},
			Summary: "index listing summary should not win",
			Content: "# 叶根断裂\n\n压气机叶片根部因疲劳或过载发生断裂的失效模式。\n\n## 机理\n\n后面的正文不要进 desc。",
		},
		{Slug: "entity/vendor", Title: "供应商"},
	})
	if !strings.Contains(got, "id: concept/root-crack") {
		t.Fatalf("missing slug: %s", got)
	}
	if !strings.Contains(got, "keywords: 叶根断裂, 根部开裂, leaf root crack") {
		t.Fatalf("keywords = %s", got)
	}
	if !strings.Contains(got, "desc: 压气机叶片根部因疲劳或过载发生断裂的失效模式。") {
		t.Fatalf("missing first paragraph: %s", got)
	}
	if strings.Contains(got, "index listing summary") || strings.Contains(got, "后面的正文") {
		t.Fatalf("desc should be first paragraph only: %s", got)
	}
	if strings.Count(got, "desc:") != 1 {
		t.Fatalf("empty body should omit desc: %s", got)
	}
	if strings.Contains(got, "folder") || strings.Contains(got, "wiki_path") {
		t.Fatalf("prompt leaked programmable fields: %s", got)
	}
}

func TestFirstMarkdownParagraphSkipsTitleAndImages(t *testing.T) {
	got := firstMarkdownParagraph("# CAGR\n\n![p1](resource://a)\n\n![p2](resource://b)\n\n预计2026-2032年全球[[entity/electrostatic-chuck|静电卡盘]]市场CAGR为5.84%。\n\n## 应用\n\n不要这段。")
	if got != "预计2026-2032年全球静电卡盘市场CAGR为5.84%。" {
		t.Fatalf("got %q", got)
	}
}

func TestLeafDescriptionPrefersFirstParagraphThenSummary(t *testing.T) {
	got := leafDescription(types.WikiLeafKeywordCard{
		Summary: "index summary",
		Content: "# 标题\n\n**活性元素加入策略**是针对Al和Ti的特殊加料方法。\n\n## 细节\n\n后面忽略。",
	})
	if got != "活性元素加入策略是针对Al和Ti的特殊加料方法。" {
		t.Fatalf("should use first paragraph: %q", got)
	}

	long := strings.Repeat("裂纹扩展机理 ", 40)
	fallback := leafDescription(types.WikiLeafKeywordCard{Summary: "SUMMARY:  " + long})
	if !strings.HasPrefix(fallback, "裂纹扩展机理") {
		t.Fatalf("empty body should fall back to summary: %q", fallback)
	}
	if strings.Contains(fallback, "SUMMARY:") {
		t.Fatalf("should strip SUMMARY prefix: %q", fallback)
	}
	if utf8.RuneCountInString(strings.TrimSuffix(fallback, "...")) > wikiLeafRelateDescMaxRunes {
		t.Fatalf("desc longer than cap: %d", utf8.RuneCountInString(fallback))
	}
	if leafDescription(types.WikiLeafKeywordCard{}) != "" {
		t.Fatal("empty card should yield empty desc")
	}
}

func TestBuildWikiAssocResultNestsLeavesUnderCategoryPath(t *testing.T) {
	pages := []*types.WikiPage{
		{
			Slug: "concept/adhesive-crack", Title: "胶层开裂", PageType: types.WikiPageTypeConcept,
			CategoryPath: types.StringArray{"工艺", "失效模式"}, WikiPath: "concept/工艺/失效模式/胶层开裂",
			SourceRefs: types.StringArray{"doc-b|手册B"},
			ChunkRefs:  types.StringArray{"chunk-2"},
		},
		{
			Slug: "concept/root-crack", Title: "叶根断裂", PageType: types.WikiPageTypeConcept,
			CategoryPath: types.StringArray{"工艺", "失效模式"}, WikiPath: "concept/工艺/失效模式/叶根断裂",
			SourceRefs: types.StringArray{"doc-a|手册A"},
			ChunkRefs:  types.StringArray{"chunk-1"},
			OutLinks:   types.StringArray{"concept/adhesive-crack"},
		},
		{
			Slug: "entity/orphan", Title: "未分类", PageType: types.WikiPageTypeEntity,
			WikiPath: "entity/未分类",
		},
	}
	chunks := map[string]*types.Chunk{
		"chunk-1": {ID: "chunk-1", KnowledgeID: "doc-a", Content: "叶根裂纹原文"},
	}

	got := buildWikiAssocResult("叶根断裂原因", pages, chunks)
	if got.Query != "叶根断裂原因" {
		t.Fatalf("query = %q", got.Query)
	}
	if len(got.Pages) != 3 {
		t.Fatalf("pages = %d", len(got.Pages))
	}

	var rootLeaves int
	var process *types.WikiKnowledgeAssocNode
	for _, n := range got.Tree {
		if n.Name == "" {
			rootLeaves += len(n.Leaves)
			continue
		}
		if n.Name == "工艺" {
			process = n
		}
	}
	if rootLeaves != 1 {
		t.Fatalf("uncategorized leaves = %d, want 1", rootLeaves)
	}
	if process == nil || len(process.Children) != 1 || process.Children[0].Name != "失效模式" {
		t.Fatalf("process node = %+v", process)
	}
	mode := process.Children[0]
	if len(mode.Leaves) != 2 {
		t.Fatalf("failure-mode leaves = %d, want 2", len(mode.Leaves))
	}
	if mode.Leaves[0].Slug != "concept/root-crack" {
		t.Fatalf("first leaf by wiki_path = %s", mode.Leaves[0].Slug)
	}
	root := findLeaf(got.Tree, "concept/root-crack")
	if root == nil {
		t.Fatal("missing root-crack leaf")
	}
	if len(root.Sources) != 1 || root.Sources[0].KnowledgeID != "doc-a" || root.Sources[0].Title != "手册A" {
		t.Fatalf("sources = %+v", root.Sources)
	}
	if len(root.Chunks) != 1 || root.Chunks[0].Content != "叶根裂纹原文" {
		t.Fatalf("chunks = %+v", root.Chunks)
	}
}

func findLeaf(nodes []*types.WikiKnowledgeAssocNode, slug string) *types.WikiKnowledgeAssocLeaf {
	for _, n := range nodes {
		for _, leaf := range n.Leaves {
			if leaf.Slug == slug {
				return leaf
			}
		}
		if found := findLeaf(n.Children, slug); found != nil {
			return found
		}
	}
	return nil
}

type assocStubChat struct {
	mu         sync.Mutex
	content    string
	lastPrompt string
	calls      atomic.Int32
}

func (s *assocStubChat) Chat(_ context.Context, messages []chat.Message, _ *chat.ChatOptions) (*types.ChatResponse, error) {
	s.calls.Add(1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(messages) > 0 {
		s.lastPrompt = messages[0].Content
	}
	return &types.ChatResponse{Content: s.content}, nil
}

func (s *assocStubChat) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, errors.New("unused")
}

func (s *assocStubChat) GetModelName() string { return "stub" }
func (s *assocStubChat) GetModelID() string   { return "stub-model" }

type assocStubModelService struct {
	interfaces.ModelService
	model chat.Chat
}

func (s *assocStubModelService) GetChatModel(context.Context, string) (chat.Chat, error) {
	return s.model, nil
}

type assocStubKBService struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *assocStubKBService) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

func TestAssociateLeavesUsesModelJudgementAndAssemblesTree(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.WikiPage{}))

	ctx := context.Background()
	repo := repository.NewWikiPageRepository(db)
	now := time.Now()
	require.NoError(t, repo.Create(ctx, &types.WikiPage{
		ID: "p1", TenantID: 1, KnowledgeBaseID: "kb-assoc",
		Slug: "concept/root-crack", Title: "叶根断裂", PageType: types.WikiPageTypeConcept,
		Status: types.WikiPageStatusPublished,
		Content: "# 叶根断裂\n\n压气机叶片根部因疲劳或过载发生断裂的失效模式。\n\n## 机理\n\n叶根断裂机理",
		Summary: "index listing should not be used as desc",
		CategoryPath: types.StringArray{"工艺", "失效模式"},
		WikiPath:     "concept/工艺/失效模式/叶根断裂",
		SourceRefs:   types.StringArray{"doc-a|手册A"},
		Version:      1, CreatedAt: now, UpdatedAt: now,
	}))
	require.NoError(t, repo.Create(ctx, &types.WikiPage{
		ID: "p2", TenantID: 1, KnowledgeBaseID: "kb-assoc",
		Slug: "entity/vendor", Title: "供应商", PageType: types.WikiPageTypeEntity,
		Status: types.WikiPageStatusPublished, Content: "无关供应商",
		Version: 1, CreatedAt: now, UpdatedAt: now,
	}))

	model := &assocStubChat{content: `{"related":["concept/root-crack"]}`}
	svc := NewWikiPageService(
		repo, nil,
		&assocStubKBService{kb: &types.KnowledgeBase{
			ID: "kb-assoc", SummaryModelID: "m1",
			WikiConfig: &types.WikiConfig{SynthesisModelID: "wiki-llm"},
		}},
		nil, nil, &assocStubModelService{model: model},
	)

	got, err := svc.AssociateLeaves(ctx, "kb-assoc", "叶根为什么会裂", 10, "")
	require.NoError(t, err)
	if !strings.Contains(model.lastPrompt, "id: concept/root-crack") {
		t.Fatalf("prompt missing leaf card: %s", model.lastPrompt)
	}
	if !strings.Contains(model.lastPrompt, "desc: 压气机叶片根部因疲劳或过载发生断裂的失效模式。") {
		t.Fatalf("prompt missing first paragraph desc: %s", model.lastPrompt)
	}
	if strings.Contains(model.lastPrompt, "index listing") || strings.Contains(model.lastPrompt, "叶根断裂机理") {
		t.Fatalf("prompt used summary or later markdown sections: %s", model.lastPrompt)
	}
	if strings.Contains(model.lastPrompt, "工艺") || strings.Contains(model.lastPrompt, "手册A") {
		t.Fatalf("prompt leaked programmable context: %s", model.lastPrompt)
	}
	if findLeaf(got.Tree, "concept/root-crack") == nil {
		t.Fatalf("missing related leaf in tree: %+v", got.Tree)
	}
	if findLeaf(got.Tree, "entity/vendor") != nil {
		t.Fatal("unrelated leaf should not appear")
	}
	leaf := findLeaf(got.Tree, "concept/root-crack")
	if len(leaf.Sources) != 1 || leaf.Sources[0].Title != "手册A" {
		t.Fatalf("assembled sources = %+v", leaf.Sources)
	}
	if len(got.Tree) == 0 || got.Tree[0].Name != "工艺" {
		t.Fatalf("tree root = %+v", got.Tree)
	}
}

type assocRoutingChat struct {
	calls atomic.Int32
}

func (s *assocRoutingChat) Chat(_ context.Context, messages []chat.Message, _ *chat.ChatOptions) (*types.ChatResponse, error) {
	s.calls.Add(1)
	prompt := ""
	if len(messages) > 0 {
		prompt = messages[0].Content
	}
	var related []string
	if strings.Contains(prompt, "id: concept/alpha") {
		related = append(related, "concept/alpha")
	}
	if strings.Contains(prompt, "id: concept/omega") {
		related = append(related, "concept/omega")
	}
	raw, err := json.Marshal(map[string][]string{"related": related})
	if err != nil {
		return nil, err
	}
	return &types.ChatResponse{Content: string(raw)}, nil
}

func (s *assocRoutingChat) ChatStream(context.Context, []chat.Message, *chat.ChatOptions) (<-chan types.StreamResponse, error) {
	return nil, errors.New("unused")
}

func (s *assocRoutingChat) GetModelName() string { return "stub" }
func (s *assocRoutingChat) GetModelID() string   { return "stub-model" }

func TestRelateLeafCardsMergesParallelBatches(t *testing.T) {
	cards := make([]types.WikiLeafKeywordCard, wikiLeafRelateBatchSize+1)
	cards[0] = types.WikiLeafKeywordCard{Slug: "concept/alpha", Title: "Alpha"}
	for i := 1; i < wikiLeafRelateBatchSize; i++ {
		cards[i] = types.WikiLeafKeywordCard{Slug: fmt.Sprintf("concept/pad-%02d", i), Title: "Pad"}
	}
	cards[wikiLeafRelateBatchSize] = types.WikiLeafKeywordCard{Slug: "concept/omega", Title: "Omega"}

	model := &assocRoutingChat{}
	got, err := (&wikiPageService{}).relateLeafCards(context.Background(), model, "q", cards, "")
	require.NoError(t, err)
	if got := model.calls.Load(); got != 2 {
		t.Fatalf("batch calls = %d, want 2", got)
	}
	require.Equal(t, []string{"concept/alpha", "concept/omega"}, got)
}

func TestRelateLeafCardsUsesCallerPrompt(t *testing.T) {
	cards := []types.WikiLeafKeywordCard{
		{Slug: "concept/alpha", Title: "Alpha"},
	}
	model := &assocStubChat{content: `{"related":["concept/alpha"]}`}
	got, err := (&wikiPageService{}).relateLeafCards(
		context.Background(), model, "写一篇产品说明", cards,
		"只选会改变产品卖点表述的叶子。同领域背景不要选。",
	)
	require.NoError(t, err)
	require.Equal(t, []string{"concept/alpha"}, got)
	model.mu.Lock()
	prompt := model.lastPrompt
	model.mu.Unlock()
	if !strings.Contains(prompt, "<judging_instructions>") {
		t.Fatalf("missing caller shell: %s", prompt)
	}
	if !strings.Contains(prompt, "只选会改变产品卖点表述的叶子") {
		t.Fatalf("missing caller prompt: %s", prompt)
	}
	if strings.Contains(prompt, "change the writing strategy") {
		t.Fatalf("default writing criterion should be replaced: %s", prompt)
	}
}
