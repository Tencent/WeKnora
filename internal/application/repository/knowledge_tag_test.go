package repository

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

const knowledgeTagTestDDL = `
CREATE TABLE IF NOT EXISTS knowledges (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    type VARCHAR(50) NOT NULL DEFAULT 'file',
    title VARCHAR(255) NOT NULL DEFAULT '',
    parse_status VARCHAR(50) NOT NULL DEFAULT 'completed',
    deleted_at DATETIME
);
CREATE TABLE IF NOT EXISTS knowledge_tags (
    id VARCHAR(36) PRIMARY KEY,
    seq_id INTEGER NOT NULL,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    name VARCHAR(128) NOT NULL,
    color VARCHAR(32),
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE IF NOT EXISTS knowledge_tag_relations (
    knowledge_id VARCHAR(36) NOT NULL,
    tag_id VARCHAR(36) NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (knowledge_id, tag_id)
);
CREATE TABLE IF NOT EXISTS chunks (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    knowledge_base_id VARCHAR(36) NOT NULL,
    tag_id VARCHAR(36),
    deleted_at DATETIME
);
`

func setupKnowledgeTagTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupKnowledgeTestDB(t)
	require.NoError(t, db.Exec(knowledgeTagTestDDL).Error)
	return db
}

func seedKnowledgeTagFixture(t *testing.T, db *gorm.DB) (kbID, knowledgeID, tagA, tagB string) {
	t.Helper()
	kbID = uuid.New().String()
	knowledgeID = uuid.New().String()
	tagA = uuid.New().String()
	tagB = uuid.New().String()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, parse_status)
		VALUES (?, 1, ?, 'file', 'doc', 'completed')
	`, knowledgeID, kbID).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_tags (id, seq_id, tenant_id, knowledge_base_id, name)
		VALUES (?, 1, 1, ?, 'alpha'), (?, 2, 1, ?, 'beta')
	`, tagA, kbID, tagB, kbID).Error)
	return kbID, knowledgeID, tagA, tagB
}

