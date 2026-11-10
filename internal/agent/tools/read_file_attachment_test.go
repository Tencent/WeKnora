package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/application/attachment"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeAttachmentStore struct {
	mu          sync.Mutex
	attachments types.MessageAttachments
	documents   map[string]*types.TemporaryDocument
	// readyAfter flips a pending document to ready after this many loads.
	readyAfter map[string]int
	loads      map[string]int
}

func (s *fakeAttachmentStore) SessionAttachments(context.Context) (types.MessageAttachments, error) {
	return s.attachments, nil
}

func (s *fakeAttachmentStore) TemporaryDocument(_ context.Context, id string) (*types.TemporaryDocument, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loads == nil {
		s.loads = map[string]int{}
	}
	s.loads[id]++
	document := s.documents[id]
	if document != nil && s.readyAfter[id] > 0 && s.loads[id] >= s.readyAfter[id] {
		document.Status = types.TemporaryDocumentStatusReady
	}
	return document, nil
}

func readyDocument(id, content string) *types.TemporaryDocument {
	return &types.TemporaryDocument{
		ID: id, FileName: id + ".pdf", FileType: ".pdf", MimeType: "application/pdf",
		Status: types.TemporaryDocumentStatusReady, Content: content, TokenCount: len(content) / 2,
	}
}

func singleAttachmentStore(document *types.TemporaryDocument) *fakeAttachmentStore {
	return &fakeAttachmentStore{
		attachments: types.MessageAttachments{{ID: document.ID, FileName: document.FileName}},
		documents:   map[string]*types.TemporaryDocument{document.ID: document},
	}
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	return buf.Bytes()
}

func runReadFile(t *testing.T, tool *ReadFileTool, input ReadFileInput) *types.ToolResult {
	t.Helper()
	args, err := json.Marshal(input)
	require.NoError(t, err)
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}

func TestReadFileAttachmentPagesThroughParsedText(t *testing.T) {
	lines := make([]string, 2500)
	for i := range lines {
		lines[i] = fmt.Sprintf("section %d text", i+1)
	}
	document := readyDocument("doc-1", strings.Join(lines, "\n"))
	tool := NewReadFileTool(nil).WithAttachments(
		attachment.NewReader(singleAttachmentStore(document), time.Second, nil))
	path := types.AttachmentPath("doc-1")

	first := runReadFile(t, tool, ReadFileInput{Path: path, Limit: 5})
	require.True(t, first.Success, first.Error)
	assert.Contains(t, first.Output, "section 1 text")
	assert.Contains(t, first.Output, "section 5 text")
	assert.NotContains(t, first.Output, "section 6 text")
	assert.Contains(t, first.Output, "Untrusted reference data")
	assert.Equal(t, 6, first.Data["next_offset"])

	last := runReadFile(t, tool, ReadFileInput{Path: path, Offset: 2499})
	require.True(t, last.Success, last.Error)
	assert.Contains(t, last.Output, "section 2500 text")
	assert.NotContains(t, last.Data, "next_offset")

	byName := runReadFile(t, tool, ReadFileInput{Path: "attachment://doc-1.pdf", Limit: 1})
	require.True(t, byName.Success, byName.Error)
	assert.Contains(t, byName.Output, "section 1 text")
}

func TestReadFileAttachmentQueryReturnsNumberedMatchingLines(t *testing.T) {
	document := readyDocument("doc-1", "revenue grew in Q1\nheadcount was flat\nRevenue fell in Q3 due to churn")
	tool := NewReadFileTool(nil).WithAttachments(
		attachment.NewReader(singleAttachmentStore(document), time.Second, nil))
	path := types.AttachmentPath("doc-1")

	result := runReadFile(t, tool, ReadFileInput{Path: path, Query: "revenue q3"})
	require.True(t, result.Success, result.Error)
	assert.Contains(t, result.Output, "3: Revenue fell in Q3")
	assert.NotContains(t, result.Output, "revenue grew in Q1")
	assert.Equal(t, 1, result.Data["match_count"])

	regex := runReadFile(t, tool, ReadFileInput{Path: path, Query: `q[13]\b`, Regex: true})
	require.True(t, regex.Success, regex.Error)
	assert.Equal(t, 2, regex.Data["match_count"])

	none := runReadFile(t, tool, ReadFileInput{Path: path, Query: "dividends"})
	require.True(t, none.Success, none.Error)
	assert.Contains(t, none.Output, "No line matches")

	invalid := runReadFile(t, tool, ReadFileInput{Path: path, Query: "(", Regex: true})
	assert.False(t, invalid.Success)
}

