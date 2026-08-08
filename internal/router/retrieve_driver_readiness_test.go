package router

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCompareRetrieveDriversReportsSanitizedDifference(t *testing.T) {
	missing, unexpected := compareRetrieveDrivers(
		[]string{"opensearch", "postgres"},
		[]string{"postgres", "qdrant"},
	)
	assert.Equal(t, []string{"opensearch"}, missing)
	assert.Equal(t, []string{"qdrant"}, unexpected)
}