func TestSetKnowledgeTags_DedupesAndReplaces(t *testing.T) {
	db := setupKnowledgeTagTestDB(t)
	repo := &knowledgeRepository{db: db}
	_, knowledgeID, tagA, tagB := seedKnowledgeTagFixture(t, db)
	ctx := context.Background()

	err := repo.SetKnowledgeTags(ctx, knowledgeID, []string{tagA, tagA, tagB})
	require.NoError(t, err)

	var count int64
	require.NoError(t, db.Model(&types.KnowledgeTagRelation{}).
		Where("knowledge_id = ?", knowledgeID).Count(&count).Error)
	assert.Equal(t, int64(2), count)

	err = repo.SetKnowledgeTags(ctx, knowledgeID, []string{tagB})
	require.NoError(t, err)
	require.NoError(t, db.Model(&types.KnowledgeTagRelation{}).
		Where("knowledge_id = ?", knowledgeID).Count(&count).Error)
	assert.Equal(t, int64(1), count)

	err = repo.SetKnowledgeTags(ctx, knowledgeID, nil)
	require.NoError(t, err)
	require.NoError(t, db.Model(&types.KnowledgeTagRelation{}).
		Where("knowledge_id = ?", knowledgeID).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

func TestGetKnowledgeTags_ReturnsTagDetails(t *testing.T) {
	db := setupKnowledgeTagTestDB(t)
	repo := &knowledgeRepository{db: db}
	_, knowledgeID, tagA, tagB := seedKnowledgeTagFixture(t, db)
	ctx := context.Background()

	require.NoError(t, repo.SetKnowledgeTags(ctx, knowledgeID, []string{tagA, tagB}))

	tagMap, err := repo.GetKnowledgeTags(ctx, []string{knowledgeID})
	require.NoError(t, err)
	require.Len(t, tagMap[knowledgeID], 2)

	names := map[string]bool{}
	for _, tag := range tagMap[knowledgeID] {
		names[tag.Name] = true
	}
	assert.True(t, names["alpha"])
	assert.True(t, names["beta"])
}

func TestDeleteKnowledgeTagRelations(t *testing.T) {
	db := setupKnowledgeTagTestDB(t)
	repo := &knowledgeRepository{db: db}
	_, knowledgeID, tagA, _ := seedKnowledgeTagFixture(t, db)
	ctx := context.Background()

	require.NoError(t, repo.SetKnowledgeTags(ctx, knowledgeID, []string{tagA}))
	require.NoError(t, repo.DeleteKnowledgeTagRelations(ctx, knowledgeID))

	var count int64
	require.NoError(t, db.Model(&types.KnowledgeTagRelation{}).
		Where("knowledge_id = ?", knowledgeID).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

func TestApplyKnowledgeListFilter_TagIDsOrSemantics(t *testing.T) {
	db := setupKnowledgeTagTestDB(t)
	repo := &knowledgeRepository{db: db}
	ctx := context.Background()

	kbID := uuid.New().String()
	docA := uuid.New().String()
	docB := uuid.New().String()
	docC := uuid.New().String()
	tagA := uuid.New().String()
	tagB := uuid.New().String()

	for _, row := range []struct{ id, title string }{
		{docA, "a"}, {docB, "b"}, {docC, "c"},
	} {
		require.NoError(t, db.Exec(`
			INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, parse_status)
			VALUES (?, 1, ?, 'file', ?, 'completed')
		`, row.id, kbID, row.title).Error)
	}
	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_tags (id, seq_id, tenant_id, knowledge_base_id, name)
		VALUES (?, 1, 1, ?, 't-a'), (?, 2, 1, ?, 't-b')
	`, tagA, kbID, tagB, kbID).Error)
	require.NoError(t, repo.SetKnowledgeTags(ctx, docA, []string{tagA}))
	require.NoError(t, repo.SetKnowledgeTags(ctx, docB, []string{tagB}))
	require.NoError(t, repo.SetKnowledgeTags(ctx, docC, []string{tagA, tagB}))

	query := db.WithContext(ctx).Model(&types.Knowledge{}).
		Where("tenant_id = ? AND knowledge_base_id = ?", uint64(1), kbID)
	query = applyKnowledgeListFilter(query, types.KnowledgeListFilter{
		TagIDs: []string{tagA, tagB},
	})

	var ids []string
	require.NoError(t, query.Pluck("id", &ids).Error)
	assert.ElementsMatch(t, []string{docA, docB, docC}, ids)
}

func TestBatchCountReferences_ScopedToKnowledgeBase(t *testing.T) {
	db := setupKnowledgeTagTestDB(t)
	knowledgeRepo := &knowledgeRepository{db: db}
	tagRepo := &knowledgeTagRepository{db: db}
	ctx := context.Background()

	kb1 := uuid.New().String()
	kb2 := uuid.New().String()
	doc1 := uuid.New().String()
	doc2 := uuid.New().String()
	tag1 := uuid.New().String()
	tag2 := uuid.New().String()

	require.NoError(t, db.Exec(`
		INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, parse_status)
		VALUES (?, 1, ?, 'file', 'kb1-doc', 'completed'), (?, 1, ?, 'file', 'kb2-doc', 'completed')
	`, doc1, kb1, doc2, kb2).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO knowledge_tags (id, seq_id, tenant_id, knowledge_base_id, name)
		VALUES (?, 1, 1, ?, 'tag-kb1'), (?, 2, 1, ?, 'tag-kb2')
	`, tag1, kb1, tag2, kb2).Error)
	require.NoError(t, knowledgeRepo.SetKnowledgeTags(ctx, doc1, []string{tag1}))
	// Stale relation: doc in kb2 still linked to tag1 (simulates pre-fix move bug)
	require.NoError(t, knowledgeRepo.SetKnowledgeTags(ctx, doc2, []string{tag1}))

	counts, err := tagRepo.BatchCountReferences(ctx, 1, kb1, []string{tag1})
	require.NoError(t, err)
	assert.Equal(t, int64(1), counts[tag1].KnowledgeCount)
}

func TestApplyKnowledgeListFilter_FolderRootNamedAndOmitted(t *testing.T) {
	db := setupKnowledgeTagTestDB(t)
	repo := &knowledgeRepository{db: db}
	kbID := uuid.New().String()
	for _, row := range []struct{ id, folder string }{
		{"root", ""}, {"parent", "folder-a"}, {"descendant", "folder-child"},
	} {
		require.NoError(t, db.Exec(`
			INSERT INTO knowledges (id, tenant_id, knowledge_base_id, type, title, parse_status, folder_id)
			VALUES (?, 1, ?, 'file', ?, 'completed', ?)
		`, row.id, kbID, row.id, row.folder).Error)
	}

	list := func(filter types.KnowledgeListFilter) []string {
		var ids []string
		query := db.Model(&types.Knowledge{}).Where("tenant_id = ? AND knowledge_base_id = ?", 1, kbID)
		require.NoError(t, applyKnowledgeListFilter(query, filter).Order("id").Pluck("id", &ids).Error)
		return ids
	}
	assert.Equal(t, []string{"descendant", "parent", "root"}, list(types.KnowledgeListFilter{}))
	assert.Equal(t, []string{"root"}, list(types.KnowledgeListFilter{FolderIDSet: true, FolderID: types.FolderRootID}))
	assert.Equal(t, []string{"parent"}, list(types.KnowledgeListFilter{FolderIDSet: true, FolderID: "folder-a"}))
	_ = repo
}

func TestCheckKnowledgeExists_DuplicateRemainsGlobalAcrossFolders(t *testing.T) {
	db := setupKnowledgeTagTestDB(t)
	repo := &knowledgeRepository{db: db}
	kbID := uuid.New().String()
	require.NoError(t, db.Exec(`
		INSERT INTO knowledges
		(id, tenant_id, knowledge_base_id, type, title, parse_status, folder_id, file_hash, file_name, file_size)
		VALUES ('doc-a', 1, ?, 'file', 'doc', 'completed', 'folder-a', 'same-hash', 'doc.txt', 3)
	`, kbID).Error)

	exists, knowledge, err := repo.CheckKnowledgeExists(context.Background(), 1, kbID, &types.KnowledgeCheckParams{
		Type: "file", FileHash: "same-hash",
	})
	require.NoError(t, err)
	require.True(t, exists)
	require.Equal(t, "doc-a", knowledge.ID)
}
