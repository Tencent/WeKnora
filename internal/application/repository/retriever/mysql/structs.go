package mysql

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

type mysqlRepository struct {
	db          *sql.DB
	host        string
	port        int
	username    string
	password    string
	database    string
	tablePrefix string
}

type MysqlVectorEmbedding struct {
	ID              string
	Content         string
	SourceID        string
	SourceType      int
	ChunkID         string
	KnowledgeID     string
	KnowledgeBaseID string
	TagID           string
	IsEnabled       bool
	Embedding       []float32
}

type MysqlVectorEmbeddingWithScore struct {
	MysqlVectorEmbedding
	Score float64
}

func (r *mysqlRepository) getTableName(dim int) string {
	return fmt.Sprintf("%s%d", r.tablePrefix, dim)
}

func normalizeTablePrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return defaultTablePrefix
	}
	if !strings.HasSuffix(prefix, "_") {
		prefix += "_"
	}
	return prefix
}

func quoteIdentifier(name string) string {
	return "`" + strings.ReplaceAll(name, "`", "``") + "`"
}

func escapeLikePattern(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}

func embeddingToJSON(embedding []float32) (string, error) {
	if len(embedding) == 0 {
		return "[]", nil
	}
	raw, err := json.Marshal(embedding)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func parseEmbeddingJSON(raw []byte) ([]float32, error) {
	var vec []float32
	if err := json.Unmarshal(raw, &vec); err != nil {
		return nil, fmt.Errorf("decode embedding JSON: %w", err)
	}
	return vec, nil
}
