package service

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/infrastructure/docparser"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
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
	large := sessionDocument("large", ".pdf", types.TemporaryDocumentStatusReady, 50000,
		"# Part 1\nlong\n## Part 2\nlonger")
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
	// Normal sends persist the storage URL before the agent stages inputs.
	attachments := types.MessageAttachments{{
		ID: "doc-1", URL: resourceRef, FileName: "report.pdf",
		FileType: ".pdf", FileSize: 7,
	}}

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

func TestSentAttachmentRemainsReadableAfterExpiryAndFork(t *testing.T) {
	db := stagingTempDocDB(t)
	require.NoError(t, db.AutoMigrate(&types.Session{}, &types.Message{}))
	parent := &types.Session{TenantID: 7}
	fork := &types.Session{TenantID: 7}
	require.NoError(t, db.Create(parent).Error)
	require.NoError(t, db.Create(fork).Error)
	document := sessionDocument("retained", ".pdf", types.TemporaryDocumentStatusReady, 50000, "complete retained body")
	document.SessionID = parent.ID
	document.ExpiresAt = time.Now().Add(-time.Hour)
	document.ImageRefs = types.JSON(`[{"url":"/files/retained-chart.png"}]`)
	require.NoError(t, db.Create(document).Error)
	unused := sessionDocument("unused", ".pdf", types.TemporaryDocumentStatusReady, 50000, "unused body")
	unused.SessionID, unused.ExpiresAt = parent.ID, document.ExpiresAt
	require.NoError(t, db.Create(unused).Error)
	repo := repository.NewTemporaryDocumentRepository(db)
	messages := repository.NewMessageRepository(db)
	svc := &temporaryDocumentService{repo: repo, fileService: &stagingFileService{}, sessionAttachments: messages}
	manifest, err := svc.DescribeForAgent(context.Background(), 7, parent.ID, []string{document.ID})
	require.NoError(t, err)
	require.Empty(t, manifest.Attachments[0].Content)
	// Forks copy the attachment manifest, not the parent's temporary document.
	for _, session := range []*types.Session{parent, fork} {
		require.NoError(t, db.Create(&types.Message{
			SessionID: session.ID, Role: "user", Attachments: manifest.Attachments,
		}).Error)
	}
	require.NoError(t, db.Delete(parent).Error)
	require.NoError(t, svc.CleanupExpired(context.Background()))
	removed, err := repo.GetByID(context.Background(), 7, unused.ID)
	require.NoError(t, err)
	require.Nil(t, removed, "unused uploads must still expire")
	store := &agentSessionAttachments{messages: messages, documents: repo, tenantID: 7, sessionID: fork.ID}
	reader := tools.NewReadAttachmentTool(store, 0)
	args, err := json.Marshal(tools.ReadAttachmentInput{Attachment: types.AttachmentHandle(document.ID)})
	require.NoError(t, err)
	result, err := reader.Execute(context.Background(), args)
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	assert.Contains(t, result.Output, document.Content)
	retained, err := repo.GetByID(context.Background(), 7, document.ID)
	require.NoError(t, err)
	require.NotNil(t, retained)
	assert.JSONEq(t, string(document.ImageRefs), string(retained.ImageRefs))
	// After the final live conversation is deleted the same cleanup reclaims it.
	require.NoError(t, db.Delete(fork).Error)
	require.NoError(t, svc.CleanupExpired(context.Background()))
	removed, err = repo.GetByID(context.Background(), 7, document.ID)
	require.NoError(t, err)
	assert.Nil(t, removed)
}

func TestAgentAttachmentParseWaitConfiguration(t *testing.T) {
	t.Setenv("WEKNORA_CHAT_ATTACHMENT_WAIT_TIMEOUT_SEC", "90")
	assert.Equal(t, 90*time.Second, agentAttachmentParseWait(&types.AgentConfig{}))
	assert.Equal(t, 120*time.Second, agentAttachmentParseWait(&types.AgentConfig{AttachmentParseWaitTimeoutSec: 120}))
	t.Setenv("WEKNORA_CHAT_ATTACHMENT_WAIT_TIMEOUT_SEC", "invalid")
	assert.Equal(t, defaultAgentAttachmentParseWait, agentAttachmentParseWait(nil))
}

