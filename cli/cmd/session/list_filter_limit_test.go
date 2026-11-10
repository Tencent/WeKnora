package sessioncmd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestListAllPagesLimitAppliesAfterSince(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	now := time.Now()
	sessions := []sdk.Session{
		{ID: "old", UpdatedAt: now.Add(-30 * 24 * time.Hour).Format(time.RFC3339)},
		{ID: "recent-one", UpdatedAt: now.Add(-time.Hour).Format(time.RFC3339)},
		{ID: "recent-two", UpdatedAt: now.Add(-2 * time.Hour).Format(time.RFC3339)},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, err := strconv.Atoi(r.URL.Query().Get("page"))
		assert.NoError(t, err)
		assert.Equal(t, "/api/v1/sessions", r.URL.Path)
		assert.Equal(t, "1", r.URL.Query().Get("page_size"))
		data := []sdk.Session{}
		if page >= 1 && page <= len(sessions) {
			data = append(data, sessions[page-1])
		}
		_ = json.NewEncoder(w).Encode(sdk.SessionListResponse{Success: true, Data: data, Total: len(sessions)})
	}))
	defer server.Close()
	require.NoError(t, runList(context.Background(), &ListOptions{PageSize: 1, Limit: 1, AllPages: true, Since: "7d"},
		&cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}, sdk.NewClient(server.URL)))
	var envelope struct {
		Data []sdk.Session `json:"data"`
		Meta struct {
			HasMore    bool `json:"has_more"`
			TotalCount int  `json:"total_count"`
		} `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &envelope))
	require.Len(t, envelope.Data, 1)
	assert.Equal(t, "recent-one", envelope.Data[0].ID)
	assert.True(t, envelope.Meta.HasMore)
	assert.Equal(t, 3, envelope.Meta.TotalCount)
}
