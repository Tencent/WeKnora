package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type confluenceKnowledgeStub struct {
	interfaces.KnowledgeService
	knowledge *types.Knowledge
}

func (s confluenceKnowledgeStub) GetKnowledgeByIDOnly(context.Context, string) (*types.Knowledge, error) {
	return s.knowledge, nil
}

func (s confluenceKnowledgeStub) GetKnowledgeByID(context.Context, string) (*types.Knowledge, error) {
	return s.knowledge, nil
}

type confluenceDataSourceStub struct {
	interfaces.DataSourceRepository
	sources map[string]*types.DataSource
	lookups []string
}

func (s *confluenceDataSourceStub) FindByID(ctx context.Context, id string) (*types.DataSource, error) {
	s.lookups = append(s.lookups, id)
	source := s.sources[id]
	if source == nil || source.TenantID != types.MustTenantIDFromContext(ctx) {
		return nil, errors.New("data source unavailable")
	}
	return source, nil
}

func TestKnowledgeDetailConfluenceAttachmentBaseURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, channel, sourceID, url string
		wantLookup                   bool
		forbidden                    bool
	}{
		{
			name: "first site", channel: types.ChannelConfluence,
			sourceID: "first", url: "https://first.example/confluence", wantLookup: true,
		},
		{
			name: "shared document at second site", channel: types.ChannelConfluence,
			sourceID: "second", url: "https://team.atlassian.net/wiki", wantLookup: true,
		},
		{name: "uploaded file", channel: types.ChannelWeb, sourceID: "first"},
		{name: "missing provenance", channel: types.ChannelConfluence},
		{name: "lookup failed", channel: types.ChannelConfluence, sourceID: "missing", wantLookup: true},
		{
			name: "source in another knowledge base", channel: types.ChannelConfluence,
			sourceID: "unrelated", wantLookup: true,
		},
		{name: "URL includes credentials", channel: types.ChannelConfluence, sourceID: "userinfo", wantLookup: true},
		{name: "document access denied", channel: types.ChannelConfluence, sourceID: "second", forbidden: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			metadata, err := json.Marshal(map[string]string{"datasource_id": tc.sourceID})
			require.NoError(t, err)
			doc := &types.Knowledge{
				ID: "doc", TenantID: 8, KnowledgeBaseID: "kb", Channel: tc.channel, Metadata: types.JSON(metadata),
			}
			sources := &confluenceDataSourceStub{sources: map[string]*types.DataSource{
				"first": {
					TenantID: 8, KnowledgeBaseID: "kb", Type: types.ConnectorTypeConfluence,
					Config: types.JSON(`{"settings":{"base_url":"https://first.example/confluence/"}}`),
				},
				"second": {
					TenantID: 8, KnowledgeBaseID: "kb", Type: types.ConnectorTypeConfluence,
					Config: types.JSON(`{"credentials":{
						"edition":"cloud","base_url":"team.atlassian.net","api_token":"private-token"
					}}`),
				},
				"unrelated": {
					TenantID: 8, KnowledgeBaseID: "different-kb", Type: types.ConnectorTypeConfluence,
					Config: types.JSON(`{"settings":{"base_url":"https://unrelated.example"}}`),
				},
				"userinfo": {
					TenantID: 8, KnowledgeBaseID: "kb", Type: types.ConnectorTypeConfluence,
					Config: types.JSON(`{"settings":{"base_url":"https://user:password@wiki.example"}}`),
				},
			}}
			h := &KnowledgeHandler{kgService: confluenceKnowledgeStub{knowledge: doc}, dataSourceRepo: sources}
			if !tc.forbidden {
				h.kbShareService = &downloadShareStub{permission: types.OrgRoleViewer}
			}
			router := gin.New()
			router.Use(middleware.ErrorHandler(), func(c *gin.Context) {
				ctx := types.WithCaller(c.Request.Context(), types.Caller{
					TenantID: 7, UserID: "viewer", Role: types.TenantRoleViewer,
				})
				c.Request = c.Request.WithContext(types.WithExecutionTenant(ctx, 7))
				c.Next()
			})
			router.GET("/knowledge/:id", h.GetKnowledge)
			resp := httptest.NewRecorder()
			router.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/knowledge/doc", nil))

			if tc.forbidden {
				require.Equal(t, http.StatusForbidden, resp.Code, resp.Body.String())
			} else {
				require.Equal(t, http.StatusOK, resp.Code, resp.Body.String())
				require.Contains(t, resp.Body.String(), `"id":"doc"`)
				if tc.url == "" {
					require.NotContains(t, resp.Body.String(), "confluence_base_url")
				} else {
					require.Contains(t, resp.Body.String(), `"confluence_base_url":"`+tc.url+`"`)
				}
				require.NotContains(t, resp.Body.String(), "private-token")
			}
			if tc.wantLookup {
				require.Equal(t, []string{tc.sourceID}, sources.lookups)
			} else {
				require.Empty(t, sources.lookups)
			}
		})
	}
}
