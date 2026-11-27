package types

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCreationFolderValidation(t *testing.T) {
	folder, err := CreationFolder()
	require.NoError(t, err)
	require.Empty(t, folder)
	folder, err = CreationFolder(KnowledgeCreationOptions{FolderPath: "ERP/库存"})
	require.NoError(t, err)
	require.Equal(t, "ERP/库存", folder)
	for _, raw := range []string{
		"../private", "/private", " /private", "C:\\private", "a\x00b",
		strings.Repeat("a", MaxKnowledgeFolderSegmentLength+1),
		strings.Repeat("a/", MaxKnowledgeFolderDepth) + "a",
	} {
		_, err := CreationFolder(KnowledgeCreationOptions{FolderPath: raw})
		require.Error(t, err, raw)
	}
}
