package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProcessingArtifactTraceCacheStatus(t *testing.T) {
	assert.Equal(t, "bypass", processingArtifactTraceCacheStatus(false, false))
	assert.Equal(t, "miss", processingArtifactTraceCacheStatus(true, false))
	assert.Equal(t, "hit", processingArtifactTraceCacheStatus(true, true))
}
