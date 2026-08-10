package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fileUpdateRepoStub struct {
	interfaces.KnowledgeRepository

	knowledge      *types.Knowledge
	listKnowledges []*types.Knowledge
	stageResult    *types.KnowledgeFileUpdateStageResult
	slot           *types.KnowledgeFileUpdateSlot
	stagedPayload  types.JSON
	stagedExpected *uint64
	stageCalls     int
	applyCalls     int
	applyValues    map[string]interface{}
	tags           map[string][]*types.KnowledgeTag
	tagsErr        error
}

func (r *fileUpdateRepoStub) TransitionKnowledgeFileUpdateState(
	_ context.Context, _ uint64, _ string, version uint64, fromState, toState, lastError string,
) (bool, error) {
	if r.slot == nil || r.slot.ActiveVersion == nil || *r.slot.ActiveVersion != version ||
		r.slot.ActiveState != fromState {
		return false, nil
	}
	r.slot.ActiveState = toState
	r.slot.LastError = lastError
	return true, nil
}

func (r *fileUpdateRepoStub) CancelFailedKnowledgeFileUpdate(
	_ context.Context, _ uint64, _ string, version uint64,
) (*types.KnowledgeFileUpdateSlot, error) {
	if r.slot == nil || r.slot.ActiveVersion == nil || *r.slot.ActiveVersion != version ||
		r.slot.ActiveState != types.KnowledgeFileUpdateStateFailed {
		return nil, repository.ErrKnowledgeFileUpdateStateConflict
	}
	cancelled := r.slot
	r.slot = nil
	return cancelled, nil
}

func (r *fileUpdateRepoStub) GetKnowledgeFileUpdateSlot(
	context.Context, uint64, string,
) (*types.KnowledgeFileUpdateSlot, error) {
	return r.slot, nil
}

func (r *fileUpdateRepoStub) GetKnowledgeByID(
	context.Context, uint64, string,
) (*types.Knowledge, error) {
	copy := *r.knowledge
	return &copy, nil
}

func (r *fileUpdateRepoStub) ListKnowledgeByKnowledgeBaseID(
	context.Context, uint64, string,
) ([]*types.Knowledge, error) {
	return r.listKnowledges, nil
}

func (r *fileUpdateRepoStub) CheckKnowledgeExistsExcluding(
	context.Context, uint64, string, string, *types.KnowledgeCheckParams,
) (bool, *types.Knowledge, error) {
	return false, nil, nil
}

func (r *fileUpdateRepoStub) StageKnowledgeFileUpdate(
	_ context.Context,
	_ uint64,
	_ string,
	_ string,
	payload types.JSON,
	expectedVersion *uint64,
) (*types.KnowledgeFileUpdateStageResult, error) {
	r.stageCalls++
	r.stagedPayload = append(types.JSON(nil), payload...)
	r.stagedExpected = expectedVersion
	return r.stageResult, nil
}

func (r *fileUpdateRepoStub) UpdateApplyingKnowledgeFileColumns(
	_ context.Context,
	_ uint64,
	knowledgeID string,
	kbID string,
	expectedFilePath string,
	expectedFileHash string,
	values map[string]interface{},
) (bool, error) {
	r.applyCalls++
	r.applyValues = values
	if r.knowledge == nil ||
		r.knowledge.ID != knowledgeID ||
		r.knowledge.KnowledgeBaseID != kbID ||
		r.knowledge.ParseStatus != types.ParseStatusReplacing ||
		r.knowledge.FilePath != expectedFilePath ||
		r.knowledge.FileHash != expectedFileHash {
		return false, nil
	}
	if status, ok := values["parse_status"].(string); ok {
		r.knowledge.ParseStatus = status
	}
	if message, ok := values["error_message"].(string); ok {
		r.knowledge.ErrorMessage = message
	}
	return true, nil
}

func (r *fileUpdateRepoStub) GetKnowledgeTags(
	context.Context, []string,
) (map[string][]*types.KnowledgeTag, error) {
	if r.tagsErr != nil {
		return nil, r.tagsErr
	}
	if r.tags != nil {
		return r.tags, nil
	}
	return map[string][]*types.KnowledgeTag{}, nil
}

type fileUpdateTaskStub struct {
	tasks []*asynq.Task
}

func (s *fileUpdateTaskStub) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	s.tasks = append(s.tasks, task)
	return &asynq.TaskInfo{ID: "update-task", Queue: types.QueueMaintenance}, nil
}

