package handler

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// messageLimitCap is the upper bound both message endpoints must enforce.
// Kept as a test-local literal (not a reference to the production constant)
// so the red-before/green-after pair works without the fix present.
const messageLimitCap = 200

// messageLimitDefault is the fallback applied when the requested limit is
// absent or non-positive.
const messageLimitDefault = 20

// searchParamsStub captures the params the search handler forwards downstream.
type searchParamsStub struct {
	interfaces.MessageService
	got chan *types.MessageSearchParams
}

func (s *searchParamsStub) SearchMessages(
	_ context.Context, params *types.MessageSearchParams,
) (*types.MessageSearchResult, error) {
	s.got <- params
	return &types.MessageSearchResult{}, nil
}

// TestLoadMessagesClampsLimit covers GET /messages/:session_id/load on both
// branches (recent and before_time). A request without bounds used to reach
// the repository as-is; a negative limit cancels GORM's LIMIT clause
// (`limit >= 0` guard), returning the whole session history in one request.
func TestLoadMessagesClampsLimit(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want int
	}{
		{"non_numeric_falls_back_to_default", "/messages/s1/load?limit=abc", messageLimitDefault},
		{"out_of_range_falls_back_to_default", "/messages/s1/load?limit=99999999999999999999", messageLimitDefault},
		{"empty_falls_back_to_default", "/messages/s1/load?limit=", messageLimitDefault},
		{"missing_falls_back_to_default", "/messages/s1/load", messageLimitDefault},
		{"zero_falls_back_to_default", "/messages/s1/load?limit=0", messageLimitDefault},
		{"negative_cannot_cancel_limit", "/messages/s1/load?limit=-1", messageLimitDefault},
		{"large_negative_cannot_cancel_limit", "/messages/s1/load?limit=-1000000", messageLimitDefault},
		{"huge_is_capped", "/messages/s1/load?limit=100000000", messageLimitCap},
		{"one_above_cap_is_capped", "/messages/s1/load?limit=201", messageLimitCap},
		{"at_cap_is_kept", "/messages/s1/load?limit=200", messageLimitCap},
		{"in_range_is_kept", "/messages/s1/load?limit=7", 7},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make(chan int, 1)
			svc := &stubMessageService{
				getRecent: func(_ context.Context, _ string, limit int) ([]*types.Message, error) {
					got <- limit
					return []*types.Message{}, nil
				},
			}
			newMessageTestRouter(svc).ServeHTTP(httptest.NewRecorder(),
				httptest.NewRequest(http.MethodGet, tc.url, nil))

			if limit := <-got; limit != tc.want {
				t.Fatalf("load forwarded limit=%d; want %d", limit, tc.want)
			}
		})
	}
}

// TestLoadMessagesClampsLimitBeforeTime pins the same clamp on the
// before_time branch, which forwards to a different service method.
func TestLoadMessagesClampsLimitBeforeTime(t *testing.T) {
	got := make(chan int, 1)
	svc := &stubMessageService{
		getBeforeTime: func(_ context.Context, _ string, _ time.Time, limit int) ([]*types.Message, error) {
			got <- limit
			return []*types.Message{}, nil
		},
	}
	newMessageTestRouter(svc).ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet,
			"/messages/s1/load?limit=-1&before_time=2026-05-20T09:43:37Z", nil))

	if limit := <-got; limit != messageLimitDefault {
		t.Fatalf("load(before_time) forwarded limit=%d; want %d", limit, messageLimitDefault)
	}
}

// TestSearchMessagesClampsLimitBeforeForwarding covers POST /messages/search.
// The service multiplies the forwarded limit (bounded over-fetch), so an
// unbounded request used to inflate the SQL LIMIT by the same factor.
func TestSearchMessagesClampsLimitBeforeForwarding(t *testing.T) {
	cases := []struct {
		name string
		body string
		want int
	}{
		{"missing_falls_back_to_default", `{"query":"x"}`, messageLimitDefault},
		{"zero_falls_back_to_default", `{"query":"x","limit":0}`, messageLimitDefault},
		{"negative_falls_back_to_default", `{"query":"x","limit":-5}`, messageLimitDefault},
		{"huge_is_capped", `{"query":"x","limit":100000000}`, messageLimitCap},
		{"one_above_cap_is_capped", `{"query":"x","limit":201}`, messageLimitCap},
		{"at_cap_is_kept", `{"query":"x","limit":200}`, messageLimitCap},
		{"in_range_is_kept", `{"query":"x","limit":5}`, 5},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := make(chan *types.MessageSearchParams, 1)
			svc := &searchParamsStub{got: got}

			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.Use(middleware.ErrorHandler())
			h := &MessageHandler{MessageService: svc}
			r.POST("/messages/search", h.SearchMessages)

			r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(
				http.MethodPost, "/messages/search", bytes.NewBufferString(tc.body)))

			if params := <-got; params.Limit != tc.want {
				t.Fatalf("search forwarded limit=%d; want %d", params.Limit, tc.want)
			}
		})
	}
}
