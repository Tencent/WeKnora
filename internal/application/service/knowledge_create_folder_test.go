package service

import (
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"testing"
	"time"

	werrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

const createKnowledgeFolderID = "10000000-0000-4000-8000-000000000001"

type createKnowledgePlacementResolverStub struct {
	result string
	err    error
	calls  int
	kbID   string
	rawID  string
}

func (s *createKnowledgePlacementResolverStub) ResolveForCreate(
	_ context.Context,
	knowledgeBaseID string,
	rawFolderID string,
) (string, error) {
	s.calls++
	s.kbID = knowledgeBaseID
	s.rawID = rawFolderID
	return s.result, s.err
}

type createKnowledgeFolderRepoStub struct {
	interfaces.KnowledgeRepository

	existing          *types.Knowledge
	updateErr         error
	updateColumnErr   error
	checkCalls        int
	createCalls       int
	updateCalls       int
	updateColumnCalls int
	created           *types.Knowledge
	updated           *types.Knowledge
	updatedColumnID   string
	updatedColumnName string
	updatedColumn     interface{}
	checkedParams     *types.KnowledgeCheckParams
}

func (r *createKnowledgeFolderRepoStub) CheckKnowledgeExists(
	_ context.Context,
	_ uint64,
	_ string,
	params *types.KnowledgeCheckParams,
) (bool, *types.Knowledge, error) {
	r.checkCalls++
	if params != nil {
		copy := *params
		r.checkedParams = &copy
	}
	return r.existing != nil, r.existing, nil
}

func (r *createKnowledgeFolderRepoStub) CreateKnowledge(
	_ context.Context,
	knowledge *types.Knowledge,
) error {
	r.createCalls++
	copy := *knowledge
	r.created = &copy
	return nil
}

func (r *createKnowledgeFolderRepoStub) UpdateKnowledge(
	_ context.Context,
	knowledge *types.Knowledge,
) error {
	r.updateCalls++
	copy := *knowledge
	r.updated = &copy
	return r.updateErr
}

func (r *createKnowledgeFolderRepoStub) UpdateKnowledgeColumn(
	_ context.Context,
	id string,
	column string,
	value interface{},
) error {
	r.updateColumnCalls++
	r.updatedColumnID = id
	r.updatedColumnName = column
	r.updatedColumn = value
	return r.updateColumnErr
}

func (r *createKnowledgeFolderRepoStub) GetKnowledgeTags(
	_ context.Context,
	_ []string,
) (map[string][]*types.KnowledgeTag, error) {
	return map[string][]*types.KnowledgeTag{}, nil
}

func TestCreateKnowledgeFromFileWritesResolvedFolderOnInitialModel(t *testing.T) {
	repo := &createKnowledgeFolderRepoStub{}
	resolver := &createKnowledgePlacementResolverStub{result: createKnowledgeFolderID}
	task := &createKnowledgeTaskEnqueuerStub{}
	fileService := &createKnowledgeFileServiceStub{}
	svc := &knowledgeService{
		repo:                    repo,
		kbService:               createKnowledgeFolderKBService(),
		fileSvc:                 fileService,
		task:                    task,
		folderPlacementResolver: resolver,
	}

	knowledge, err := svc.CreateKnowledgeFromFile(
		newCreateKnowledgeFileContext(),
		"kb-1",
		newMultipartFileHeader(t, "doc.txt", "hello"),
		nil,
		nil,
		"",
		nil,
		"",
		nil,
		createKnowledgeFolderID,
	)

	require.NoError(t, err)
	require.NotNil(t, knowledge)
	require.Equal(t, 1, resolver.calls)
	require.Equal(t, createKnowledgeFolderID, resolver.rawID)
	require.Equal(t, 1, repo.createCalls)
	require.NotNil(t, repo.created)
	require.Equal(t, createKnowledgeFolderID, repo.created.FolderID)
	require.Zero(t, repo.created.FolderVersion)
	require.Zero(t, repo.created.FolderIndexedVersion)
	require.Zero(t, repo.updateCalls)
	require.Zero(t, repo.updateColumnCalls)
	require.Equal(t, 1, fileService.saveCalls)
	require.Equal(t, 1, task.calls)
}

func TestCreateKnowledgeFromFileRejectsFolderBeforeOpeningFile(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "not found", err: ErrKnowledgeFolderNotFound},
		{name: "data integrity", err: ErrKnowledgeFolderDataIntegrity},
		{name: "canceled", err: context.Canceled},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &createKnowledgeFolderRepoStub{}
			resolver := &createKnowledgePlacementResolverStub{err: test.err}
			fileService := &createKnowledgeFileServiceStub{}
			task := &createKnowledgeTaskEnqueuerStub{}
			svc := &knowledgeService{
				repo:                    repo,
				kbService:               createKnowledgeFolderKBService(),
				fileSvc:                 fileService,
				task:                    task,
				folderPlacementResolver: resolver,
			}
			unopenable := &multipart.FileHeader{Filename: "doc.txt", Size: 5}

			knowledge, err := svc.CreateKnowledgeFromFile(
				newCreateKnowledgeFileContext(),
				"kb-1",
				unopenable,
				nil,
				nil,
				"",
				nil,
				"",
				nil,
				createKnowledgeFolderID,
			)

			require.ErrorIs(t, err, test.err)
			require.Nil(t, knowledge)
			require.Equal(t, 1, resolver.calls)
			require.Zero(t, repo.checkCalls)
			require.Zero(t, repo.createCalls)
			require.Zero(t, fileService.saveCalls)
			require.Zero(t, task.calls)
		})
	}
}

