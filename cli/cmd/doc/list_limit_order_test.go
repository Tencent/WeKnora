package doc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListAllPagesSortsBeforeLimit(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	now := time.Now()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/knowledge-bases/kb-test/knowledge", r.URL.Path)
		_ = json.NewEncoder(w).Encode(sdk.KnowledgeListResponse{
			Success: true, Total: 2,
			Data: []sdk.Knowledge{
				{ID: "older", UpdatedAt: now.Add(-time.Hour)},
				{ID: "newer", UpdatedAt: now},
			},
		})
	}))
	defer server.Close()
	require.NoError(t, runList(context.Background(), &ListOptions{PageSize: 2, Limit: 1, AllPages: true},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, sdk.NewClient(server.URL), "kb-test"))
	var envelope struct {
		Data []sdk.Knowledge `json:"data"`
		Meta struct {
			HasMore bool `json:"has_more"`
		} `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &envelope))
	require.Len(t, envelope.Data, 1)
	assert.Equal(t, "newer", envelope.Data[0].ID)
	assert.True(t, envelope.Meta.HasMore)
}