func TestReadFileAttachmentWaitsForParsingDocument(t *testing.T) {
	document := readyDocument("doc-1", "parsed body")
	document.Status = types.TemporaryDocumentStatusProcessing
	store := singleAttachmentStore(document)
	store.readyAfter = map[string]int{"doc-1": 2}
	tool := NewReadFileTool(nil).WithAttachments(attachment.NewReader(store, 5*time.Second, nil))

	result := runReadFile(t, tool, ReadFileInput{Path: types.AttachmentPath("doc-1")})

	require.True(t, result.Success, result.Error)
	assert.Contains(t, result.Output, "parsed body")
}

func TestReadFileAttachmentReportsStillParsingAfterWait(t *testing.T) {
	document := readyDocument("doc-1", "parsed body")
	document.Status = types.TemporaryDocumentStatusProcessing
	tool := NewReadFileTool(nil).WithAttachments(attachment.NewReader(singleAttachmentStore(document), 0, nil))

	result := runReadFile(t, tool, ReadFileInput{Path: types.AttachmentPath("doc-1")})

	assert.False(t, result.Success)
	assert.Contains(t, result.Error, "still being parsed")
}

func TestReadFileAttachmentRejectsDocumentsOutsideSession(t *testing.T) {
	store := singleAttachmentStore(readyDocument("doc-1", "mine"))
	store.documents["other"] = readyDocument("other", "someone else's")
	tool := NewReadFileTool(nil).WithAttachments(attachment.NewReader(store, time.Second, nil))

	for _, path := range []string{
		"attachment://other", types.AttachmentPath("other"), types.AttachmentPath("other") + "/images/1",
	} {
		result := runReadFile(t, tool, ReadFileInput{Path: path})
		assert.False(t, result.Success)
		assert.Contains(t, result.Error, "unknown attachment")
		assert.NotContains(t, result.Output, "someone else's")
	}
	unavailable := runReadFile(t, NewReadFileTool(nil), ReadFileInput{Path: types.AttachmentPath("doc-1")})
	assert.False(t, unavailable.Success)
}

func TestReadFileAttachmentFallsBackToStoredExcerptWhenExpired(t *testing.T) {
	store := &fakeAttachmentStore{
		attachments: types.MessageAttachments{
			{ID: "doc-1", FileName: "brief.pdf"},
			{ID: "doc-1", FileName: "brief.pdf", Content: "saved excerpt"},
		},
		documents: map[string]*types.TemporaryDocument{},
	}
	tool := NewReadFileTool(nil).WithAttachments(attachment.NewReader(store, time.Second, nil))

	result := runReadFile(t, tool, ReadFileInput{Path: "attachment://brief.pdf"})

	require.True(t, result.Success, result.Error)
	assert.Contains(t, result.Output, "saved excerpt")
	assert.Contains(t, result.Output, "has expired")
	assert.Equal(t, true, result.Data["expired"])
}

func TestReadFileAttachmentListsInventory(t *testing.T) {
	pending := readyDocument("doc-2", "x")
	pending.Status = types.TemporaryDocumentStatusProcessing
	store := &fakeAttachmentStore{
		attachments: types.MessageAttachments{
			{FileName: "inline.txt", Content: "inline only"},
			{ID: "doc-1", FileName: "a.pdf"},
			{ID: "doc-2", FileName: "b.pdf"},
		},
		documents: map[string]*types.TemporaryDocument{"doc-1": readyDocument("doc-1", "x"), "doc-2": pending},
	}
	tool := NewReadFileTool(nil).WithAttachments(attachment.NewReader(store, time.Second, nil))

	result := runReadFile(t, tool, ReadFileInput{Path: "attachment://"})

	require.True(t, result.Success, result.Error)
	assert.Contains(t, result.Output, `count="2"`)
	assert.Contains(t, result.Output, types.AttachmentPath("doc-1"))
	assert.Contains(t, result.Output, `status="processing"`)
	assert.NotContains(t, result.Output, "inline.txt")
}

