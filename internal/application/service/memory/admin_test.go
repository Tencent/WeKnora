package memory

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func memoryAdminContext(ctx context.Context, tenant uint64, role types.TenantRole) context.Context {
	return types.WithCaller(ctx, types.Caller{TenantID: tenant, UserID: "operator", Role: role})
}

func seedAdminItem(t *testing.T, svc *Service, scope interfaces.MemoryScope, status, content string) *types.MemoryItem {
	t.Helper()
	_, err := svc.repo.EnsureSubject(t.Context(), scope)
	require.NoError(t, err)
	item := &types.MemoryItem{
		ID: uuid.NewString(), Kind: types.MemoryKindFact,
		Topic: content, Content: content, NormalizedKey: types.MemoryItemKey(content, content),
		Status: status, Origin: types.MemoryOriginExtracted, SourceMessageID: uuid.NewString(),
	}
	require.NoError(t, svc.repo.SaveItem(t.Context(), scope, item, ""))
	return item
}

func TestMemoryAdminAuthorizationEveryOperation(t *testing.T) {
	svc, _, _ := newMemoryHarness(t)
	// RBAC rollout is disabled; the service must still enforce the admin boundary.
	svc.config = &config.Config{}
	admin := memoryAdminContext(t.Context(), 1, types.TenantRoleAdmin)
	cases := map[string]context.Context{
		"missing caller":      t.Context(),
		"viewer":              memoryAdminContext(t.Context(), 1, types.TenantRoleViewer),
		"contributor":         memoryAdminContext(t.Context(), 1, types.TenantRoleContributor),
		"missing user":        types.WithCaller(t.Context(), types.Caller{TenantID: 1, Role: types.TenantRoleOwner}),
		"missing tenant":      memoryAdminContext(t.Context(), 0, types.TenantRoleAdmin),
		"API key":             types.WithTenantAPIKeyScope(admin, types.TenantAPIKeyScope{KeyID: 7}),
		"empty API key scope": types.WithTenantAPIKeyScope(admin, types.TenantAPIKeyScope{}),
	}
	for name, ctx := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, err := svc.AdminListSubjects(ctx, 50, 0)
			require.ErrorIs(t, err, ErrMemoryAdminForbidden)
			_, _, err = svc.AdminListItems(ctx, "missing", "", 50, 0)
			require.ErrorIs(t, err, ErrMemoryAdminForbidden)
			_, err = svc.AdminConfirmItem(ctx, "missing", "missing")
			require.ErrorIs(t, err, ErrMemoryAdminForbidden)
			require.ErrorIs(t, svc.AdminRejectItem(ctx, "missing", "missing"), ErrMemoryAdminForbidden)
			_, err = svc.AdminClear(ctx, "missing")
			require.ErrorIs(t, err, ErrMemoryAdminForbidden)
			require.ErrorIs(t, svc.AdminSetEnabled(ctx, "missing", true), ErrMemoryAdminForbidden)
		})
	}
}

func TestMemoryAdminExplicitScopeUsesCallerTenantAndUUID(t *testing.T) {
	svc, _, tenants := newMemoryHarness(t)
	ownCtx := enabledCtx(t, tenants, 1, "operator")
	ownItem, err := svc.Remember(ownCtx, types.MemoryItem{Kind: types.MemoryKindFact, Content: "operator uses Emacs"})
	require.NoError(t, err)
	targetScope := interfaces.MemoryScope{TenantID: 1, SubjectID: "api_external_user:customer/with/slashes"}
	targetItem := seedAdminItem(t, svc, targetScope, types.MemoryStatusPending, "customer uses Neovim")
	target, err := svc.repo.GetSubject(t.Context(), targetScope)
	require.NoError(t, err)
	foreignScope := interfaces.MemoryScope{TenantID: 2, SubjectID: targetScope.SubjectID}
	foreignItem := seedAdminItem(t, svc, foreignScope, types.MemoryStatusPending, "foreign secret")
	foreign, err := svc.repo.GetSubject(t.Context(), foreignScope)
	require.NoError(t, err)
	ctx := types.WithExecutionTenant(memoryAdminContext(ownCtx, 1, types.TenantRoleOwner), 2)
	items, total, err := svc.AdminListItems(ctx, target.ID, "", 50, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, targetItem.ID, items[0].ID)
	_, _, err = svc.AdminListItems(ctx, targetScope.SubjectID, "", 50, 0)
	require.ErrorIs(t, err, ErrSubjectNotFound, "opaque storage IDs are not route UUIDs")
	for _, id := range []string{foreign.ID, uuid.NewString()} {
		_, _, err = svc.AdminListItems(ctx, id, "", 50, 0)
		require.ErrorIs(t, err, ErrSubjectNotFound)
		_, err = svc.AdminConfirmItem(ctx, id, targetItem.ID)
		require.ErrorIs(t, err, ErrSubjectNotFound)
		require.ErrorIs(t, svc.AdminRejectItem(ctx, id, targetItem.ID), ErrSubjectNotFound)
		_, err = svc.AdminClear(ctx, id)
		require.ErrorIs(t, err, ErrSubjectNotFound)
		require.ErrorIs(t, svc.AdminSetEnabled(ctx, id, false), ErrSubjectNotFound)
	}
	for _, id := range []string{foreignItem.ID, ownItem.ID, uuid.NewString()} {
		_, err = svc.AdminConfirmItem(ctx, target.ID, id)
		require.ErrorIs(t, err, ErrItemNotFound)
		require.ErrorIs(t, svc.AdminRejectItem(ctx, target.ID, id), ErrItemNotFound)
	}
	_, err = svc.AdminConfirmItem(ctx, target.ID, targetItem.ID)
	require.NoError(t, err)
	items, total, err = svc.ListItems(ownCtx, "", 50, 0)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, ownItem.ID, items[0].ID)
	summaries, total, err := svc.AdminListSubjects(ctx, 50, 0)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, summaries, 2)
}

