package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type wikiSearchKBService struct {
	interfaces.KnowledgeBaseService
	byID map[string]*types.KnowledgeBase
}

func (s *wikiSearchKBService) GetKnowledgeBasesByIDsOnly(
	_ context.Context, ids []string,
) ([]*types.KnowledgeBase, error) {
	out := make([]*types.KnowledgeBase, 0, len(ids))
	for _, id := range ids {
		if kb, ok := s.byID[id]; ok {
			out = append(out, kb)
		}
	}
	return out, nil
}

type wikiSearchShareService struct {
	interfaces.KBShareService
	allowed map[string]map[uint64]bool
	err     error
}

func (s *wikiSearchShareService) CheckTenantKBPermission(
	_ context.Context, kbID string, callerTenantID uint64, _ types.TenantRole,
) (types.OrgMemberRole, bool, error) {
	if s.err != nil {
		return "", false, s.err
	}
	return types.OrgRoleViewer, s.allowed[kbID][callerTenantID], nil
}

func makeWikiSearchKB(id string, tenantID uint64) *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID:               id,
		TenantID:         tenantID,
		IndexingStrategy: types.IndexingStrategy{WikiEnabled: true},
	}
}

func setupWikiSearchService(
	t *testing.T,
	kbs map[string]*types.KnowledgeBase,
	share interfaces.KBShareService,
) (context.Context, interfaces.WikiPageService, interfaces.WikiPageRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.WikiFolder{}, &types.WikiPage{}, &types.WikiPageRevision{}))
	repo := repository.NewWikiPageRepository(db)
	svc := NewWikiPageService(repo, nil, &wikiSearchKBService{byID: kbs}, nil, nil, share)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	return ctx, svc, repo
}

func TestSearchPagesAcross_RanksAcrossEnabledKBs(t *testing.T) {
	ctx, svc, repo := setupWikiSearchService(t, map[string]*types.KnowledgeBase{
		"kb-a": makeWikiSearchKB("kb-a", 1),
		"kb-b": makeWikiSearchKB("kb-b", 1),
	}, nil)

	titleHit := &types.WikiPage{
		ID: "p-title", TenantID: 1, KnowledgeBaseID: "kb-b",
		Slug: "entity/wangxin", Title: "王新", PageType: types.WikiPageTypeEntity,
		Status: types.WikiPageStatusPublished, Content: "other", Version: 1,
		CreatedAt: time.Now(), UpdatedAt: time.Now().Add(-time.Hour),
	}
	contentHit := &types.WikiPage{
		ID: "p-body", TenantID: 1, KnowledgeBaseID: "kb-a",
		Slug: "entity/huawei", Title: "华为", PageType: types.WikiPageTypeEntity,
		Status: types.WikiPageStatusPublished, Content: "mentions 王新", Version: 1,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, repo.Create(ctx, titleHit))
	require.NoError(t, repo.Create(ctx, contentHit))

	pages, err := svc.SearchPagesAcross(ctx, []string{"kb-a", "kb-b"}, "王新", 10)
	require.NoError(t, err)
	require.Len(t, pages, 2)
	assert.Equal(t, "p-title", pages[0].ID)
	assert.Equal(t, "p-body", pages[1].ID)
}

func TestSearchPagesAcross_RejectsNonWikiKB(t *testing.T) {
	ctx, svc, _ := setupWikiSearchService(t, map[string]*types.KnowledgeBase{
		"kb-wiki": makeWikiSearchKB("kb-wiki", 1),
		"kb-doc": {
			ID: "kb-doc", TenantID: 1,
			IndexingStrategy: types.IndexingStrategy{VectorEnabled: true},
		},
	}, nil)

	_, err := svc.SearchPagesAcross(ctx, []string{"kb-wiki", "kb-doc"}, "q", 10)
	require.Error(t, err)
	app, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrBadRequest, app.Code)
}

func TestSearchPagesAcross_MissingKBIsNotFound(t *testing.T) {
	ctx, svc, _ := setupWikiSearchService(t, map[string]*types.KnowledgeBase{
		"kb-wiki": makeWikiSearchKB("kb-wiki", 1),
	}, nil)

	_, err := svc.SearchPagesAcross(ctx, []string{"kb-wiki", "kb-missing"}, "q", 10)
	require.Error(t, err)
	app, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrNotFound, app.Code)
}

func TestSearchPagesAcross_ForeignTenantWithoutShareIsNotFound(t *testing.T) {
	ctx, svc, _ := setupWikiSearchService(t, map[string]*types.KnowledgeBase{
		"kb-foreign": makeWikiSearchKB("kb-foreign", 99),
	}, &wikiSearchShareService{})

	_, err := svc.SearchPagesAcross(ctx, []string{"kb-foreign"}, "q", 10)
	require.Error(t, err)
	app, ok := apperrors.IsAppError(err)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrNotFound, app.Code)
}

func TestSearchPagesAcross_ForeignTenantWithShareOK(t *testing.T) {
	ctx, svc, repo := setupWikiSearchService(t, map[string]*types.KnowledgeBase{
		"kb-foreign": makeWikiSearchKB("kb-foreign", 99),
	}, &wikiSearchShareService{
		allowed: map[string]map[uint64]bool{"kb-foreign": {1: true}},
	})

	page := &types.WikiPage{
		ID: "p-shared", TenantID: 99, KnowledgeBaseID: "kb-foreign",
		Slug: "concept/share", Title: "共享", PageType: types.WikiPageTypeConcept,
		Status: types.WikiPageStatusPublished, Content: "body", Version: 1,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	require.NoError(t, repo.Create(ctx, page))

	pages, err := svc.SearchPagesAcross(ctx, []string{"kb-foreign"}, "共享", 10)
	require.NoError(t, err)
	require.Len(t, pages, 1)
	assert.Equal(t, "p-shared", pages[0].ID)
}

func TestIsInvalidWikiSearchQuery(t *testing.T) {
	assert.True(t, isInvalidWikiSearchQuery(fmt.Errorf("pq: invalid regular expression: quantifier operand invalid")))
	assert.False(t, isInvalidWikiSearchQuery(fmt.Errorf("connection refused")))
}
