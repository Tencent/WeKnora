package drive

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource/connector/feishu/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsSystemJunkFileName(t *testing.T) {
	for _, name := range []string{
		".DS_Store", " .DS_Store ", "Thumbs.db", "desktop.ini",
		"._resource-fork", "~$budget.xlsx",
	} {
		assert.True(t, isSystemJunkFileName(name), "want junk: %q", name)
	}
	for _, name := range []string{
		"notes.md", "DS_Store", "Thumbs.db.bak", "report.pdf", "",
	} {
		assert.False(t, isSystemJunkFileName(name), "want document: %q", name)
	}
}

// Fetch must short-circuit before touching the client: passing nil proves no
// export/download request is made for a junk file.
func TestDriveFetchSkipsSystemJunkWithoutFetching(t *testing.T) {
	o := &driveOps{}
	items, err := o.Fetch(context.Background(), nil, core.DriveFile{Name: ".DS_Store"}, "resource")
	require.NoError(t, err)
	assert.Empty(t, items)
}