func TestReadFileAttachmentReportsParseFailure(t *testing.T) {
	failed := readyDocument("doc-1", "")
	failed.Status = types.TemporaryDocumentStatusFailed
	failed.ErrorMessage = "encrypted PDF"
	tool := NewReadFileTool(nil).WithAttachments(attachment.NewReader(singleAttachmentStore(failed), time.Second, nil))

	result := runReadFile(t, tool, ReadFileInput{Path: types.AttachmentPath("doc-1")})

	assert.False(t, result.Success)
	assert.Contains(t, result.Error, "encrypted PDF")
}

func TestReadFileAttachmentNumbersImagesAndReadsThemOnDemand(t *testing.T) {
	var refs []types.TemporaryDocumentImage
	var body []string
	for i := 0; i < 6; i++ {
		url := fmt.Sprintf("local://chart-%d.png", i)
		refs = append(refs, types.TemporaryDocumentImage{URL: url})
		body = append(body, fmt.Sprintf("step %d ![shot](%s)", i, url))
	}
	document := readyDocument("report", strings.Join(body, "\n"))
	raw, err := json.Marshal(refs)
	require.NoError(t, err)
	document.ImageRefs = types.JSON(raw)
	data := pngBytes(t)
	var (
		mu    sync.Mutex
		reads []string
	)
	tool := NewReadFileTool(nil).WithAttachments(attachment.NewReader(singleAttachmentStore(document), 0,
		func(_ context.Context, ref string) ([]byte, error) {
			mu.Lock()
			defer mu.Unlock()
			reads = append(reads, ref)
			return data, nil
		}))
	path := types.AttachmentPath("report")

	text := runReadFile(t, tool, ReadFileInput{Path: path})
	require.True(t, text.Success, text.Error)
	assert.Contains(t, text.Output, "step 2 ![shot](local://chart-2.png) [image #3]")
	assert.Contains(t, text.Output, "It has 6 images")
	assert.Empty(t, text.Images)
	list := runReadFile(t, tool, ReadFileInput{Path: "attachment://"})
	assert.Contains(t, list.Output, `images="6"`)
	require.Empty(t, reads, "listing and text reads must not fetch images")

	batch := runReadFile(t, tool, ReadFileInput{Path: path + "/images", Limit: 30})
	require.True(t, batch.Success, batch.Error)
	require.Len(t, batch.Images, 4, "image batches are capped independently of the text limit")
	assert.ElementsMatch(t, []string{refs[0].URL, refs[1].URL, refs[2].URL, refs[3].URL}, reads)
	assert.Contains(t, batch.Images[0], "data:image/png;base64,")
	assert.Equal(t, path+"/images/5", batch.Data["next_path"])

	assert.Empty(t, batch.ImageDetail, "reading depth defaults to the engine's brief description")

	single := runReadFile(t, tool, ReadFileInput{
		Path: path + "/images/3", Detail: " FULL ", Focus: " the Q3 revenue ",
	})
	require.True(t, single.Success, single.Error)
	require.Len(t, single.Images, 1)
	assert.Equal(t, refs[2].URL, reads[len(reads)-1])
	assert.Equal(t, types.ImageDetailFull, single.ImageDetail)
	assert.Equal(t, "the Q3 revenue", single.ImageFocus)
	assert.Equal(t, path+"/images/3", single.Data["path"])
	invalid := runReadFile(t, tool, ReadFileInput{Path: path + "/images/3", Detail: "max"})
	assert.False(t, invalid.Success)
	assert.Contains(t, invalid.Error, "brief or full")

	tail := runReadFile(t, tool, ReadFileInput{Path: path + "/images/5", Limit: 4})
	require.True(t, tail.Success, tail.Error)
	require.Len(t, tail.Images, 2)
	assert.NotContains(t, tail.Data, "next_path")

	reads = nil
	for _, bad := range []string{path + "/images/7", path + "/images/0", path + "/images/x", path + "/pages"} {
		assert.False(t, runReadFile(t, tool, ReadFileInput{Path: bad}).Success, bad)
	}
	assert.Empty(t, reads, "invalid positions cannot read storage")
}

