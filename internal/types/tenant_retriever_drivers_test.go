package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseRetrieverDriversNormalizesAndDeduplicates(t *testing.T) {
	assert.Equal(t,
		[]string{"postgres", "opensearch"},
		ParseRetrieverDrivers(" POSTGRES, opensearch, postgres,  "),
	)
}