func newFileUpdateService(
	repo *fileUpdateRepoStub, fileSvc interfaces.FileService, task interfaces.TaskEnqueuer,
) *knowledgeService {
	return &knowledgeService{
		repo: repo,
		kbService: &createKnowledgeFileKBServiceStub{kb: &types.KnowledgeBase{
			ID: repo.knowledge.KnowledgeBaseID,
		}},
		fileSvc: fileSvc,
		task:    task,
	}
}

func TestUpdateKnowledgeFileFirstVersionBecomesActive(t *testing.T) {
	repo := &fileUpdateRepoStub{
		knowledge: &types.Knowledge{
			ID: "knowledge-1", KnowledgeBaseID: "kb-1", Type: "file",
			FilePath: "old/path.md", FileName: "old.md", FileHash: "old-hash",
			ParseStatus: types.ParseStatusCompleted,
		},
		stageResult: &types.KnowledgeFileUpdateStageResult{
			Version: 1, State: types.KnowledgeFileUpdateResultActive, ActiveVersion: 1,
		},
	}
	fileSvc := &createKnowledgeFileServiceStub{}
	task := &fileUpdateTaskStub{}
	expected := uint64(0)

	result, err := newFileUpdateService(repo, fileSvc, task).UpdateKnowledgeFile(
		newCreateKnowledgeFileContext(),
		&types.KnowledgeFileUpdateRequest{
			KnowledgeBaseID: "kb-1", KnowledgeID: "knowledge-1",
			File:                  newMultipartFileHeader(t, "new.md", "new content"),
			ExpectedUpdateVersion: &expected,
		},
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, uint64(1), result.UpdateVersion)
	assert.Equal(t, types.KnowledgeFileUpdateResultActive, result.UpdateState)
	assert.Equal(t, types.ParseStatusCompleted, result.Knowledge.ParseStatus,
		"accepting an update must not interrupt the current parsed version")
	assert.Equal(t, &expected, repo.stagedExpected)
	require.Len(t, task.tasks, 1)
	assert.Equal(t, types.TypeKnowledgeFileUpdate, task.tasks[0].Type())

	var staged types.KnowledgeFileUpdatePayload
	require.NoError(t, json.Unmarshal(repo.stagedPayload, &staged))
	assert.Equal(t, "new.md", staged.NewFileName)
	assert.Empty(t, staged.NewFolderPath)
	assert.NotEmpty(t, staged.NewFileHash)
	assert.Empty(t, staged.OldFilePath, "the coordinator captures the current source only when it is safe to apply")
}

func TestCreateOrUpdateKnowledgeFromFileMatchesUniqueFilename(t *testing.T) {
	existing := &types.Knowledge{
		ID: "knowledge-1", KnowledgeBaseID: "kb-1", Type: "file",
		FilePath: "old/path.md", FileName: "doc.md", FileHash: "old-hash",
		ParseStatus: types.ParseStatusCompleted,
	}
	repo := &fileUpdateRepoStub{
		knowledge:      existing,
		listKnowledges: []*types.Knowledge{existing},
		stageResult: &types.KnowledgeFileUpdateStageResult{
			Version: 1, State: types.KnowledgeFileUpdateResultActive, ActiveVersion: 1,
		},
	}
	fileSvc := &createKnowledgeFileServiceStub{}
	task := &fileUpdateTaskStub{}

	result, err := newFileUpdateService(repo, fileSvc, task).CreateOrUpdateKnowledgeFromFile(
		newCreateKnowledgeFileContext(),
		&types.KnowledgeFileCreateOrUpdateRequest{
			KnowledgeBaseID: "kb-1",
			File:            newMultipartFileHeader(t, "doc.md", "new content"),
		},
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "updated", result.Action)
	assert.Equal(t, uint64(1), result.UpdateVersion)
	assert.Equal(t, 1, repo.stageCalls)
	assert.Equal(t, "knowledge-1", fileSvc.savedWithKnowledgeID)
}