func TestReadFileStagedCopyReadsAsTheAttachment(t *testing.T) {
	document := readyDocument("brief", "# Marketing\nparsed docx body")
	document.Status = types.TemporaryDocumentStatusProcessing
	store := singleAttachmentStore(document)
	store.readyAfter = map[string]int{"brief": 2}
	source := &fakeSandboxFileSource{
		stat: &sandbox.RemoteStatEntry{Path: "/workspace/input/ab/brief.docx", Type: sandbox.RemoteEntryFile, Size: 4},
		data: []byte("PK\x03\x04"),
	}
	copyPath := "/workspace/input/ab/brief.docx"
	tool := NewReadFileTool(source).WithAttachments(attachment.NewReader(store, 5*time.Second, nil)).
		WithAttachmentCopies(map[string]string{copyPath: types.AttachmentPath("brief")})

	result, err := tool.Execute(sandboxFileTestContext(),
		json.RawMessage(`{"path":"/workspace/input/ab/./brief.docx"}`))

	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	assert.Contains(t, result.Output, "parsed docx body")
	assert.Contains(t, result.Output, "showing parsed content from "+types.AttachmentPath("brief"))
	assert.Zero(t, source.readCalls, "the binary original is not downloaded")

	other, err := tool.Execute(sandboxFileTestContext(), json.RawMessage(`{"path":"/workspace/input/ab/other.docx"}`))
	require.NoError(t, err)
	assert.Equal(t, 1, source.readCalls, "other sandbox files still read from the sandbox")
	assert.NotContains(t, other.Output, "parsed docx body")
}

func TestReadFileAttachmentImageUploadShowsTheImage(t *testing.T) {
	document := &types.TemporaryDocument{
		ID: "shot", FileName: "shot.png", FileType: ".png", MimeType: "image/png",
		ResourceRef: "local://uploads/shot.png", Status: types.TemporaryDocumentStatusReady,
		Content: "Settings > Welcome message",
	}
	data := pngBytes(t)
	var reads []string
	tool := NewReadFileTool(nil).WithAttachments(attachment.NewReader(singleAttachmentStore(document), 0,
		func(_ context.Context, ref string) ([]byte, error) {
			reads = append(reads, ref)
			return data, nil
		}))

	result := runReadFile(t, tool, ReadFileInput{Path: types.AttachmentPath("shot")})

	require.True(t, result.Success, result.Error)
	assert.Contains(t, result.Output, "Settings > Welcome message")
	require.Len(t, result.Images, 1)
	assert.Equal(t, []string{"local://uploads/shot.png"}, reads)
}

func TestReadFileAttachmentImageErrorsAreReported(t *testing.T) {
	document := readyDocument("report", "text")
	tool := NewReadFileTool(nil).WithAttachments(attachment.NewReader(singleAttachmentStore(document), 0, nil))
	path := types.AttachmentPath("report") + "/images"
	assert.Contains(t, runReadFile(t, tool, ReadFileInput{Path: path}).Error, "no stored images")
	document.ImageRefs = types.JSON(`[{"url":"local://chart.png"}]`)
	assert.Contains(t, runReadFile(t, tool, ReadFileInput{Path: path}).Error, "storage is unavailable")
	tool.WithAttachments(attachment.NewReader(singleAttachmentStore(document), 0,
		func(context.Context, string) ([]byte, error) { return nil, fmt.Errorf("missing image") }))
	assert.Contains(t, runReadFile(t, tool, ReadFileInput{Path: path}).Error, "missing image")
	tool.WithAttachments(attachment.NewReader(singleAttachmentStore(document), 0,
		func(context.Context, string) ([]byte, error) { return []byte("not an image"), nil }))
	assert.Contains(t, runReadFile(t, tool, ReadFileInput{Path: path}).Error, "failed to prepare")
}

