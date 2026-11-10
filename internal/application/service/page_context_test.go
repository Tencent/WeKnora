package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

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
	}, targets, kbInfos, false)
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
	}, targets, kbInfos, false)
	require.NotNil(t, info)
	assert.Nil(t, info.Document, "a document from another base must be dropped")
	assert.Nil(t, info.WikiPage, "a Wiki page from another base must be dropped")
	assert.Empty(t, info.WikiIssues)

	assert.Nil(t, svc.resolvePageContextInfo(ctx, &types.PageContext{KnowledgeBaseID: "kb-b"}, targets, kbInfos, false),
		"a base outside the search targets must be dropped")
	assert.Nil(t, svc.resolvePageContextInfo(ctx, nil, targets, kbInfos, false))
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
				tc.targets, []*agent.KnowledgeBaseInfo{{ID: "kb", Name: "Handbook"}}, false)
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
			info := svc.resolvePageContextInfo(ctx, &types.PageContext{AgentID: id}, nil, nil, false)
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

func TestPageContextInTargetsKeepsSettingsAndChat(t *testing.T) {
	got := pageContextInTargets(context.Background(), &types.PageContext{
		Page: " chat ", Section: " answer ", ModelID: " m-1 ", MCPServiceID: " mcp-1 ",
		SessionID: " s-1 ", MessageID: " msg-1 ",
		KnowledgeBaseID: "outside",
	}, nil)
	assert.Equal(t, &types.PageContext{
		Page: "chat", Section: "answer", ModelID: "m-1", MCPServiceID: "mcp-1", SessionID: "s-1", MessageID: "msg-1",
	}, got)

	assert.Nil(t, pageContextInTargets(context.Background(), &types.PageContext{Page: "admin"}, nil),
		"an unknown page identifier is dropped")
	got = pageContextInTargets(context.Background(),
		&types.PageContext{Page: types.PageAgentEditor, Section: "<b>Tools</b>", AgentID: "a"}, nil)
	assert.Equal(t, &types.PageContext{Page: types.PageAgentEditor, AgentID: "a"}, got,
		"a section that is not a plain key is dropped")
	assert.Nil(t, pageContextInTargets(context.Background(), &types.PageContext{Section: "tools"}, nil),
		"a section means nothing without its page")
	assert.Nil(t, pageContextInTargets(context.Background(), &types.PageContext{MessageID: "msg-1"}, nil),
		"a message without its session cannot be looked up")
}

type pageContextModelService struct {
	interfaces.ModelService
}

func (*pageContextModelService) GetModelByID(_ context.Context, id string) (*types.Model, error) {
	if id == "m-1" {
		return &types.Model{ID: id, Name: "qwen-max", DisplayName: "Qwen", Type: types.ModelTypeKnowledgeQA}, nil
	}
	return nil, errors.New("not found")
}

type pageContextMCPService struct {
	interfaces.MCPServiceService
}

func (*pageContextMCPService) GetMCPServiceByID(
	_ context.Context, tenantID uint64, id string,
) (*types.MCPService, error) {
	if id == "mcp-1" && tenantID == 1 {
		return &types.MCPService{ID: id, Name: "Jira", TransportType: types.MCPTransportSSE, Enabled: true}, nil
	}
	return nil, errors.New("not found")
}

type pageContextMessageService struct {
	interfaces.MessageService
}

func (*pageContextMessageService) GetMessage(_ context.Context, sessionID, id string) (*types.Message, error) {
	if sessionID != "s-1" || id != "msg-1" {
		return nil, errors.New("session not found")
	}
	return &types.Message{
		ID: id, SessionID: sessionID, RequestID: "r-2", Role: "assistant", AgentID: "agent-1",
		Content: "Use the handbook.",
		KnowledgeReferences: types.References{
			{KnowledgeID: "k-1", KnowledgeBaseID: "kb-1", KnowledgeTitle: "Handbook"},
			{KnowledgeID: "k-1", KnowledgeBaseID: "kb-1", KnowledgeTitle: "Handbook"},
			{KnowledgeID: "k-2", KnowledgeFilename: "faq.pdf"},
		},
	}, nil
}

func (*pageContextMessageService) GetMessagesBySessionBeforeTime(
	context.Context, string, time.Time, int,
) ([]*types.Message, error) {
	return []*types.Message{
		{RequestID: "r-1", Role: "user", Content: "Earlier question"},
		{RequestID: "r-1", Role: "assistant", Content: "Earlier answer"},
		{RequestID: "r-2", Role: "user", Content: "  Where are the rules?  "},
	}, nil
}

