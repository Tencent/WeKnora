package service

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// The KB double models retrieval's filter-before-TopK contract; messages,
// ownership checks, keyword search and Q&A partner lookup use a real database.
type historySearchKB struct {
	interfaces.KnowledgeBaseService
	candidates []*types.SearchResult
	params     types.SearchParams
}

func (k *historySearchKB) HybridSearch(
	_ context.Context, _ string, p types.SearchParams,
) ([]*types.SearchResult, error) {
	k.params = p
	var found []*types.SearchResult
	for _, item := range k.candidates {
		if slices.Contains(p.ExcludeKnowledgeIDs, item.KnowledgeID) {
			continue
		}
		found = append(found, item)
		if len(found) == p.MatchCount {
			break
		}
	}
	return found, nil
}

func newHistorySearchFixture(t *testing.T) (*messageService, *historySearchKB, context.Context, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&types.Session{}, &types.Message{}, &types.MessageArtifactRecord{}))
	kb := &historySearchKB{}
	for _, session := range []types.Session{
		{ID: "current", TenantID: 7, UserID: "alice"},
		{ID: "older", TenantID: 7, UserID: "alice"},
		{ID: "colleague", TenantID: 7, UserID: "bob"},
		{ID: "other-tenant", TenantID: 8, UserID: "alice"},
	} {
		require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Create(&session).Error)
	}
	add := func(sid string, count int) {
		for i := 0; i < count; i++ {
			id := fmt.Sprintf("%s-%d", sid, i)
			score := 1 - float64(len(kb.candidates))/1000
			kb.candidates = append(kb.candidates, &types.SearchResult{KnowledgeID: id, Score: score})
			stamp := time.Unix(2000-int64(len(kb.candidates)), 0)
			require.NoError(t, db.Create(&types.Message{
				SessionID: sid, Role: "assistant", Content: "deployment config answer",
				RequestID: id, KnowledgeID: id, CreatedAt: stamp,
			}).Error)
			require.NoError(t, db.Create(&types.Message{
				SessionID: sid, Role: "user", Content: "question", RequestID: id, CreatedAt: stamp,
			}).Error)
		}
	}
	add("current", 70)
	add("colleague", 1)
	add("other-tenant", 1)
	add("older", 25)
	svc := &messageService{messageRepo: repository.NewMessageRepository(db), kbService: kb}
	ctx := context.WithValue(t.Context(), types.TenantIDContextKey, uint64(7))
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, &types.Tenant{
		ChatHistoryConfig: &types.ChatHistoryConfig{
			Enabled: true, EmbeddingModelID: "embed", KnowledgeBaseID: "history",
		},
		RetrievalConfig: &types.RetrievalConfig{EmbeddingTopK: 30},
	})
	return svc, kb, ctx, db
}

func TestMessageSearchExcludesSessionBeforeRetrievalLimits(t *testing.T) {
	for _, mode := range []types.MessageSearchMode{
		types.MessageSearchModeKeyword, types.MessageSearchModeVector, types.MessageSearchModeHybrid,
	} {
		for _, limit := range []int{0, -1, 1, 5, 20} {
			t.Run(fmt.Sprintf("%s/limit=%d", mode, limit), func(t *testing.T) {
				svc, kb, ctx, _ := newHistorySearchFixture(t)
				result, err := svc.SearchMessages(ctx, &types.MessageSearchParams{
					Query: "deployment", Mode: mode, Limit: limit, OwnerID: "alice", ExcludeSessionID: "current",
				})
				require.NoError(t, err)
				want := limit
				if want <= 0 {
					want = 20
				}
				require.Equal(t, want, result.Total)
				require.Len(t, result.Items, want)
				for _, item := range result.Items {
					require.Equal(t, "older", item.SessionID)
					require.Equal(t, "question", item.QueryContent)
					require.Equal(t, "deployment config answer", item.AnswerContent)
				}
				if mode != types.MessageSearchModeKeyword {
					require.Len(t, kb.params.ExcludeKnowledgeIDs, 70)
					require.Equal(t, 30, kb.params.MatchCount, "do not fix starvation by increasing TopK")
				}
			})
		}
	}
}

func TestMessageSearchSessionExclusionControls(t *testing.T) {
	for _, mode := range []types.MessageSearchMode{
		types.MessageSearchModeKeyword, types.MessageSearchModeVector, types.MessageSearchModeHybrid,
	} {
		for _, tc := range []struct {
			name, exclude string
			onlyCurrent   bool
			sessionIDs    []string
			want          int
			wantSession   string
		}{
			{name: "no exclusion", want: 5, wantSession: "current"},
			{name: "current only", exclude: "current", onlyCurrent: true, want: 0},
			{name: "allowlist excludes current", exclude: "current", sessionIDs: []string{"current"}, want: 0},
			{
				name: "allowlist includes older", exclude: "current",
				sessionIDs: []string{"current", "older"}, want: 5, wantSession: "older",
			},
		} {
			t.Run(string(mode)+"/"+tc.name, func(t *testing.T) {
				svc, kb, ctx, db := newHistorySearchFixture(t)
				if tc.onlyCurrent {
					require.NoError(t, db.Where("session_id != ?", "current").Delete(&types.Message{}).Error)
					kb.candidates = kb.candidates[:70]
				}
				result, err := svc.SearchMessages(ctx, &types.MessageSearchParams{
					Query: "deployment", Mode: mode, Limit: 5, OwnerID: "alice",
					ExcludeSessionID: tc.exclude, SessionIDs: tc.sessionIDs,
				})
				require.NoError(t, err)
				require.Equal(t, tc.want, result.Total)
				for _, item := range result.Items {
					require.Equal(t, tc.wantSession, item.SessionID)
				}
				if tc.exclude == "" {
					require.Empty(t, kb.params.ExcludeKnowledgeIDs)
				}
			})
		}
	}
}

// Hybrid search must still recall an exchange with no literal keyword match.
func TestMessageSearchExclusionPreservesSemanticOnlyHistory(t *testing.T) {
	for _, mode := range []types.MessageSearchMode{types.MessageSearchModeVector, types.MessageSearchModeHybrid} {
		t.Run(string(mode), func(t *testing.T) {
			svc, _, ctx, db := newHistorySearchFixture(t)
			require.NoError(t, db.Model(&types.Message{}).Where("session_id = ? AND role = ?", "older", "assistant").
				Update("content", "release settings").Error)
			result, err := svc.SearchMessages(ctx, &types.MessageSearchParams{
				Query: "deployment", Mode: mode, Limit: 5, OwnerID: "alice", ExcludeSessionID: "current",
			})
			require.NoError(t, err)
			require.Len(t, result.Items, 5)
			for _, item := range result.Items {
				require.Equal(t, "older", item.SessionID)
				require.Equal(t, "release settings", item.AnswerContent)
			}
		})
	}
}