func TestReadFileAttachmentImagesKeepPartialSuccessAndAdvancePastFailures(t *testing.T) {
	document := readyDocument("report", "text")
	document.ImageRefs = types.JSON(`[
		{"url":"good-1"},{"url":"missing-2"},{"url":"invalid-3"},{"url":"good-4"},
		{"url":"missing-5"},{"url":"invalid-6"},{"url":"good-7"}
	]`)
	data := pngBytes(t)
	tool := NewReadFileTool(nil).WithAttachments(attachment.NewReader(singleAttachmentStore(document), 0,
		func(_ context.Context, ref string) ([]byte, error) {
			switch {
			case strings.HasPrefix(ref, "missing"):
				return nil, fmt.Errorf("object not found")
			case strings.HasPrefix(ref, "invalid"):
				return []byte("not an image"), nil
			default:
				return data, nil
			}
		}))
	path := types.AttachmentPath("report")

	first := runReadFile(t, tool, ReadFileInput{Path: path + "/images", Limit: 4})
	require.True(t, first.Success, first.Error)
	require.Len(t, first.Images, 2)
	assert.Equal(t, []int{1, 4}, first.Data["image_numbers"])
	assert.Equal(t, path+"/images/5", first.Data["next_path"])
	assert.Contains(t, first.Output, "failed to read image 2")
	assert.Contains(t, first.Output, "failed to prepare image 3")

	failed := runReadFile(t, tool, ReadFileInput{Path: path + "/images/5", Limit: 2})
	require.False(t, failed.Success)
	assert.Empty(t, failed.Images)
	assert.Equal(t, path+"/images/7", failed.Data["next_path"], "all-failed pages must still reach later images")
	assert.Contains(t, failed.Error, path+"/images/7")
}

func TestAttachmentImageReadsDefaultToOneAndLimitFullReads(t *testing.T) {
	document := readyDocument("figures", "text")
	document.ImageRefs = types.JSON(`[{"url":"first"},{"url":"second"},{"url":"third"}]`)
	data := pngBytes(t)
	for _, tc := range []struct {
		name   string
		detail string
		limit  int
		want   int
	}{
		{name: "default brief", want: 1},
		{name: "explicit brief batch", detail: "brief", limit: 3, want: 3},
		{name: "full stays single", detail: "full", limit: 3, want: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var reads atomic.Int32
			tool := NewReadFileTool(nil).WithAttachments(attachment.NewReader(singleAttachmentStore(document), 0,
				func(context.Context, string) ([]byte, error) {
					reads.Add(1)
					return data, nil
				}))
			result := runReadFile(t, tool, ReadFileInput{
				Path: types.AttachmentPath(document.ID) + "/images", Detail: tc.detail, Limit: tc.limit,
				Focus: "Q3 revenue",
			})
			require.True(t, result.Success, result.Error)
			assert.Len(t, result.Images, tc.want)
			assert.Equal(t, int32(tc.want), reads.Load())
			assert.Equal(t, "Q3 revenue", result.ImageFocus)
			if tc.want == 1 {
				assert.Equal(t, types.AttachmentPath(document.ID)+"/images/2", result.Data["next_path"])
			}
		})
	}
}

func TestAttachmentRejectsInvalidLineCursors(t *testing.T) {
	tool := NewReadFileTool(nil).WithAttachments(
		attachment.NewReader(singleAttachmentStore(readyDocument("text", "abc")), 0, nil))
	for _, input := range []ReadFileInput{
		{LineOffset: -1}, {LineOffset: 3}, {LineOffset: 1, Offset: 2}, {LineOffset: 1, Query: "abc"},
	} {
		input.Path = types.AttachmentPath("text")
		result := runReadFile(t, tool, input)
		assert.False(t, result.Success, "%+v", input)
	}
}

func TestReadFileAttachmentCancellationIsNotReportedAsElapsedWait(t *testing.T) {
	document := readyDocument("report", "text")
	document.Status = types.TemporaryDocumentStatusProcessing
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tool := NewReadFileTool(nil).WithAttachments(
		attachment.NewReader(singleAttachmentStore(document), 120*time.Second, nil))

	result, err := tool.Execute(ctx, json.RawMessage(`{"path":"`+types.AttachmentPath("report")+`"}`))

	require.ErrorIs(t, err, context.Canceled)
	require.False(t, result.Success)
	assert.NotContains(t, result.Error, "after waiting")
}

func TestReadFileExecutionTimeoutFollowsAttachmentWait(t *testing.T) {
	assert.Zero(t, NewReadFileTool(nil).ExecutionTimeout())
	tool := NewReadFileTool(nil).WithAttachments(attachment.NewReader(&fakeAttachmentStore{}, 90*time.Second, nil))
	assert.Equal(t, 95*time.Second, tool.ExecutionTimeout())
	assert.Contains(t, tool.Description(), "/images/N")
}

