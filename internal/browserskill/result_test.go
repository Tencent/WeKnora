package browserskill

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNavigationIncompleteOnlyMatchesNavigationTimeouts(t *testing.T) {
	for _, method := range []string{"navigate", "navigate_back", "navigate_forward", "reload", "wait_for_navigation"} {
		require.True(t, NavigationIncomplete(method, json.RawMessage(`{"reached":"timeout"}`)), method)
		require.False(t, NavigationIncomplete(method, json.RawMessage(`{"reached":"domcontentloaded"}`)), method)
	}
	require.False(t, NavigationIncomplete("evaluate", json.RawMessage(`{"reached":"timeout"}`)))
	require.False(t, NavigationIncomplete("observe", json.RawMessage(`{"text":"timeout"}`)))
}

func TestStatusPageURL(t *testing.T) {
	require.Equal(t, "https://example.com/path",
		statusPageURL("https://user:pass@example.com/path?token=secret#private"))
	for _, value := range []string{"javascript:alert(1)", "file:///tmp/test", "invalid"} {
		require.Empty(t, statusPageURL(value))
	}
}
