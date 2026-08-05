package types

import (
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestWikiProvenanceModelsAutoMigrate(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:wiki_provenance_schema?mode=memory&cache=shared"), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlite handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	models := []any{
		&KnowledgeRevision{},
		&WikiProvenancePageRevision{},
		&WikiPageBlock{},
		&WikiBlockSource{},
		&WikiPageSource{},
	}
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("auto migrate provenance models: %v", err)
	}

	for _, table := range []string{
		"knowledge_revisions",
		"wiki_provenance_page_revisions",
		"wiki_page_blocks",
		"wiki_block_sources",
		"wiki_page_sources",
	} {
		if !db.Migrator().HasTable(table) {
			t.Fatalf("expected table %s", table)
		}
	}

	revision := &KnowledgeRevision{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		KnowledgeID:     "knowledge-1",
		RevisionNo:      1,
	}
	if err := db.Create(revision).Error; err != nil {
		t.Fatalf("create knowledge revision: %v", err)
	}

	pageRevision := &WikiProvenancePageRevision{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		PageID:          "page-1",
		RevisionNo:      1,
	}
	if err := db.Create(pageRevision).Error; err != nil {
		t.Fatalf("create page revision: %v", err)
	}

	block := &WikiPageBlock{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		PageID:          "page-1",
		PageRevisionID:  pageRevision.ID,
		BlockType:       WikiBlockFact,
		Content:         "A sourced fact.",
	}
	if err := db.Create(block).Error; err != nil {
		t.Fatalf("create page block: %v", err)
	}

	source := &WikiBlockSource{
		TenantID:            1,
		KnowledgeBaseID:     "kb-1",
		PageID:              "page-1",
		BlockID:             block.ID,
		KnowledgeID:         "knowledge-1",
		KnowledgeRevisionID: revision.ID,
	}
	if err := db.Create(source).Error; err != nil {
		t.Fatalf("create block source: %v", err)
	}

	pageSource := &WikiPageSource{
		TenantID:                1,
		KnowledgeBaseID:         "kb-1",
		PageID:                  "page-1",
		KnowledgeID:             "knowledge-1",
		SupportedBlockCount:     1,
		LastKnowledgeRevisionID: &revision.ID,
	}
	if err := db.Create(pageSource).Error; err != nil {
		t.Fatalf("create page source: %v", err)
	}
}
