package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type faqListKnowledgeServiceStub struct {
	interfaces.KnowledgeService
	callCount int
	isEnabled *bool
}

func (s *faqListKnowledgeServiceStub) ListFAQEntries(
	_ context.Context,
	_ string,
	page *types.Pagination,
	_ []string,
	_ int64,
	_ string,
	_ string,
	_ string,
	isEnabled *bool,
) (*types.PageResult, error) {
	s.callCount++
	s.isEnabled = isEnabled
	return types.NewPageResult(0, page, []*types.FAQEntry{}), nil
}

func TestParseOptionalFAQEnabled(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name      string
		query     string
		want      bool
		wantValue bool
		wantError bool
	}{
		{name: "omitted"},
		{name: "enabled", query: "?is_enabled=true", want: true, wantValue: true},
		{name: "disabled", query: "?is_enabled=false", want: true},
		{name: "case insensitive", query: "?is_enabled=TRUE", want: true, wantValue: true},
		{name: "invalid", query: "?is_enabled=invalid", wantError: true},
		{name: "numeric", query: "?is_enabled=1", wantError: true},
		{name: "empty", query: "?is_enabled=", wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Request = httptest.NewRequest("GET", "/faq/entries"+tt.query, nil)

			value, err := parseOptionalFAQEnabled(context)
			if (err != nil) != tt.wantError {
				t.Fatalf("error = %v, wantError %v", err, tt.wantError)
			}
			if tt.wantError {
				return
			}
			if (value != nil) != tt.want {
				t.Fatalf("value presence = %v, want %v", value != nil, tt.want)
			}
			if value != nil && *value != tt.wantValue {
				t.Fatalf("value = %v, want %v", *value, tt.wantValue)
			}
		})
	}
}

func TestFAQListEntriesPassesEnabledFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name      string
		query     string
		want      bool
		wantValue bool
	}{
		{name: "all entries"},
		{name: "enabled entries", query: "?is_enabled=true", want: true, wantValue: true},
		{name: "disabled entries", query: "?is_enabled=false", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &faqListKnowledgeServiceStub{}
			handler := &FAQHandler{knowledgeService: service}
			context, _ := gin.CreateTestContext(httptest.NewRecorder())
			context.Params = gin.Params{{Key: "id", Value: "kb-1"}}
			context.Request = httptest.NewRequest("GET", "/faq/entries"+tt.query, nil)

			handler.ListEntries(context)

			if len(context.Errors) != 0 {
				t.Fatalf("unexpected errors: %v", context.Errors)
			}
			if service.callCount != 1 {
				t.Fatalf("service call count = %d, want 1", service.callCount)
			}
			if (service.isEnabled != nil) != tt.want {
				t.Fatalf("filter presence = %v, want %v", service.isEnabled != nil, tt.want)
			}
			if service.isEnabled != nil && *service.isEnabled != tt.wantValue {
				t.Fatalf("filter = %v, want %v", *service.isEnabled, tt.wantValue)
			}
		})
	}
}

func TestFAQListEntriesRejectsInvalidEnabledFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)

	service := &faqListKnowledgeServiceStub{}
	handler := &FAQHandler{knowledgeService: service}
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	router.GET("/knowledge-bases/:id/faq/entries", handler.ListEntries)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest("GET", "/knowledge-bases/kb-1/faq/entries?is_enabled=invalid", nil)

	router.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}
	if service.callCount != 0 {
		t.Fatalf("service call count = %d, want 0", service.callCount)
	}
}
