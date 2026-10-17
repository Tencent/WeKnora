package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// videoBytes stands in for a real mp4 payload: enough to prove the bytes are
// saved verbatim, with no attempt to interpret them.
const videoBytes = "\x00\x00\x00\x18ftypmp42-training-video"

// The archive path is the only way a video becomes a knowledge row: the bytes
// are saved, the row is written as "stored", and — the part that keeps it out of
// retrieval — nothing is ever dispatched to parse or chunk it.
func TestCreateStoredKnowledgeFromFileArchivesVideoWithoutParsing(t *testing.T) {
	t.Parallel()

	repo := &createKnowledgeFileRepoStub{}
	fileSvc := &createKnowledgeFileServiceStub{}
	task := &createKnowledgeTaskEnqueuerStub{}
	svc := &knowledgeService{
		repo:      repo,
		kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{ID: "kb-1"}},
		fileSvc:   fileSvc,
		task:      task,
	}

	knowledge, err := svc.CreateStoredKnowledgeFromFile(
		newCreateKnowledgeFileContext(),
		"kb-1",
		newMultipartFileHeader(t, "Lesson.mp4", videoBytes),
		map[string]string{"channel": "dingtalk", "external_id": "node-1"},
		"",
		nil,
		types.ChannelDingtalk,
	)

	require.NoError(t, err)
	require.NotNil(t, knowledge)
	require.Equal(t, types.ParseStatusStored, knowledge.ParseStatus)
	require.Equal(t, "mp4", knowledge.FileType)
	require.Equal(t, "Lesson.mp4", knowledge.FileName)
	require.Equal(t, "disabled", knowledge.EnableStatus,
		"an archived file must not claim to be searchable")

	// The file is the deliverable: saved exactly once, row path points at it.
	require.Equal(t, 1, fileSvc.saveCalls)
	require.Equal(t, knowledge.ID, fileSvc.savedWithKnowledgeID)
	require.Equal(t, 1, repo.createCalls)
	require.NotNil(t, repo.createdKnowledge)
	require.Equal(t, "stored/"+knowledge.ID, repo.createdKnowledge.FilePath)

	// No parse task: this is what leaves the entry with zero chunks, and no
	// chunk is what keeps it out of retrieval results.
	require.Zero(t, task.calls, "a stored entry must not dispatch a parse task")
}

// The plain upload path keeps rejecting videos. This is the guard that stops the
// archive capability from becoming a general "upload anything" hole.
func TestCreateKnowledgeFromFileStillRejectsVideoUploads(t *testing.T) {
	t.Parallel()

	repo := &createKnowledgeFileRepoStub{}
	fileSvc := &createKnowledgeFileServiceStub{}
	task := &createKnowledgeTaskEnqueuerStub{}
	svc := &knowledgeService{
		repo:      repo,
		kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{ID: "kb-1"}},
		fileSvc:   fileSvc,
		task:      task,
	}

	knowledge, err := svc.CreateKnowledgeFromFile(
		newCreateKnowledgeFileContext(),
		"kb-1",
		newMultipartFileHeader(t, "Lesson.mp4", videoBytes),
		nil,
		nil,
		"",
		nil,
		"",
		nil,
	)

	require.Error(t, err)
	require.Contains(t, err.Error(), "暂不支持上传视频文件")
	require.Nil(t, knowledge)
	require.Zero(t, fileSvc.saveCalls)
	require.Zero(t, repo.createCalls)
	require.Zero(t, task.calls)
}

// Metadata travels through the public upload form, so it must never be able to
// ask for an archive. Only the dedicated service entry point can, and it is not
// reachable from an HTTP request.
func TestVideoUploadCannotForgeTheStoreOnlyFlagThroughMetadata(t *testing.T) {
	t.Parallel()

	repo := &createKnowledgeFileRepoStub{}
	fileSvc := &createKnowledgeFileServiceStub{}
	task := &createKnowledgeTaskEnqueuerStub{}
	svc := &knowledgeService{
		repo:      repo,
		kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{ID: "kb-1"}},
		fileSvc:   fileSvc,
		task:      task,
	}

	_, err := svc.CreateKnowledgeFromFile(
		newCreateKnowledgeFileContext(),
		"kb-1",
		newMultipartFileHeader(t, "Lesson.mp4", videoBytes),
		map[string]string{
			"store_without_indexing": "true",
			"store_unindexed_media":  "true",
			"parse_status":           types.ParseStatusStored,
		},
		nil,
		"",
		nil,
		"",
		nil,
	)

	require.Error(t, err)
	require.Contains(t, err.Error(), "暂不支持上传视频文件")
	require.Zero(t, fileSvc.saveCalls)
	require.Zero(t, repo.createCalls)
	require.Zero(t, task.calls)
}

