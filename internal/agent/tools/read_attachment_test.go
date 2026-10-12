package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"sync"
	"testing"
	"time"

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

func chunkedDocument(t *testing.T, id string, contents ...string) *types.TemporaryDocument {
	t.Helper()
	chunks := make([]types.TemporaryDocumentChunk, 0, len(contents))
	for i, content := range contents {
		chunks = append(chunks, types.TemporaryDocumentChunk{Seq: i, Content: content, TokenCount: 10})
	}
	raw, err := json.Marshal(chunks)
	require.NoError(t, err)
	return &types.TemporaryDocument{
		ID: id, FileName: id + ".pdf", FileType: ".pdf", Status: types.TemporaryDocumentStatusReady,
		Chunks: types.JSON(raw), ChunkCount: len(chunks), TokenCount: 10 * len(chunks),
	}
}

func runReadAttachment(t *testing.T, tool *ReadAttachmentTool, input ReadAttachmentInput) *types.ToolResult {
	t.Helper()
	args, err := json.Marshal(input)
	require.NoError(t, err)
	result, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	require.NotNil(t, result)
	return result
}

func TestReadAttachmentPagesThroughChunks(t *testing.T) {
	contents := make([]string, 12)
	for i := range contents {
		contents[i] = fmt.Sprintf("section %d text", i)
	}
	store := &fakeAttachmentStore{
		attachments: types.MessageAttachments{{ID: "doc-1", FileName: "doc-1.pdf"}},
		documents:   map[string]*types.TemporaryDocument{"doc-1": chunkedDocument(t, "doc-1", contents...)},
	}
	tool := NewReadAttachmentTool(store, time.Second)
	handle := types.AttachmentHandle("doc-1")

	first := runReadAttachment(t, tool, ReadAttachmentInput{Attachment: handle, Limit: 5})
	require.True(t, first.Success, first.Error)
	assert.Contains(t, first.Output, "section 0 text")
	assert.Contains(t, first.Output, "section 4 text")
	assert.NotContains(t, first.Output, "section 5 text")
	assert.Equal(t, 5, first.Data["next_offset"])

	last := runReadAttachment(t, tool, ReadAttachmentInput{Attachment: handle, Offset: 10, Limit: 5})
	require.True(t, last.Success, last.Error)
	assert.Contains(t, last.Output, "section 11 text")
	assert.NotContains(t, last.Data, "next_offset")

	outOfRange := runReadAttachment(t, tool, ReadAttachmentInput{Attachment: handle, Offset: 12})
	assert.False(t, outOfRange.Success)
}

func TestReadAttachmentSearchesWithinDocument(t *testing.T) {
	store := &fakeAttachmentStore{
		attachments: types.MessageAttachments{{ID: "doc-1", FileName: "doc-1.pdf"}},
		documents: map[string]*types.TemporaryDocument{"doc-1": chunkedDocument(t, "doc-1",
			"revenue grew in Q1", "headcount was flat", "Revenue fell in Q3 due to churn")},
	}
	tool := NewReadAttachmentTool(store, time.Second)

	result := runReadAttachment(t, tool, ReadAttachmentInput{
		Attachment: types.AttachmentHandle("doc-1"), Query: "revenue q3",
	})

	require.True(t, result.Success, result.Error)
	assert.Contains(t, result.Output, "Revenue fell in Q3")
	assert.NotContains(t, result.Output, "revenue grew in Q1")
	assert.Equal(t, 1, result.Data["match_count"])
}

func TestReadAttachmentWaitsForParsingDocument(t *testing.T) {
	document := chunkedDocument(t, "doc-1", "parsed body")
	document.Status = types.TemporaryDocumentStatusProcessing
	store := &fakeAttachmentStore{
		attachments: types.MessageAttachments{{ID: "doc-1", FileName: "doc-1.pdf"}},
		documents:   map[string]*types.TemporaryDocument{"doc-1": document},
		readyAfter:  map[string]int{"doc-1": 2},
	}
	tool := NewReadAttachmentTool(store, 5*time.Second)

	result := runReadAttachment(t, tool, ReadAttachmentInput{Attachment: types.AttachmentHandle("doc-1")})

	require.True(t, result.Success, result.Error)
	assert.Contains(t, result.Output, "parsed body")
}

func TestReadAttachmentReportsStillParsingAfterWait(t *testing.T) {
	document := chunkedDocument(t, "doc-1", "parsed body")
	document.Status = types.TemporaryDocumentStatusProcessing
	store := &fakeAttachmentStore{
		attachments: types.MessageAttachments{{ID: "doc-1", FileName: "doc-1.pdf"}},
		documents:   map[string]*types.TemporaryDocument{"doc-1": document},
	}
	tool := NewReadAttachmentTool(store, 0)

	result := runReadAttachment(t, tool, ReadAttachmentInput{Attachment: types.AttachmentHandle("doc-1")})

	assert.False(t, result.Success)
	assert.Contains(t, result.Error, "still being parsed")
}

