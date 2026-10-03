package session

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type engineTestSessions struct{ interfaces.SessionService }

func (engineTestSessions) GetOwnedSession(context.Context, string) (*types.Session, error) {
	return &types.Session{ID: "session-1", TenantID: 7}, nil
}

type engineTestDocuments struct {
	interfaces.TemporaryDocumentService
	options types.TemporaryDocumentCreateOptions
}

func (d *engineTestDocuments) Create(
	_ context.Context, _ uint64, _, _, _ string, _ int64, _ io.Reader, options types.TemporaryDocumentCreateOptions,
) (*types.TemporaryDocument, error) {
	d.options = options
	return &types.TemporaryDocument{ID: "doc-1"}, nil
}

func uploadWithEngine(t *testing.T, h *Handler, agentID string) {
	t.Helper()
	body := &bytes.Buffer{}
	form := multipart.NewWriter(body)
	part, err := form.CreateFormFile("file", "report.pdf")
	require.NoError(t, err)
	_, _ = part.Write([]byte("%PDF-1.4"))
	require.NoError(t, form.WriteField("parser_engine", "mineru_cloud"))
	require.NoError(t, form.WriteField("agent_id", agentID))
	require.NoError(t, form.Close())

	c, ctx := newResolveAgentTestContext(7)
	c.Params = gin.Params{{Key: "session_id", Value: "session-1"}}
	req := httptest.NewRequest(http.MethodPost, "/", body).WithContext(ctx)
	req.Header.Set("Content-Type", form.FormDataContentType())
	c.Request = req
	h.UploadTemporaryDocument(c)
	require.Empty(t, c.Errors)
}

// A shared agent parses in its owner's workspace; the caller must not pick
// which of the owner's parser engines (and credentials) runs.
func TestUploadTemporaryDocumentIgnoresCallerEngineForSharedAgents(t *testing.T) {
	shared := &engineTestDocuments{}
	h := &Handler{
		sessionService:     engineTestSessions{},
		temporaryDocuments: shared,
		agentShareService:  &resolveAgentShareStub{agent: &types.CustomAgent{ID: "shared-agent", TenantID: 84}},
		customAgentService: &resolveOwnAgentStub{err: context.Canceled},
	}
	uploadWithEngine(t, h, "shared-agent")
	require.Equal(t, uint64(84), shared.options.ResourceTenantID)
	require.Empty(t, shared.options.ParserEngine)

	own := &engineTestDocuments{}
	h.temporaryDocuments = own
	h.customAgentService = &resolveOwnAgentStub{agent: &types.CustomAgent{ID: "own-agent", TenantID: 7}}
	uploadWithEngine(t, h, "own-agent")
	require.Equal(t, "mineru_cloud", own.options.ParserEngine, "the caller's own workspace keeps its choice")
}
