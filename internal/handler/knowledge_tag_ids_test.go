package handler

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseCommaSeparatedTagIDs(t *testing.T) {
	assert.Nil(t, parseCommaSeparatedTagIDs(""))
	assert.Equal(t, []string{"a", "b"}, parseCommaSeparatedTagIDs("a,b"))
	assert.Equal(t, []string{"a", "b"}, parseCommaSeparatedTagIDs(" a , b "))
	assert.Equal(t, []string{"a"}, parseCommaSeparatedTagIDs("a,__untagged__,,"))
}
