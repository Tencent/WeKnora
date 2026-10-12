package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sessionDocument(id, fileType, status string, tokens int, content string) *types.TemporaryDocument {
	return &types.TemporaryDocument{
		ID: id, TenantID: 7, SessionID: "session-1", ResourceRef: "local://tenant/" + id,
		FileName: id + fileType, FileType: fileType, FileSize: 10, Status: status,
		Content: content, TokenCount: tokens, ChunkCount: 3, ExpiresAt: time.Now().Add(time.Hour),
	}
}

func TestDescribeForAgentInlinesOnlySmallReadyDocuments(t *testing.T) {
	small := sessionDocument("small", ".md", types.TemporaryDocumentStatusReady, 300, "# Title\nsmall body")
	large := sessionDocument("large", ".pdf", types.TemporaryDocumentStatusReady, 50000, "# Part 1\nlong\n## Part 2\nlonger")
	parsing := sessionDocument("parsing", ".pdf", types.TemporaryDocumentStatusProcessing, 0, "")
	svc := &temporaryDocumentService{
		repo: &forkAccessDocRepo{docs: []*types.TemporaryDocument{small, large, parsing}},
	}

	got, err := svc.DescribeForAgent(context.Background(), 7, "session-1", []string{"small", "large", "parsing"})

	require.NoError(t, err)
	require.Len(t, got.Attachments, 3)
	byID := map[string]types.MessageAttachment{}
	for _, attachment := range got.Attachments {
		byID[attachment.ID] = attachment
	}

	assert.Equal(t, types.AttachmentContentModeFull, byID["small"].ContentMode)
	assert.Equal(t, "# Title\nsmall body", byID["small"].Content)

	assert.Equal(t, types.AttachmentContentModeOnDemand, byID["large"].ContentMode)
	assert.Empty(t, byID["large"].Content)
	assert.Equal(t, "# Part 1\n## Part 2", byID["large"].Outline)
	assert.Equal(t, 50000, byID["large"].TokenCount)

	assert.Equal(t, types.TemporaryDocumentStatusProcessing, byID["parsing"].Status)
	assert.Equal(t, types.AttachmentContentModeOnDemand, byID["parsing"].ContentMode)
	assert.Empty(t, byID["parsing"].Content)
}

func TestDescribeForAgentCapsTotalInlineTokens(t *testing.T) {
	var docs []*types.TemporaryDocument
	var ids []string
	for _, id := range []string{"a", "b", "c", "d"} {
		docs = append(docs, sessionDocument(id, ".md", types.TemporaryDocumentStatusReady,
			types.AgentAttachmentInlineTokens, "body "+id))
		ids = append(ids, id)
	}
	svc := &temporaryDocumentService{repo: &forkAccessDocRepo{docs: docs}}

	got, err := svc.DescribeForAgent(context.Background(), 7, "session-1", ids)

	require.NoError(t, err)
	inlined := 0
	for _, attachment := range got.Attachments {
		if attachment.Content != "" {
			inlined++
		}
	}
	assert.Equal(t, agentAttachmentInlineBudget/types.AgentAttachmentInlineTokens, inlined)
}

func TestTemporaryDocumentOutlineIsBounded(t *testing.T) {
	var b strings.Builder
	for i := 0; i < temporaryDocumentOutlineMaxHeadings+5; i++ {
		b.WriteString("## heading\nbody\n")
	}
	outline := temporaryDocumentOutline(b.String())

	lines := strings.Split(outline, "\n")
	assert.Len(t, lines, temporaryDocumentOutlineMaxHeadings+1)
	assert.Equal(t, "…", lines[len(lines)-1])
	assert.Empty(t, temporaryDocumentOutline("no headings here\n#hashtag"))
}

