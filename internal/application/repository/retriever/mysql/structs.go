package mysql

import (
	"database/sql"
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

func embeddingToJSON(embedding []float32) string {
	if len(embedding) == 0 {
		return "[]"
	}
	parts := make([]string, len(embedding))
	for i, v := range embedding {
		parts[i] = fmt.Sprintf("%g", v)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func parseEmbeddingJSON(raw []byte) ([]float32, error) {
	s := strings.Trim(string(raw), "[]")
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	vec := make([]float32, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		var v float32
		if _, err := fmt.Sscanf(p, "%f", &v); err != nil {
			return nil, fmt.Errorf("parse float: %s: %w", p, err)
		}
		vec = append(vec, v)
	}
	return vec, nil
}

func embeddingLiteral(embedding []float32) string {
	if len(embedding) == 0 {
		return "JSON_ARRAY()"
	}
	parts := make([]string, len(embedding))
	for i, v := range embedding {
		parts[i] = fmt.Sprintf("%g", v)
	}
	return "JSON_ARRAY(" + strings.Join(parts, ",") + ")"
}