func TestCreateOrUpdateKnowledgeFromFileMatchesPathQualifiedUniqueFilename(t *testing.T) {
	existing := &types.Knowledge{
		ID: "knowledge-1", KnowledgeBaseID: "kb-1", Type: "file",
		FilePath: "old/path.md", FileName: "doc.md", FileHash: "old-hash",
		ParseStatus: types.ParseStatusCompleted,
	}
	repo := &fileUpdateRepoStub{
		knowledge:      existing,
		listKnowledges: []*types.Knowledge{existing},
		stageResult: &types.KnowledgeFileUpdateStageResult{
			Version: 1, State: types.KnowledgeFileUpdateResultActive, ActiveVersion: 1,
		},
	}
	fileSvc := &createKnowledgeFileServiceStub{}
	task := &fileUpdateTaskStub{}

	result, err := newFileUpdateService(repo, fileSvc, task).CreateOrUpdateKnowledgeFromFile(
		newCreateKnowledgeFileContext(),
		&types.KnowledgeFileCreateOrUpdateRequest{
			KnowledgeBaseID: "kb-1",
			File:            newMultipartFileHeader(t, "doc.md", "new content"),
			CustomFileName:  "docs/spec/doc.md",
		},
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "updated", result.Action)
	assert.Equal(t, 1, repo.stageCalls)
	var staged types.KnowledgeFileUpdatePayload
	require.NoError(t, json.Unmarshal(repo.stagedPayload, &staged))
	assert.Equal(t, "doc.md", staged.NewFileName)
	assert.Equal(t, "docs/spec", staged.NewFolderPath)
}

func TestCreateOrUpdateKnowledgeFromFileRejectsAmbiguousFilename(t *testing.T) {
	first := &types.Knowledge{
		ID: "knowledge-1", KnowledgeBaseID: "kb-1", Type: "file",
		FilePath: "old/one.md", FileName: "doc.md", FileHash: "one",
		ParseStatus: types.ParseStatusCompleted,
	}
	second := &types.Knowledge{
		ID: "knowledge-2", KnowledgeBaseID: "kb-1", Type: "file",
		FilePath: "old/two.md", FileName: "doc.md", FileHash: "two",
		ParseStatus: types.ParseStatusCompleted,
	}
	repo := &fileUpdateRepoStub{
		knowledge:      first,
		listKnowledges: []*types.Knowledge{first, second},
	}
	fileSvc := &createKnowledgeFileServiceStub{}
	task := &fileUpdateTaskStub{}

	result, err := newFileUpdateService(repo, fileSvc, task).CreateOrUpdateKnowledgeFromFile(
		newCreateKnowledgeFileContext(),
		&types.KnowledgeFileCreateOrUpdateRequest{
			KnowledgeBaseID: "kb-1",
			File:            newMultipartFileHeader(t, "doc.md", "new content"),
		},
	)

	require.Error(t, err)
	require.Nil(t, result)
	assert.Zero(t, repo.stageCalls)
	assert.Zero(t, fileSvc.saveCalls)
	assert.Empty(t, task.tasks)
}

