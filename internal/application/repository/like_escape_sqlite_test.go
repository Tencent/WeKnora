package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// SQLite — the database behind the Lite build — has no default LIKE escape
// character, while Postgres defaults to backslash. escapeLikeKeyword /
// escapeLikePattern emit \% and \_ on the assumption that the pattern is
// consumed by a predicate with an explicit ESCAPE clause; without one, SQLite
// searches for a literal backslash and silently returns zero rows (and a zero
// total) for any keyword containing %, _ or \.
//
// These tests pin that invariant at each fixed call site. Each case seeds a
// row whose name/content contains the literal character plus a decoy row that
// the same keyword would match if the character were still a wildcard, so a
// missing ESCAPE fails on "no rows" and a missing escape fails on "extra row".

// likeEscapeTestDDL creates the join targets of the keyword searches that are
// not part of the AutoMigrate set below.
const likeEscapeTestDDL = `
CREATE TABLE IF NOT EXISTS users (
    id VARCHAR(36) PRIMARY KEY,
    username VARCHAR(100) NOT NULL DEFAULT '',
    email VARCHAR(255) NOT NULL DEFAULT '',
    deleted_at DATETIME
);
CREATE TABLE IF NOT EXISTS im_channel_sessions (
    session_id VARCHAR(36),
    platform VARCHAR(32),
    chat_id VARCHAR(255),
    thread_id VARCHAR(255),
    user_id VARCHAR(64),
    agent_id VARCHAR(64),
    im_channel_id VARCHAR(64)
);
`

func setupLikeEscapeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + uuid.New().String() + "?mode=memory&cache=shared&_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	// The knowledges / knowledge_bases schemas are inlined by the existing
	// helpers (AutoMigrate does not map their jsonb columns onto SQLite).
	require.NoError(t, db.Exec(knowledgesTestDDL).Error)
	require.NoError(t, db.Exec(knowledgeBasesTestDDL).Error)
	require.NoError(t, db.Exec(likeEscapeTestDDL).Error)
	require.NoError(t, db.AutoMigrate(
		&types.KnowledgeTag{},
		&types.Chunk{},
		&types.Session{},
		&types.Tenant{},
		&types.TenantMember{},
	))
	return db
}

func seedLikeEscapeKnowledgeBase(t *testing.T, db *gorm.DB, tenantID uint64, kbID string) {
	t.Helper()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_bases (id, name, tenant_id, type, embedding_model_id, summary_model_id)
		VALUES (?, 'like-escape-kb', ?, 'document', 'test-embedding', 'test-summary')
	`, kbID, tenantID).Error)
}

func seedLikeEscapeKnowledge(
	t *testing.T, db *gorm.DB, tenantID uint64, kbID, id, fileName, title string,
) {
	t.Helper()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, file_name, source, parse_status)
		VALUES (?, ?, ?, 'file', ?, ?, 'manual', 'completed')
	`, id, tenantID, kbID, title, fileName).Error)
}

func seedLikeEscapeKnowledgeSet(t *testing.T, db *gorm.DB, tenantID uint64, kbID string) (literalID string) {
	t.Helper()
	literalID = uuid.New().String()
	// Underscore: literal "IMG_2534" must not match "IMGx2534".
	seedLikeEscapeKnowledge(t, db, tenantID, kbID, literalID, "IMG_2534.png", "IMG_2534")
	seedLikeEscapeKnowledge(t, db, tenantID, kbID, uuid.New().String(), "IMGx2534.png", "IMGx2534")
	// Percent: literal "100%" must not match "1000".
	seedLikeEscapeKnowledge(t, db, tenantID, kbID, uuid.New().String(), "100% done.pdf", "100% done")
	seedLikeEscapeKnowledge(t, db, tenantID, kbID, uuid.New().String(), "1000 done.pdf", "1000 done")
	return literalID
}

// Covers the document-list keyword filter in applyKnowledgeListFilter
// (reused by the agent list_documents tool and the MCP retrieve tool).
func TestListPagedKnowledgeKeywordTreatsLikeWildcardsLiterally(t *testing.T) {
	db := setupLikeEscapeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()
	const tenantID = uint64(1)
	kbID := uuid.New().String()
	seedLikeEscapeKnowledgeBase(t, db, tenantID, kbID)
	literalID := seedLikeEscapeKnowledgeSet(t, db, tenantID, kbID)
	page := &types.Pagination{Page: 1, PageSize: 50}

	for _, keyword := range []string{"IMG_2534", "100%"} {
		rows, total, err := repo.ListPagedKnowledgeByKnowledgeBaseID(
			ctx, tenantID, kbID, page, types.KnowledgeListFilter{Keyword: keyword},
		)
		require.NoError(t, err, "keyword %q", keyword)
		require.Len(t, rows, 1, "keyword %q must match the literal row only", keyword)
		assert.Equal(t, int64(1), total, "keyword %q count", keyword)
		if keyword == "IMG_2534" {
			assert.Equal(t, literalID, rows[0].ID)
		}
	}
}

