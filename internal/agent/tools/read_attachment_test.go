package tools

import (
	"context"
	"encoding/json"
	"fmt"
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

	result := runReadAttachment(t, tool, ReadAttachmentInput{Attachment: types.AttachmentHandle("doc-1"), Query: "revenue q3"})

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
