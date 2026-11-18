package session

import (
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/middleware"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

type stubKnowledgeServiceForArtifact struct {
	interfaces.KnowledgeService
	gotFileName string
	gotBytes    string
	returned    *types.Knowledge
	err         error
	calls       int
}

func (s *stubKnowledgeServiceForArtifact) CreateKnowledgeFromFile(
	_ context.Context,
	_ string,
	file *multipart.FileHeader,
	_ map[string]string,
	_ *bool,
	_ string,
	_ []string,
	_ string,
	_ *types.KnowledgeProcessOverrides,
) (*types.Knowledge, error) {
	s.calls++
	s.gotFileName = file.Filename
	opened, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer opened.Close()
	data, _ := io.ReadAll(opened)
	s.gotBytes = string(data)
	return s.returned, s.err
}

type stubKnowledgeBaseServiceForArtifact struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s *stubKnowledgeBaseServiceForArtifact) GetKnowledgeBaseByID(
	_ context.Context, _ string,
) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

func artifactKnowledgeTestRouter(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.ErrorHandler(), func(c *gin.Context) {
		ctx := c.Request.Context()
		ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(42))
		ctx = context.WithValue(ctx, types.UserIDContextKey, "user-1")
		ctx = types.WithCaller(ctx, types.Caller{
			TenantID: 42, UserID: "user-1", Role: types.TenantRoleContributor,
		})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	r.POST("/sessions/:id/messages/:message_id/artifacts/:index/knowledge", h.AddMessageArtifactToKnowledge)
	return r
}

func TestAddMessageArtifactToKnowledgeHappyPath(t *testing.T) {
	const (
		sessionID = "sess-1"
		messageID = "msg-1"
		kbID      = "kb-1"
		fileName  = "skill-report.md"
		body      = "artifact body"
	)
	fileSvc := &fakeArtifactFileService{url: "tenant-42/exports/" + fileName, data: []byte(body)}
	kgSvc := &stubKnowledgeServiceForArtifact{
		returned: &types.Knowledge{ID: "knowledge-1", FileName: fileName},
	}
	h := &Handler{
		sessionService: &stubSessionServiceForArtifacts{
			getSession: func(context.Context, string) (*types.Session, error) {
				return &types.Session{ID: sessionID, TenantID: 42, UserID: "user-1"}, nil
			},
		},
		messageService: &stubMessageServiceForArtifacts{
			getMessage: func(context.Context, string, string) (*types.Message, error) {
				return &types.Message{
					ID:        messageID,
					SessionID: sessionID,
					Artifacts: types.MessageArtifacts{{
						URL:      "resource://artifact-1",
						FileName: fileName,
						FileSize: int64(len(body)),
					}},
				}, nil
			},
		},
		knowledgebaseService: &stubKnowledgeBaseServiceForArtifact{
			kb: &types.KnowledgeBase{ID: kbID, TenantID: 42, Name: "Docs"},
		},
		knowledgeService: kgSvc,
		fileService:      fileSvc,
		resourceCatalog: &artifactCatalogStub{
			resource: &types.StoredResource{
				PhysicalPath: "tenant-42/exports/" + fileName,
				TenantID:     42,
			},
		},
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/sessions/"+sessionID+"/messages/"+messageID+"/artifacts/0/knowledge",
		strings.NewReader(`{"knowledge_base_id":"`+kbID+`"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	artifactKnowledgeTestRouter(h).ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if kgSvc.calls != 1 {
		t.Fatalf("CreateKnowledgeFromFile calls = %d, want 1", kgSvc.calls)
	}
	if kgSvc.gotFileName != fileName || kgSvc.gotBytes != body {
		t.Fatalf("imported file = %q/%q, want %q/%q", kgSvc.gotFileName, kgSvc.gotBytes, fileName, body)
	}
	if !strings.Contains(recorder.Body.String(), `"knowledge_id"`) &&
		!strings.Contains(recorder.Body.String(), `"knowledge-1"`) {
		t.Fatalf("response missing knowledge id: %s", recorder.Body.String())
	}
}

func TestAddMessageArtifactToKnowledgeRejectsDeletedArtifact(t *testing.T) {
	now := time.Now()
	h := &Handler{
		sessionService: &stubSessionServiceForArtifacts{
			getSession: func(context.Context, string) (*types.Session, error) {
				return &types.Session{ID: "sess-1", TenantID: 42}, nil
			},
		},
		messageService: &stubMessageServiceForArtifacts{
			getMessage: func(context.Context, string, string) (*types.Message, error) {
				return &types.Message{
					Artifacts: types.MessageArtifacts{{
						FileName: "gone.md", URL: "resource://gone", DeletedAt: &now,
					}},
				}, nil
			},
		},
	}
	req := httptest.NewRequest(
		http.MethodPost,
		"/sessions/sess-1/messages/msg-1/artifacts/0/knowledge",
		strings.NewReader(`{"knowledge_base_id":"kb-1"}`),
	)
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	artifactKnowledgeTestRouter(h).ServeHTTP(recorder, req)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
	}
}
