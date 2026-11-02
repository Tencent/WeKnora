package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
)

type explicitFolderRepoStub struct {
	interfaces.KnowledgeRepository
	tenant        uint64
	kb, path      string
	calls         int
	err, batchErr error
	cleanupCalls  int
}

func (r *explicitFolderRepoStub) CreateKnowledgeFolder(_ context.Context, tenant uint64, kb, path string) error {
	r.tenant, r.kb, r.path = tenant, kb, path
	r.calls++
	return r.err
}

func (r *explicitFolderRepoStub) DeleteKnowledgeFolder(_ context.Context, tenant uint64, kb, path string) error {
	r.tenant, r.kb, r.path = tenant, kb, path
	r.calls++
	return r.err
}

func (r *explicitFolderRepoStub) DeleteKnowledgeFolders(_ context.Context, tenant uint64, kb string) error {
	r.tenant, r.kb = tenant, kb
	r.cleanupCalls++
	return r.err
}

func (r *explicitFolderRepoStub) GetKnowledgeBatch(context.Context, uint64, []string) ([]*types.Knowledge, error) {
	return nil, r.batchErr
}

func (r *explicitFolderRepoStub) ListKnowledgeByKnowledgeBaseID(
	context.Context,
	uint64,
	string,
) ([]*types.Knowledge, error) {
	return nil, nil
}

type explicitFolderKBStub struct {
	interfaces.KnowledgeBaseService
	kb *types.KnowledgeBase
}

func (s explicitFolderKBStub) GetKnowledgeBaseByID(context.Context, string) (*types.KnowledgeBase, error) {
	return s.kb, nil
}

func explicitFolderGrant(permission types.OrgMemberRole) context.Context {
	// Capture a real authenticated caller before selecting the execution tenant.
	// WithExecutionTenant deliberately preserves a missing (tenant-zero) caller;
	// selecting tenant 1 alone must never authorize an HTTP-style write grant.
	ctx := types.WithCaller(context.Background(), types.Caller{
		TenantID: 1, UserID: "folder-owner", Role: types.TenantRoleContributor,
	})
	ctx = types.WithExecutionTenant(ctx, 1)
	return (&access.KBAccess{
		KnowledgeBase: &types.KnowledgeBase{ID: "kb", TenantID: 1},
		Caller:        types.CallerFromContext(ctx), EffectiveTenantID: 1, Permission: permission,
	}).Context(ctx)
}

func TestCreateKnowledgeFolderValidatesAndUsesOwnerTenant(t *testing.T) {
	repo := &explicitFolderRepoStub{}
	svc := &knowledgeService{
		repo:      repo,
		kbService: explicitFolderKBStub{kb: &types.KnowledgeBase{ID: "kb", TenantID: 1}},
	}
	node, err := svc.CreateKnowledgeFolder(explicitFolderGrant(types.OrgRoleEditor), "kb", "资料", " 新目录 ")
	require.NoError(t, err)
	require.Equal(t, "资料/新目录", node.Path)
	require.Zero(t, node.TotalCount)
	require.EqualValues(t, 1, repo.tenant)
	require.Equal(t, "kb", repo.kb)
	for _, name := range []string{"", ".", "..", "a/b", "a\\b", "bad\nname", "<script>alert(1)</script>"} {
		before := repo.calls
		_, err = svc.CreateKnowledgeFolder(explicitFolderGrant(types.OrgRoleEditor), "kb", "", name)
		var appErr *apperrors.AppError
		require.ErrorAs(t, err, &appErr, name)
		require.Equal(t, 400, appErr.HTTPCode, name)
		require.Equal(t, before, repo.calls, name)
	}
}

func TestExplicitKnowledgeFolderServiceRequiresExactWriteGrant(t *testing.T) {
	for _, ctx := range []context.Context{
		context.Background(),
		types.WithExecutionTenant(context.Background(), 1),
		explicitFolderGrant(types.OrgRoleViewer),
	} {
		repo := &explicitFolderRepoStub{}
		svc := &knowledgeService{
			repo:      repo,
			kbService: explicitFolderKBStub{kb: &types.KnowledgeBase{ID: "kb", TenantID: 1}},
		}
		_, err := svc.CreateKnowledgeFolder(ctx, "kb", "", "private")
		require.Error(t, err)
		require.Error(t, svc.DeleteKnowledgeFolder(ctx, "kb", "private"))
		require.Zero(t, repo.calls)
	}
	// An execution tenant alone or a grant for another tenant never suffices.
	repo := &explicitFolderRepoStub{}
	svc := &knowledgeService{
		repo:      repo,
		kbService: explicitFolderKBStub{kb: &types.KnowledgeBase{ID: "kb", TenantID: 2}},
	}
	_, err := svc.CreateKnowledgeFolder(explicitFolderGrant(types.OrgRoleEditor), "kb", "", "private")
	require.Error(t, err)
	require.Zero(t, repo.calls)
}

func TestExplicitKnowledgeFolderServiceMapsRepositoryErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{repository.ErrKnowledgeFolderNotFound, 404},
		{repository.ErrKnowledgeFolderConflict, 409},
		{repository.ErrKnowledgeFolderNotEmpty, 409},
	} {
		repo := &explicitFolderRepoStub{err: tc.err}
		svc := &knowledgeService{
			repo:      repo,
			kbService: explicitFolderKBStub{kb: &types.KnowledgeBase{ID: "kb", TenantID: 1}},
		}
		_, err := svc.CreateKnowledgeFolder(explicitFolderGrant(types.OrgRoleEditor), "kb", "", "empty")
		var appErr *apperrors.AppError
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, tc.status, appErr.HTTPCode)
		err = svc.DeleteKnowledgeFolder(explicitFolderGrant(types.OrgRoleEditor), "kb", "empty")
		require.ErrorAs(t, err, &appErr)
		require.Equal(t, tc.status, appErr.HTTPCode)
	}
}