func TestAttachmentImageDeliveryDoesNotDependOnQuestionKeywords(t *testing.T) {
	document := sessionDocument("report", ".pdf", types.TemporaryDocumentStatusReady, 50000, "# Report\ntext")
	document.ImageRefs = types.JSON(`[{"url":"local://chart.png"},{"url":"local://page.png"}]`)
	svc := &temporaryDocumentService{repo: &forkAccessDocRepo{docs: []*types.TemporaryDocument{document}}}
	for _, query := range []string{"解释图表", "summarize", "この資料を説明して", ""} {
		got, err := svc.ResolveForPrompt(context.Background(), 7, "session-1", []string{document.ID}, query)
		require.NoError(t, err)
		assert.Empty(t, got.ImageURLs, "Quick Answer must not automatically attach document figures")
		assert.NotEmpty(t, got.Attachments[0].Content)
	}
	manifest, err := svc.DescribeForAgent(context.Background(), 7, "session-1", []string{document.ID})
	require.NoError(t, err)
	assert.Empty(t, manifest.ImageURLs, "agent document images are requested through read_attachment")
	assert.Empty(t, manifest.Attachments[0].Content)
	document.FileType = ".png"
	for _, query := range []string{"解释图表", "summarize", "この資料を説明して", ""} {
		got, err := svc.ResolveForPrompt(context.Background(), 7, "session-1", []string{document.ID}, query)
		require.NoError(t, err)
		assert.Equal(t, []string{"local://chart.png", "local://page.png"}, got.ImageURLs)
	}
	manifest, err = svc.DescribeForAgent(context.Background(), 7, "session-1", []string{document.ID})
	require.NoError(t, err)
	assert.Len(t, manifest.ImageURLs, 2, "standalone images still enter the initial vision input")
}

type attachmentStorageFiles struct {
	stagingFileService
	temporary  []bool
	readTenant uint64
}

func (f *attachmentStorageFiles) SaveBytes(
	_ context.Context, data []byte, _ uint64, name string, temporary bool,
) (string, error) {
	f.temporary = append(f.temporary, temporary)
	ref := "local://" + name
	f.files[ref] = append([]byte(nil), data...)
	return ref, nil
}

func (f *attachmentStorageFiles) GetFile(ctx context.Context, ref string) (io.ReadCloser, error) {
	f.readTenant, _ = types.TenantIDFromContext(ctx)
	return f.stagingFileService.GetFile(ctx, ref)
}

type attachmentDocumentReader struct {
	interfaces.DocumentReader
	result *types.ReadResult
}

func (r *attachmentDocumentReader) Read(context.Context, *types.ReadRequest) (*types.ReadResult, error) {
	return r.result, nil
}

func TestAttachmentSourceAndImagesAvoidProviderExpiry(t *testing.T) {
	db := stagingTempDocDB(t)
	files := &attachmentStorageFiles{stagingFileService: stagingFileService{files: map[string][]byte{}}}
	var pngData bytes.Buffer
	require.NoError(t, png.Encode(&pngData, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	svc := &temporaryDocumentService{
		repo: repository.NewTemporaryDocumentRepository(db), fileService: files,
		taskEnqueuer: &createKnowledgeTaskEnqueuerStub{}, imageResolver: docparser.NewImageResolver(),
		documentReader: &attachmentDocumentReader{result: &types.ReadResult{
			MarkdownContent: "![chart](chart.png)",
			ImageRefs: []types.ImageRef{{
				OriginalRef: "chart.png", ImageData: pngData.Bytes(), IsOriginal: true, MimeType: "image/png",
			}},
		}},
	}
	document, err := svc.Create(context.Background(), 7, "session", "report.pdf", "application/pdf", 4,
		strings.NewReader("data"), types.TemporaryDocumentCreateOptions{})
	require.NoError(t, err)
	_, images, _, err := svc.parse(context.Background(), document)
	require.NoError(t, err)
	require.Len(t, images, 1)
	assert.Equal(t, []bool{false, false}, files.temporary, "retained files must not use provider TTL")
	store := &agentSessionAttachments{files: files, tenantID: 7}
	sharedAgentCtx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(99))
	data, err := store.ReadImage(sharedAgentCtx, images[0].URL)
	require.NoError(t, err)
	assert.Equal(t, pngData.Bytes(), data)
	assert.Equal(t, uint64(7), files.readTenant, "image reads use the session owner, not the shared agent tenant")
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
