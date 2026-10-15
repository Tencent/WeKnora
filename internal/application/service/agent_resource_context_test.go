package service

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type pageResourceDispatcher struct {
	calls      []operations.Call
	identities []operations.Identity
}

func (d *pageResourceDispatcher) Dispatch(
	_ context.Context, identity operations.Identity, call operations.Call,
) (*operations.Response, error) {
	d.calls = append(d.calls, call)
	d.identities = append(d.identities, identity)
	body := ""
	status := http.StatusOK
	switch call.Path {
	case "/api/v1/models/allowed":
		body = `{"data":{"id":"allowed","name":"Visible model","api_key":"never-show"}}`
	case "/api/v1/datasource/source":
		body = `{"id":"source","name":"Visible source","knowledge_base_id":"kb","config":{"token":"never-show"}}`
	case "/api/v1/knowledge-bases/kb/faq/entries/1":
		body = `{"data":{"id":1,"standard_question":"Visible FAQ","answers":["not needed in context"]}}`
	default:
		status = http.StatusForbidden
		body = `{"error":"unavailable"}`
	}
	return &operations.Response{Status: status, Body: []byte(body)}, nil
}

func pageResourceContext() context.Context {
	ctx := types.WithCaller(context.Background(), types.Caller{UserID: "u", TenantID: 1, Role: types.TenantRoleViewer})
	return types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "u"})
}

func TestResourcePageContextReauthorizesReadsAndDropsUnavailableIDs(t *testing.T) {
	d := &pageResourceDispatcher{}
	svc := &agentService{operationDispatcher: d}
	ctx := types.WithExecutionTenant(pageResourceContext(), 99)
	info := svc.resolvePageContextInfo(ctx, &types.PageContext{ModelID: "allowed", DataSourceID: "source"}, nil, nil)
	require.NotNil(t, info)
	require.Equal(t, "Visible model", info.Model.Name)
	require.Equal(t, "Visible source", info.DataSource.Name)
	for i, call := range d.calls {
		require.Equal(t, http.MethodGet, call.Method)
		require.EqualValues(t, 1, d.identities[i].TenantID)
	}
	encoded, err := json.Marshal(info)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "never-show")
	require.Nil(t, svc.resolvePageContextInfo(ctx, &types.PageContext{ModelID: "foreign"}, nil, nil))
	require.Nil(t, svc.resolvePageContextInfo(ctx, &types.PageContext{
		KnowledgeBaseID: "other-kb", DataSourceID: "source",
	}, nil, nil))
	before := len(d.calls)
	machine := types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalAPITenant, ID: "key"})
	require.Nil(t, svc.resolvePageContextInfo(machine, &types.PageContext{ModelID: "allowed"}, nil, nil))
	require.Equal(t, before, len(d.calls))
	require.Nil(t, svc.readPageResource(ctx, "data_source.sync", map[string]any{"data_source_id": "source"}))
	require.Equal(t, before, len(d.calls))
}

func TestSelectedFAQContextStaysWithinWholeKBAndRechecksEntries(t *testing.T) {
	d := &pageResourceDispatcher{}
	svc := &agentService{operationDispatcher: d}
	page := &types.PageContext{KnowledgeBaseID: "kb", FAQEntryIDs: []int64{1, 2, 1, -1}}
	whole := types.SearchTargets{{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb"}}
	info := svc.resolvePageContextInfo(pageResourceContext(), page, whole, nil)
	require.Len(t, info.FAQEntries, 1)
	require.Equal(t, "1", info.FAQEntries[0].ID)
	require.Len(t, d.calls, 2)
	for _, targets := range []types.SearchTargets{
		{{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb", TagIDs: []string{"tag"}}},
		{{Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb", KnowledgeIDs: []string{"doc", ""}}},
	} {
		kept := pageContextInTargets(context.Background(), page, targets)
		require.Empty(t, kept.FAQEntryIDs)
	}
	require.Nil(t, pageContextInTargets(context.Background(), page, nil))
}

func TestPageContextSelectionBoundsAndDoesNotWidenDocumentScope(t *testing.T) {
	page := &types.PageContext{KnowledgeBaseID: "kb", SelectedKnowledgeIDs: []string{"allowed", "foreign", "allowed"}}
	targets := types.SearchTargets{{
		Type: types.SearchTargetTypeKnowledge, KnowledgeBaseID: "kb", KnowledgeIDs: []string{"allowed"},
	}}
	kept := pageContextInTargets(context.Background(), page, targets)
	require.Equal(t, []string{"allowed"}, kept.SelectedKnowledgeIDs)
	for i := int64(1); i <= 50; i++ {
		page.FAQEntryIDs = append(page.FAQEntryIDs, i)
	}
	whole := types.SearchTargets{{Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb"}}
	require.Len(t, pageContextInTargets(context.Background(), page, whole).FAQEntryIDs, 20)
	require.True(t, pageContextInTargets(context.Background(), page, whole).SelectionTruncated)
	require.Nil(t, pageContextInTargets(context.Background(), &types.PageContext{PageType: "admin_secret"}, nil))
	collection := pageContextInTargets(context.Background(), &types.PageContext{PageType: "models"}, nil)
	require.Equal(t, "models", collection.PageType)
}
