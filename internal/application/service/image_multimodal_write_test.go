package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
)

// The multimodal task persists OCR/caption chunks through chunkService, whose
// validateChunkWrites → requireKBWrite needs an explicit grant that async
// workers never receive from middleware. grantKBWrite supplies it — and must
// supply it *before* the first write, or the VLM output is dropped and
// indexChunks (which also writes the image_vector child) never runs at all.
func TestImageMultimodalGrantKBWrite(t *testing.T) {
	t.Parallel()

	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 7}
	payload := &types.ImageMultimodalPayload{TenantID: 7, KnowledgeBaseID: "kb-1"}

	t.Run("grants editor write on the task KB", func(t *testing.T) {
		svc := &ImageMultimodalService{kbService: &orphanKBService{kb: kb}}
		ctx := svc.grantKBWrite(context.Background(), payload)
		if !access.HasKBGrant(ctx, kb.ID, kb.TenantID, types.OrgRoleEditor) {
			t.Fatal("task ctx must carry a KB editor grant, or chunk writes are rejected")
		}
	})

	// Re-granting must not rebuild the ctx: downstream stages put their own
	// values on it (tenant, language, span handles) and would lose them.
	t.Run("is idempotent over an already granted ctx", func(t *testing.T) {
		svc := &ImageMultimodalService{kbService: &orphanKBService{kb: kb}}
		type markerKey struct{}
		marked := context.WithValue(svc.grantKBWrite(context.Background(), payload),
			markerKey{}, "keep")
		if got := svc.grantKBWrite(marked, payload).Value(markerKey{}); got != "keep" {
			t.Fatalf("re-grant must preserve ctx values, got %v", got)
		}
	})

	t.Run("leaves ctx untouched when KB cannot be resolved", func(t *testing.T) {
		for name, svc := range map[string]*ImageMultimodalService{
			"no kb service": {},
			"missing KB":    {kbService: &orphanKBService{err: repository.ErrKnowledgeBaseNotFound}},
		} {
			base := context.Background()
			if got := svc.grantKBWrite(base, payload); got != base {
				t.Fatalf("%s: expected the original ctx back, got a derived one", name)
			}
		}
	})

	// A payload tenant that does not own the KB must not be granted — the write
	// should fail loudly rather than be authorized across tenants.
	t.Run("refuses a foreign tenant", func(t *testing.T) {
		svc := &ImageMultimodalService{kbService: &orphanKBService{kb: kb}}
		foreign := &types.ImageMultimodalPayload{TenantID: 9, KnowledgeBaseID: "kb-1"}
		ctx := svc.grantKBWrite(context.Background(), foreign)
		if access.HasKBGrant(ctx, kb.ID, kb.TenantID, types.OrgRoleEditor) {
			t.Fatal("must not grant KB write for a tenant that does not own the KB")
		}
	})
}