func TestReadAttachmentRejectsDocumentsOutsideSession(t *testing.T) {
	store := &fakeAttachmentStore{
		attachments: types.MessageAttachments{{ID: "doc-1", FileName: "doc-1.pdf"}},
		documents: map[string]*types.TemporaryDocument{
			"doc-1": chunkedDocument(t, "doc-1", "mine"),
			"other": chunkedDocument(t, "other", "someone else's"),
		},
	}
	tool := NewReadAttachmentTool(store, time.Second)

	for _, ref := range []string{"other", types.AttachmentHandle("other")} {
		result := runReadAttachment(t, tool, ReadAttachmentInput{Attachment: ref})
		assert.False(t, result.Success)
		assert.Contains(t, result.Error, "unknown attachment")
		assert.NotContains(t, result.Output, "someone else's")
	}
}

func TestReadAttachmentFallsBackToStoredExcerptWhenExpired(t *testing.T) {
	store := &fakeAttachmentStore{
		attachments: types.MessageAttachments{
			{ID: "doc-1", FileName: "brief.pdf"},
			{ID: "doc-1", FileName: "brief.pdf", Content: "saved excerpt"},
		},
		documents: map[string]*types.TemporaryDocument{},
	}
	tool := NewReadAttachmentTool(store, time.Second)

	result := runReadAttachment(t, tool, ReadAttachmentInput{Attachment: "brief.pdf"})

	require.True(t, result.Success, result.Error)
	assert.Contains(t, result.Output, "saved excerpt")
	assert.Contains(t, result.Output, `status="expired"`)
}

func TestReadAttachmentListsInventoryWhenNoAttachmentGiven(t *testing.T) {
	pending := chunkedDocument(t, "doc-2", "x")
	pending.Status = types.TemporaryDocumentStatusProcessing
	store := &fakeAttachmentStore{
		attachments: types.MessageAttachments{
			{FileName: "inline.txt", Content: "inline only"},
			{ID: "doc-1", FileName: "a.pdf"},
			{ID: "doc-2", FileName: "b.pdf"},
		},
		documents: map[string]*types.TemporaryDocument{"doc-1": chunkedDocument(t, "doc-1", "x"), "doc-2": pending},
	}
	tool := NewReadAttachmentTool(store, time.Second)

	result := runReadAttachment(t, tool, ReadAttachmentInput{})

	require.True(t, result.Success, result.Error)
	assert.Contains(t, result.Output, `count="2"`)
	assert.Contains(t, result.Output, types.AttachmentHandle("doc-1"))
	assert.Contains(t, result.Output, `status="processing"`)
	assert.NotContains(t, result.Output, "inline.txt")
}

func TestReadAttachmentReportsParseFailure(t *testing.T) {
	failed := chunkedDocument(t, "doc-1")
	failed.Status = types.TemporaryDocumentStatusFailed
	failed.ErrorMessage = "encrypted PDF"
	store := &fakeAttachmentStore{
		attachments: types.MessageAttachments{{ID: "doc-1", FileName: "doc-1.pdf"}},
		documents:   map[string]*types.TemporaryDocument{"doc-1": failed},
	}

	result := runReadAttachment(t, NewReadAttachmentTool(store, time.Second),
		ReadAttachmentInput{Attachment: types.AttachmentHandle("doc-1")})

	assert.False(t, result.Success)
	assert.Contains(t, result.Error, "encrypted PDF")
}

