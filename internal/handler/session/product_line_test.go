package session

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// CreateSession 的 product_line 透传（customer-model-lines AC-3）：
// 带值落库、缺省空串、超长（>64）拒绝。
func TestCreateSessionProductLine(t *testing.T) {
	t.Run("carries product_line", func(t *testing.T) {
		h := handlerWithApprovedDirs()
		w := postSession(t, h, `{"title":"pl","product_line":"kingdee-ai-suite"}`)
		require.Equal(t, http.StatusCreated, w.Code)
		require.NotNil(t, h.created)
		require.Equal(t, "kingdee-ai-suite", h.created.ProductLine)
	})

	t.Run("defaults to empty when absent", func(t *testing.T) {
		h := handlerWithApprovedDirs()
		w := postSession(t, h, `{"title":"no-pl"}`)
		require.Equal(t, http.StatusCreated, w.Code)
		require.NotNil(t, h.created)
		require.Empty(t, h.created.ProductLine)
	})

	t.Run("empty string stays empty", func(t *testing.T) {
		h := handlerWithApprovedDirs()
		w := postSession(t, h, `{"title":"empty-pl","product_line":""}`)
		require.Equal(t, http.StatusCreated, w.Code)
		require.Empty(t, h.created.ProductLine)
	})

	t.Run("rejects values longer than 64 chars", func(t *testing.T) {
		h := handlerWithApprovedDirs()
		w := postSession(t, h, `{"product_line":"`+strings.Repeat("x", 65)+`"}`)
		require.Equal(t, http.StatusBadRequest, w.Code)
		require.Nil(t, h.created)
	})
}
