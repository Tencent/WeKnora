package types

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestKnowledgeFolderBeforeCreate(t *testing.T) {
	folder := &KnowledgeFolder{}
	err := folder.BeforeCreate(nil)
	require.NoError(t, err)
	require.NotEmpty(t, folder.ID)
}

func TestFolderConstants(t *testing.T) {
	require.Equal(t, "", FolderRootID)
	require.Equal(t, "__root__", FolderRootFilter)
}