// Covers SearchKnowledge.
func TestSearchKnowledgeKeywordTreatsLikeWildcardsLiterally(t *testing.T) {
	db := setupLikeEscapeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()
	const tenantID = uint64(1)
	kbID := uuid.New().String()
	seedLikeEscapeKnowledgeBase(t, db, tenantID, kbID)
	literalID := seedLikeEscapeKnowledgeSet(t, db, tenantID, kbID)

	rows, _, err := repo.SearchKnowledge(ctx, tenantID, "IMG_2534", 0, 10, nil)
	require.NoError(t, err)
	require.Len(t, rows, 1, "underscore must match literally, not as a wildcard")
	assert.Equal(t, literalID, rows[0].ID)

	rows, _, err = repo.SearchKnowledge(ctx, tenantID, "100%", 0, 10, nil)
	require.NoError(t, err)
	require.Len(t, rows, 1, "percent must match literally, not as a wildcard")
	assert.Contains(t, rows[0].Title, "100%")
}

// Covers SearchKnowledgeInScopes (GET /api/v1/knowledge/search).
func TestSearchKnowledgeInScopesKeywordTreatsLikeWildcardsLiterally(t *testing.T) {
	db := setupLikeEscapeTestDB(t)
	repo := NewKnowledgeRepository(db).(*knowledgeRepository)
	ctx := context.Background()
	const tenantID = uint64(1)
	kbID := uuid.New().String()
	seedLikeEscapeKnowledgeBase(t, db, tenantID, kbID)
	literalID := seedLikeEscapeKnowledgeSet(t, db, tenantID, kbID)
	scopes := []types.KnowledgeSearchScope{{TenantID: tenantID, KBID: kbID}}

	rows, hasMore, total, err := repo.SearchKnowledgeInScopes(ctx, scopes, "IMG_2534", 0, 10, nil)
	require.NoError(t, err)
	require.Len(t, rows, 1, "underscore must match literally, not as a wildcard")
	assert.Equal(t, literalID, rows[0].ID)
	assert.False(t, hasMore)
	assert.Equal(t, int64(1), total)

	rows, _, total, err = repo.SearchKnowledgeInScopes(ctx, scopes, "100%", 0, 10, nil)
	require.NoError(t, err)
	require.Len(t, rows, 1, "percent must match literally, not as a wildcard")
	assert.Equal(t, int64(1), total)
}

// Covers both the count and the data query of the tag search.
func TestListTagsByKBKeywordTreatsLikeWildcardsLiterally(t *testing.T) {
	db := setupLikeEscapeTestDB(t)
	repo := NewKnowledgeTagRepository(db).(*knowledgeTagRepository)
	ctx := context.Background()
	const tenantID = uint64(1)
	kbID := uuid.New().String()

	literalID := uuid.New().String()
	require.NoError(t, db.Create(&types.KnowledgeTag{
		ID: literalID, TenantID: tenantID, KnowledgeBaseID: kbID, Name: "v1_1",
	}).Error)
	require.NoError(t, db.Create(&types.KnowledgeTag{
		ID: uuid.New().String(), TenantID: tenantID, KnowledgeBaseID: kbID, Name: "v1x1",
	}).Error)

	tags, total, err := repo.ListByKB(ctx, tenantID, kbID, &types.Pagination{Page: 1, PageSize: 50}, "v1_1")
	require.NoError(t, err)
	require.Len(t, tags, 1, "underscore must match literally, not as a wildcard")
	assert.Equal(t, literalID, tags[0].ID)
	assert.Equal(t, int64(1), total, "the count query must use the same escape")
}

// Covers both branches of SearchTenants (with and without a tenant id).
func TestSearchTenantsKeywordTreatsLikeWildcardsLiterally(t *testing.T) {
	db := setupLikeEscapeTestDB(t)
	repo := NewTenantRepository(db).(*tenantRepository)
	ctx := context.Background()

	literal := &types.Tenant{Name: "Acme_Prod", Description: "literal underscore", Status: "active"}
	require.NoError(t, db.Create(literal).Error)
	require.NoError(t, db.Create(&types.Tenant{
		Name: "AcmeXProd", Description: "wildcard decoy", Status: "active",
	}).Error)

	// keyword-only branch (tenantID = 0).
	tenants, total, err := repo.SearchTenants(ctx, "Acme_Prod", 0, 1, 20)
	require.NoError(t, err)
	require.Len(t, tenants, 1, "underscore must match literally, not as a wildcard")
	assert.Equal(t, literal.ID, tenants[0].ID)
	assert.Equal(t, int64(1), total)

	// tenantID + keyword branch.
	tenants, total, err = repo.SearchTenants(ctx, "Acme_Prod", literal.ID, 1, 20)
	require.NoError(t, err)
	require.Len(t, tenants, 1)
	assert.Equal(t, literal.ID, tenants[0].ID)
	assert.Equal(t, int64(1), total)
}

