package web_search

import (
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// emptyResultDiagnostics is implemented by providers that can explain an empty
// search result set (used by the settings "test connection" flow).
type emptyResultDiagnostics interface {
	EmptyResultDiagnostics() string
}

// EmptyTestResultsError builds a provider-specific message when a connectivity
// test search succeeds but returns no usable results.
func EmptyTestResultsError(providerType string, provider any) error {
	detail := ""
	if dr, ok := provider.(emptyResultDiagnostics); ok {
		detail = dr.EmptyResultDiagnostics()
	}

	switch types.WebSearchProviderType(providerType) {
	case types.WebSearchProviderTypeSearxng:
		if detail != "" {
			return fmt.Errorf("searxng returned 0 results: %s", detail)
		}
		return fmt.Errorf(
			"searxng returned 0 results; verify the instance URL, JSON format in settings.yml, and upstream search engine connectivity",
		)
	case types.WebSearchProviderTypeDuckDuckGo:
		return fmt.Errorf("duckduckgo returned 0 results; verify network connectivity and proxy settings")
	default:
		return fmt.Errorf("search returned 0 results, please verify your API key and configuration")
	}
}

func formatUnresponsiveEngines(engines [][]string) string {
	if len(engines) == 0 {
		return ""
	}
	parts := make([]string, 0, len(engines))
	for _, e := range engines {
		if len(e) == 0 {
			continue
		}
		if len(e) == 1 {
			parts = append(parts, e[0])
			continue
		}
		parts = append(parts, e[0]+" ("+e[1]+")")
	}
	if len(parts) == 0 {
		return ""
	}
	return "unresponsive engines: " + strings.Join(parts, ", ")
}