func TestUpdateKnowledgeFilePathQualifiedNameStagesFolderMove(t *testing.T) {
	repo := &fileUpdateRepoStub{
		knowledge: &types.Knowledge{
			ID: "knowledge-1", KnowledgeBaseID: "kb-1", Type: "file",
			FilePath: "old/path.md", FileName: "old.md", FileHash: "old-hash",
			ParseStatus: types.ParseStatusCompleted,
		},
		stageResult: &types.KnowledgeFileUpdateStageResult{
			Version: 1, State: types.KnowledgeFileUpdateResultActive, ActiveVersion: 1,
		},
	}
	task := &fileUpdateTaskStub{}

	result, err := newFileUpdateService(repo, &createKnowledgeFileServiceStub{}, task).UpdateKnowledgeFile(
		newCreateKnowledgeFileContext(),
		&types.KnowledgeFileUpdateRequest{
			KnowledgeBaseID: "kb-1",
			KnowledgeID:     "knowledge-1",
			File:            newMultipartFileHeader(t, "upload.bin", "latest content"),
			CustomFileName:  "docs/spec/latest.md",
		},
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	var staged types.KnowledgeFileUpdatePayload
	require.NoError(t, json.Unmarshal(repo.stagedPayload, &staged))
	assert.Equal(t, "latest.md", staged.NewFileName)
	assert.Equal(t, "docs/spec", staged.NewFolderPath)
	assert.Equal(t, "md", staged.NewFileType)
}

func TestUpdateKnowledgeFileWhileProcessingOnlyReplacesPending(t *testing.T) {
	superseded, err := json.Marshal(types.KnowledgeFileUpdatePayload{NewFilePath: "staged/b.md"})
	require.NoError(t, err)
	repo := &fileUpdateRepoStub{
		knowledge: &types.Knowledge{
			ID: "knowledge-1", KnowledgeBaseID: "kb-1", Type: "file",
			FilePath: "old/path.md", FileName: "old.md", FileHash: "old-hash",
			ParseStatus: types.ParseStatusProcessing,
		},
		stageResult: &types.KnowledgeFileUpdateStageResult{
			Version: 3, State: types.KnowledgeFileUpdateResultPending, ActiveVersion: 1,
			ReplacedPendingPayload: types.JSON(superseded),
		},
	}
	fileSvc := &createKnowledgeFileServiceStub{}
	task := &fileUpdateTaskStub{}

	result, err := newFileUpdateService(repo, fileSvc, task).UpdateKnowledgeFile(
		newCreateKnowledgeFileContext(),
		&types.KnowledgeFileUpdateRequest{
			KnowledgeBaseID: "kb-1", KnowledgeID: "knowledge-1",
			File: newMultipartFileHeader(t, "latest.md", "latest content"),
		},
	)

	require.NoError(t, err)
	assert.Equal(t, uint64(3), result.UpdateVersion)
	assert.Equal(t, types.KnowledgeFileUpdateResultPending, result.UpdateState)
	require.Len(t, task.tasks, 1, "pending submissions opportunistically wake the active coordinator")
	assert.Equal(t, 1, fileSvc.deleteCalls)
	assert.Equal(t, "staged/b.md", fileSvc.deletedPath)
}

func TestUpdateKnowledgeFileDoesNotRollbackWhenSupersededCleanupFails(t *testing.T) {
	superseded, err := json.Marshal(types.KnowledgeFileUpdatePayload{NewFilePath: "staged/b.md"})
	require.NoError(t, err)
	repo := &fileUpdateRepoStub{
		knowledge: &types.Knowledge{
			ID: "knowledge-1", KnowledgeBaseID: "kb-1", Type: "file",
			FilePath: "old/path.md", FileName: "old.md", FileHash: "old-hash",
			ParseStatus: types.ParseStatusProcessing,
		},
		stageResult: &types.KnowledgeFileUpdateStageResult{
			Version: 3, State: types.KnowledgeFileUpdateResultPending, ActiveVersion: 1,
			ReplacedPendingPayload: types.JSON(superseded),
		},
	}
	fileSvc := &createKnowledgeFileServiceStub{deleteErr: errors.New("storage unavailable")}
	task := &fileUpdateTaskStub{}

	result, err := newFileUpdateService(repo, fileSvc, task).UpdateKnowledgeFile(
		newCreateKnowledgeFileContext(),
		&types.KnowledgeFileUpdateRequest{
			KnowledgeBaseID: "kb-1", KnowledgeID: "knowledge-1",
			File: newMultipartFileHeader(t, "latest.md", "latest content"),
		},
	)

	require.NoError(t, err)
	assert.Equal(t, uint64(3), result.UpdateVersion)
	assert.Equal(t, 1, fileSvc.deleteCalls)
	require.Len(t, task.tasks, 1)
}

func TestUpdateKnowledgeFileSameLatestPendingIsUnchanged(t *testing.T) {
	file := newMultipartFileHeader(t, "latest.md", "latest content")
	hash, err := calculateFileHash(file)
	require.NoError(t, err)
	pending, err := json.Marshal(types.KnowledgeFileUpdatePayload{
		NewFileName: "latest.md", NewFileType: "md", NewFileSize: file.Size, NewFileHash: hash,
	})
	require.NoError(t, err)
	activeVersion, pendingVersion := uint64(1), uint64(2)
	repo := &fileUpdateRepoStub{
		knowledge: &types.Knowledge{
			ID: "knowledge-1", KnowledgeBaseID: "kb-1", Type: "file",
			FilePath: "old/path.md", FileName: "old.md", FileHash: "old-hash",
			ParseStatus:       types.ParseStatusProcessing,
			FileUpdateVersion: 2, FileUpdateState: types.KnowledgeFileUpdateResultPending,
		},
		slot: &types.KnowledgeFileUpdateSlot{
			LatestVersion: 2, ActiveVersion: &activeVersion, PendingVersion: &pendingVersion,
			PendingPayload: types.JSON(pending),
		},
	}
	fileSvc := &createKnowledgeFileServiceStub{}
	task := &fileUpdateTaskStub{}

	result, err := newFileUpdateService(repo, fileSvc, task).UpdateKnowledgeFile(
		newCreateKnowledgeFileContext(),
		&types.KnowledgeFileUpdateRequest{
			KnowledgeBaseID: "kb-1", KnowledgeID: "knowledge-1", File: file,
		},
	)

	require.NoError(t, err)
	assert.Equal(t, "unchanged", result.Action)
	assert.Equal(t, uint64(2), result.UpdateVersion)
	assert.Equal(t, types.KnowledgeFileUpdateResultPending, result.UpdateState)
	assert.Zero(t, repo.stageCalls)
	assert.Empty(t, task.tasks)
	assert.Zero(t, fileSvc.saveCalls)
	assert.Zero(t, fileSvc.deleteCalls)
}

func TestUpdateKnowledgeFileSameCurrentFileAndExplicitChannelIsUnchanged(t *testing.T) {
	file := newMultipartFileHeader(t, "latest.md", "latest content")
	hash, err := calculateFileHash(file)
	require.NoError(t, err)
	repo := &fileUpdateRepoStub{
		knowledge: &types.Knowledge{
			ID: "knowledge-1", KnowledgeBaseID: "kb-1", Type: "file",
			FilePath: "current/path.md", FileName: "latest.md", FileHash: hash,
			Channel: "api-e2e", ParseStatus: types.ParseStatusCompleted,
			FileUpdateVersion: 4, FileUpdateState: types.KnowledgeFileUpdateStateIdle,
		},
	}
	fileSvc := &createKnowledgeFileServiceStub{}
	task := &fileUpdateTaskStub{}

	result, err := newFileUpdateService(repo, fileSvc, task).UpdateKnowledgeFile(
		newCreateKnowledgeFileContext(),
		&types.KnowledgeFileUpdateRequest{
			KnowledgeBaseID: "kb-1", KnowledgeID: "knowledge-1", File: file,
			Channel: "api-e2e", ChannelProvided: true,
		},
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "unchanged", result.Action)
	assert.Equal(t, uint64(4), result.Knowledge.FileUpdateVersion)
	assert.Zero(t, repo.stageCalls)
	assert.Zero(t, fileSvc.saveCalls)
	assert.Empty(t, task.tasks)
}

func TestUpdateKnowledgeFileSameCurrentFileAndDifferentChannelStagesUpdate(t *testing.T) {
	file := newMultipartFileHeader(t, "latest.md", "latest content")
	hash, err := calculateFileHash(file)
	require.NoError(t, err)
	repo := &fileUpdateRepoStub{
		knowledge: &types.Knowledge{
			ID: "knowledge-1", KnowledgeBaseID: "kb-1", Type: "file",
			FilePath: "current/path.md", FileName: "latest.md", FileHash: hash,
			Channel: "api-e2e", ParseStatus: types.ParseStatusCompleted,
			FileUpdateVersion: 4, FileUpdateState: types.KnowledgeFileUpdateStateIdle,
		},
		stageResult: &types.KnowledgeFileUpdateStageResult{
			Version: 5, State: types.KnowledgeFileUpdateResultActive, ActiveVersion: 5,
		},
	}
	fileSvc := &createKnowledgeFileServiceStub{}
	task := &fileUpdateTaskStub{}

	result, err := newFileUpdateService(repo, fileSvc, task).UpdateKnowledgeFile(
		newCreateKnowledgeFileContext(),
		&types.KnowledgeFileUpdateRequest{
			KnowledgeBaseID: "kb-1", KnowledgeID: "knowledge-1", File: file,
			Channel: "another-channel", ChannelProvided: true,
		},
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "updated", result.Action)
	assert.Equal(t, uint64(5), result.UpdateVersion)
	assert.Equal(t, 1, repo.stageCalls)
	assert.Equal(t, 1, fileSvc.saveCalls)
	require.Len(t, task.tasks, 1)
}

func TestSameAsCurrentKnowledgeFileComparesExplicitConfig(t *testing.T) {
	storedOverrides := &types.KnowledgeProcessOverrides{
		ParserEngineOverrides: map[string]string{"mode": "accurate"},
	}
	existing := &types.Knowledge{
		ID: "knowledge-1", FileName: "latest.md", FileHash: "same-hash",
		Channel: "api-e2e", FileUpdateState: types.KnowledgeFileUpdateStateIdle,
		Metadata: types.JSON(`{"source":"sync","owner":"docs"}`),
	}
	repo := &fileUpdateRepoStub{
		knowledge: existing,
		tags: map[string][]*types.KnowledgeTag{
			existing.ID: {{ID: "tag-1"}, {ID: "tag-2"}},
		},
	}
	svc := &knowledgeService{repo: repo}

	request := func() *types.KnowledgeFileUpdateRequest {
		return &types.KnowledgeFileUpdateRequest{
			Channel: "api-e2e", ChannelProvided: true,
			Metadata: map[string]string{"source": "sync"}, MetadataProvided: true,
			TagIDs: []string{"tag-2", "tag-1", "tag-1"}, TagIDsProvided: true,
			ProcessOverrides: &types.KnowledgeProcessOverrides{
				ParserEngineOverrides: map[string]string{"mode": "accurate"},
			},
		}
	}

	unchanged, err := svc.sameAsCurrentKnowledgeFile(
		context.Background(), existing, request(), storedOverrides, "", "latest.md", "same-hash",
	)
	require.NoError(t, err)
	assert.True(t, unchanged)

	tests := map[string]func(*types.KnowledgeFileUpdateRequest){
		"channel": func(req *types.KnowledgeFileUpdateRequest) { req.Channel = "web" },
		"metadata": func(req *types.KnowledgeFileUpdateRequest) {
			req.Metadata["source"] = "manual"
		},
		"tags": func(req *types.KnowledgeFileUpdateRequest) { req.TagIDs = []string{"tag-1"} },
		"process config": func(req *types.KnowledgeFileUpdateRequest) {
			req.ProcessOverrides.ParserEngineOverrides["mode"] = "fast"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			req := request()
			mutate(req)
			unchanged, err := svc.sameAsCurrentKnowledgeFile(
				context.Background(), existing, req, storedOverrides, "", "latest.md", "same-hash",
			)
			require.NoError(t, err)
			assert.False(t, unchanged)
		})
	}
}

