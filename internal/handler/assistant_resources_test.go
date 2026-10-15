package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type resourceSessions struct {
	interfaces.SessionService
	tenant uint64
	fail   bool
}

func (s *resourceSessions) GetOwnedSession(ctx context.Context, id string) (*types.Session, error) {
	s.tenant = types.MustTenantIDFromContext(ctx)
	if s.fail {
		return nil, fmt.Errorf("denied")
	}
	return &types.Session{ID: id, TenantID: s.tenant}, nil
}

type resourceMessages struct{ interfaces.MessageRepository }

func (*resourceMessages) GetSessionAttachments(context.Context, string) (types.MessageAttachments, error) {
	return types.MessageAttachments{{ID: "selected"}}, nil
}

type resourceDocuments struct {
	interfaces.TemporaryDocumentService
	doc    *types.TemporaryDocument
	gets   int
	opened int
}

func (d *resourceDocuments) Get(
	_ context.Context, tenant uint64, session, id string,
) (*types.TemporaryDocument, error) {
	d.gets++
	if tenant != 1 || session != "current" || id != "selected" {
		return nil, fmt.Errorf("wrong scope")
	}
	return d.doc, nil
}

func (d *resourceDocuments) OpenFile(context.Context, uint64, string, string) (io.ReadCloser, string, error) {
	d.opened++
	return io.NopCloser(strings.NewReader(`[{"id":123,"standard_question":"Q","answers":["A"]}]`)), "import.json", nil
}

func resourceContext() context.Context {
	ctx := types.WithInternalDispatch(context.Background(), types.InternalDispatch{UserID: "u", TenantID: 1})
	ctx = types.WithAuditAgentAction(ctx, types.AuditAgentAction{
		SessionID: "current", Operation: "faq.import_selected",
	})
	return context.WithValue(ctx, types.TenantIDContextKey, uint64(9))
}

func TestAssistantAttachmentRequiresCurrentOwnedSessionInventory(t *testing.T) {
	sessions := &resourceSessions{}
	docs := &resourceDocuments{doc: &types.TemporaryDocument{
		ID: "selected", TenantID: 1, ResourceRef: "secret-storage-path",
	}}
	h := &AssistantResourceHandler{sessions: sessions, messages: &resourceMessages{}, documents: docs}
	_, _, _, err := h.selected(context.Background(), "selected")
	require.Error(t, err)
	require.Zero(t, docs.gets)
	_, _, _, err = h.selected(resourceContext(), "unselected")
	require.Error(t, err)
	require.Zero(t, docs.gets)
	ctx, session, doc, err := h.selected(resourceContext(), "selected")
	require.NoError(t, err)
	require.EqualValues(t, 1, sessions.tenant)
	require.EqualValues(t, 1, types.MustTenantIDFromContext(ctx))
	require.Equal(t, "current", session)
	encoded, err := json.Marshal(attachmentMetadata(doc, session))
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "secret-storage")
	sessions.fail = true
	_, _, _, err = h.selected(resourceContext(), "selected")
	require.Error(t, err)
	require.Equal(t, 1, docs.gets)
	sessions.fail = false
	docs.doc.ExpiresAt = time.Now().Add(-time.Second)
	_, _, _, err = h.selected(resourceContext(), "selected")
	require.Error(t, err)
}

func TestSelectedFAQImportDropsExportIDsAndCannotReplaceKB(t *testing.T) {
	payload, err := selectedFAQPayload(strings.NewReader(
		`[{"id":100000001,"standard_question":"Q","answers":["A"],"is_enabled":false}]`,
	))
	require.NoError(t, err)
	require.Equal(t, types.FAQBatchModeAppend, payload.Mode)
	require.Nil(t, payload.Entries[0].ID)
	require.False(t, *payload.Entries[0].IsEnabled)
	for _, body := range []string{
		`[]`, `{"mode":"replace","entries":[]}`, `[{"standard_question":"Q"}]`, strings.Repeat("x", (1<<20)+1),
	} {
		_, err := selectedFAQPayload(strings.NewReader(body))
		require.Error(t, err)
	}
	rows := make([]types.FAQEntryPayload, 201)
	raw, _ := json.Marshal(rows)
	_, err = selectedFAQPayload(strings.NewReader(string(raw)))
	require.Error(t, err)
}

func TestAttachmentMultipartBoundsOriginalBytes(t *testing.T) {
	_, _, errName := attachmentForm(strings.NewReader("body"), "evil\r\nheader.txt", 20)
	require.Error(t, errName)
	_, _, err := attachmentForm(strings.NewReader("oversized"), "test.txt", 3)
	require.Error(t, err)
	header, cleanup, err := attachmentForm(strings.NewReader("original bytes"), "doc.txt", 20)
	require.NoError(t, err)
	defer cleanup()
	file, err := header.Open()
	require.NoError(t, err)
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(file)
	require.NoError(t, err)
	require.Equal(t, "original bytes", string(data))
}

type resourceFAQKnowledge struct {
	interfaces.KnowledgeService
	tenant  uint64
	kb      string
	payload *types.FAQBatchUpsertPayload
}

func (k *resourceFAQKnowledge) UpsertFAQEntries(
	ctx context.Context, kb string, p *types.FAQBatchUpsertPayload,
) (string, error) {
	k.tenant = types.MustTenantIDFromContext(ctx)
	k.kb = kb
	k.payload = p
	return "task", nil
}

func TestAssistantImportReadsCallerAttachmentAndWritesAuthorizedKB(t *testing.T) {
	docs := &resourceDocuments{doc: &types.TemporaryDocument{ID: "selected", TenantID: 1, FileName: "import.json"}}
	knowledge := &resourceFAQKnowledge{}
	h := &AssistantResourceHandler{
		sessions: &resourceSessions{}, messages: &resourceMessages{}, documents: docs, knowledge: knowledge,
	}
	r := gin.New()
	r.POST("/:id", h.ImportFAQ)
	req := httptest.NewRequest(http.MethodPost, "/shared-kb", strings.NewReader(
		`{"attachment_id":"selected","session_id":"foreign","mode":"replace"}`,
	))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(resourceContext())
	recorder := httptest.NewRecorder()
	r.ServeHTTP(recorder, req)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.EqualValues(t, 9, knowledge.tenant)
	require.Equal(t, "shared-kb", knowledge.kb)
	require.Equal(t, types.FAQBatchModeAppend, knowledge.payload.Mode)
	require.Equal(t, 1, docs.opened)
}
