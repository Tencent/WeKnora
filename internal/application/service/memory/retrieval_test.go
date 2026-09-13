package memory

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// The point of phase two is that memory changes what gets retrieved, not only
// what the answer prompt says. These tests pin the behaviours that make that
// true, and the ones that keep it from becoming a way to assert wrong things
// about a person or to read somebody else's data.

func TestRetrievalContextCarriesWhoIsAsking(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")

	seedDigest(t, svc, ctx, "## 用户画像\n- 在做医学影像的后端\n\n## 用户偏好\n- 回答简短\n")

	memCtx := svc.RetrievalContextFor(ctx)
	require.False(t, memCtx.Empty())
	require.Contains(t, memCtx.Background, "医学影像")
	require.NotContains(t, memCtx.Background, "回答简短",
		"a preference about answer style has no business inside a search query")
}

func TestRetrievalContextIsEmptyWhenConditioningIsOff(t *testing.T) {
	svc, _, tenantRepo := newMemoryHarness(t)
	ctx := enabledCtx(t, tenantRepo, 1, "alice")
	seedDigest(t, svc, ctx, "## 用户画像\n- 在做医学影像的后端\n")

	off := false
	tenantRepo.set(1, &types.MemoryConfig{
		Enabled:               true,
		WriteMode:             types.MemoryWriteAuto,
		RetrievalConditioning: &off,
	})

	require.True(t, svc.RetrievalContextFor(ctx).Empty())
	// The answer prompt is a separate switch, and turning off conditioning
	// must not quietly turn off memory itself.
	require.Contains(t, svc.Recall(ctx, "医学影像").Prompt, "医学影像")
}

func scopeFor(t *testing.T, ctx context.Context) interfaces.MemoryScope {
	t.Helper()
	scope, err := ResolveScope(ctx)
	require.NoError(t, err)
	return scope
}