func TestCreateKnowledgeFromURLRejectsFolderBeforeStorageAndURLValidation(t *testing.T) {
	folderErr := ErrKnowledgeFolderDataIntegrity
	resolver := &createKnowledgePlacementResolverStub{err: folderErr}
	repo := &createKnowledgeFolderRepoStub{}
	task := &createKnowledgeTaskEnqueuerStub{}
	svc := &knowledgeService{
		repo:                    repo,
		kbService:               createKnowledgeFolderKBService(),
		task:                    task,
		folderPlacementResolver: resolver,
	}

	knowledge, err := svc.CreateKnowledgeFromURL(
		newCreateKnowledgeFileContext(),
		"kb-1",
		"http://127.0.0.1/private",
		"",
		"",
		nil,
		"",
		nil,
		"",
		nil,
		createKnowledgeFolderID,
	)

	require.ErrorIs(t, err, folderErr)
	require.Nil(t, knowledge)
	require.Equal(t, 1, resolver.calls)
	require.Zero(t, repo.checkCalls)
	require.Zero(t, repo.createCalls)
	require.Zero(t, task.calls)
}

func TestCreateKnowledgeFromURLChecksStorageBeforeSSRFAndDuplicate(t *testing.T) {
	repo := &createKnowledgeFolderRepoStub{
		existing: &types.Knowledge{ID: "must-not-be-read"},
	}
	task := &createKnowledgeTaskEnqueuerStub{}
	svc := &knowledgeService{
		repo:      repo,
		kbService: createKnowledgeFolderKBService(),
		task:      task,
	}

	knowledge, err := svc.CreateKnowledgeFromURL(
		newCreateKnowledgeFileContext(),
		"kb-1",
		"http://127.0.0.1/private",
		"",
		"",
		nil,
		"",
		nil,
		"",
		nil,
		"",
	)

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrInvalidURL)
	appErr, ok := werrors.IsAppError(err)
	require.True(t, ok)
	require.Equal(t, werrors.ErrBadRequest, appErr.Code)
	require.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
	require.NotEmpty(t, appErr.Message)
	require.Nil(t, knowledge)
	require.Zero(t, repo.checkCalls)
	require.Zero(t, repo.createCalls)
	require.Zero(t, task.calls)
}

func TestCreateKnowledgeFromURLReturnsDirectInvalidURLSentinel(t *testing.T) {
	tests := []struct {
		name     string
		rawURL   string
		fileName string
	}{
		{
			name:   "invalid local web URL",
			rawURL: "not a URL",
		},
		{
			name:     "invalid local file URL",
			rawURL:   "not a URL",
			fileName: "document.pdf",
		},
		{
			name:   "SSRF rejected web URL",
			rawURL: "http://127.0.0.1/private?token=sensitive",
		},
		{
			name:   "SSRF rejected file URL",
			rawURL: "http://127.0.0.1/private.pdf?token=sensitive",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &createKnowledgeFolderRepoStub{
				existing: &types.Knowledge{ID: "must-not-be-read"},
			}
			task := &createKnowledgeTaskEnqueuerStub{}
			svc := &knowledgeService{
				repo:      repo,
				kbService: createKnowledgeFolderKBService(),
				fileSvc:   &createKnowledgeFileServiceStub{},
				task:      task,
			}

			knowledge, err := svc.CreateKnowledgeFromURL(
				newCreateKnowledgeFileContext(),
				"kb-1",
				test.rawURL,
				test.fileName,
				"",
				nil,
				"",
				nil,
				"",
				nil,
				"",
			)

			require.Same(t, ErrInvalidURL, err)
			require.ErrorIs(t, err, ErrInvalidURL)
			require.EqualError(t, err, "invalid URL")
			require.Nil(t, knowledge)
			require.Zero(t, repo.checkCalls)
			require.Zero(t, repo.createCalls)
			require.Zero(t, task.calls)
			require.NotContains(t, err.Error(), test.rawURL)
			require.NotContains(t, err.Error(), "127.0.0.1")
			require.NotContains(t, err.Error(), "token=sensitive")
		})
	}
}

