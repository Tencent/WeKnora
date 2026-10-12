package session

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessTextFileKeepsSmallFilesWhole(t *testing.T) {
	attachment := &types.MessageAttachment{}
	(&AttachmentProcessor{}).processTextFile(context.Background(), []byte("header\r\nbody\nfooter\n"), attachment)

	assert.Equal(t, "header\nbody\nfooter", attachment.Content)
	assert.Equal(t, 3, attachment.LineCount)
	assert.False(t, attachment.IsTruncated)
}

func TestProcessTextFileCapsASingleLineOverTheByteLimit(t *testing.T) {
	// A minified JSON export is one line, here longer than bufio.MaxScanTokenSize:
	// it keeps a prefix of maxTextFileContentBytes and is marked truncated.
	longLine := `{"rows":[` + strings.Repeat(`{"id":1,"name":"item"},`, bufio.MaxScanTokenSize/20) + `{}]}`
	require.Greater(t, len(longLine), bufio.MaxScanTokenSize)

	attachment := &types.MessageAttachment{}
	(&AttachmentProcessor{}).processTextFile(context.Background(), []byte(longLine), attachment)

	assert.Equal(t, longLine[:maxTextFileContentBytes], attachment.Content)
	assert.Equal(t, 1, attachment.LineCount)
	assert.True(t, attachment.IsTruncated)
}

func TestProcessTextFileByteCapKeepsValidUTF8(t *testing.T) {
	data := []byte(strings.Repeat("中", maxTextFileContentBytes))

	attachment := &types.MessageAttachment{}
	(&AttachmentProcessor{}).processTextFile(context.Background(), data, attachment)

	assert.True(t, attachment.IsTruncated)
	assert.True(t, utf8.ValidString(attachment.Content))
	assert.LessOrEqual(t, len(attachment.Content), maxTextFileContentBytes)
	assert.Greater(t, len(attachment.Content), maxTextFileContentBytes-utf8.UTFMax)
}

func TestProcessTextFileStillTruncatesByLineCount(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= maxTextFileLines+1; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}

	attachment := &types.MessageAttachment{}
	(&AttachmentProcessor{}).processTextFile(context.Background(), []byte(sb.String()), attachment)

	assert.Equal(t, maxTextFileLines+1, attachment.LineCount)
	assert.True(t, attachment.IsTruncated)
	assert.True(t, strings.HasSuffix(attachment.Content, fmt.Sprintf("line %d", maxTextFileLines)))
}
