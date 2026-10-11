package session

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProcessTextFileKeepsLinesLongerThanTheScannerDefault(t *testing.T) {
	// A minified JSON export is a single line; past bufio.MaxScanTokenSize the
	// default scanner stopped with "token too long" and the model got an error.
	longLine := `{"rows":[` + strings.Repeat(`{"id":1,"name":"item"},`, bufio.MaxScanTokenSize/20) + `{}]}`
	require.Greater(t, len(longLine), bufio.MaxScanTokenSize)
	data := []byte("header\n" + longLine + "\r\nfooter\n")

	attachment := &types.MessageAttachment{}
	err := (&AttachmentProcessor{}).processTextFile(context.Background(), data, attachment)

	require.NoError(t, err)
	assert.Equal(t, "header\n"+longLine+"\nfooter", attachment.Content)
	assert.Equal(t, 3, attachment.LineCount)
	assert.False(t, attachment.IsTruncated)
}

func TestProcessTextFileStillTruncatesByLineCount(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= maxTextFileLines+1; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}

	attachment := &types.MessageAttachment{}
	err := (&AttachmentProcessor{}).processTextFile(context.Background(), []byte(sb.String()), attachment)

	require.NoError(t, err)
	assert.Equal(t, maxTextFileLines+1, attachment.LineCount)
	assert.True(t, attachment.IsTruncated)
	assert.True(t, strings.HasSuffix(attachment.Content, fmt.Sprintf("line %d", maxTextFileLines)))
}
