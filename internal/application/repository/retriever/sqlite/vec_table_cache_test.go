package sqlite

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateCachedVecTableAllowsRetryAfterRolledBackDDL(t *testing.T) {
	const dimension = 1536
	cache := map[int]bool{dimension: true}

	ready, err := validateCachedVecTable(cache, dimension, func() (bool, error) {
		return false, nil
	})
	require.NoError(t, err)
	assert.False(t, ready)
	assert.False(t, cache[dimension], "rolled-back table must be evicted from the cache")

	ready, err = validateCachedVecTable(cache, dimension, func() (bool, error) {
		t.Fatal("uncached retry must proceed directly to table creation")
		return false, nil
	})
	require.NoError(t, err)
	assert.False(t, ready)

	cache[dimension] = true
	ready, err = validateCachedVecTable(cache, dimension, func() (bool, error) {
		return true, nil
	})
	require.NoError(t, err)
	assert.True(t, ready)
	assert.True(t, cache[dimension])
}
