package service

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	chatExcelID = "11111111-1111-4111-8111-111111111111"
	chatPDFID   = "22222222-2222-4222-8222-222222222222"
)

func TestMergeChatUploadsPrefersCurrentURL(t *testing.T) {
	merged := mergeChatUploads(
		types.MessageAttachments{{
			ID: "a", URL: "current://a.csv", FileName: "a.csv", FileType: ".csv",
		}},
		types.MessageAttachments{
			{ID: "a", FileName: "old.csv", FileType: ".csv"},
			{ID: " b ", FileName: "b.xlsx", FileType: ".xlsx"},
			{ID: "", FileName: "skip.csv", FileType: ".csv"},
		},
	)
	require.Len(t, merged, 2)
	assert.Equal(t, "current://a.csv", merged[0].URL)
	assert.Equal(t, "a.csv", merged[0].FileName)
	assert.Equal(t, "b", merged[1].ID)
	assert.Empty(t, merged[1].URL)
}

func TestRegisterToolsKeepsDataToolsForChatUploadWithoutKnowledgeBase(t *testing.T) {
	svc := &agentService{}
	cfg := &types.AgentConfig{
		AllowedTools: []string{tools.ToolDataAnalysis, tools.ToolDataSchema, tools.ToolSearchKnowledge},
		ChatUploads: types.MessageAttachments{{
			ID: chatExcelID, FileName: "sales.xlsx", FileType: ".xlsx",
		}},
	}
	registry := tools.NewToolRegistry()
	require.NoError(t, svc.registerTools(t.Context(), registry, cfg, nil, nil, "sess-1"))
	names := registry.ListTools()
	require.Contains(t, names, tools.ToolDataAnalysis)
	require.Contains(t, names, tools.ToolDataSchema)
	require.NotContains(t, names, tools.ToolSearchKnowledge)

	analysis, err := registry.GetTool(tools.ToolDataAnalysis)
	require.NoError(t, err)
	schema, err := registry.GetTool(tools.ToolDataSchema)
	require.NoError(t, err)
	analysisStore := analysis.(*tools.DataAnalysisTool).ChatAttachments()
	schemaStore := schema.(*tools.DataSchemaTool).ChatAttachments()
	require.NotNil(t, analysisStore)
	assert.Same(t, analysisStore, schemaStore)
}

func TestRegisterToolsDropsDataToolsWithoutKnowledgeOrChatUpload(t *testing.T) {
	svc := &agentService{}
	cfg := &types.AgentConfig{
		AllowedTools: []string{tools.ToolDataAnalysis, tools.ToolDataSchema},
	}
	registry := tools.NewToolRegistry()
	require.NoError(t, svc.registerTools(t.Context(), registry, cfg, nil, nil, "sess-1"))
	names := registry.ListTools()
	assert.NotContains(t, names, tools.ToolDataAnalysis)
	assert.NotContains(t, names, tools.ToolDataSchema)
}

func TestPrepareChatTabularAttachmentsUsesCallerURLWithoutDatabase(t *testing.T) {
	files := &stagingFileService{files: map[string][]byte{
		"local://sales.xlsx": []byte("xlsx-bytes"),
		"local://manual.pdf": []byte("%PDF"),
	}}
	svc := &agentService{fileService: files}
	items := svc.prepareChatTabularAttachments(context.Background(), &types.AgentConfig{
		ChatUploads: types.MessageAttachments{
			{ID: chatExcelID, FileName: "sales.xlsx", FileType: ".XLSX", URL: "local://sales.xlsx"},
			{ID: chatPDFID, FileName: "manual.pdf", FileType: "pdf", URL: "local://manual.pdf"},
		},
	}, "sess-1")
	require.Len(t, items, 1)
	assert.Equal(t, chatExcelID, items[0].ID)
	assert.Equal(t, "xlsx", items[0].FileType)
	assert.Equal(t, "sales.xlsx", items[0].FileName)
	body := readChatUpload(t, items[0])
	assert.Equal(t, "xlsx-bytes", body)
	assert.Equal(t, 1, files.getCalls["local://sales.xlsx"])
	assert.Zero(t, files.getCalls["local://manual.pdf"])
}

func TestPrepareChatTabularAttachmentsReadsSameSessionRow(t *testing.T) {
	db := stagingTempDocDB(t)
	require.NoError(t, db.Create(&types.TemporaryDocument{
		ID:          chatExcelID,
		TenantID:    7,
		SessionID:   "sess-1",
		ResourceRef: "local://row.xlsx",
		FileName:    "from-row.xlsx",
		FileType:    ".xlsx",
		FileSize:    4,
		Status:      types.TemporaryDocumentStatusReady,
		ExpiresAt:   time.Now().Add(time.Hour),
	}).Error)
	files := &stagingFileService{files: map[string][]byte{
		"local://row.xlsx":     []byte("from-row"),
		"caller://poison.xlsx": []byte("poison"),
	}}
	svc := &agentService{db: db, fileService: files}
	items := svc.prepareChatTabularAttachments(context.Background(), &types.AgentConfig{
		ChatUploadTenantID: 7,
		ChatUploads: types.MessageAttachments{{
			ID: chatExcelID, FileName: "caller.xlsx", FileType: ".csv", URL: "caller://poison.xlsx",
		}},
	}, "sess-1")
	require.Len(t, items, 1)
	assert.Equal(t, "xlsx", items[0].FileType, "the temporary-document row wins over the caller file type")
	assert.Equal(t, "from-row.xlsx", items[0].FileName)
	assert.Equal(t, "from-row", readChatUpload(t, items[0]))
	assert.Zero(t, files.getCalls["caller://poison.xlsx"])
}

