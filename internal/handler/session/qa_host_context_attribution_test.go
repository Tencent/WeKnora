package session

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type hostContextSessionStub struct{ interfaces.SessionService }

func (*hostContextSessionStub) GetOwnedSession(context.Context, string) (*types.Session, error) {
	return &types.Session{ID: "session-1", TenantID: 1}, nil
}

type hostContextSuggestionStub struct {
	interfaces.MessageSuggestionService
	queries []string
}

func (s *hostContextSuggestionStub) ValidateAttribution(
	_ context.Context, _ string, query string, _ *types.SuggestionAttribution,
) error {
	s.queries = append(s.queries, query)
	if query != "What is the refund policy?" {
		return fmt.Errorf("question text does not match")
	}
	return nil
}

func TestQAHostContextPreservesSuggestionAttribution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, query := range []string{"What is the refund policy?", "A different question"} {
		t.Run(query, func(t *testing.T) {
			suggestions := &hostContextSuggestionStub{}
			h := &Handler{sessionService: &hostContextSessionStub{}, suggestionService: suggestions}
			body, err := json.Marshal(map[string]any{
				"query": query, "channel": "embed",
				"host_context":           map[string]any{"page": "/refunds", "userId": 123},
				"suggestion_attribution": map[string]string{"suggestion_set_id": "set-1", "question_id": "question-1"},
			})
			require.NoError(t, err)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Params = gin.Params{{Key: "session_id", Value: "session-1"}}
			c.Request = httptest.NewRequest(http.MethodPost, "/knowledge-chat/session-1", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")

			rc, request, err := h.parseQARequest(c, "test")
			require.Equal(t, []string{query}, suggestions.queries)
			if query == "A different question" {
				require.ErrorContains(t, err, "invalid suggestion attribution")
				return
			}
			require.NoError(t, err)
			expected := "[Host context]\npage: /refunds\nuserId: 123\n\n" + query
			require.Equal(t, expected, request.Query)
			require.Equal(t, expected, rc.buildQARequest().Query)
		})
	}
}
