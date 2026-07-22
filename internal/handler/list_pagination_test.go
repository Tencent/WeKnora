package handler

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func runListPaginationRequest(rawQuery string) (int, int, bool, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	var page int
	var pageSize int
	var ok bool
	router := gin.New()
	router.Use(middleware.ErrorHandler())
	router.GET("/list", func(c *gin.Context) {
		page, pageSize, ok = parseListPagination(c)
		if !ok {
			return
		}
		c.Status(http.StatusNoContent)
	})

	target := "/list"
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	return page, pageSize, ok, recorder
}

func TestParseListPagination(t *testing.T) {
	tests := []struct {
		name         string
		query        string
		wantPage     int
		wantPageSize int
		wantOK       bool
		wantStatus   int
	}{
		{name: "defaults", wantPage: 1, wantPageSize: defaultListPageSize, wantOK: true, wantStatus: http.StatusNoContent},
		{name: "first page", query: "page=1", wantPage: 1, wantPageSize: defaultListPageSize, wantOK: true, wantStatus: http.StatusNoContent},
		{name: "maximum page size", query: "page_size=100", wantPage: 1, wantPageSize: maxListPageSize, wantOK: true, wantStatus: http.StatusNoContent},
		{name: "zero page", query: "page=0", wantStatus: http.StatusBadRequest},
		{name: "negative page", query: "page=-1", wantStatus: http.StatusBadRequest},
		{name: "non integer page", query: "page=invalid", wantStatus: http.StatusBadRequest},
		{name: "zero page size", query: "page_size=0", wantStatus: http.StatusBadRequest},
		{name: "page size over maximum", query: "page_size=101", wantStatus: http.StatusBadRequest},
		{name: "non integer page size", query: "page_size=invalid", wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, pageSize, ok, recorder := runListPaginationRequest(tt.query)
			assert.Equal(t, tt.wantOK, ok)
			assert.Equal(t, tt.wantStatus, recorder.Code, recorder.Body.String())
			if tt.wantOK {
				assert.Equal(t, tt.wantPage, page)
				assert.Equal(t, tt.wantPageSize, pageSize)
				return
			}
			assert.Zero(t, page)
			assert.Zero(t, pageSize)
		})
	}
}

func TestParseListPaginationRejectsOffsetOverflow(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	pageSize := maxListPageSize
	maxSafePage := maxInt/pageSize + 1
	firstOverflowPage := maxSafePage + 1

	page, gotPageSize, ok, recorder := runListPaginationRequest(
		"page=" + strconv.Itoa(maxSafePage) + "&page_size=" + strconv.Itoa(pageSize),
	)
	assert.True(t, ok)
	assert.Equal(t, http.StatusNoContent, recorder.Code, recorder.Body.String())
	assert.Equal(t, maxSafePage, page)
	assert.Equal(t, pageSize, gotPageSize)
	assert.LessOrEqual(t, (maxSafePage-1)*pageSize, maxInt)

	page, gotPageSize, ok, recorder = runListPaginationRequest(
		"page=" + strconv.Itoa(firstOverflowPage) + "&page_size=" + strconv.Itoa(pageSize),
	)
	assert.False(t, ok)
	assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	assert.Zero(t, page)
	assert.Zero(t, gotPageSize)
	assert.Greater(t, firstOverflowPage-1, maxInt/pageSize)

	beyondInt := strconv.FormatUint(uint64(maxInt)+1, 10)
	page, gotPageSize, ok, recorder = runListPaginationRequest("page=" + beyondInt)
	assert.False(t, ok)
	assert.Equal(t, http.StatusBadRequest, recorder.Code, recorder.Body.String())
	assert.Zero(t, page)
	assert.Zero(t, gotPageSize)
}