func TestRetryKnowledgeFileUpdateRearmsExactFailedVersion(t *testing.T) {
	version := uint64(7)
	repo := &fileUpdateRepoStub{
		knowledge: &types.Knowledge{
			ID: "knowledge-1", TenantID: 1, KnowledgeBaseID: "kb-1", Type: "file",
			FilePath: "old/path.md", ParseStatus: types.ParseStatusCompleted,
		},
		slot: &types.KnowledgeFileUpdateSlot{
			KnowledgeID: "knowledge-1", TenantID: 1, KnowledgeBaseID: "kb-1",
			ActiveVersion: &version, ActiveState: types.KnowledgeFileUpdateStateFailed,
		},
	}
	task := &fileUpdateTaskStub{}
	svc := newFileUpdateService(repo, &createKnowledgeFileServiceStub{}, task)

	_, err := svc.RetryKnowledgeFileUpdate(newCreateKnowledgeFileContext(), "knowledge-1")
	require.NoError(t, err)
	assert.Equal(t, types.KnowledgeFileUpdateStateWaiting, repo.slot.ActiveState)
	require.Len(t, task.tasks, 1)
	var wake types.KnowledgeFileUpdateTaskPayload
	require.NoError(t, json.Unmarshal(task.tasks[0].Payload(), &wake))
	assert.Equal(t, version, wake.ActiveVersion)
}