func TestCreateKnowledgeFromURLWritesFolderForWebAndFileURL(t *testing.T) {
	allowCreateKnowledgeTestURLHost(t)
	tests := []struct {
		name     string
		rawURL   string
		wantType string
	}{
		{name: "web URL", rawURL: "http://phase33b.invalid/page", wantType: "url"},
		{name: "remote file URL", rawURL: "http://phase33b.invalid/document.pdf", wantType: "file_url"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			repo := &createKnowledgeFolderRepoStub{}
			resolver := &createKnowledgePlacementResolverStub{result: createKnowledgeFolderID}
			task := &createKnowledgeTaskEnqueuerStub{}
			svc := &knowledgeService{
				repo:                    repo,
				kbService:               createKnowledgeFolderKBService(),
				fileSvc:                 &createKnowledgeFileServiceStub{},
				task:                    task,
				folderPlacementResolver: resolver,
			}

			knowledge, err := svc.CreateKnowledgeFromURL(
				newCreateKnowledgeFileContext(),
				"kb-1",
				test.rawURL,
				"",
				"",
				nil,
				"",
				nil,
				"",
				nil,
				createKnowledgeFolderID,
			)

			require.NoError(t, err)
			require.NotNil(t, knowledge)
			require.Equal(t, 1, resolver.calls)
			require.Equal(t, 1, repo.createCalls)
			require.NotNil(t, repo.created)
			require.Equal(t, test.wantType, repo.created.Type)
			require.Equal(t, createKnowledgeFolderID, repo.created.FolderID)
			require.Zero(t, repo.created.FolderVersion)
			require.Zero(t, repo.created.FolderIndexedVersion)
			require.Zero(t, repo.updateCalls)
			require.Equal(t, 1, task.calls)
		})
	}
}

func TestCreateKnowledgeFromManualWritesFolderAndRejectsInvalidPlacementWithoutSideEffects(t *testing.T) {
	t.Run("valid non-root", func(t *testing.T) {
		repo := &createKnowledgeFolderRepoStub{}
		resolver := &createKnowledgePlacementResolverStub{result: createKnowledgeFolderID}
		svc := &knowledgeService{
			repo:                    repo,
			kbService:               createKnowledgeFolderKBService(),
			fileSvc:                 &createKnowledgeFileServiceStub{},
			folderPlacementResolver: resolver,
		}

		knowledge, err := svc.CreateKnowledgeFromManual(
			newCreateKnowledgeFileContext(),
			"kb-1",
			&types.ManualKnowledgePayload{
				Title:   "Manual",
				Content: "content",
				Status:  types.ManualKnowledgeStatusDraft,
			},
			"",
			createKnowledgeFolderID,
		)

		require.NoError(t, err)
		require.NotNil(t, knowledge)
		require.Equal(t, 1, repo.createCalls)
		require.Equal(t, createKnowledgeFolderID, repo.created.FolderID)
		require.Zero(t, repo.created.FolderVersion)
		require.Zero(t, repo.created.FolderIndexedVersion)
		require.Zero(t, repo.updateCalls)
	})

	t.Run("invalid placement", func(t *testing.T) {
		repo := &createKnowledgeFolderRepoStub{}
		resolver := &createKnowledgePlacementResolverStub{err: ErrKnowledgeFolderNotFound}
		task := &createKnowledgeTaskEnqueuerStub{}
		svc := &knowledgeService{
			repo:                    repo,
			kbService:               createKnowledgeFolderKBService(),
			fileSvc:                 &createKnowledgeFileServiceStub{},
			task:                    task,
			folderPlacementResolver: resolver,
		}

		knowledge, err := svc.CreateKnowledgeFromManual(
			newCreateKnowledgeFileContext(),
			"kb-1",
			&types.ManualKnowledgePayload{
				Title:   "Manual",
				Content: "content",
				Status:  types.ManualKnowledgeStatusPublish,
			},
			"",
			createKnowledgeFolderID,
		)

		require.ErrorIs(t, err, ErrKnowledgeFolderNotFound)
		require.Nil(t, knowledge)
		require.Zero(t, repo.createCalls)
		require.Zero(t, repo.updateCalls)
		require.Zero(t, task.calls)
	})

	t.Run("publish preserves enqueue order", func(t *testing.T) {
		repo := &createKnowledgeFolderRepoStub{}
		resolver := &createKnowledgePlacementResolverStub{result: createKnowledgeFolderID}
		task := &createKnowledgeTaskEnqueuerStub{}
		svc := &knowledgeService{
			repo:                    repo,
			kbService:               createKnowledgeFolderKBService(),
			fileSvc:                 &createKnowledgeFileServiceStub{},
			task:                    task,
			folderPlacementResolver: resolver,
		}

		knowledge, err := svc.CreateKnowledgeFromManual(
			newCreateKnowledgeFileContext(),
			"kb-1",
			&types.ManualKnowledgePayload{
				Title:   "Manual",
				Content: "content",
				Status:  types.ManualKnowledgeStatusPublish,
			},
			"",
			createKnowledgeFolderID,
		)

		require.NoError(t, err)
		require.NotNil(t, knowledge)
		require.Equal(t, 1, repo.createCalls)
		require.Equal(t, createKnowledgeFolderID, repo.created.FolderID)
		require.Zero(t, repo.updateCalls)
		require.Equal(t, 1, task.calls)
	})
}

