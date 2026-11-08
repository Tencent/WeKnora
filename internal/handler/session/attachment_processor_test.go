package session

import (
	"bufio"
	"context"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
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

// recordingDocReader keeps the last read request so a test can see which
// engine the attachment path actually asked for.
type recordingDocReader struct {
	interfaces.DocumentReader
	last *types.ReadRequest
}

func (r *recordingDocReader) Read(_ context.Context, req *types.ReadRequest) (*types.ReadResult, error) {
	r.last = req
	return &types.ReadResult{MarkdownContent: "text"}, nil
}

// The engine is resolved per attachment file type: a message with a PDF and a
// spreadsheet must not run both through the PDF's engine.
func TestAttachmentParserEngineResolverResolvesPerFileType(t *testing.T) {
	types.SetDeploymentParserDefaults("mineru", []string{"pdf"}, nil)
	t.Cleanup(func() { types.SetDeploymentParserDefaults("", nil, nil) })

	tenantConfig := &types.ParserEngineConfig{
		ChatParserEngineRules: []types.ParserEngineRule{{FileTypes: []string{"docx"}, Engine: "paddleocr_vl"}},
	}
	resolve := attachmentParserEngineResolver(nil, tenantConfig)
	assert.Equal(t, "mineru", resolve("pdf"))
	assert.Equal(t, "paddleocr_vl", resolve("docx"))
	assert.Empty(t, resolve("xlsx"))

	// The same file type: a tenant rule beats the deployment default.
	pdfTenant := &types.ParserEngineConfig{
		ChatParserEngineRules: []types.ParserEngineRule{{FileTypes: []string{"pdf"}, Engine: "paddleocr_vl"}},
	}
	assert.Equal(t, "paddleocr_vl", attachmentParserEngineResolver(nil, pdfTenant)("pdf"))

	agent := &types.CustomAgent{Config: types.CustomAgentConfig{
		ChatParserEngineRules: []types.ParserEngineRule{{FileTypes: []string{"pdf"}, Engine: "builtin"}},
	}}
	resolve = attachmentParserEngineResolver(agent, tenantConfig)
	assert.Equal(t, "builtin", resolve("pdf"), "an explicit agent rule beats tenant rules and the deployment default")
	assert.Equal(t, "paddleocr_vl", resolve("docx"))
}

func TestProcessWithDocumentReaderUsesTheAttachmentsOwnEngine(t *testing.T) {
	// markitdown only exists in the docreader service, so NewReader routes it
	// to the recording reader and the test can see the request.
	types.SetDeploymentParserDefaults("markitdown", []string{"pdf"}, nil)
	t.Cleanup(func() { types.SetDeploymentParserDefaults("", nil, nil) })

	reader := &recordingDocReader{}
	processor := &AttachmentProcessor{documentReader: reader}
	resolve := attachmentParserEngineResolver(nil, nil)

	require.NoError(t, processor.processWithDocumentReader(
		context.Background(), []byte("data"), "report.pdf", ".pdf", &types.MessageAttachment{}, 1, resolve))
	assert.Equal(t, "markitdown", reader.last.ParserEngine)

	require.NoError(t, processor.processWithDocumentReader(
		context.Background(), []byte("data"), "sheet.xlsx", ".xlsx", &types.MessageAttachment{}, 1, resolve))
	assert.Empty(t, reader.last.ParserEngine, "a spreadsheet must not inherit the PDF engine")
}