func TestDiscardKnowledgeFileUpdateCleansFailedActiveAndPending(t *testing.T) {
	version, pendingVersion := uint64(7), uint64(8)
	active, err := json.Marshal(types.KnowledgeFileUpdatePayload{NewFilePath: "staged/a.md"})
	require.NoError(t, err)
	pending, err := json.Marshal(types.KnowledgeFileUpdatePayload{NewFilePath: "staged/b.md"})
	require.NoError(t, err)
	repo := &fileUpdateRepoStub{
		knowledge: &types.Knowledge{
			ID: "knowledge-1", TenantID: 1, KnowledgeBaseID: "kb-1", Type: "file",
			FilePath: "old/path.md", ParseStatus: types.ParseStatusCompleted,
		},
		slot: &types.KnowledgeFileUpdateSlot{
			KnowledgeID: "knowledge-1", TenantID: 1, KnowledgeBaseID: "kb-1",
			ActiveVersion: &version, ActiveState: types.KnowledgeFileUpdateStateFailed,
			ActivePayload: types.JSON(active), PendingVersion: &pendingVersion, PendingPayload: types.JSON(pending),
		},
	}
	fileSvc := &createKnowledgeFileServiceStub{}
	svc := newFileUpdateService(repo, fileSvc, &fileUpdateTaskStub{})

	_, err = svc.DiscardKnowledgeFileUpdate(newCreateKnowledgeFileContext(), "knowledge-1")
	require.NoError(t, err)
	assert.Nil(t, repo.slot)
	assert.Equal(t, 2, fileSvc.deleteCalls)
}

