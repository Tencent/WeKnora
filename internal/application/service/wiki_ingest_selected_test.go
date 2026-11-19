package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestWikiIngestSelectedDocumentUsesExistingClaimsAndFollowUp(t *testing.T) {
	for _, mode := range []string{"lite", "redis"} {
		for _, interrupt := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/complete", true: "/interrupted"}[interrupt], func(t *testing.T) {
				var rdb *redis.Client
				if mode == "redis" {
					server := miniredis.RunT(t)
					rdb = redis.NewClient(&redis.Options{Addr: server.Addr()})
					t.Cleanup(func() { require.NoError(t, rdb.Close()) })
				}
				svc, db := newWikiLookupRecoveryService(t, "kb", rdb)
				ordinary := seedWikiLookupTask(t, svc, db, "kb", "untouched")
				seedWikiLookupTask(t, svc, db, "kb", "selected")
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				lookup := svc.knowledgeSvc.(*wikiKnowledgeLookupStub)
				if interrupt {
					lookup.lookup = func(ctx context.Context, id string) (*types.Knowledge, error) {
						cancel()
						return svc.knowledgeRepo.GetKnowledgeByIDOnly(ctx, id)
					}
				}
				raw, err := json.Marshal(WikiIngestPayload{TenantID: 7, KnowledgeBaseID: "kb", KnowledgeID: "selected"})
				require.NoError(t, err)
				require.NoError(t, svc.ProcessWikiIngest(ctx, asynq.NewTask(types.TypeWikiIngest, raw)))
				var untouched types.TaskPendingOp
				require.NoError(t, db.Where("dedup_key=?", "untouched").First(&untouched).Error)
				require.Zero(t, untouched.FailCount)
				require.Nil(t, untouched.ClaimedAt)
				if interrupt {
					var selected types.TaskPendingOp
					require.NoError(t, db.Where("dedup_key=?", "selected").First(&selected).Error)
					require.Equal(t, 1, selected.FailCount)
					require.Nil(t, selected.ClaimedAt)
				} else {
					var count int64
					require.NoError(t, db.Model(&types.TaskPendingOp{}).
						Where("dedup_key=?", "selected").Count(&count).Error)
					require.Zero(t, count)
				}
				for _, task := range svc.task.(*wikiGuardTaskQueue).tasks {
					if task.Type() != types.TypeWikiIngest {
						continue
					}
					var follow WikiIngestPayload
					require.NoError(t, json.Unmarshal(task.Payload(), &follow))
					require.Empty(t, follow.KnowledgeID, "ordinary fan-out must not stay pinned to the selected key")
				}
				lookup.lookup = svc.knowledgeRepo.GetKnowledgeByIDOnly
				require.NoError(t, svc.ProcessWikiIngest(context.Background(), ordinary))
				count, err := svc.pendingRepo.PendingCount(context.Background(), wikiTaskType, wikiTaskScope, "kb")
				require.NoError(t, err)
				require.Zero(t, count)
			})
		}
	}
}

func TestWikiIngestRejectsInvalidSelectedIdentityBeforeAccessingServices(t *testing.T) {
	for _, id := range []string{" invalid ", strings.Repeat("x", 65)} {
		raw, err := json.Marshal(WikiIngestPayload{KnowledgeID: id})
		require.NoError(t, err)
		svc := &wikiIngestService{}
		require.Error(t, svc.ProcessWikiIngest(context.Background(), asynq.NewTask(types.TypeWikiIngest, raw)))
	}
}