func TestReadFileWorkspaceReturnsImageFilesAsImages(t *testing.T) {
	data := pngBytes(t)
	source := &fakeSandboxFileSource{
		stat: &sandbox.RemoteStatEntry{
			Path: "/workspace/input/shot.png", Type: sandbox.RemoteEntryFile, Size: int64(len(data)),
		},
		data: data,
	}

	result, err := NewReadFileTool(source).Execute(sandboxFileTestContext(),
		json.RawMessage(`{"path":"/workspace/input/shot.png"}`))

	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	require.Len(t, result.Images, 1)
	assert.Contains(t, result.Images[0], "data:image/png;base64,")
	assert.Equal(t, true, result.Data["image"])
	assert.Equal(t, "image_view", result.Data["display_type"])
	assert.Equal(t, "shot.png", result.Data["file_name"])
	assert.NotContains(t, result.Output, "base64")
}

func TestReadFileWorkspaceQuerySearchesTextFiles(t *testing.T) {
	data := []byte("alpha\nbeta gamma\nGamma delta\n")
	source := &fakeSandboxFileSource{
		stat: &sandbox.RemoteStatEntry{
			Path: "/workspace/output/notes.txt", Type: sandbox.RemoteEntryFile, Size: int64(len(data)),
		},
		data: data,
	}

	result, err := NewReadFileTool(source).Execute(sandboxFileTestContext(),
		json.RawMessage(`{"path":"/workspace/output/notes.txt","query":"gamma","offset":3}`))

	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	assert.Contains(t, result.Output, "3: Gamma delta")
	assert.NotContains(t, result.Output, "2: beta gamma")
	assert.Equal(t, 1, result.Data["match_count"])
}

func TestAttachmentReadsBeyondSandboxDownloadLimit(t *testing.T) {
	body := strings.Repeat("row data\n", 1048577) + "last row needle\n"
	document := readyDocument("large", body)
	tool := NewReadFileTool(nil).WithAttachments(attachment.NewReader(singleAttachmentStore(document), 0, nil))
	address := types.AttachmentPath(document.ID)
	page := runReadFile(t, tool, ReadFileInput{Path: address, Limit: 2})
	require.True(t, page.Success, page.Error)
	assert.Contains(t, page.Output, "row data\nrow data\n")
	assert.Equal(t, 3, page.Data["next_offset"])
	last := runReadFile(t, tool, ReadFileInput{Path: address, Offset: 1048578})
	require.True(t, last.Success, last.Error)
	assert.Contains(t, last.Output, "last row needle")
	search := runReadFile(t, tool, ReadFileInput{Path: address, Query: "needle"})
	require.True(t, search.Success, search.Error)
	assert.Contains(t, search.Output, "1048578: last row needle")
}

func TestAttachmentLongLinesRoundTripThroughCursors(t *testing.T) {
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired=%v", expired), func(t *testing.T) {
			line := strings.Repeat("中文🙂abc", 12000) + "needle at end"
			document := readyDocument("long", line+"\nfinal line\n")
			store := singleAttachmentStore(document)
			if expired {
				store.attachments[0].Content = document.Content
				delete(store.documents, document.ID)
			}
			registry := NewToolRegistry()
			registry.RegisterTool(NewReadFileTool(nil).WithAttachments(attachment.NewReader(store, 0, nil)))
			input := ReadFileInput{Path: types.AttachmentPath(document.ID), Limit: 1, MaxBytes: 4096}
			var recovered strings.Builder
			for calls := 0; ; calls++ {
				require.Less(t, calls, 100, "cursor must make progress")
				args, err := json.Marshal(input)
				require.NoError(t, err)
				result, err := registry.ExecuteTool(context.Background(), ToolReadFile, args)
				require.NoError(t, err)
				require.True(t, result.Success, result.Error)
				require.NotContains(t, result.Output, "output truncated")
				require.True(t, utf8.ValidString(result.Output))
				_, body, ok := strings.Cut(result.Output, "```\n")
				require.True(t, ok)
				body, _, ok = strings.Cut(body, "\n```\n")
				require.True(t, ok)
				recovered.WriteString(body)
				next, continuing := result.Data["next_line_offset"].(int)
				if !continuing {
					assert.Equal(t, 2, result.Data["next_offset"])
					break
				}
				require.Greater(t, next, input.LineOffset)
				input.Offset = result.Data["next_offset"].(int)
				input.LineOffset = next
			}
			assert.Equal(t, line, recovered.String())
		})
	}
}