func TestCreateKnowledgeNonRootFailsClosedWithoutResolver(t *testing.T) {
	svc := &knowledgeService{
		repo:      &createKnowledgeFolderRepoStub{},
		kbService: createKnowledgeFolderKBService(),
	}

	knowledge, err := svc.CreateKnowledgeFromManual(
		newCreateKnowledgeFileContext(),
		"kb-1",
		&types.ManualKnowledgePayload{
			Title:   "Manual",
			Content: "content",
			Status:  types.ManualKnowledgeStatusDraft,
		},
		"",
		createKnowledgeFolderID,
	)

	require.ErrorIs(t, err, ErrKnowledgeFolderInternal)
	require.Nil(t, knowledge)
}

func TestCreateKnowledgeRootCompatibilityDoesNotRequireResolver(t *testing.T) {
	t.Run("URL", func(t *testing.T) {
		allowCreateKnowledgeTestURLHost(t)
		tests := []struct {
			name   string
			rawURL string
		}{
			{name: "web", rawURL: "http://phase33b.invalid/page"},
			{name: "remote file", rawURL: "http://phase33b.invalid/document.pdf"},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				repo := &createKnowledgeFolderRepoStub{}
				task := &createKnowledgeTaskEnqueuerStub{}
				svc := &knowledgeService{
					repo:      repo,
					kbService: createKnowledgeFolderKBService(),
					fileSvc:   &createKnowledgeFileServiceStub{},
					task:      task,
				}

				knowledge, err := svc.CreateKnowledgeFromURL(
					newCreateKnowledgeFileContext(),
					"kb-1",
					test.rawURL,
					"",
					"",
					nil,
					"",
					nil,
					"",
					nil,
					"",
				)

				require.NoError(t, err)
				require.NotNil(t, knowledge)
				require.Equal(t, 1, repo.createCalls)
				require.Equal(t, "", repo.created.FolderID)
			})
		}
	})

	t.Run("manual", func(t *testing.T) {
		repo := &createKnowledgeFolderRepoStub{}
		svc := &knowledgeService{
			repo:      repo,
			kbService: createKnowledgeFolderKBService(),
			fileSvc:   &createKnowledgeFileServiceStub{},
		}

		knowledge, err := svc.CreateKnowledgeFromManual(
			newCreateKnowledgeFileContext(),
			"kb-1",
			&types.ManualKnowledgePayload{
				Title:   "Manual",
				Content: "content",
				Status:  types.ManualKnowledgeStatusDraft,
			},
			"",
			"",
		)

		require.NoError(t, err)
		require.NotNil(t, knowledge)
		require.Equal(t, 1, repo.createCalls)
		require.Equal(t, "", repo.created.FolderID)
	})
}

