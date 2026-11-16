package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// An unset allowed_tools selects the default tools and an empty one means no
// built-in tools, so the difference must survive a round trip through the
// database column.
func TestCustomAgentConfigKeepsUnsetAndEmptyToolListsApart(t *testing.T) {
	for _, allowed := range [][]string{nil, {}, {"thinking"}} {
		value, err := CustomAgentConfig{AllowedTools: allowed}.Value()
		require.NoError(t, err)
		var loaded CustomAgentConfig
		require.NoError(t, loaded.Scan(value))
		require.Equal(t, allowed == nil, loaded.AllowedTools == nil, "%v", allowed)
		require.Equal(t, len(allowed), len(loaded.AllowedTools), "%v", allowed)
	}
}