func TestPrepareChatTabularAttachmentsDeniesOtherSessionAndMissingRow(t *testing.T) {
	db := stagingTempDocDB(t)
	require.NoError(t, db.Create(&types.TemporaryDocument{
		ID:          chatExcelID,
		TenantID:    7,
		SessionID:   "other-session",
		ResourceRef: "local://row.xlsx",
		FileName:    "secret.xlsx",
		FileType:    ".xlsx",
		FileSize:    4,
		Status:      types.TemporaryDocumentStatusReady,
		ExpiresAt:   time.Now().Add(time.Hour),
	}).Error)
	files := &stagingFileService{files: map[string][]byte{
		"local://row.xlsx":     []byte("secret"),
		"caller://poison.xlsx": []byte("poison"),
	}}
	svc := &agentService{db: db, fileService: files}
	ctx := context.Background()

	otherSession := svc.prepareChatTabularAttachments(ctx, &types.AgentConfig{
		ChatUploadTenantID: 7,
		ChatUploads: types.MessageAttachments{{
			ID: chatExcelID, FileName: "caller.xlsx", FileType: ".xlsx", URL: "caller://poison.xlsx",
		}},
	}, "sess-1")
	assert.Empty(t, otherSession, "a fork reference check that cannot be proven must fail closed")

	missing := svc.prepareChatTabularAttachments(ctx, &types.AgentConfig{
		ChatUploadTenantID: 7,
		ChatUploads: types.MessageAttachments{{
			ID: chatPDFID, FileName: "missing.xlsx", FileType: ".xlsx", URL: "caller://poison.xlsx",
		}},
	}, "sess-1")
	assert.Empty(t, missing)

	wrongTenant := svc.prepareChatTabularAttachments(ctx, &types.AgentConfig{
		ChatUploadTenantID: 8,
		ChatUploads: types.MessageAttachments{{
			ID: chatExcelID, FileName: "caller.xlsx", FileType: ".xlsx", URL: "caller://poison.xlsx",
		}},
	}, "other-session")
	assert.Empty(t, wrongTenant)
	assert.Zero(t, files.getCalls["local://row.xlsx"])
	assert.Zero(t, files.getCalls["caller://poison.xlsx"])
}

func TestPrepareChatTabularAttachmentsRejectsPDFRow(t *testing.T) {
	db := stagingTempDocDB(t)
	require.NoError(t, db.Create(&types.TemporaryDocument{
		ID:          chatPDFID,
		TenantID:    7,
		SessionID:   "sess-1",
		ResourceRef: "local://manual.pdf",
		FileName:    "manual.pdf",
		FileType:    ".pdf",
		FileSize:    4,
		Status:      types.TemporaryDocumentStatusReady,
		ExpiresAt:   time.Now().Add(time.Hour),
	}).Error)
	files := &stagingFileService{files: map[string][]byte{"local://manual.pdf": []byte("%PDF")}}
	svc := &agentService{db: db, fileService: files}
	items := svc.prepareChatTabularAttachments(context.Background(), &types.AgentConfig{
		ChatUploadTenantID: 7,
		ChatUploads: types.MessageAttachments{{
			ID: chatPDFID, FileName: "renamed.xlsx", FileType: ".xlsx", URL: "caller://poison.xlsx",
		}},
	}, "sess-1")
	assert.Empty(t, items, "a PDF temporary document must not become a data-analysis file")
	assert.Zero(t, files.getCalls["local://manual.pdf"])
}

func TestSessionMessagesReferenceAttachmentRejectsUnsafeIDs(t *testing.T) {
	svc := &agentService{db: stagingTempDocDB(t)}
	for _, id := range []string{`bad"id`, `bad'id`, `bad\id`, "bad%id"} {
		assert.False(t, svc.sessionMessagesReferenceAttachment(context.Background(), "sess-1", id), id)
	}
}

func TestAttachChatUploadsFillsTheToolAllowlist(t *testing.T) {
	files := &stagingFileService{files: map[string][]byte{"local://sales.xlsx": []byte("xlsx")}}
	svc := &agentService{fileService: files}
	cfg := &types.AgentConfig{
		AllowedTools: []string{tools.ToolDataSchema, tools.ToolDataAnalysis},
		ChatUploads: types.MessageAttachments{{
			ID: chatExcelID, FileName: "sales.xlsx", FileType: ".xlsx", URL: "local://sales.xlsx",
		}},
	}
	registry := tools.NewToolRegistry()
	require.NoError(t, svc.registerTools(t.Context(), registry, cfg, nil, nil, "sess-1"))
	// The prompt assertion lives in the agent package. Here the service must
	// fill the allowlist the registered data tools already share.
	svc.attachChatUploads(context.Background(), &agent.AgentEngine{}, registry, cfg, "sess-1")

	tool, err := registry.GetTool(tools.ToolDataAnalysis)
	require.NoError(t, err)
	listed := tool.(*tools.DataAnalysisTool).ChatAttachments().List()
	require.Len(t, listed, 1)
	assert.Equal(t, chatExcelID, listed[0].ID)
	assert.Equal(t, "xlsx", listed[0].FileType)
	assert.Equal(t, "xlsx", readChatUpload(t, listed[0]))
}

func readChatUpload(t *testing.T, item tools.ChatTabularAttachment) string {
	t.Helper()
	reader, err := item.Open(context.Background())
	require.NoError(t, err)
	defer reader.Close()
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	return string(body)
}
