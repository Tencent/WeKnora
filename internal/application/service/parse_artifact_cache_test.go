package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseArtifactCacheKeyIncludesTenantFilenameAndTitle(t *testing.T) {
	content := []byte("same bytes")
	cfg := parseArtifactConfig{
		ParserEngine: "docreader",
		FileType:     "pdf",
		TenantID:     1,
		FileName:     "a.pdf",
		Title:        "Document A",
	}
	base := parseArtifactCacheKey(content, cfg)
	require.NotEmpty(t, base)

	tenantChanged := cfg
	tenantChanged.TenantID = 2
	require.NotEqual(t, base, parseArtifactCacheKey(content, tenantChanged))

	fileChanged := cfg
	fileChanged.FileName = "b.pdf"
	require.NotEqual(t, base, parseArtifactCacheKey(content, fileChanged))

	titleChanged := cfg
	titleChanged.Title = "Document B"
	require.NotEqual(t, base, parseArtifactCacheKey(content, titleChanged))
}