func TestAttachmentPagesReserveAllOutputMetadata(t *testing.T) {
	for _, copyPath := range []string{"", "/workspace/input/abcdef/report.pdf"} {
		t.Run(copyPath, func(t *testing.T) {
			lines := make([]string, 1000)
			for i := range lines {
				lines[i] = fmt.Sprintf("%04d-%s", i, strings.Repeat("x", 95))
			}
			document := readyDocument("budget", strings.Join(lines, "\n")+"\n")
			document.FileName = strings.Repeat("f", 250) + ".pdf"
			document.ImageRefs = types.JSON(`[{"url":"local://figure.png"}]`)
			address := types.AttachmentPath(document.ID)
			tool := NewReadFileTool(nil).WithAttachments(attachment.NewReader(singleAttachmentStore(document), 0, nil))
			if copyPath != "" {
				tool.WithAttachmentCopies(map[string]string{copyPath: address})
				address = copyPath
			}
			registry := NewToolRegistry()
			registry.RegisterTool(tool)
			var recovered strings.Builder
			for offset := 1; ; {
				args, err := json.Marshal(ReadFileInput{Path: address, Offset: offset})
				require.NoError(t, err)
				result, err := registry.ExecuteTool(context.Background(), ToolReadFile, args)
				require.NoError(t, err)
				require.True(t, result.Success, result.Error)
				require.LessOrEqual(t, utf8.RuneCountInString(result.Output), DefaultMaxToolOutput)
				require.NotContains(t, result.Output, "output truncated")
				_, body, ok := strings.Cut(result.Output, "```\n")
				require.True(t, ok)
				body, _, ok = strings.Cut(body, "```\n")
				require.True(t, ok)
				recovered.WriteString(body)
				next, more := result.Data["next_offset"].(int)
				if !more {
					break
				}
				require.Greater(t, next, offset)
				offset = next
			}
			assert.Equal(t, document.Content, recovered.String())
		})
	}
}

func TestAttachmentImageInstructionsUseCompleteAddresses(t *testing.T) {
	doc := readyDocument("chart-report", "Report")
	doc.ImageRefs = types.JSON(`[{"url":"local://chart.png"}]`)
	data := pngBytes(t)
	tool := NewReadFileTool(nil).WithAttachments(attachment.NewReader(singleAttachmentStore(doc), 0,
		func(context.Context, string) ([]byte, error) { return data, nil }))
	require.Contains(t, tool.Description(), "attachment://<handle>/images/N")
	require.NotContains(t, tool.Description(), ". /images/N reads")
	require.Contains(t, tool.Description(), "calculations, use full + focus")
	require.NotContains(t, tool.Description(), "full only when the user explicitly requests")
	text := runReadFile(t, tool, ReadFileInput{Path: types.AttachmentPath(doc.ID)})
	require.True(t, text.Success)
	_, next, ok := strings.Cut(text.Output, "Start with read_file path=")
	require.True(t, ok)
	address, _, ok := strings.Cut(next, ";")
	require.True(t, ok)
	image := runReadFile(t, tool, ReadFileInput{Path: address, Detail: "full", Focus: "quarterly values"})
	require.True(t, image.Success, image.Error)
	require.Equal(t, types.AttachmentPath(doc.ID)+"/images/1", image.Data["path"])
	require.Len(t, image.Images, 1)
}

func TestBareImageReadGuidanceDoesNotShadowSandboxFiles(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(fmt.Sprint(exists), func(t *testing.T) {
			source := &fakeSandboxFileSource{}
			if exists {
				source.data = []byte("real sandbox file")
				source.stat = &sandbox.RemoteStatEntry{Type: sandbox.RemoteEntryFile, Size: int64(len(source.data))}
			}
			store := singleAttachmentStore(readyDocument("report", "not the sandbox file"))
			tool := NewReadFileTool(source).WithAttachments(attachment.NewReader(store, 0, nil))
			result, err := tool.Execute(sandboxFileTestContext(), json.RawMessage(`{"path":"/images/1"}`))
			require.NoError(t, err)
			require.Empty(t, store.loads, "a bare path must never guess an attachment")
			if exists {
				require.True(t, result.Success, result.Error)
				require.Contains(t, result.Output, "real sandbox file")
			} else {
				require.False(t, result.Success)
				require.Contains(t, result.Error, "attachment://<handle>/images/1")
			}
		})
	}
}
