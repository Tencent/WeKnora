package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A page context is a hint inside this turn's search targets: it never adds a
// base, keeps a document only where a target covers it, and bounds the issue
// list the client can send.
func TestPageContextInTargets(t *testing.T) {
	ctx := context.Background()
	wholeBase := types.SearchTargets{{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-a"}}
	documents := types.SearchTargets{{
		Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb-a", KnowledgeIDs: []string{"doc-1"},
	}}

	got := pageContextInTargets(ctx, &types.PageContext{
		KnowledgeBaseID: " kb-a ",
		KnowledgeID:     " doc-9 ",
		WikiSlug:        " entity/acme ",
		WikiIssueIDs:    []string{"i-1", " i-2 ", "", "i-1"},
	}, wholeBase)
	assert.Equal(t, &types.PageContext{
		KnowledgeBaseID: "kb-a",
		KnowledgeID:     "doc-9",
		WikiSlug:        "entity/acme",
		WikiIssueIDs:    []string{"i-1", "i-2"},
	}, got)

	got = pageContextInTargets(ctx, &types.PageContext{KnowledgeBaseID: "kb-a", KnowledgeID: "doc-9"}, documents)
	require.NotNil(t, got)
	assert.Empty(t, got.KnowledgeID, "a document outside the document scope must be dropped")

	assert.Nil(t, pageContextInTargets(ctx, &types.PageContext{KnowledgeBaseID: "kb-other"}, wholeBase),
		"a base outside the search targets must not be added")
	assert.Nil(t, pageContextInTargets(ctx, &types.PageContext{KnowledgeID: "doc-1"}, wholeBase))
	assert.Nil(t, pageContextInTargets(ctx, nil, wholeBase))

	many := make([]string, types.MaxPageContextIssues+5)
	for i := range many {
		many[i] = fmt.Sprintf("i-%d", i)
	}
	got = pageContextInTargets(ctx, &types.PageContext{KnowledgeBaseID: "kb-a", WikiIssueIDs: many}, wholeBase)
	require.NotNil(t, got)
	assert.Len(t, got.WikiIssueIDs, types.MaxPageContextIssues)
}

// A turn paused for approval resumes with the page the user had open.
func TestResumableAgentRequestKeepsPageContext(t *testing.T) {
	page := &types.PageContext{KnowledgeBaseID: "kb-a", WikiIssueIDs: []string{"i-1"}}
	resumable := resumableAgentRequest(context.Background(), &types.QARequest{
		Session:     &types.Session{ID: "s", TenantID: 1},
		CustomAgent: &types.CustomAgent{ID: types.BuiltinWeKnoraAssistantID},
		PageContext: page,
	})
	assert.Equal(t, page, resumable.PageContext)
}

type pageContextWikiService struct {
	interfaces.WikiPageService
	pages  map[string]*types.WikiPage
	issues []*types.WikiPageIssue
}

func (s *pageContextWikiService) GetPageBySlug(_ context.Context, kbID, slug string) (*types.WikiPage, error) {
	if page := s.pages[kbID+"/"+slug]; page != nil {
		return page, nil
	}
	return nil, errors.New("not found")
}

func (s *pageContextWikiService) ListIssues(_ context.Context, kbID, _, _ string) ([]*types.WikiPageIssue, error) {
	var out []*types.WikiPageIssue
	for _, issue := range s.issues {
		if issue.KnowledgeBaseID == kbID {
			out = append(out, issue)
		}
	}
	return out, nil
}

// Every part of the page context is checked to exist in its base; parts that
// do not resolve are dropped one by one instead of discarding the context.
func TestResolvePageContextInfo(t *testing.T) {
	svc := &agentService{
		knowledgeService: &tagTargetKnowledgeService{knowledges: []*types.Knowledge{
			{ID: "doc-in", KnowledgeBaseID: "kb-a", Title: "Corners"},
			{ID: "doc-elsewhere", KnowledgeBaseID: "kb-b", Title: "Other"},
		}},
		wikiPageService: &pageContextWikiService{
			pages: map[string]*types.WikiPage{
				"kb-a/entity/acme": {Slug: "entity/acme", Title: "Acme"},
				"kb-b/entity/beta": {Slug: "entity/beta", Title: "Beta"},
			},
			issues: []*types.WikiPageIssue{
				{ID: "i-1", KnowledgeBaseID: "kb-a", Slug: "entity/acme", IssueType: "broken_link", Status: "pending"},
				{ID: "i-2", KnowledgeBaseID: "kb-a", Slug: "entity/acme", IssueType: "stale", Status: "resolved"},
				{ID: "i-other", KnowledgeBaseID: "kb-b", Slug: "entity/beta", IssueType: "stale", Status: "pending"},
			},
		},
	}
	targets := types.SearchTargets{{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-a"}}
	kbInfos := []*agent.KnowledgeBaseInfo{{ID: "kb-a", Name: "Handbook"}}
	ctx := context.Background()

	info := svc.resolvePageContextInfo(ctx, &types.PageContext{
		KnowledgeBaseID: "kb-a",
		KnowledgeID:     "doc-in",
		WikiSlug:        "entity/acme",
		WikiIssueIDs:    []string{"i-2", "i-other", "missing", "i-1"},
	}, targets, kbInfos)
	require.NotNil(t, info)
	assert.Equal(t, "Handbook", info.KnowledgeBaseName)
	require.NotNil(t, info.Document)
	assert.Equal(t, "Corners", info.Document.Title)
	assert.Equal(t, &agent.PageContextWikiPage{Slug: "entity/acme", Title: "Acme"}, info.WikiPage)
	require.Len(t, info.WikiIssues, 2, "issues of another base or unknown IDs must be dropped")
	assert.Equal(t, "i-2", info.WikiIssues[0].ID, "issues keep the order the client listed them in")
	assert.Equal(t, "resolved", info.WikiIssues[0].Status)
	assert.Equal(t, "i-1", info.WikiIssues[1].ID)

	info = svc.resolvePageContextInfo(ctx, &types.PageContext{
		KnowledgeBaseID: "kb-a",
		KnowledgeID:     "doc-elsewhere",
		WikiSlug:        "entity/beta",
	}, targets, kbInfos)
	require.NotNil(t, info)
	assert.Nil(t, info.Document, "a document from another base must be dropped")
	assert.Nil(t, info.WikiPage, "a Wiki page from another base must be dropped")
	assert.Empty(t, info.WikiIssues)

	assert.Nil(t, svc.resolvePageContextInfo(ctx, &types.PageContext{KnowledgeBaseID: "kb-b"}, targets, kbInfos),
		"a base outside the search targets must be dropped")
	assert.Nil(t, svc.resolvePageContextInfo(ctx, nil, targets, kbInfos))
}

type pageContextKnowledgeService struct {
	interfaces.KnowledgeService
	tags map[string][]*types.KnowledgeTag
	err  error
}

func (s *pageContextKnowledgeService) GetKnowledgeTags(
	context.Context, []string,
) (map[string][]*types.KnowledgeTag, error) {
	return s.tags, s.err
}

func TestResolvePageContextWikiScope(t *testing.T) {
	document := &types.SearchTarget{
		Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb", KnowledgeIDs: []string{"selected"},
	}
	tag := &types.SearchTarget{
		Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb", TagIDs: []string{"tag"},
	}
	whole := &types.SearchTarget{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb"}
	for _, tc := range []struct {
		name      string
		targets   types.SearchTargets
		source    string
		pageType  string
		tagErr    error
		issueOnly bool
		want      bool
	}{
		{name: "selected document", targets: types.SearchTargets{document}, source: "selected", want: true},
		{name: "other document", targets: types.SearchTargets{document}, source: "other"},
		{name: "selected tag", targets: types.SearchTargets{tag}, source: "tagged", want: true},
		{name: "other tag", targets: types.SearchTargets{tag}, source: "other"},
		{
			name: "tag lookup failure", targets: types.SearchTargets{tag},
			source: "tagged", tagErr: errors.New("lookup failed"),
		},
		{name: "document and tag union", targets: types.SearchTargets{document, tag}, source: "tagged", want: true},
		{
			name: "whole base supersedes document", targets: types.SearchTargets{document, whole},
			source: "other", want: true,
		},
		{name: "uncited page", targets: types.SearchTargets{document}},
		{
			name: "structural page", targets: types.SearchTargets{document},
			source: "selected", pageType: types.WikiPageTypeIndex,
		},
		{
			name: "issue only in scope", targets: types.SearchTargets{document},
			source: "selected", issueOnly: true, want: true,
		},
		{name: "issue only outside scope", targets: types.SearchTargets{document}, source: "other", issueOnly: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := &types.WikiPage{KnowledgeBaseID: "kb", Slug: "entity/acme", Title: "Acme", PageType: tc.pageType}
			if tc.source != "" {
				page.SourceRefs = types.StringArray{tc.source + "|Source"}
			}
			svc := &agentService{
				knowledgeService: &pageContextKnowledgeService{tags: map[string][]*types.KnowledgeTag{
					"tagged": {{ID: "tag"}},
				}, err: tc.tagErr},
				wikiPageService: &pageContextWikiService{
					pages:  map[string]*types.WikiPage{"kb/entity/acme": page},
					issues: []*types.WikiPageIssue{{ID: "issue", KnowledgeBaseID: "kb", Slug: page.Slug}},
				},
			}
			pageContext := &types.PageContext{
				KnowledgeBaseID: "kb", WikiSlug: page.Slug, WikiIssueIDs: []string{"issue"},
			}
			if tc.issueOnly {
				pageContext.WikiSlug = ""
			}
			info := svc.resolvePageContextInfo(context.Background(), pageContext,
				tc.targets, []*agent.KnowledgeBaseInfo{{ID: "kb", Name: "Handbook"}})
			require.NotNil(t, info)
			if tc.want {
				if !tc.issueOnly {
					require.NotNil(t, info.WikiPage)
					require.Equal(t, page.Title, info.WikiPage.Title)
				}
				require.Len(t, info.WikiIssues, 1)
			} else {
				require.Nil(t, info.WikiPage, "out-of-scope page metadata must not enter the model context")
				require.Empty(t, info.WikiIssues, "issue references must obey the same page scope")
			}
		})
	}
}

func TestPageContextInTargetsKeepsAgent(t *testing.T) {
	for _, page := range []*types.PageContext{
		{AgentID: " agent-a "},
		{AgentID: "agent-a", KnowledgeBaseID: "outside", KnowledgeID: "doc"},
	} {
		assert.Equal(t, &types.PageContext{AgentID: "agent-a"}, pageContextInTargets(context.Background(), page, nil))
	}
}

type pageContextAgentRepo struct {
	interfaces.CustomAgentRepository
}

func (*pageContextAgentRepo) GetAgentByID(_ context.Context, id string, tenantID uint64) (*types.CustomAgent, error) {
	if (id == "own" && tenantID == 1) || (id == "foreign" && tenantID == 2) {
		return &types.CustomAgent{ID: id, TenantID: tenantID, Name: "My agent"}, nil
	}
	return nil, repository.ErrCustomAgentNotFound
}

func TestResolveAgentPageContext(t *testing.T) {
	require.NoError(t, types.LoadBuiltinAgentsConfig(filepath.Join("..", "..", "..", "config")))
	svc := &agentService{customAgentService: &customAgentService{repo: &pageContextAgentRepo{}}}
	ctx := types.WithCaller(context.Background(), types.Caller{TenantID: 1, UserID: "u1"})
	ctx = types.WithExecutionTenant(ctx, 2)
	for _, id := range []string{"own", "unknown", "foreign", types.BuiltinSmartReasoningID} {
		t.Run(id, func(t *testing.T) {
			info := svc.resolvePageContextInfo(ctx, &types.PageContext{AgentID: id}, nil, nil)
			if id == "own" || id == types.BuiltinSmartReasoningID {
				require.NotNil(t, info)
				require.NotNil(t, info.Agent)
				assert.Equal(t, id, info.Agent.ID)
				assert.NotEmpty(t, info.Agent.Name)
			} else {
				assert.Nil(t, info)
			}
		})
	}
}
