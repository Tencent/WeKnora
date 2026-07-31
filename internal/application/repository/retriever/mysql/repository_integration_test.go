package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	gomysql "github.com/go-sql-driver/mysql"

	appdb "github.com/Tencent/WeKnora/internal/database"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestMySQLVectorRetrieveIntegration(t *testing.T) {
	dsn := os.Getenv("WEKNORA_MYSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("set WEKNORA_MYSQL_TEST_DSN to run the real MySQL integration test")
	}
	baseConfig, err := gomysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse WEKNORA_MYSQL_TEST_DSN: %v", err)
	}
	clientConfig, err := appdb.MySQLRetrieverConfigFromEnv(
		baseConfig.User,
		baseConfig.Passwd,
		baseConfig.Addr,
		baseConfig.DBName,
	)
	if err != nil {
		t.Fatalf("build MySQL retriever connection config: %v", err)
	}
	dsn = clientConfig.DSN

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	db.SetMaxOpenConns(2)
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("connect to WEKNORA_MYSQL_TEST_DSN: %v", err)
	}

	var database string
	if err := db.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&database); err != nil {
		t.Fatalf("resolve test database: %v", err)
	}
	if database == "" {
		t.Fatal("WEKNORA_MYSQL_TEST_DSN must select a disposable test database")
	}

	prefix := fmt.Sprintf("weknora_it_%d_", time.Now().UnixNano())
	repo := &mysqlRepository{db: db, database: database, tablePrefix: prefix}
	const dimension = 1024
	table := repo.getTableName(dimension)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		if _, dropErr := db.ExecContext(cleanupCtx, "DROP TABLE IF EXISTS "+quoteIdentifier(table)); dropErr != nil {
			t.Errorf("drop integration table: %v", dropErr)
		}
	})

	legacyDDL := strings.NewReplacer(
		"content           LONGTEXT", "content           TEXT",
		"embedding         JSON NULL", "embedding         JSON NOT NULL",
	).Replace(fmt.Sprintf(createTableTpl, quoteIdentifier(table)))
	if _, err := db.ExecContext(ctx, legacyDDL); err != nil {
		t.Fatalf("create legacy TEXT retriever table: %v", err)
	}
	if err := repo.ensureTable(ctx, dimension); err != nil {
		t.Fatalf("ensureTable() error = %v", err)
	}
	var contentType string
	if err := db.QueryRowContext(
		ctx,
		`SELECT DATA_TYPE FROM information_schema.columns
		 WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND COLUMN_NAME = 'content'`,
		database,
		table,
	).Scan(&contentType); err != nil {
		t.Fatalf("read upgraded content type: %v", err)
	}
	if contentType != "longtext" {
		t.Fatalf("content type = %q, want longtext", contentType)
	}
	var embeddingNullable string
	if err := db.QueryRowContext(
		ctx,
		`SELECT IS_NULLABLE FROM information_schema.columns
		 WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ? AND COLUMN_NAME = 'embedding'`,
		database,
		table,
	).Scan(&embeddingNullable); err != nil {
		t.Fatalf("read upgraded embedding nullability: %v", err)
	}
	if embeddingNullable != "YES" {
		t.Fatalf("embedding IS_NULLABLE = %q, want YES", embeddingNullable)
	}

	const rowCount = 200
	rows := make([]*MysqlVectorEmbedding, 0, rowCount)
	for row := 0; row < rowCount; row++ {
		vector := make([]float32, dimension)
		for column := range vector {
			vector[column] = float32(math.Sin(float64((row + 1) * (column + 1))))
		}
		id := fmt.Sprintf("row-%03d", row)
		rows = append(rows, &MysqlVectorEmbedding{
			ID:              id,
			Content:         "content " + id,
			SourceID:        "source-" + id,
			SourceType:      int(types.ChunkSourceType),
			ChunkID:         "chunk-" + id,
			KnowledgeID:     "knowledge-" + id,
			KnowledgeBaseID: "kb-1024",
			TagID:           "tag-1",
			IsEnabled:       true,
			Embedding:       vector,
		})
	}
	rows[0].Content = "oversizedmarker " + strings.Repeat("payload ", 10000)

	insertStarted := time.Now()
	if err := repo.insertRows(ctx, table, rows); err != nil {
		t.Fatalf("parameterized insertRows() error = %v", err)
	}
	t.Logf("parameterized insert: %d rows x %d dimensions in %s", rowCount, dimension, time.Since(insertStarted))

	var storedRows, minDimension, maxDimension int
	if err := db.QueryRowContext(
		ctx,
		"SELECT COUNT(*), MIN(JSON_LENGTH(embedding)), MAX(JSON_LENGTH(embedding)) FROM "+quoteIdentifier(table),
	).Scan(&storedRows, &minDimension, &maxDimension); err != nil {
		t.Fatalf("verify stored embeddings: %v", err)
	}
	if storedRows != rowCount || minDimension != dimension || maxDimension != dimension {
		t.Fatalf(
			"stored rows/dimensions = %d/%d/%d, want %d/%d/%d",
			storedRows,
			minDimension,
			maxDimension,
			rowCount,
			dimension,
			dimension,
		)
	}
	var storedContentBytes int
	if err := db.QueryRowContext(
		ctx,
		"SELECT OCTET_LENGTH(content) FROM "+quoteIdentifier(table)+" WHERE id = ?",
		rows[0].ID,
	).Scan(&storedContentBytes); err != nil {
		t.Fatalf("verify oversized content: %v", err)
	}
	if storedContentBytes <= 65535 {
		t.Fatalf("stored oversized content bytes = %d, want > 65535", storedContentBytes)
	}

	target := rows[137]
	if _, err := db.ExecContext(
		ctx,
		"UPDATE "+quoteIdentifier(table)+
			" SET is_enabled = NULL, source_id = NULL, source_type = NULL,"+
			" tag_id = NULL, content = 'mysql retrieval target' WHERE id = ?",
		target.ID,
	); err != nil {
		t.Fatalf("install nullable metadata fixture: %v", err)
	}

	retrieveStarted := time.Now()
	result, err := repo.VectorRetrieve(ctx, types.RetrieveParams{
		Embedding:           target.Embedding,
		KnowledgeBaseIDs:    []string{"kb-1024"},
		ExcludeKnowledgeIDs: []string{"knowledge-row-000"},
		ExcludeChunkIDs:     []string{"chunk-row-001"},
		Threshold:           -1,
		TopK:                10,
	})
	if err != nil {
		t.Fatalf("VectorRetrieve() error = %v", err)
	}
	t.Logf("exact retrieval: %d rows x %d dimensions in %s", rowCount, dimension, time.Since(retrieveStarted))
	if len(result) != 1 || len(result[0].Results) != 10 {
		t.Fatalf("VectorRetrieve() returned %#v, want one result group with 10 hits", result)
	}
	if got := result[0].Results[0]; got.ID != target.ID || !got.IsEnabled || math.Abs(got.Score-1) > 1e-6 {
		t.Fatalf("top result = %#v, want NULL-enabled target %q with score 1", got, target.ID)
	}
	for _, hit := range result[0].Results {
		if hit.KnowledgeID == "knowledge-row-000" || hit.ChunkID == "chunk-row-001" {
			t.Fatalf("excluded row returned: %#v", hit)
		}
	}

	keywordResult, err := repo.KeywordsRetrieve(ctx, types.RetrieveParams{
		Query:            "mysql",
		KnowledgeBaseIDs: []string{"kb-1024"},
		Threshold:        0,
		TopK:             10,
	})
	if err != nil {
		t.Fatalf("KeywordsRetrieve() with nullable metadata error = %v", err)
	}
	if len(keywordResult) != 1 || len(keywordResult[0].Results) == 0 ||
		keywordResult[0].Results[0].ID != target.ID ||
		!keywordResult[0].Results[0].IsEnabled {
		t.Fatalf("nullable keyword result = %#v, want target %q", keywordResult, target.ID)
	}
	keywordResult, err = repo.KeywordsRetrieve(ctx, types.RetrieveParams{
		Query:            "mysql",
		KnowledgeBaseIDs: []string{"kb-1024"},
		Threshold:        math.MaxFloat64,
		TopK:             10,
	})
	if err != nil {
		t.Fatalf("KeywordsRetrieve() high-threshold error = %v", err)
	}
	if len(keywordResult) != 1 || len(keywordResult[0].Results) != 0 {
		t.Fatalf("high-threshold keyword result = %#v, want no hits", keywordResult)
	}
	keywordResult, err = repo.KeywordsRetrieve(ctx, types.RetrieveParams{
		Query:            "oversizedmarker",
		KnowledgeBaseIDs: []string{"kb-1024"},
		Threshold:        0,
		TopK:             10,
	})
	if err != nil {
		t.Fatalf("KeywordsRetrieve() oversized content error = %v", err)
	}
	if len(keywordResult) != 1 || len(keywordResult[0].Results) != 1 ||
		keywordResult[0].Results[0].ID != rows[0].ID {
		t.Fatalf("oversized keyword result = %#v, want row %q", keywordResult, rows[0].ID)
	}
	keywordOnly := &types.IndexInfo{
		ID:              "keyword-only-row",
		Content:         "keywordonlymarker mysql fulltext content",
		SourceID:        "keyword-only-chunk",
		SourceType:      types.ChunkSourceType,
		ChunkID:         "keyword-only-chunk",
		KnowledgeID:     "keyword-only-knowledge",
		KnowledgeBaseID: "keyword-only-kb",
		TagID:           "tag-keyword",
		IsEnabled:       true,
	}
	if err := repo.Save(ctx, keywordOnly, map[string]any{"dimension": dimension}); err != nil {
		t.Fatalf("Save() keyword-only row error = %v", err)
	}
	var keywordEmbeddingIsNull bool
	if err := db.QueryRowContext(
		ctx,
		"SELECT embedding IS NULL FROM "+quoteIdentifier(table)+" WHERE id = ?",
		keywordOnly.ID,
	).Scan(&keywordEmbeddingIsNull); err != nil {
		t.Fatalf("verify keyword-only NULL embedding: %v", err)
	}
	if !keywordEmbeddingIsNull {
		t.Fatal("keyword-only row stored a non-NULL embedding")
	}
	keywordResult, err = repo.KeywordsRetrieve(ctx, types.RetrieveParams{
		Query:            "keywordonlymarker",
		KnowledgeBaseIDs: []string{keywordOnly.KnowledgeBaseID},
		Threshold:        0,
		TopK:             10,
	})
	if err != nil {
		t.Fatalf("KeywordsRetrieve() keyword-only row error = %v", err)
	}
	if len(keywordResult) != 1 || len(keywordResult[0].Results) != 1 ||
		keywordResult[0].Results[0].ID != keywordOnly.ID {
		t.Fatalf("keyword-only result = %#v, want row %q", keywordResult, keywordOnly.ID)
	}
	vectorResult, err := repo.VectorRetrieve(ctx, types.RetrieveParams{
		Embedding:        target.Embedding,
		KnowledgeBaseIDs: []string{keywordOnly.KnowledgeBaseID},
		Threshold:        -1,
		TopK:             10,
	})
	if err != nil {
		t.Fatalf("VectorRetrieve() with keyword-only row error = %v", err)
	}
	if len(vectorResult) != 1 || len(vectorResult[0].Results) != 0 {
		t.Fatalf("keyword-only row leaked into vector results: %#v", vectorResult)
	}

	const (
		copiedChunkID     = "keyword-only-copy-chunk"
		copiedKnowledgeID = "keyword-only-copy-knowledge"
		copiedKBID        = "keyword-only-copy-kb"
	)
	if err := repo.CopyIndices(
		ctx,
		keywordOnly.KnowledgeBaseID,
		map[string]string{keywordOnly.KnowledgeID: copiedKnowledgeID},
		map[string]string{keywordOnly.ChunkID: copiedChunkID},
		copiedKBID,
		dimension,
		"",
	); err != nil {
		t.Fatalf("CopyIndices() keyword-only row error = %v", err)
	}
	var copiedCount int
	if err := db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM "+quoteIdentifier(table)+
			" WHERE knowledge_base_id = ? AND chunk_id = ? AND embedding IS NULL",
		copiedKBID,
		copiedChunkID,
	).Scan(&copiedCount); err != nil {
		t.Fatalf("verify copied keyword-only row: %v", err)
	}
	if copiedCount != 1 {
		t.Fatalf("copied keyword-only rows = %d, want 1", copiedCount)
	}
	if err := repo.DeleteByChunkIDList(ctx, []string{copiedChunkID}, dimension, ""); err != nil {
		t.Fatalf("DeleteByChunkIDList() keyword-only copy error = %v", err)
	}
	if err := db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM "+quoteIdentifier(table)+" WHERE chunk_id = ?",
		copiedChunkID,
	).Scan(&copiedCount); err != nil {
		t.Fatalf("verify deleted keyword-only copy: %v", err)
	}
	if copiedCount != 0 {
		t.Fatalf("deleted keyword-only rows = %d, want 0", copiedCount)
	}

	keywordVector := make([]float32, dimension)
	keywordVector[0] = 1
	if err := repo.Save(ctx, keywordOnly, map[string]any{
		"embedding": map[string][]float32{keywordOnly.SourceID: keywordVector},
	}); err != nil {
		t.Fatalf("Save() vector upsert over keyword-only row error = %v", err)
	}
	vectorResult, err = repo.VectorRetrieve(ctx, types.RetrieveParams{
		Embedding:        keywordVector,
		KnowledgeBaseIDs: []string{keywordOnly.KnowledgeBaseID},
		Threshold:        0.999999,
		TopK:             1,
	})
	if err != nil {
		t.Fatalf("VectorRetrieve() after keyword-only upsert error = %v", err)
	}
	if len(vectorResult) != 1 || len(vectorResult[0].Results) != 1 ||
		vectorResult[0].Results[0].ID != keywordOnly.ID {
		t.Fatalf("vector upsert result = %#v, want row %q", vectorResult, keywordOnly.ID)
	}
	if err := assertMySQLRetrieverRepeatableRead(ctx, db, table, target.ID); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(
		ctx,
		"UPDATE "+quoteIdentifier(table)+" SET embedding = JSON_ARRAY(1) WHERE id = ?",
		rows[0].ID,
	); err != nil {
		t.Fatalf("install malformed-dimension fixture: %v", err)
	}
	_, err = repo.VectorRetrieve(ctx, types.RetrieveParams{
		Embedding:        target.Embedding,
		KnowledgeBaseIDs: []string{"kb-1024"},
		Threshold:        -1,
		TopK:             10,
	})
	if err == nil {
		t.Fatal("VectorRetrieve() accepted a stored dimension mismatch")
	}
}

