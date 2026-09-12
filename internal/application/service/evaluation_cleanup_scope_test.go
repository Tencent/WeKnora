package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestEvaluationCleanupPinsCreatedResourceBindings(t *testing.T) {
	for _, scenario := range []string{"canceled", "moved", "missing", "foreign-tenant"} {
		t.Run(scenario, func(t *testing.T) {
			f := newDocumentWriteFixture(t)
			service := &EvaluationService{knowledgeService: f.svc}
			ctx := f.ctx
			switch scenario {
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "moved":
				require.NoError(
					t,
					f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").Update("knowledge_base_id", "other").Error,
				)
			case "missing":
				require.NoError(t, f.db.Where("id = ?", "doc").Delete(&types.Knowledge{}).Error)
			case "foreign-tenant":
				require.NoError(t, f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").Update("tenant_id", 8).Error)
			}
			err := service.deleteEvaluationKnowledge(ctx, "kb", "doc")
			if scenario == "moved" {
				requireForbiddenWrite(t, err)
			} else {
				require.NoError(t, err)
			}
			var count int64
			require.NoError(t, f.db.Model(&types.Knowledge{}).Where("id = ?", "doc").Count(&count).Error)
			if scenario == "moved" || scenario == "foreign-tenant" {
				require.EqualValues(t, 1, count, "cleanup must preserve documents outside the captured binding")
				require.Zero(t, f.chunkRepo.writes)
				require.Empty(t, f.files.deleted)
			} else {
				require.Zero(t, count)
			}
		})
	}
}
