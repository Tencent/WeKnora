// Command migration-profile-gen generates schema profiles using isolated fixture databases.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	retriever "github.com/Tencent/WeKnora/internal/application/repository/retriever/sqlite"
	"github.com/Tencent/WeKnora/internal/database"
	"github.com/Tencent/WeKnora/internal/types"
	sqlitevec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	gormsqlite "gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func main() {
	root := flag.String("root", "migrations", "migration root")
	output := flag.String("output", "internal/database/migration_profiles.json.gz", "output file")
	flag.Parse()
	sqlitevec.Auto()
	if err := database.WriteMigrationProfiles(
		context.Background(),
		os.Getenv("TEST_PROFILE_DSN"),
		*root,
		*output,
		initializeRuntime,
	); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func initializeRuntime(path string, dimension int) error {
	db, err := gorm.Open(gormsqlite.Open(path), &gorm.Config{})
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	repo := retriever.NewSQLiteRetrieveEngineRepository(db)
	if dimension == 0 {
		return nil
	}
	vector := make([]float32, dimension)
	vector[0] = 1
	info := &types.IndexInfo{
		SourceID:        "x03-profile",
		SourceType:      types.ChunkSourceType,
		ChunkID:         "x03-chunk",
		KnowledgeID:     "x03-knowledge",
		KnowledgeBaseID: "x03-kb",
		Content:         "runtime profile",
		IsEnabled:       true,
	}
	return repo.Save(
		context.Background(),
		info,
		map[string]any{"embedding": map[string][]float32{info.SourceID: vector}},
	)
}
