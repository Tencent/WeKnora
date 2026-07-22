package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractDocumentHashID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		filename string
		content  string
		want     string
	}{
		{
			name:     "plain assignment",
			filename: "告警策略.md",
			content:  "hash_id = alarm-policy\n\n# 告警策略\n",
			want:     "alarm-policy",
		},
		{
			name:     "no spaces around equals",
			filename: "doc.md",
			content:  "hash_id=docs/alert.md\nbody",
			want:     "docs/alert.md",
		},
		{
			name:     "markdown comment style",
			filename: "doc.md",
			content:  "# hash_id = alarm-policy\n# Title\n",
			want:     "alarm-policy",
		},
		{
			name:     "utf8 bom prefix",
			filename: "doc.txt",
			content:  "\ufeffhash_id = bom-id\nhello",
			want:     "bom-id",
		},
		{
			name:     "crlf line ending",
			filename: "doc.md",
			content:  "hash_id = crlf-id\r\nrest",
			want:     "crlf-id",
		},
		{
			name:     "missing declaration",
			filename: "doc.md",
			content:  "# Title\nhash_id = later\n",
			want:     "",
		},
		{
			name:     "binary extension skipped",
			filename: "doc.pdf",
			content:  "hash_id = should-ignore\n",
			want:     "",
		},
		{
			name:     "empty file",
			filename: "doc.md",
			content:  "",
			want:     "",
		},
		{
			name:     "only hash_id line",
			filename: "doc.md",
			content:  "hash_id = solo",
			want:     "solo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fh := newMultipartFileHeader(t, tt.filename, tt.content)
			got, err := extractDocumentHashID(fh, tt.filename)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSupportsDocumentHashID(t *testing.T) {
	t.Parallel()
	assert.True(t, supportsDocumentHashID("a.md"))
	assert.True(t, supportsDocumentHashID("a.markdown"))
	assert.True(t, supportsDocumentHashID("a.TXT"))
	assert.False(t, supportsDocumentHashID("a.pdf"))
	assert.False(t, supportsDocumentHashID("a.docx"))
}