func assertMySQLRetrieverRepeatableRead(
	ctx context.Context,
	db *sql.DB,
	table string,
	id string,
) error {
	reader, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open reader connection: %w", err)
	}
	defer reader.Close()
	writer, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open writer connection: %w", err)
	}
	defer writer.Close()

	if _, err := reader.ExecContext(
		ctx,
		"SET SESSION TRANSACTION ISOLATION LEVEL READ COMMITTED",
	); err != nil {
		return fmt.Errorf("set reader session default to READ COMMITTED: %w", err)
	}
	tx, err := reader.BeginTx(ctx, vectorReadTransactionOptions())
	if err != nil {
		return fmt.Errorf("begin explicit repeatable-read transaction: %w", err)
	}
	defer tx.Rollback()

	var before string
	if err := tx.QueryRowContext(
		ctx,
		"SELECT content FROM "+quoteIdentifier(table)+" WHERE id = ?",
		id,
	).Scan(&before); err != nil {
		return fmt.Errorf("read content before concurrent update: %w", err)
	}
	const updated = "mysql concurrent update"
	if _, err := writer.ExecContext(
		ctx,
		"UPDATE "+quoteIdentifier(table)+" SET content = ? WHERE id = ?",
		updated,
		id,
	); err != nil {
		return fmt.Errorf("concurrently update content: %w", err)
	}
	defer func() {
		_, _ = writer.ExecContext(
			context.Background(),
			"UPDATE "+quoteIdentifier(table)+" SET content = ? WHERE id = ?",
			before,
			id,
		)
	}()

	var during string
	if err := tx.QueryRowContext(
		ctx,
		"SELECT content FROM "+quoteIdentifier(table)+" WHERE id = ?",
		id,
	).Scan(&during); err != nil {
		return fmt.Errorf("read content after concurrent update: %w", err)
	}
	if during != before {
		return fmt.Errorf(
			"explicit repeatable-read transaction observed concurrent value %q, want snapshot value %q",
			during,
			before,
		)
	}
	return nil
}
