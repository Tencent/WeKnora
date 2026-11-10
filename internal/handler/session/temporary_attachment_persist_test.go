package session

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// attachmentMsgStub implements just the two MessageService methods used by
// persistResolvedAttachmentContent; the rest are inherited from the embedded
// interface and panic if unexpectedly called.
type attachmentMsgStub struct {
	interfaces.MessageService
	stored      *types.Message
	updated     *types.Message
	updateCalls int
}

func (s *attachmentMsgStub) GetMessage(_ context.Context, _ string, _ string) (*types.Message, error) {
	return s.stored, nil
}

func (s *attachmentMsgStub) UpdateMessage(_ context.Context, message *types.Message) error {
	s.updateCalls++
	s.updated = message
	return nil
}

func newAttachmentReqCtx() *qaRequestContext {
	return &qaRequestContext{
		sessionID:     "sess-1",
		userMessageID: "user-msg-1",
		session:       &types.Session{TenantID: 42},
	}
}

// TestPersistResolvedAttachmentContent_EnrichesMatchingAttachments is the core
// regression guard for PR #2086: the user message is created with
// metadata-only attachment entries, and the parsed content selected after the
// SSE stream starts must be written back so multi-turn (Agent-mode) history can
// replay it via the Attachments column.
func TestPersistResolvedAttachmentContent_EnrichesMatchingAttachments(t *testing.T) {
	stub := &attachmentMsgStub{
		stored: &types.Message{
			ID:        "user-msg-1",
			SessionID: "sess-1",
			Role:      "user",
			Attachments: types.MessageAttachments{
				{ID: "doc-1", FileName: "report.pdf", FileType: ".pdf"},
				// Legacy inline upload without an ID must stay untouched.
				{FileName: "note.txt", FileType: ".txt", Content: "legacy inline"},
			},
		},
	}
	h := &Handler{messageService: stub}

	resolved := types.MessageAttachments{
		{
			ID: "doc-1", FileName: "report.pdf", FileType: ".pdf",
			Content: "parsed body text", ContentMode: "selected_chunks",
			SelectedChunks: 1, TotalChunks: 3, TokenCount: 12,
		},
	}
	h.persistResolvedAttachmentContent(context.Background(), newAttachmentReqCtx(), resolved)

	require.Equal(t, 1, stub.updateCalls, "matching attachment must trigger one persist")
	require.NotNil(t, stub.updated)
	require.Len(t, stub.updated.Attachments, 2)
	assert.Equal(t, "parsed body text", stub.updated.Attachments[0].Content)
	assert.Equal(t, "selected_chunks", stub.updated.Attachments[0].ContentMode)
	assert.Equal(t, 3, stub.updated.Attachments[0].TotalChunks)
	// Legacy inline attachment (no ID) is preserved verbatim.
	assert.Equal(t, "legacy inline", stub.updated.Attachments[1].Content)
}

func TestPersistResolvedAttachmentContent_NoMatchSkipsUpdate(t *testing.T) {
	stub := &attachmentMsgStub{
		stored: &types.Message{
			ID: "user-msg-1", SessionID: "sess-1", Role: "user",
			Attachments: types.MessageAttachments{{ID: "doc-1", FileName: "a.pdf"}},
		},
	}
	h := &Handler{messageService: stub}

	resolved := types.MessageAttachments{{ID: "doc-other", Content: "x"}}
	h.persistResolvedAttachmentContent(context.Background(), newAttachmentReqCtx(), resolved)

	assert.Equal(t, 0, stub.updateCalls, "no matching ID must not persist")
}

func TestPersistResolvedAttachmentContent_GuardsEmptyInput(t *testing.T) {
	stub := &attachmentMsgStub{}
	h := &Handler{messageService: stub}

	// No resolved attachments.
	h.persistResolvedAttachmentContent(context.Background(), newAttachmentReqCtx(), nil)
	// No user message ID.
	rc := newAttachmentReqCtx()
	rc.userMessageID = ""
	h.persistResolvedAttachmentContent(context.Background(), rc, types.MessageAttachments{{ID: "doc-1", Content: "x"}})

	assert.Equal(t, 0, stub.updateCalls)
}

func TestNormalizeTemporaryAttachmentIDs(t *testing.T) {
	t.Run("rejects over limit before lookup", func(t *testing.T) {
		ids := []string{"a", "b", "c", "d", "e", "f"}
		_, err := normalizeTemporaryAttachmentIDs(ids)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "at most 5")
	})

	t.Run("dedupes and drops empty", func(t *testing.T) {
		got, err := normalizeTemporaryAttachmentIDs([]string{" a ", "", "b", "a", "c"})
		require.NoError(t, err)
		assert.Equal(t, []string{"a", "b", "c"}, got)
	})

	t.Run("allows max unique ids", func(t *testing.T) {
		got, err := normalizeTemporaryAttachmentIDs([]string{"1", "2", "3", "4", "5"})
		require.NoError(t, err)
		assert.Equal(t, []string{"1", "2", "3", "4", "5"}, got)
	})
}
