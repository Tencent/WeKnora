package types

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSubKnowledgeValidationAndFingerprint(t *testing.T) {
	valid := SubKnowledgeInput{
		SourceNamespace: "docs",
		RootID:          "collection-1",
		NodeType:        "root",
		SourceID:        "document-1",
		ReferenceID:     "external-origin",
	}
	require.NoError(t, valid.Validate())
	for _, edit := range []func(*SubKnowledgeInput){
		func(f *SubKnowledgeInput) { f.SourceNamespace = "" },
		func(f *SubKnowledgeInput) { f.RootID = "" },
		func(f *SubKnowledgeInput) { f.ParentID = "other" },
		func(f *SubKnowledgeInput) { f.ReferenceID = f.SourceID },
		func(f *SubKnowledgeInput) { f.NodeType = "unknown" },
		func(f *SubKnowledgeInput) { f.SortOrder = -1 },
		func(f *SubKnowledgeInput) { f.SourceID = strings.Repeat("x", 129) },
	} {
		bad := valid
		edit(&bad)
		require.Error(t, bad.Validate())
	}
	require.NotEqual(
		t,
		valid.ContentHash("标题", "![图](https://a.invalid/1)", "publish"),
		valid.ContentHash("标题", "![图](https://a.invalid/2)", "publish"),
		"image changes must change the source fingerprint",
	)
}
