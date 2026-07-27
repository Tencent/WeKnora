package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/userinput"
	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
)

type fakeUserInputResolver struct {
	err       error
	calls     int
	tenantID  uint64
	userID    string
	pendingID string
	answer    userinput.Answer
	snapshot  *userinput.PendingSnapshot
}

func (f *fakeUserInputResolver) Resolve(tenantID uint64, userID, pendingID string, answer userinput.Answer) error {
	f.calls++
	f.tenantID = tenantID
	f.userID = userID
	f.pendingID = pendingID
	f.answer = answer
	return f.err
}

func (f *fakeUserInputResolver) GetPending(
	_ context.Context,
	_ uint64,
	_, _ string,
) (*userinput.PendingSnapshot, error) {
	if f.err != nil {
		return nil, f.err
	}
	if f.snapshot == nil {
		return nil, userinput.ErrPendingNotFound
	}
	return f.snapshot, nil
}

func userInputRouter(resolver *fakeUserInputResolver, tenantID uint64, userID string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	auth := func(c *gin.Context) {
		if tenantID != 0 {
			c.Set(types.TenantIDContextKey.String(), tenantID)
		}
		if userID != "" {
			ctx := types.WithPrincipal(c.Request.Context(), types.Principal{Type: types.PrincipalWebUser, ID: userID})
			c.Request = c.Request.WithContext(ctx)
		}
		c.Next()
	}
	router.POST("/agent/user-inputs/:pending_id", func(c *gin.Context) {
		auth(c)
	}, NewUserInputHandler(resolver).Resolve)
	router.GET("/agent/user-inputs/pending", auth, NewUserInputHandler(resolver).GetPending)
	return router
}

func TestUserInputHandlerGetsPendingQuestion(t *testing.T) {
	resolver := &fakeUserInputResolver{snapshot: &userinput.PendingSnapshot{
		PendingID: "pending-1", SessionID: "session-1",
		Question: userinput.Question{Text: "请补充日期", Mode: userinput.ModeDate},
	}}
	router := userInputRouter(resolver, 10000, "user-1")
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/agent/user-inputs/pending?session_id=session-1", nil)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "pending-1") {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func performUserInputRequest(router *gin.Engine, body string) *httptest.ResponseRecorder {
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/agent/user-inputs/pending-1", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	return response
}

func TestUserInputHandlerResolveSuccess(t *testing.T) {
	resolver := &fakeUserInputResolver{}
	response := performUserInputRequest(userInputRouter(resolver, 10000, "user-1"), `{
        "selected_option_ids":["written"],"other_text":"补充","skipped":false
    }`)
	if response.Code != http.StatusOK || resolver.calls != 1 {
		t.Fatalf("status = %d, body = %s, calls = %d", response.Code, response.Body.String(), resolver.calls)
	}
	if resolver.tenantID != 10000 || resolver.userID != "web_user:user-1" || resolver.pendingID != "pending-1" {
		t.Fatalf("resolve arguments = %+v", resolver)
	}
	if len(resolver.answer.SelectedOptionIDs) != 1 || resolver.answer.OtherText != "补充" {
		t.Fatalf("answer = %+v", resolver.answer)
	}
}

func TestUserInputHandlerRejectsBadRequestAndMissingPrincipal(t *testing.T) {
	tests := []struct {
		name     string
		tenantID uint64
		userID   string
		body     string
		want     int
	}{
		{name: "malformed json", tenantID: 10000, userID: "user-1", body: `{"skipped":`, want: http.StatusBadRequest},
		{name: "missing tenant", userID: "user-1", body: `{"skipped":true}`, want: http.StatusBadRequest},
		{name: "missing principal", tenantID: 10000, body: `{"skipped":true}`, want: http.StatusUnauthorized},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := &fakeUserInputResolver{}
			response := performUserInputRequest(userInputRouter(resolver, tt.tenantID, tt.userID), tt.body)
			if response.Code != tt.want || resolver.calls != 0 {
				t.Fatalf("status = %d, body = %s, calls = %d", response.Code, response.Body.String(), resolver.calls)
			}
		})
	}
}

func TestUserInputHandlerMapsResolveErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "invalid answer", err: userinput.ErrInvalidAnswer, want: http.StatusBadRequest},
		{name: "tenant mismatch", err: userinput.ErrTenantMismatch, want: http.StatusForbidden},
		{name: "user mismatch", err: userinput.ErrUserMismatch, want: http.StatusForbidden},
		{name: "missing pending", err: userinput.ErrPendingNotFound, want: http.StatusNotFound},
		{name: "already resolved", err: userinput.ErrAlreadyResolved, want: http.StatusConflict},
		{name: "owner unavailable", err: userinput.ErrOwnerUnavailable, want: http.StatusServiceUnavailable},
		{name: "unexpected", err: errors.New("boom"), want: http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolver := &fakeUserInputResolver{err: tt.err}
			response := performUserInputRequest(userInputRouter(resolver, 10000, "user-1"), `{"skipped":true}`)
			if response.Code != tt.want {
				t.Fatalf("status = %d, want %d, body = %s", response.Code, tt.want, response.Body.String())
			}
		})
	}
}