func TestReadAttachmentPagesThroughDocumentImagesOnDemand(t *testing.T) {
	document := chunkedDocument(t, "report", "report text")
	var refs []types.TemporaryDocumentImage
	for i := 0; i < 6; i++ {
		refs = append(refs, types.TemporaryDocumentImage{URL: fmt.Sprintf("local://chart-%d.png", i)})
	}
	raw, err := json.Marshal(refs)
	require.NoError(t, err)
	document.ImageRefs = types.JSON(raw)
	store := &fakeAttachmentStore{
		attachments: types.MessageAttachments{{ID: document.ID, FileName: document.FileName}},
		documents:   map[string]*types.TemporaryDocument{document.ID: document},
	}
	var pngData bytes.Buffer
	require.NoError(t, png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	var reads []string
	tool := NewReadAttachmentTool(store, 0).WithImageReader(func(_ context.Context, ref string) ([]byte, error) {
		reads = append(reads, ref)
		return pngData.Bytes(), nil
	})
	for _, input := range []ReadAttachmentInput{{}, {Attachment: "report"}, {Attachment: "report", Query: "text"}} {
		result := runReadAttachment(t, tool, input)
		require.True(t, result.Success, result.Error)
		assert.Contains(t, result.Output, `total_images="6"`)
		assert.Empty(t, result.Images)
	}
	require.Empty(t, reads, "listing and text reads must not fetch images")
	first := runReadAttachment(t, tool, ReadAttachmentInput{Attachment: "report", Images: true, Limit: 30})
	require.True(t, first.Success, first.Error)
	require.Len(t, first.Images, 4, "image batches are capped independently of the text limit")
	assert.Equal(t, []string{refs[0].URL, refs[1].URL, refs[2].URL, refs[3].URL}, reads)
	assert.Contains(t, first.Images[0], "data:image/png;base64,")
	assert.Equal(t, 4, first.Data["next_offset"])
	last := runReadAttachment(t, tool, ReadAttachmentInput{Attachment: "report", Images: true, Offset: 4})
	require.True(t, last.Success, last.Error)
	require.Len(t, last.Images, 2)
	assert.NotContains(t, last.Data, "next_offset")
	outOfRange := runReadAttachment(t, tool, ReadAttachmentInput{Attachment: "report", Images: true, Offset: 6})
	assert.False(t, outOfRange.Success)
	assert.False(t, runReadAttachment(t, tool, ReadAttachmentInput{Attachment: "other-session", Images: true}).Success)
	assert.Len(t, reads, 6, "invalid positions and unauthorized documents cannot read storage")
}

func TestReadAttachmentImageErrorsAreReported(t *testing.T) {
	document := chunkedDocument(t, "report", "text")
	store := &fakeAttachmentStore{
		attachments: types.MessageAttachments{{ID: "report"}},
		documents:   map[string]*types.TemporaryDocument{"report": document},
	}
	tool := NewReadAttachmentTool(store, 0)
	input := ReadAttachmentInput{Attachment: "report", Images: true}
	assert.Contains(t, runReadAttachment(t, tool, input).Error, "no extracted images")
	document.ImageRefs = types.JSON(`[{"url":"local://chart.png"}]`)
	assert.Contains(t, runReadAttachment(t, tool, input).Error, "storage is unavailable")
	tool.WithImageReader(func(context.Context, string) ([]byte, error) { return nil, fmt.Errorf("missing image") })
	assert.Contains(t, runReadAttachment(t, tool, input).Error, "missing image")
	tool.WithImageReader(func(context.Context, string) ([]byte, error) { return []byte("not an image"), nil })
	assert.Contains(t, runReadAttachment(t, tool, input).Error, "failed to prepare")
}

func TestReadAttachmentCancellationDoesNotReportConfiguredWaitAsElapsed(t *testing.T) {
	document := chunkedDocument(t, "report", "text")
	document.Status = types.TemporaryDocumentStatusProcessing
	store := &fakeAttachmentStore{
		attachments: types.MessageAttachments{{ID: "report"}},
		documents:   map[string]*types.TemporaryDocument{"report": document},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tool := NewReadAttachmentTool(store, 120*time.Second)
	result, err := tool.Execute(ctx, json.RawMessage(`{"attachment":"report"}`))
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, result.Success)
	assert.NotContains(t, result.Error, "after waiting")
}

func TestReadAttachmentImagesKeepPartialSuccessAndAdvancePastFailures(t *testing.T) {
	document := chunkedDocument(t, "report", "text")
	document.ImageRefs = types.JSON(`[
		{"url":"good-0"},{"url":"missing-1"},{"url":"invalid-2"},{"url":"good-3"},
		{"url":"missing-4"},{"url":"invalid-5"},{"url":"good-6"}
	]`)
	store := &fakeAttachmentStore{
		attachments: types.MessageAttachments{{ID: "report"}},
		documents:   map[string]*types.TemporaryDocument{"report": document},
	}
	var pngData bytes.Buffer
	require.NoError(t, png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	tool := NewReadAttachmentTool(store, 0).WithImageReader(func(_ context.Context, ref string) ([]byte, error) {
		switch ref {
		case "missing-1", "missing-4":
			return nil, fmt.Errorf("object not found")
		case "invalid-2", "invalid-5":
			return []byte("not an image"), nil
		default:
			return pngData.Bytes(), nil
		}
	})
	first := runReadAttachment(t, tool, ReadAttachmentInput{Attachment: "report", Images: true})
	require.True(t, first.Success, first.Error)
	require.Len(t, first.Images, 2)
	assert.Equal(t, 2, first.Data["returned"])
	assert.Equal(t, []int{0, 3}, first.Data["image_indexes"])
	assert.Equal(t, 4, first.Data["next_offset"])
	assert.Contains(t, first.Output, "failed to read attachment image 1")
	assert.Contains(t, first.Output, "failed to prepare attachment image 2")
	failed := runReadAttachment(t, tool, ReadAttachmentInput{Attachment: "report", Images: true, Offset: 4, Limit: 2})
	require.False(t, failed.Success)
	assert.Empty(t, failed.Images)
	assert.Equal(t, 0, failed.Data["returned"])
	assert.Equal(t, 6, failed.Data["next_offset"], "all-failed pages must still allow reading later images")
	assert.Contains(t, failed.Error, "offset=6")
	last := runReadAttachment(t, tool, ReadAttachmentInput{Attachment: "report", Images: true, Offset: 6})
	require.True(t, last.Success, last.Error)
	require.Len(t, last.Images, 1)
	assert.Equal(t, []int{6}, last.Data["image_indexes"])
	assert.NotContains(t, last.Data, "next_offset")
}
