package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveMarketplaceDownloadURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "github shorthand",
			input:    "owner/repo",
			expected: "https://codeload.github.com/owner/repo/zip/HEAD",
		},
		{
			name:     "github url standard",
			input:    "https://github.com/owner/repo",
			expected: "https://codeload.github.com/owner/repo/zip/HEAD",
		},
		{
			name:     "github url branch",
			input:    "https://github.com/owner/repo/tree/main",
			expected: "https://codeload.github.com/owner/repo/zip/main",
		},
		{
			name:     "direct archive url",
			input:    "https://example.com/plugins.tar.gz",
			expected: "https://example.com/plugins.tar.gz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveMarketplaceDownloadURL(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, got)
		})
	}
}