func TestDeleteKnowledgeFolderRejectsRootAndNoncanonicalPath(t *testing.T) {
	repo := &explicitFolderRepoStub{}
	svc := &knowledgeService{
		repo:      repo,
		kbService: explicitFolderKBStub{kb: &types.KnowledgeBase{ID: "kb", TenantID: 1}},
	}
	for _, path := range []string{"", " ", "/docs", "docs/../child", "docs//child"} {
		require.Error(t, svc.DeleteKnowledgeFolder(explicitFolderGrant(types.OrgRoleEditor), "kb", path))
	}
	require.Zero(t, repo.calls)
	require.NoError(t, svc.DeleteKnowledgeFolder(explicitFolderGrant(types.OrgRoleEditor), "kb", "docs/child"))
	require.Equal(t, "docs/child", repo.path)
}

func explicitFolderDeleteTask(t *testing.T, payload types.KnowledgeListDeletePayload) *asynq.Task {
	t.Helper()
	data, err := json.Marshal(payload)
	require.NoError(t, err)
	return asynq.NewTask(types.TypeKnowledgeListDelete, data)
}

func TestClearKnowledgeFoldersRunsInDeletionTaskEvenWithoutDocuments(t *testing.T) {
	for _, ids := range [][]string{nil, {"already-deleted"}} {
		repo := &explicitFolderRepoStub{}
		svc := &knowledgeService{repo: repo}
		task := explicitFolderDeleteTask(
			t,
			types.KnowledgeListDeletePayload{TenantID: 1, KnowledgeBaseID: "kb", KnowledgeIDs: ids, ClearFolders: true},
		)
		require.NoError(t, svc.ProcessKnowledgeListDelete(context.Background(), task))
		require.Equal(t, 1, repo.cleanupCalls)
		require.EqualValues(t, 1, repo.tenant)
		require.Equal(t, "kb", repo.kb)
		// Retry is safe when the document snapshot was already removed.
		require.NoError(t, svc.ProcessKnowledgeListDelete(context.Background(), task))
		require.Equal(t, 2, repo.cleanupCalls)
	}
}

func TestClearKnowledgeFolderTaskPreservesErrorsAndOrdinaryDeletes(t *testing.T) {
	repo := &explicitFolderRepoStub{err: errors.New("folder cleanup failed")}
	svc := &knowledgeService{repo: repo}
	task := explicitFolderDeleteTask(
		t,
		types.KnowledgeListDeletePayload{TenantID: 1, KnowledgeBaseID: "kb", ClearFolders: true},
	)
	require.ErrorContains(t, svc.ProcessKnowledgeListDelete(context.Background(), task), "folder cleanup failed")
	repo.cleanupCalls = 0
	ordinary := explicitFolderDeleteTask(
		t,
		types.KnowledgeListDeletePayload{TenantID: 1, KnowledgeBaseID: "kb", KnowledgeIDs: []string{"already-deleted"}},
	)
	require.NoError(t, svc.ProcessKnowledgeListDelete(context.Background(), ordinary))
	require.Zero(t, repo.cleanupCalls)
	invalid := explicitFolderDeleteTask(t, types.KnowledgeListDeletePayload{TenantID: 1, ClearFolders: true})
	require.ErrorIs(t, svc.ProcessKnowledgeListDelete(context.Background(), invalid), asynq.SkipRetry)
	repo.batchErr = errors.New("document deletion preflight failed")
	task = explicitFolderDeleteTask(
		t,
		types.KnowledgeListDeletePayload{
			TenantID:        1,
			KnowledgeBaseID: "kb",
			KnowledgeIDs:    []string{"doc"},
			ClearFolders:    true,
		},
	)
	require.ErrorContains(
		t,
		svc.ProcessKnowledgeListDelete(context.Background(), task),
		"document deletion preflight failed",
	)
	require.Zero(t, repo.cleanupCalls, "a failed document task must not clear directories")
}

// Existing KB cleanup doubles intentionally have no explicit folders.
func (emptyKBKnowledgeRepo) DeleteKnowledgeFolders(context.Context, uint64, string) error { return nil }

func (populatedKBKnowledgeRepo) DeleteKnowledgeFolders(context.Context, uint64, string) error {
	return nil
}

func TestDeleteKnowledgeBaseTaskCleansExplicitFoldersAndRetriesErrors(t *testing.T) {
	for _, cleanupErr := range []error{nil, errors.New("explicit folder cleanup failed")} {
		repo := &explicitFolderRepoStub{err: cleanupErr}
		svc := &knowledgeBaseService{kgRepo: repo}
		err := svc.ProcessKBDelete(context.Background(), kbDeletePayload(t, "kb", 1))
		if cleanupErr != nil {
			require.ErrorIs(t, err, cleanupErr)
		} else {
			require.NoError(t, err)
		}
		require.Equal(t, 1, repo.cleanupCalls)
		require.EqualValues(t, 1, repo.tenant)
		require.Equal(t, "kb", repo.kb)
	}
}
