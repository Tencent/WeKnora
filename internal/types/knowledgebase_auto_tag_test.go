package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAutoTagConfigNormalize(t *testing.T) {
	config := &AutoTagConfig{Enabled: true}
	config.Normalize()
	assert.Equal(t, 3, config.MaxTags)

	config.MaxTags = 99
	config.Normalize()
	assert.Equal(t, 10, config.MaxTags)
}
