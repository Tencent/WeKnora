package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWebSearchFilterValidation(t *testing.T) {
	for _, freshness := range []string{"", "pd", "pw", "pm", "py", "2026-01-01to2026-09-07"} {
		require.NoError(t, (WebSearchFilters{Country: "DE", Freshness: freshness}).Validate())
	}
	for _, f := range []WebSearchFilters{
		{Country: "Germany"},
		{Country: "D1"},
		{Freshness: "week"},
		{Freshness: "2026-09-07to2026-01-01"},
		{Freshness: "2026-02-30to2026-03-01"},
	} {
		require.Error(t, f.Validate())
	}
	for _, country := range []string{"", "us", "ALL"} {
		require.NoError(t, (WebSearchFilters{Country: country}).Validate())
	}
}