func TestStageSessionAttachmentsWritesParsedTextBesideBinary(t *testing.T) {
	db := stagingTempDocDB(t)
	resourceRef := "local://tenant/attachment-1"
	require.NoError(t, db.Create(&types.TemporaryDocument{
		ID: "doc-1", TenantID: 7, SessionID: "session-1", ResourceRef: resourceRef,
		FileName: "report.pdf", FileType: ".pdf", FileSize: 7,
		Status: types.TemporaryDocumentStatusReady, Content: "# Report\nparsed text",
		ExpiresAt: time.Now().Add(time.Hour),
	}).Error)
	manager := &stagingSandboxManager{sandboxType: sandbox.SandboxTypeCube}
	service := &agentService{
		db:              db,
		sandboxMgr:      manager,
		fileService:     &stagingFileService{files: map[string][]byte{resourceRef: []byte("%PDF-17")}},
		sandboxResolver: stubSandboxResolver{mgr: manager},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	layout := sandbox.RemoteWorkspaceLayout()
	attachments := types.MessageAttachments{{ID: "doc-1", FileName: "report.pdf", FileType: ".pdf", FileSize: 7}}

	staged, err := service.stageSessionAttachments(ctx, "session-1", "cfg-remote", 7, attachments, layout)

	require.NoError(t, err)
	require.Len(t, staged, 1)
	require.Equal(t, staged[0].Path+parsedSidecarSuffix, staged[0].ParsedPath)
	assert.Equal(t, "# Report\nparsed text", string(manager.files[staged[0].ParsedPath]))
	assert.Contains(t, buildSandboxAttachmentsPrompt(staged, layout), `parsed_text_path="`+staged[0].ParsedPath+`"`)

	// A second pass keeps the sidecar instead of deleting it as stale.
	manager.writes = nil
	_, err = service.stageSessionAttachments(ctx, "session-1", "cfg-remote", 7, attachments, layout)
	require.NoError(t, err)
	assert.Empty(t, manager.writes)
	assert.Empty(t, manager.removes)
}

func TestStageSessionAttachmentsSkipsParsedTextForPlainText(t *testing.T) {
	db := stagingTempDocDB(t)
	resourceRef := "local://tenant/notes"
	require.NoError(t, db.Create(&types.TemporaryDocument{
		ID: "doc-1", TenantID: 7, SessionID: "session-1", ResourceRef: resourceRef,
		FileName: "notes.md", FileType: ".md", FileSize: 5,
		Status: types.TemporaryDocumentStatusReady, Content: "notes", ExpiresAt: time.Now().Add(time.Hour),
	}).Error)
	manager := &stagingSandboxManager{sandboxType: sandbox.SandboxTypeCube}
	service := &agentService{
		db:              db,
		sandboxMgr:      manager,
		fileService:     &stagingFileService{files: map[string][]byte{resourceRef: []byte("notes")}},
		sandboxResolver: stubSandboxResolver{mgr: manager},
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	staged, err := service.stageSessionAttachments(ctx, "session-1", "cfg-remote", 7,
		types.MessageAttachments{{ID: "doc-1", FileName: "notes.md", FileType: ".md", FileSize: 5}},
		sandbox.RemoteWorkspaceLayout())

	require.NoError(t, err)
	require.Len(t, staged, 1)
	assert.Empty(t, staged[0].ParsedPath)
}

func TestRegisterAttachmentReaderFollowsSessionAttachments(t *testing.T) {
	db := stagingTempDocDB(t)
	require.NoError(t, db.AutoMigrate(&types.Message{}))
	service := &agentService{db: db}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	config := &types.AgentConfig{}

	empty := tools.NewToolRegistry()
	service.registerAttachmentReader(ctx, empty, config, "session-1")
	_, err := empty.GetTool(tools.ToolReadAttachment)
	assert.Error(t, err, "a session without stored attachments must not offer read_attachment")

	require.NoError(t, db.Create(&types.Message{
		ID: "m1", SessionID: "session-1", Role: "user", Content: "see file",
		Attachments: types.MessageAttachments{{ID: "doc-1", FileName: "report.pdf"}},
	}).Error)
	registry := tools.NewToolRegistry()
	service.registerAttachmentReader(ctx, registry, config, "session-1")
	_, err = registry.GetTool(tools.ToolReadAttachment)
	assert.NoError(t, err)
}
