package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNativeKnowledgeChildrenOptions(t *testing.T) {
	for _, depth := range []string{"", "0", "1", "2", "all", "-1", "32"} {
		_, err := ParseKnowledgeChildrenOptions(depth, "")
		require.NoError(t, err)
	}
	for _, depth := range []string{"-2", "33", "bad"} {
		_, err := ParseKnowledgeChildrenOptions(depth, "")
		require.Error(t, err)
	}
	for _, limit := range []string{"0", "1001", "bad"} {
		_, err := ParseKnowledgeChildrenOptions("1", limit)
		require.Error(t, err)
	}
}