func TestMemoryAdminPendingLifecycleAndDisabledScope(t *testing.T) {
	svc, _, tenants := newMemoryHarness(t)
	scope := interfaces.MemoryScope{TenantID: 1, SubjectID: "api_external_user:accountless"}
	active := seedAdminItem(t, svc, scope, types.MemoryStatusActive, "current preference")
	pending := seedAdminItem(t, svc, scope, types.MemoryStatusPending, "proposed preference")
	rejected := seedAdminItem(t, svc, scope, types.MemoryStatusPending, "declined preference")
	archived := seedAdminItem(t, svc, scope, types.MemoryStatusArchived, "old preference")
	subject, err := svc.repo.GetSubject(t.Context(), scope)
	require.NoError(t, err)
	ctx := memoryAdminContext(t.Context(), 1, types.TenantRoleAdmin)
	tenants.set(1, &types.MemoryConfig{Enabled: false})
	require.NoError(t, svc.AdminSetEnabled(ctx, subject.ID, false))
	items, total, err := svc.AdminListItems(ctx, subject.ID, types.MemoryStatusPending, 0, -1)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	require.Len(t, items, 2)
	confirmed, err := svc.AdminConfirmItem(ctx, subject.ID, pending.ID)
	require.NoError(t, err)
	require.Equal(t, types.MemoryStatusActive, confirmed.Status)
	for _, item := range []*types.MemoryItem{active, archived} {
		require.ErrorIs(t, svc.AdminRejectItem(ctx, subject.ID, item.ID), types.ErrMemoryConflict)
		unchanged, err := svc.repo.GetItem(ctx, scope, item.ID)
		require.NoError(t, err)
		require.Equal(t, item.Status, unchanged.Status)
	}
	require.NoError(t, svc.AdminRejectItem(ctx, subject.ID, rejected.ID))
	forgotten, err := svc.repo.HasTombstone(ctx, scope, types.MemoryFingerprint(rejected.Content))
	require.NoError(t, err)
	require.True(t, forgotten)
	missing, err := svc.repo.GetItem(ctx, scope, rejected.ID)
	require.NoError(t, err)
	require.Nil(t, missing)
	removed, err := svc.AdminClear(ctx, subject.ID)
	require.NoError(t, err)
	require.EqualValues(t, 3, removed)
	subject, err = svc.repo.GetSubject(ctx, scope)
	require.NoError(t, err)
	require.False(t, subject.Enabled)
	require.Empty(t, subject.BlockText)
	forgotten, err = svc.repo.HasTombstone(ctx, scope, types.MemoryFingerprint(rejected.Content))
	require.NoError(t, err)
	require.False(t, forgotten, "admin clear removes the snapshot including tombstones")
	require.NoError(t, svc.AdminSetEnabled(ctx, subject.ID, true))
	subject, err = svc.repo.GetSubject(ctx, scope)
	require.NoError(t, err)
	require.True(t, subject.Enabled)
}

func TestMemoryServiceWriteEnforcesWorkspacePendingLimit(t *testing.T) {
	svc, _, tenants := newMemoryHarness(t)
	ctx := enabledCtx(t, tenants, 1, "alice")
	tenants.set(1, &types.MemoryConfig{Enabled: true, WriteMode: types.MemoryWriteAuto, MaxItems: 2})
	active, err := svc.Remember(ctx, types.MemoryItem{
		Kind: types.MemoryKindFact, Content: "active fact is retained", Origin: types.MemoryOriginExplicit,
	})
	require.NoError(t, err)
	var first *types.MemoryItem
	for i := range 3 {
		item, err := svc.Remember(ctx, types.MemoryItem{
			Kind:  types.MemoryKindFact,
			Topic: fmt.Sprintf("proposal-%d", i), Content: fmt.Sprintf("unique proposal number %d", i),
			Origin: types.MemoryOriginExtracted, Inferred: true,
		})
		require.NoError(t, err)
		if i == 0 {
			first = item
		}
	}
	_, total, err := svc.ListItems(ctx, types.MemoryStatusPending, 50, 0)
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	scope, err := ResolveScope(ctx)
	require.NoError(t, err)
	oldest, err := svc.repo.GetItem(ctx, scope, first.ID)
	require.NoError(t, err)
	require.Equal(t, types.MemoryStatusArchived, oldest.Status)
	retained, err := svc.repo.GetItem(ctx, scope, active.ID)
	require.NoError(t, err)
	require.Equal(t, types.MemoryStatusActive, retained.Status)
}

