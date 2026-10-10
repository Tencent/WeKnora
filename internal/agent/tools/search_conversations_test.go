package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type conversationSearchService struct {
	interfaces.MessageService
	items  []*types.MessageSearchGroupItem
	params *types.MessageSearchParams
}

func (s *conversationSearchService) SearchMessages(
	_ context.Context, p *types.MessageSearchParams,
) (*types.MessageSearchResult, error) {
	s.params = p
	var items []*types.MessageSearchGroupItem
	for _, item := range s.items {
		if p.ExcludeSessionID != "" && item.SessionID == p.ExcludeSessionID {
			continue
		}
		items = append(items, item)
		if len(items) == p.Limit {
			break
		}
	}
	return &types.MessageSearchResult{Items: items, Total: len(items)}, nil
}

func TestSearchConversationsExcludesCurrentSessionBeforeLimit(t *testing.T) {
	for _, tc := range []struct {
		name        string
		limit, want int
	}{{"default", 0, 5}, {"negative", -1, 5}, {"one", 1, 1}, {"maximum", 8, 8}, {"capped", 100, 8}} {
		t.Run(tc.name, func(t *testing.T) {
			s := &conversationSearchService{}
			for i := 0; i < 70; i++ {
				s.items = append(s.items, &types.MessageSearchGroupItem{
					SessionID: "current", QueryContent: "current conversation",
				})
			}
			for i := 0; i < 10; i++ {
				s.items = append(s.items, &types.MessageSearchGroupItem{
					SessionID: "older", QueryContent: "past conversation",
				})
			}
			args := fmt.Sprintf(`{"query":" config ","limit":%d,`+
				`"exclude_session_id":"older","owner_id":"someone-else"}`, tc.limit)
			result, err := NewSearchConversationsTool(s, "owner", "current").Execute(t.Context(), json.RawMessage(args))
			require.NoError(t, err)
			require.Equal(t, tc.want, result.Data["matches"])
			require.Equal(t, tc.want, s.params.Limit)
			require.Equal(t, "current", s.params.ExcludeSessionID)
			require.Equal(t, "owner", s.params.OwnerID)
			require.Equal(t, "config", s.params.Query)
			require.NotContains(t, result.Output, "current conversation")
		})
	}
}

func TestSearchConversationsCurrentOnlyAndNoCurrent(t *testing.T) {
	for _, tc := range []struct {
		name, current string
		want          int
	}{{"only current", "current", 0}, {"no current", "", 1}} {
		t.Run(tc.name, func(t *testing.T) {
			s := &conversationSearchService{items: []*types.MessageSearchGroupItem{
				{SessionID: "current", QueryContent: "config"},
			}}
			result, err := NewSearchConversationsTool(s, "owner", tc.current).Execute(
				context.Background(), json.RawMessage(`{"query":"config"}`),
			)
			require.NoError(t, err)
			require.Equal(t, tc.want, result.Data["matches"])
		})
	}
}
