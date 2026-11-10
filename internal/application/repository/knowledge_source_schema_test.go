package repository

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestKnowledgeSourceSchemaAllowsObjectStorageURLs(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:knowledge_source_schema?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sqlite handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.AutoMigrate(&types.Knowledge{}); err != nil {
		t.Fatalf("auto migrate knowledge: %v", err)
	}

	var sourceType string
	rows, err := db.Raw("PRAGMA table_info(knowledges)").Rows()
	if err != nil {
		t.Fatalf("inspect knowledge schema: %v", err)
	}

	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan column info: %v", err)
		}
		if name == "source" {
			sourceType = strings.ToLower(typ)
			break
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close column info rows: %v", err)
	}
	if sourceType != "varchar(2048)" {
		t.Fatalf("knowledge source column type = %q, want varchar(2048)", sourceType)
	}

	longURL := "https://example-bucket.cos.ap-beijing.myqcloud.com/test/" +
		strings.Repeat("encoded-path-segment-", 20) + ".docx"
	knowledge := &types.Knowledge{
		TenantID:        1,
		KnowledgeBaseID: "kb-1",
		Type:            types.KnowledgeTypeManual,
		Title:           "long source",
		Source:          longURL,
		ParseStatus:     types.ParseStatusPending,
		EnableStatus:    "enabled",
	}
	if err := db.Create(knowledge).Error; err != nil {
		t.Fatalf("create knowledge with long source: %v", err)
	}
}
