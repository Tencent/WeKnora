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

func uploadTemporaryDocument(t *testing.T, h *Handler, fields map[string]string) {
	t.Helper()
	body := &bytes.Buffer{}
	form := multipart.NewWriter(body)
	part, err := form.CreateFormFile("file", "report.pdf")
	require.NoError(t, err)
	_, _ = part.Write([]byte("%PDF-1.4"))
	for name, value := range fields {
		require.NoError(t, form.WriteField(name, value))
	}
	require.NoError(t, form.Close())

	c, ctx := newResolveAgentTestContext(7)
	c.Params = gin.Params{{Key: "session_id", Value: "session-1"}}
	req := httptest.NewRequest(http.MethodPost, "/", body).WithContext(ctx)
	req.Header.Set("Content-Type", form.FormDataContentType())
	c.Request = req
	h.UploadTemporaryDocument(c)
	require.Empty(t, c.Errors)
}

func uploadWithEngine(t *testing.T, h *Handler, agentID string) {
	t.Helper()
	uploadTemporaryDocument(t, h, map[string]string{"parser_engine": "mineru_cloud", "agent_id": agentID})
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

// A deployment default engine is not an agent choice. The upload must keep
// options.ParserEngine empty so the parse worker resolves the tenant's chat
// rules first and only then the deployment default.
func TestUploadTemporaryDocumentLeavesTenantRulePrecedenceToWorker(t *testing.T) {
	types.SetDeploymentParserDefaults("mineru", []string{"pdf"}, nil)
	t.Cleanup(func() { types.SetDeploymentParserDefaults("", nil, nil) })

	docs := &engineTestDocuments{}
	h := &Handler{
		sessionService:     engineTestSessions{},
		temporaryDocuments: docs,
		customAgentService: &resolveOwnAgentStub{agent: &types.CustomAgent{ID: "own-agent", TenantID: 7}},
	}
	uploadTemporaryDocument(t, h, map[string]string{"agent_id": "own-agent"})
	require.Empty(t, docs.options.ParserEngine,
		"tenant rules must be resolved by the worker, not pre-empted by the deployment default")
}

// An agent's own rule still decides the engine at upload time.
func TestUploadTemporaryDocumentUsesExplicitAgentRule(t *testing.T) {
	types.SetDeploymentParserDefaults("mineru", []string{"pdf"}, nil)
	t.Cleanup(func() { types.SetDeploymentParserDefaults("", nil, nil) })

	docs := &engineTestDocuments{}
	h := &Handler{
		sessionService:     engineTestSessions{},
		temporaryDocuments: docs,
		customAgentService: &resolveOwnAgentStub{agent: &types.CustomAgent{
			ID: "own-agent", TenantID: 7,
			Config: types.CustomAgentConfig{
				ChatParserEngineRules: []types.ParserEngineRule{{FileTypes: []string{"pdf"}, Engine: "paddleocr_vl"}},
			},
		}},
	}
	uploadTemporaryDocument(t, h, map[string]string{"agent_id": "own-agent"})
	require.Equal(t, "paddleocr_vl", docs.options.ParserEngine)
}