// Settings and chat resources are the assistant's only, and each is dropped
// unless the caller can read it.
func TestResolveAssistantPageContext(t *testing.T) {
	svc := &agentService{
		modelService:      &pageContextModelService{},
		mcpServiceService: &pageContextMCPService{},
		messageService:    &pageContextMessageService{},
	}
	ctx := types.WithCaller(context.Background(), types.Caller{TenantID: 1, UserID: "u1"})
	page := &types.PageContext{
		Page: types.PageChat, ModelID: "m-1", MCPServiceID: "mcp-1", SessionID: "s-1", MessageID: "msg-1",
	}

	assert.Nil(t, svc.resolvePageContextInfo(ctx, page, nil, nil, false),
		"other agents never see settings or chat resources")

	info := svc.resolvePageContextInfo(ctx, page, nil, nil, true)
	require.NotNil(t, info)
	assert.Equal(t, types.PageChat, info.Page)
	assert.Equal(t, &agent.PageContextModel{ID: "m-1", Name: "Qwen", Type: string(types.ModelTypeKnowledgeQA)},
		info.Model, "the display name wins and no endpoint or key is carried")
	assert.Equal(t, &agent.PageContextMCPService{ID: "mcp-1", Name: "Jira", Transport: "sse", Enabled: true},
		info.MCPService)
	require.NotNil(t, info.Message)
	assert.Equal(t, "Where are the rules?", info.Message.Question, "the question of the same request is quoted")
	assert.Equal(t, "Use the handbook.", info.Message.Answer)
	assert.Equal(t, []*agent.PageContextReference{
		{KnowledgeID: "k-1", KnowledgeBaseID: "kb-1", Title: "Handbook"},
		{KnowledgeID: "k-2", Title: "faq.pdf"},
	}, info.Message.References)

	info = svc.resolvePageContextInfo(ctx, &types.PageContext{
		Page: types.PageMCPServices, ModelID: "gone", MCPServiceID: "gone", SessionID: "other", MessageID: "msg-1",
	}, nil, nil, true)
	require.NotNil(t, info)
	assert.Nil(t, info.Model)
	assert.Nil(t, info.MCPService)
	assert.Nil(t, info.Message, "a message in a session the caller cannot read is dropped")
}

// The open view and the selection are what the user sees; they are bounded
// and reach only the assistant.
func TestPageContextViewAndSelection(t *testing.T) {
	long := strings.Repeat("字", types.PageSelectionMaxRunes+10)
	got := pageContextInTargets(context.Background(), &types.PageContext{
		View: "  Edit \n Markdown  ",
		Selection: &types.PageSelection{
			Text: long, Field: " Content ", Ref: "e3", StartLine: 4, EndLine: 6,
		},
	}, nil)
	require.NotNil(t, got, "a view or selection alone is kept")
	assert.Equal(t, "Edit Markdown", got.View)
	require.NotNil(t, got.Selection)
	assert.Equal(t, types.PageSelectionMaxRunes, utf8.RuneCountInString(got.Selection.Text))
	assert.Equal(t, "Content", got.Selection.Field)
	assert.Equal(t, "e3", got.Selection.Ref)
	assert.Equal(t, 4, got.Selection.StartLine)

	got = pageContextInTargets(context.Background(), &types.PageContext{
		Selection: &types.PageSelection{Text: "x", Ref: "body", StartLine: 5, EndLine: 2},
	}, nil)
	require.NotNil(t, got)
	assert.Equal(t, &types.PageSelection{Text: "x"}, got.Selection, "a bad ref and line range are dropped")
	assert.Nil(t, pageContextInTargets(context.Background(),
		&types.PageContext{Selection: &types.PageSelection{Text: "  \n"}}, nil), "a blank selection is nothing")

	titled := pageContextInTargets(context.Background(), &types.PageContext{Title: " Agents \n", Page: "agents"}, nil)
	require.NotNil(t, titled, "a page title alone is kept")
	assert.Equal(t, "Agents", titled.Title)
	assert.Empty(t, titled.Page, "an unknown page identifier is still dropped")

	svc := &agentService{}
	page := &types.PageContext{Title: "Agents", View: "Edit", Selection: &types.PageSelection{Text: "x"}}
	assert.Nil(t, svc.resolvePageContextInfo(context.Background(), page, nil, nil, false),
		"other agents never see the title, view or selection")
	info := svc.resolvePageContextInfo(context.Background(), page, nil, nil, true)
	require.NotNil(t, info)
	assert.Equal(t, "Agents", info.Title)
	assert.Equal(t, "Edit", info.View)
	assert.Equal(t, "x", info.Selection.Text)
}