func TestRestoreFailedKnowledgeFileUpdateClaimRestoresReplacingStatus(t *testing.T) {
	version := uint64(7)
	active, err := json.Marshal(types.KnowledgeFileUpdatePayload{
		KnowledgeBaseID: "kb-1",
		OldParseStatus:  types.ParseStatusCompleted,
		OldFilePath:     "old/path.md",
		OldFileHash:     "old-hash",
		NewFilePath:     "staged/latest.md",
		NewFileName:     "latest.md",
		NewFileHash:     "latest-hash",
		DocumentTaskID:  "doc-task",
	})
	require.NoError(t, err)
	repo := &fileUpdateRepoStub{
		knowledge: &types.Knowledge{
			ID: "knowledge-1", TenantID: 1, KnowledgeBaseID: "kb-1", Type: "file",
			FilePath: "old/path.md", FileHash: "old-hash",
			ParseStatus:  types.ParseStatusReplacing,
			ErrorMessage: "cleanup failed",
		},
		slot: &types.KnowledgeFileUpdateSlot{
			KnowledgeID: "knowledge-1", TenantID: 1, KnowledgeBaseID: "kb-1",
			ActiveVersion: &version,
			ActiveState:   types.KnowledgeFileUpdateStateFailed,
			ActivePayload: types.JSON(active),
		},
	}
	svc := newFileUpdateService(repo, &createKnowledgeFileServiceStub{}, &fileUpdateTaskStub{})

	svc.restoreFailedKnowledgeFileUpdateClaim(context.Background(), types.KnowledgeFileUpdateTaskPayload{
		TenantID: 1, KnowledgeBaseID: "kb-1", KnowledgeID: "knowledge-1", ActiveVersion: version,
	})

	assert.Equal(t, types.ParseStatusCompleted, repo.knowledge.ParseStatus)
	assert.Empty(t, repo.knowledge.ErrorMessage)
	assert.Equal(t, 1, repo.applyCalls)
}

func TestRestoreFailedKnowledgeFileUpdateClaimDoesNotOverwriteSwitchedVersion(t *testing.T) {
	version := uint64(7)
	active, err := json.Marshal(types.KnowledgeFileUpdatePayload{
		KnowledgeBaseID: "kb-1",
		OldParseStatus:  types.ParseStatusCompleted,
		OldFilePath:     "old/path.md",
		OldFileHash:     "old-hash",
		NewFilePath:     "staged/latest.md",
		NewFileHash:     "latest-hash",
	})
	require.NoError(t, err)
	repo := &fileUpdateRepoStub{
		knowledge: &types.Knowledge{
			ID: "knowledge-1", TenantID: 1, KnowledgeBaseID: "kb-1", Type: "file",
			FilePath: "staged/latest.md", FileHash: "latest-hash",
			ParseStatus: types.ParseStatusPending,
		},
		slot: &types.KnowledgeFileUpdateSlot{
			KnowledgeID: "knowledge-1", TenantID: 1, KnowledgeBaseID: "kb-1",
			ActiveVersion: &version,
			ActiveState:   types.KnowledgeFileUpdateStateFailed,
			ActivePayload: types.JSON(active),
		},
	}
	svc := newFileUpdateService(repo, &createKnowledgeFileServiceStub{}, &fileUpdateTaskStub{})

	svc.restoreFailedKnowledgeFileUpdateClaim(context.Background(), types.KnowledgeFileUpdateTaskPayload{
		TenantID: 1, KnowledgeBaseID: "kb-1", KnowledgeID: "knowledge-1", ActiveVersion: version,
	})

	assert.Equal(t, types.ParseStatusPending, repo.knowledge.ParseStatus)
	assert.Equal(t, 1, repo.applyCalls)
}
