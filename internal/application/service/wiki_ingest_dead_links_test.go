package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Code examples in a generated summary are literal text, not wiki links.
// The ingest sanitizer must leave them alone while still cleaning prose.
func TestStripDeadWikiLinksLeavesCodeExamplesAlone(t *testing.T) {
	dead := map[string]struct{}{"concept/gone": {}}
	content := strings.Join([]string{
		"Prose [[concept/gone]] and [[concept/gone|Keep label]].",
		"",
		"```md",
		"[[concept/gone]]",
		"```",
		"",
		"Inline `[[concept/gone]]` example.",
		"",
		"    [[concept/gone]]",
		"",
	}, "\n")

	got, changed := stripDeadWikiLinks(content, dead, nil, nil)

	require.True(t, changed)
	require.Equal(t, strings.Join([]string{
		"Prose gone and Keep label.",
		"",
		"```md",
		"[[concept/gone]]",
		"```",
		"",
		"Inline `[[concept/gone]]` example.",
		"",
		"    [[concept/gone]]",
		"",
	}, "\n"), got)
}

// The resolver path and the strip path keep working outside code; a resolved
// link keeps its display text.
func TestStripDeadWikiLinksRepairsAndStripsOutsideCode(t *testing.T) {
	dead := map[string]struct{}{"concept/gone": {}, "concept/stale": {}}
	live := map[string]struct{}{"concept/live": {}}
	titles := map[string]string{"Live Page": "concept/live"}
	content := "Resolved [[concept/gone|Live Page]], stripped [[concept/stale]], " +
		"literal `[[concept/gone|Live Page]]`."

	got, changed := stripDeadWikiLinks(content, dead, live, titles)

	require.True(t, changed)
	require.Equal(t,
		"Resolved [[concept/live|Live Page]], stripped stale, "+
			"literal `[[concept/gone|Live Page]]`.", got)
}

func TestStripDeadWikiLinksNoWorkIsUnchanged(t *testing.T) {
	content := "No links here.\n\n```\n[[concept/gone]]\n```\n"

	got, changed := stripDeadWikiLinks(content, nil, nil, nil)
	require.False(t, changed)
	require.Equal(t, content, got)

	got, changed = stripDeadWikiLinks(content, map[string]struct{}{"concept/other": {}}, nil, nil)
	require.False(t, changed)
	require.Equal(t, content, got)
}