// Covers both member-search paths (count and page) of the email/username join.
func TestTenantMemberSearchTreatsLikeWildcardsLiterally(t *testing.T) {
	db := setupLikeEscapeTestDB(t)
	repo := NewTenantMemberRepository(db).(*tenantMemberRepository)
	ctx := context.Background()
	const tenantID = uint64(1)

	seedMember := func(id, username, email string) {
		t.Helper()
		require.NoError(t, db.Exec(
			`INSERT INTO users (id, username, email) VALUES (?, ?, ?)`, id, username, email).Error)
		require.NoError(t, db.Create(&types.TenantMember{
			UserID: id, TenantID: tenantID, Role: types.TenantRoleContributor,
			Status: types.TenantMemberStatusActive,
		}).Error)
	}
	literalID := uuid.New().String()
	seedMember(literalID, "ann_bob", "ann_bob@example.com")
	seedMember(uuid.New().String(), "annxbob", "annxbob@example.com")

	total, err := repo.CountFilteredByTenant(ctx, tenantID, "ann_bob")
	require.NoError(t, err)
	assert.Equal(t, int64(1), total, "underscore must match literally, not as a wildcard")

	members, err := repo.ListPagedByTenant(ctx, tenantID, "ann_bob", 0, 20)
	require.NoError(t, err)
	require.Len(t, members, 1)
	assert.Equal(t, literalID, members[0].UserID)
}

// Covers the session sidebar keyword filter in QueryPaged.
func TestSessionQueryPagedKeywordTreatsLikeWildcardsLiterally(t *testing.T) {
	db := setupLikeEscapeTestDB(t)
	repo := NewSessionRepository(db)
	ctx := context.Background()
	const tenantID = uint64(7)
	const userID = "web_user:alice"

	literal := &types.Session{TenantID: tenantID, UserID: userID, Title: "Deploy_Plan v1"}
	require.NoError(t, db.Create(literal).Error)
	require.NoError(t, db.Create(&types.Session{
		TenantID: tenantID, UserID: userID, Title: "DeployXPlan v1",
	}).Error)

	rows, total, err := repo.QueryPaged(ctx, &types.SessionListQuery{
		TenantID: tenantID, UserID: userID, Keyword: "Deploy_Plan", Page: 1, PageSize: 20,
	})
	require.NoError(t, err)
	require.Len(t, rows, 1, "underscore must match literally, not as a wildcard")
	assert.Equal(t, literal.ID, rows[0].ID)
	assert.Equal(t, int64(1), total, "the count query must use the same escape")
}

// Covers the document chunk/Faq keyword filter, which previously did not
// escape the keyword at all (so % and _ acted as wildcards).
func TestListPagedChunksKeywordTreatsLikeWildcardsLiterally(t *testing.T) {
	db := setupLikeEscapeTestDB(t)
	repo := NewChunkRepository(db).(*chunkRepository)
	ctx := context.Background()
	const tenantID = uint64(1)
	knowledgeID := uuid.New().String()
	kbID := uuid.New().String()

	literal := &types.Chunk{
		ID: uuid.New().String(), TenantID: tenantID, KnowledgeID: knowledgeID,
		KnowledgeBaseID: kbID, Content: "config v1_1 is active",
		ChunkType: types.ChunkTypeText, Status: int(types.ChunkStatusIndexed), IsEnabled: true,
	}
	require.NoError(t, db.Create(literal).Error)
	require.NoError(t, db.Create(&types.Chunk{
		ID: uuid.New().String(), TenantID: tenantID, KnowledgeID: knowledgeID,
		KnowledgeBaseID: kbID, Content: "config v1x1 is active",
		ChunkType: types.ChunkTypeText, Status: int(types.ChunkStatusIndexed), IsEnabled: true,
	}).Error)

	chunks, total, err := repo.ListPagedChunksByKnowledgeID(
		ctx, tenantID, knowledgeID, &types.Pagination{Page: 1, PageSize: 50},
		[]types.ChunkType{types.ChunkTypeText}, nil, "v1_1", "", "",
		types.KnowledgeBaseTypeDocument, nil,
	)
	require.NoError(t, err)
	require.Len(t, chunks, 1, "underscore must match literally, not as a wildcard")
	assert.Equal(t, literal.ID, chunks[0].ID)
	assert.Equal(t, int64(1), total)
}