// The archive path only accepts formats it can legitimately archive. A caller
// asking for a parseable document to be stored unindexed gets a clear refusal
// rather than a silently unsearchable document.
func TestCreateStoredKnowledgeFromFileRejectsParseableTypes(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"report.pdf", "notes.md", "call.mp3", "sheet.xlsx"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			repo := &createKnowledgeFileRepoStub{}
			fileSvc := &createKnowledgeFileServiceStub{}
			task := &createKnowledgeTaskEnqueuerStub{}
			svc := &knowledgeService{
				repo:      repo,
				kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{ID: "kb-1"}},
				fileSvc:   fileSvc,
				task:      task,
			}

			knowledge, err := svc.CreateStoredKnowledgeFromFile(
				newCreateKnowledgeFileContext(),
				"kb-1",
				newMultipartFileHeader(t, name, "payload"),
				nil,
				"",
				nil,
				"",
			)

			require.Error(t, err)
			require.Contains(t, err.Error(), "不支持仅存档的文件类型")
			require.Nil(t, knowledge)
			require.Zero(t, fileSvc.saveCalls)
			require.Zero(t, repo.createCalls)
			require.Zero(t, task.calls)
		})
	}
}

// The admission list is exactly the video family. Audio is deliberately absent:
// it has a parse path of its own, so archiving it unindexed would be a
// regression, not a feature.
func TestStorableUnindexedFileTypesAreExactlyTheVideoFamily(t *testing.T) {
	t.Parallel()

	for _, fileType := range []string{"mp4", "MP4", "mov", "avi", "mkv", "webm", "wmv", "flv", "m4v"} {
		require.True(t, isStorableUnindexedFileType(fileType), "%s must be archivable", fileType)
	}
	for _, fileType := range []string{"mp3", "wav", "m4a", "flac", "ogg", "pdf", "docx", "md", "txt", "json", ""} {
		require.False(t, isStorableUnindexedFileType(fileType), "%s must not be archivable", fileType)
	}
}

// A stored entry is settled: re-parsing it would destroy nothing (it has no
// index) but would flip the row to "failed" through the video guard, with no
// way back. The API must refuse instead.
func TestReparseKnowledgeRefusesStoredArchives(t *testing.T) {
	t.Parallel()

	knowledge := &types.Knowledge{
		ID:              "knowledge-stored",
		TenantID:        7,
		KnowledgeBaseID: "kb-1",
		Type:            "file",
		FileType:        "mp4",
		FileName:        "Lesson.mp4",
		FilePath:        "stored/knowledge-stored",
		ParseStatus:     types.ParseStatusStored,
		EnableStatus:    "disabled",
	}
	repo := &reparseFailureKnowledgeRepo{knowledge: knowledge}
	task := &createKnowledgeTaskEnqueuerStub{}
	svc := &knowledgeService{
		repo:      repo,
		kbService: &reparseFailureKBService{kb: &types.KnowledgeBase{ID: "kb-1", TenantID: 7}},
		task:      task,
	}
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	ctx, grantErr := access.WithKBTaskWrite(ctx, &types.KnowledgeBase{ID: "kb-1", TenantID: 7}, 7)
	require.NoError(t, grantErr)

	got, err := svc.ReparseKnowledge(ctx, knowledge.ID, nil)

	require.Error(t, err)
	require.Nil(t, got)
	require.Contains(t, err.Error(), "不支持重新解析")
	require.Zero(t, task.calls, "a refused reparse must not enqueue anything")
	require.Zero(t, repo.updateCalls, "a refused reparse must not rewrite the row")
	require.Equal(t, types.ParseStatusStored, knowledge.ParseStatus)
}