// confirmAfterAdminReadRepo deterministically completes a concurrent confirm
// after the service read, before the rejection reaches its repository mutation.
type confirmAfterAdminReadRepo struct {
	interfaces.MemoryRepository
	itemID    string
	confirmed bool
}

func (r *confirmAfterAdminReadRepo) GetItem(
	ctx context.Context, scope interfaces.MemoryScope, id string,
) (*types.MemoryItem, error) {
	item, err := r.MemoryRepository.GetItem(ctx, scope, id)
	if err == nil && item != nil && id == r.itemID && !r.confirmed {
		r.confirmed = true
		if err := r.ConfirmPendingItem(ctx, scope, id); err != nil {
			return nil, err
		}
	}
	return item, err
}

func TestMemoryAdminRejectCannotDeleteConcurrentlyConfirmedItem(t *testing.T) {
	svc, _, _ := newMemoryHarness(t)
	scope := interfaces.MemoryScope{TenantID: 1, SubjectID: "api_external_user:concurrent"}
	pending := seedAdminItem(t, svc, scope, types.MemoryStatusPending, "the approved memory")
	subject, err := svc.repo.GetSubject(t.Context(), scope)
	require.NoError(t, err)
	repo := &confirmAfterAdminReadRepo{MemoryRepository: svc.repo, itemID: pending.ID}
	svc.repo = repo
	ctx := memoryAdminContext(t.Context(), 1, types.TenantRoleAdmin)
	require.ErrorIs(t, svc.AdminRejectItem(ctx, subject.ID, pending.ID), types.ErrMemoryConflict)
	require.True(t, repo.confirmed)
	retained, err := repo.MemoryRepository.GetItem(ctx, scope, pending.ID)
	require.NoError(t, err)
	require.NotNil(t, retained)
	require.Equal(t, types.MemoryStatusActive, retained.Status)
	forgotten, err := svc.repo.HasTombstone(ctx, scope, types.MemoryFingerprint(pending.Content))
	require.NoError(t, err)
	require.False(t, forgotten, "a failed rejection must not leave a tombstone")
}

// deleteDuringAdminConfirmRepo removes the real item at a deterministic
// boundary, reproducing an overlapping rejection or subject clear.
type deleteDuringAdminConfirmRepo struct {
	interfaces.MemoryRepository
	itemID           string
	afterInitialRead bool
	beforeFinalRead  bool
	confirmationDone bool
	deleted          bool
}

func (r *deleteDuringAdminConfirmRepo) GetItem(
	ctx context.Context, scope interfaces.MemoryScope, id string,
) (*types.MemoryItem, error) {
	if id == r.itemID && r.beforeFinalRead && r.confirmationDone && !r.deleted {
		if err := r.DeleteItem(ctx, scope, id); err != nil {
			return nil, err
		}
		r.deleted = true
	}
	item, err := r.MemoryRepository.GetItem(ctx, scope, id)
	if err == nil && item != nil && id == r.itemID && r.afterInitialRead && !r.deleted {
		if err := r.DeleteItem(ctx, scope, id); err != nil {
			return nil, err
		}
		r.deleted = true
	}
	return item, err
}

func (r *deleteDuringAdminConfirmRepo) ConfirmPendingItem(
	ctx context.Context, scope interfaces.MemoryScope, id string,
) error {
	err := r.MemoryRepository.ConfirmPendingItem(ctx, scope, id)
	if err == nil && id == r.itemID {
		r.confirmationDone = true
	}
	return err
}

func TestMemoryAdminConfirmConcurrentlyDeletedItemReturnsNotFound(t *testing.T) {
	for _, boundary := range []string{"after initial read", "before final read"} {
		t.Run(boundary, func(t *testing.T) {
			svc, _, _ := newMemoryHarness(t)
			scope := interfaces.MemoryScope{TenantID: 1, SubjectID: "api_external_user:concurrent-delete"}
			pending := seedAdminItem(t, svc, scope, types.MemoryStatusPending, "proposal removed during confirmation")
			subject, err := svc.repo.GetSubject(t.Context(), scope)
			require.NoError(t, err)
			repo := &deleteDuringAdminConfirmRepo{
				MemoryRepository: svc.repo, itemID: pending.ID,
				afterInitialRead: boundary == "after initial read",
				beforeFinalRead:  boundary == "before final read",
			}
			svc.repo = repo
			ctx := memoryAdminContext(t.Context(), 1, types.TenantRoleAdmin)
			item, err := svc.AdminConfirmItem(ctx, subject.ID, pending.ID)
			require.True(t, repo.deleted, "the concurrent deletion must run")
			require.ErrorIs(t, err, ErrItemNotFound)
			require.Nil(t, item)
		})
	}
}
