package service

import (
	"bytes"
	"context"
	"io"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// Fork copies message attachment IDs onto the new session but leaves the
// temporary_documents row on the parent. Preview/Get are session-scoped, so
// they must still resolve a parent-owned document when this session's copied
// messages reference it — and must not leak a document that is not on the
// fork's history.

type forkAccessDocRepo struct {
	docs []*types.TemporaryDocument
}

func (r *forkAccessDocRepo) Create(context.Context, *types.TemporaryDocument) error {
	panic("unexpected Create")
}
func (r *forkAccessDocRepo) MarkProcessing(context.Context, uint64, string, time.Time) error {
	panic("unexpected MarkProcessing")
}
func (r *forkAccessDocRepo) MarkReady(context.Context, uint64, string, string, types.JSON, types.JSON, types.JSON, int, int, time.Time) error {
	panic("unexpected MarkReady")
}
func (r *forkAccessDocRepo) MarkFailed(context.Context, uint64, string, string) error {
	panic("unexpected MarkFailed")
}
func (r *forkAccessDocRepo) DeleteScoped(context.Context, uint64, string, string) error {
	panic("unexpected DeleteScoped")
}
func (r *forkAccessDocRepo) ListExpired(context.Context, time.Time, int) ([]*types.TemporaryDocument, error) {
	panic("unexpected ListExpired")
}
func (r *forkAccessDocRepo) ListScoped(context.Context, uint64, string) ([]*types.TemporaryDocument, error) {
	panic("unexpected ListScoped")
}

func (r *forkAccessDocRepo) GetByID(_ context.Context, tenantID uint64, documentID string) (*types.TemporaryDocument, error) {
	for _, doc := range r.docs {
		if doc.TenantID == tenantID && doc.ID == documentID {
			return doc, nil
		}
	}
	return nil, nil
}

func (r *forkAccessDocRepo) GetScoped(_ context.Context, tenantID uint64, sessionID, documentID string) (*types.TemporaryDocument, error) {
	for _, doc := range r.docs {
		if doc.TenantID == tenantID && doc.SessionID == sessionID && doc.ID == documentID {
			return doc, nil
		}
	}
	return nil, nil
}

type forkAccessAttachments struct {
	bySession map[string]types.MessageAttachments
}

func (s *forkAccessAttachments) GetSessionAttachments(_ context.Context, sessionID string) (types.MessageAttachments, error) {
	return s.bySession[sessionID], nil
}

func parentOwnedBrief(t *testing.T) *types.TemporaryDocument {
	t.Helper()
	return &types.TemporaryDocument{
		ID:          "doc-1",
		TenantID:    7,
		SessionID:   "parent",
		ResourceRef: "local://tenant/brief.pdf",
		FileName:    "brief.pdf",
		FileType:    ".pdf",
		FileSize:    12,
		Status:      types.TemporaryDocumentStatusReady,
		ExpiresAt:   time.Now().Add(time.Hour),
	}
}

func TestGetUsesOwningSessionScopeWithoutMessageLookup(t *testing.T) {
	doc := parentOwnedBrief(t)
	doc.SessionID = "session-1"
	svc := &temporaryDocumentService{
		repo: &forkAccessDocRepo{docs: []*types.TemporaryDocument{doc}},
	}

	got, err := svc.Get(context.Background(), 7, "session-1", "doc-1")

	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "session-1", got.SessionID)
}

func TestGetResolvesParentAttachmentReferencedByForkedSession(t *testing.T) {
	doc := parentOwnedBrief(t)
	svc := &temporaryDocumentService{
		repo: &forkAccessDocRepo{docs: []*types.TemporaryDocument{doc}},
		sessionAttachments: &forkAccessAttachments{bySession: map[string]types.MessageAttachments{
			"fork": {{ID: "doc-1", FileName: "brief.pdf"}},
		}},
	}

	got, err := svc.Get(context.Background(), 7, "fork", "doc-1")

	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "doc-1", got.ID)
	require.Equal(t, "parent", got.SessionID)
}

func TestGetHidesParentAttachmentNotOnForkedHistory(t *testing.T) {
	doc := parentOwnedBrief(t)
	svc := &temporaryDocumentService{
		repo: &forkAccessDocRepo{docs: []*types.TemporaryDocument{doc}},
		sessionAttachments: &forkAccessAttachments{bySession: map[string]types.MessageAttachments{
			"fork": {{ID: "other-doc", FileName: "later.pdf"}},
		}},
	}

	got, err := svc.Get(context.Background(), 7, "fork", "doc-1")

	require.NoError(t, err)
	require.Nil(t, got, "a fork must not preview attachments that were not copied into its history")
}

func TestOpenFileResolvesParentAttachmentReferencedByForkedSession(t *testing.T) {
	doc := parentOwnedBrief(t)
	files := &stagingFileService{files: map[string][]byte{doc.ResourceRef: []byte("%PDF-fake")}}
	svc := &temporaryDocumentService{
		repo:        &forkAccessDocRepo{docs: []*types.TemporaryDocument{doc}},
		fileService: files,
		sessionAttachments: &forkAccessAttachments{bySession: map[string]types.MessageAttachments{
			"fork": {{ID: "doc-1", FileName: "brief.pdf"}},
		}},
	}

	reader, name, err := svc.OpenFile(context.Background(), 7, "fork", "doc-1")

	require.NoError(t, err)
	require.Equal(t, "brief.pdf", name)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.True(t, bytes.Equal(body, []byte("%PDF-fake")))
}

func TestResolveForPromptUsesParentAttachmentReferencedByFork(t *testing.T) {
	doc := parentOwnedBrief(t)
	doc.Content = "parent brief body"
	svc := &temporaryDocumentService{
		repo: &forkAccessDocRepo{docs: []*types.TemporaryDocument{doc}},
		sessionAttachments: &forkAccessAttachments{bySession: map[string]types.MessageAttachments{
			"fork": {{ID: "doc-1", FileName: "brief.pdf"}},
		}},
	}

	got, err := svc.ResolveForPrompt(context.Background(), 7, "fork", []string{"doc-1"}, "summarize")

	require.NoError(t, err)
	require.Len(t, got.Attachments, 1)
	require.Equal(t, "doc-1", got.Attachments[0].ID)
	require.Equal(t, "parent brief body", got.Attachments[0].Content)
}

func TestResolveForPromptHidesParentAttachmentNotOnForkedHistory(t *testing.T) {
	doc := parentOwnedBrief(t)
	doc.Content = "secret"
	svc := &temporaryDocumentService{
		repo: &forkAccessDocRepo{docs: []*types.TemporaryDocument{doc}},
		sessionAttachments: &forkAccessAttachments{bySession: map[string]types.MessageAttachments{
			"fork": {{ID: "other-doc", FileName: "later.pdf"}},
		}},
	}

	_, err := svc.ResolveForPrompt(context.Background(), 7, "fork", []string{"doc-1"}, "summarize")

	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}
