package access

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestTransferAdmissionRequiresIndependentGrants(t *testing.T) {
	source, target := &types.KnowledgeBase{ID: "source", TenantID: 1}, &types.KnowledgeBase{ID: "target", TenantID: 1}
	ctx := types.WithCaller(
		context.Background(),
		types.Caller{TenantID: 1, UserID: "user", Role: types.TenantRoleAdmin},
	)
	_, err := WithKBTransfer(ctx, source, target, KBTransferMove, "task", false)
	require.ErrorIs(t, err, ErrForbidden)
	request := KBRequest{Caller: types.CallerFromContext(ctx)}
	sourceRead, err := ResolveKB(ctx, request, source, types.OrgRoleViewer, nil, nil)
	require.NoError(t, err)
	targetRead, err := ResolveKB(ctx, request, target, types.OrgRoleViewer, nil, nil)
	require.NoError(t, err)
	reads := targetRead.WithGrant(sourceRead.WithGrant(ctx))
	_, err = WithKBTransfer(reads, source, target, KBTransferClone, "task", false)
	require.ErrorIs(t, err, ErrForbidden, "owner read projection must not authorize target replacement")
	targetWrite, err := ResolveKB(ctx, request, target, types.OrgRoleEditor, nil, nil)
	require.NoError(t, err)
	cloneCtx, err := WithKBTransfer(targetWrite.WithGrant(reads), source, target, KBTransferClone, "task", false)
	require.NoError(t, err)
	require.NoError(t, RequireKBTransfer(logger.CloneContext(cloneCtx), source, target, KBTransferClone))
	require.False(t, HasKBGrant(cloneCtx, source.ID, 1, types.OrgRoleEditor))
	_, err = WithKBTransfer(targetWrite.WithGrant(reads), source, target, KBTransferMove, "task", false)
	require.ErrorIs(t, err, ErrForbidden)
}

func TestTransferTaskScopeIsExactAndDoesNotCreateCaller(t *testing.T) {
	source, target := &types.KnowledgeBase{ID: "source", TenantID: 7}, &types.KnowledgeBase{ID: "target", TenantID: 7}
	ctx, err := WithKBTransferTask(context.Background(), source, target, 7, KBTransferMove, "task", false)
	require.NoError(t, err)
	ctx = logger.CloneContext(ctx)
	require.Zero(t, types.CallerFromContext(ctx).TenantID)
	require.NoError(t, RequireKBTransfer(ctx, source, target, KBTransferMove))
	require.ErrorIs(t, RequireKBTransfer(ctx, source, target, KBTransferClone), ErrForbidden)
	require.ErrorIs(
		t,
		RequireKBTransfer(ctx, source, &types.KnowledgeBase{ID: "third", TenantID: 7}, KBTransferMove),
		ErrForbidden,
	)
	require.False(t, HasKBGrant(ctx, "third", 7, types.OrgRoleViewer))
	_, err = WithKBTransferTask(
		ctx,
		source,
		&types.KnowledgeBase{ID: "target", TenantID: 8},
		7,
		KBTransferMove,
		"task",
		false,
	)
	require.ErrorIs(t, err, ErrForbidden)
	changed := types.WithCaller(ctx, types.Caller{TenantID: 7, UserID: "different"})
	require.ErrorIs(t, RequireKBTransfer(changed, source, target, KBTransferMove), ErrForbidden)
}

func TestTransferAPICapabilitiesRemainSeparate(t *testing.T) {
	source, target := &types.KnowledgeBase{ID: "source", TenantID: 1}, &types.KnowledgeBase{ID: "target", TenantID: 1}
	for _, tc := range []struct {
		operation  KBTransferOperation
		capability string
		create     bool
		ids        []string
		allowed    bool
	}{
		{KBTransferClone, "manage_kbs", false, []string{"source", "target"}, true},
		{KBTransferClone, "ingest", false, []string{"source", "target"}, false},
		{KBTransferMove, "ingest", false, []string{"source", "target"}, true},
		{KBTransferMove, "manage_kbs", false, []string{"source", "target"}, false},
		{KBTransferMove, "ingest", false, []string{"source"}, false},
		{KBTransferClone, "manage_kbs", true, []string{"source"}, true},
	} {
		t.Run(
			fmt.Sprintf("%s/%s/create=%v/ids=%d", tc.operation, tc.capability, tc.create, len(tc.ids)),
			func(t *testing.T) {
				ctx, err := WithKBTransferTask(context.Background(), source, target, 1, tc.operation, "task", tc.create)
				require.NoError(t, err)
				ctx = types.WithTenantAPIKeyScope(
					ctx,
					types.TenantAPIKeyScope{Capabilities: types.StringArray{tc.capability}, KnowledgeBaseIDs: tc.ids},
				)
				require.Equal(t, tc.allowed, RequireKBTransfer(ctx, source, target, tc.operation) == nil)
			},
		)
	}
}
