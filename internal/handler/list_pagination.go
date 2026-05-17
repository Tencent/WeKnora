package handler

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
)

const (
	defaultListPageSize = 20
	maxListPageSize     = 100
)

// parseListPagination parses ?page and ?page_size for tenant-scoped list
// endpoints. Omitted keys default to page=1 and page_size=20. A present but
// malformed or out-of-range value yields a validation error on the context.
func parseListPagination(c *gin.Context) (page, pageSize int, ok bool) {
	page = 1
	pageSize = defaultListPageSize

	if s := strings.TrimSpace(c.Query("page")); s != "" {
		p, err := strconv.Atoi(s)
		if err != nil || p < 1 {
			c.Error(apperrors.NewValidationError("page must be a positive integer"))
			return 0, 0, false
		}
		page = p
	}
	if s := strings.TrimSpace(c.Query("page_size")); s != "" {
		ps, err := strconv.Atoi(s)
		if err != nil || ps < 1 || ps > maxListPageSize {
			c.Error(apperrors.NewValidationError(fmt.Sprintf("page_size must be between 1 and %d", maxListPageSize)))
			return 0, 0, false
		}
		pageSize = ps
	}
	return page, pageSize, true
}