func TestCreateKnowledgeDuplicateRefreshesTimestampWithoutMovingExistingFolder(t *testing.T) {
	allowCreateKnowledgeTestURLHost(t)
	tests := []struct {
		name                  string
		create                func(*knowledgeService) (*types.Knowledge, error)
		wantCheckType         string
		wantUpdateCalls       int
		wantUpdateColumnCalls int
	}{
		{
			name:                  "file",
			wantCheckType:         "file",
			wantUpdateColumnCalls: 1,
			create: func(svc *knowledgeService) (*types.Knowledge, error) {
				return svc.CreateKnowledgeFromFile(
					newCreateKnowledgeFileContext(),
					"kb-1",
					newMultipartFileHeader(t, "doc.txt", "same"),
					nil,
					nil,
					"",
					nil,
					"",
					nil,
					createKnowledgeFolderID,
				)
			},
		},
		{
			name:            "URL",
			wantCheckType:   "url",
			wantUpdateCalls: 1,
			create: func(svc *knowledgeService) (*types.Knowledge, error) {
				return svc.CreateKnowledgeFromURL(
					newCreateKnowledgeFileContext(),
					"kb-1",
					"http://phase33b.invalid/page",
					"",
					"",
					nil,
					"",
					nil,
					"",
					nil,
					createKnowledgeFolderID,
				)
			},
		},
		{
			name:            "remote file URL",
			wantCheckType:   "file_url",
			wantUpdateCalls: 1,
			create: func(svc *knowledgeService) (*types.Knowledge, error) {
				return svc.CreateKnowledgeFromURL(
					newCreateKnowledgeFileContext(),
					"kb-1",
					"http://phase33b.invalid/document.pdf",
					"",
					"",
					nil,
					"",
					nil,
					"",
					nil,
					createKnowledgeFolderID,
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			oldCreatedAt := time.Now().Add(-2 * time.Hour)
			oldUpdatedAt := time.Now().Add(-time.Hour)
			existing := &types.Knowledge{
				ID:                   "existing",
				KnowledgeBaseID:      "kb-1",
				FolderID:             "20000000-0000-4000-8000-000000000002",
				FolderVersion:        7,
				FolderIndexedVersion: 6,
				CreatedAt:            oldCreatedAt,
				UpdatedAt:            oldUpdatedAt,
			}
			repo := &createKnowledgeFolderRepoStub{existing: existing}
			resolver := &createKnowledgePlacementResolverStub{result: createKnowledgeFolderID}
			fileService := &createKnowledgeFileServiceStub{}
			task := &createKnowledgeTaskEnqueuerStub{}
			svc := &knowledgeService{
				repo:                    repo,
				kbService:               createKnowledgeFolderKBService(),
				fileSvc:                 fileService,
				task:                    task,
				folderPlacementResolver: resolver,
			}

			knowledge, err := test.create(svc)

			var duplicate *types.DuplicateKnowledgeError
			require.True(t, errors.As(err, &duplicate))
			require.Same(t, existing, knowledge)
			require.Equal(t, 1, resolver.calls)
			require.Equal(t, 1, repo.checkCalls)
			require.NotNil(t, repo.checkedParams)
			require.Equal(t, test.wantCheckType, repo.checkedParams.Type)
			require.Zero(t, repo.createCalls)
			require.Equal(t, test.wantUpdateCalls, repo.updateCalls)
			require.Equal(t, test.wantUpdateColumnCalls, repo.updateColumnCalls)
			require.Zero(t, fileService.saveCalls)
			require.Zero(t, task.calls)
			require.Equal(t, "20000000-0000-4000-8000-000000000002", existing.FolderID)
			require.Equal(t, uint64(7), existing.FolderVersion)
			require.Equal(t, uint64(6), existing.FolderIndexedVersion)

			if test.wantUpdateColumnCalls == 1 {
				require.Equal(t, existing.ID, repo.updatedColumnID)
				require.Equal(t, "created_at", repo.updatedColumnName)
				refreshedAt, ok := repo.updatedColumn.(time.Time)
				require.True(t, ok)
				require.True(t, refreshedAt.After(oldCreatedAt))
				require.Nil(t, repo.updated)
			} else {
				require.NotNil(t, repo.updated)
				require.Equal(t, existing.ID, repo.updated.ID)
				require.True(t, repo.updated.CreatedAt.After(oldCreatedAt))
				require.True(t, repo.updated.UpdatedAt.After(oldUpdatedAt))
				require.Equal(t, repo.updated.CreatedAt, repo.updated.UpdatedAt)
				require.Equal(t, existing.FolderID, repo.updated.FolderID)
				require.Equal(t, existing.FolderVersion, repo.updated.FolderVersion)
				require.Equal(t, existing.FolderIndexedVersion, repo.updated.FolderIndexedVersion)
				require.Empty(t, repo.updatedColumnName)
			}
		})
	}
}

func TestCreateKnowledgeDuplicateTimestampUpdateFailureIsPropagated(t *testing.T) {
	allowCreateKnowledgeTestURLHost(t)
	updateErr := errors.New("timestamp refresh failed")
	tests := []struct {
		name                  string
		rawURL                string
		isFile                bool
		wantCheckType         string
		wantUpdateCalls       int
		wantUpdateColumnCalls int
	}{
		{
			name:                  "file",
			isFile:                true,
			wantCheckType:         "file",
			wantUpdateColumnCalls: 1,
		},
		{
			name:            "URL",
			rawURL:          "http://phase33b.invalid/page",
			wantCheckType:   "url",
			wantUpdateCalls: 1,
		},
		{
			name:            "remote file URL",
			rawURL:          "http://phase33b.invalid/document.pdf",
			wantCheckType:   "file_url",
			wantUpdateCalls: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			existing := &types.Knowledge{
				ID:                   "existing",
				KnowledgeBaseID:      "kb-1",
				FolderID:             "20000000-0000-4000-8000-000000000002",
				FolderVersion:        7,
				FolderIndexedVersion: 6,
				CreatedAt:            time.Now().Add(-2 * time.Hour),
				UpdatedAt:            time.Now().Add(-time.Hour),
			}
			repo := &createKnowledgeFolderRepoStub{
				existing: existing,
			}
			if test.isFile {
				repo.updateColumnErr = updateErr
			} else {
				repo.updateErr = updateErr
			}
			resolver := &createKnowledgePlacementResolverStub{result: createKnowledgeFolderID}
			fileService := &createKnowledgeFileServiceStub{}
			task := &createKnowledgeTaskEnqueuerStub{}
			svc := &knowledgeService{
				repo:                    repo,
				kbService:               createKnowledgeFolderKBService(),
				fileSvc:                 fileService,
				task:                    task,
				folderPlacementResolver: resolver,
			}

			var knowledge *types.Knowledge
			var err error
			if test.isFile {
				knowledge, err = svc.CreateKnowledgeFromFile(
					newCreateKnowledgeFileContext(),
					"kb-1",
					newMultipartFileHeader(t, "doc.txt", "same"),
					nil,
					nil,
					"",
					nil,
					"",
					nil,
					createKnowledgeFolderID,
				)
			} else {
				knowledge, err = svc.CreateKnowledgeFromURL(
					newCreateKnowledgeFileContext(),
					"kb-1",
					test.rawURL,
					"",
					"",
					nil,
					"",
					nil,
					"",
					nil,
					createKnowledgeFolderID,
				)
			}

			require.ErrorIs(t, err, updateErr)
			require.Nil(t, knowledge)
			var duplicate *types.DuplicateKnowledgeError
			require.False(t, errors.As(err, &duplicate))
			require.Equal(t, 1, resolver.calls)
			require.Equal(t, 1, repo.checkCalls)
			require.NotNil(t, repo.checkedParams)
			require.Equal(t, test.wantCheckType, repo.checkedParams.Type)
			require.Equal(t, test.wantUpdateCalls, repo.updateCalls)
			require.Equal(t, test.wantUpdateColumnCalls, repo.updateColumnCalls)
			require.Zero(t, repo.createCalls)
			require.Zero(t, fileService.saveCalls)
			require.Zero(t, task.calls)
			require.Equal(t, "20000000-0000-4000-8000-000000000002", existing.FolderID)
			require.Equal(t, uint64(7), existing.FolderVersion)
			require.Equal(t, uint64(6), existing.FolderIndexedVersion)
		})
	}
}

func createKnowledgeFolderKBService() interfaces.KnowledgeBaseService {
	return &createKnowledgeFileKBServiceStub{
		kb: &types.KnowledgeBase{ID: "kb-1"},
	}
}

func allowCreateKnowledgeTestURLHost(t *testing.T) {
	t.Helper()
	secutils.SetSSRFWhitelistFromRaw("phase33b.invalid")
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
}
