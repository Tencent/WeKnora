package database

import (
	"database/sql"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSQLiteProcessingArtifactMigrationAndTenantBoundary(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	up, err := os.ReadFile("../../migrations/sqlite/000001_processing_artifacts.up.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(up))
	require.NoError(t, err)

	insert := `
		INSERT INTO processing_artifacts (
			tenant_id, stage, key_version, artifact_key, processor_digest,
			output_digest, output_schema, codec, inline_payload, payload,
			object_ref, payload_checksum, size_bytes
		) VALUES (?, 'embedding', 1, ?, ?, ?, 'embedding.v1', 'float32be.v1', 1, ?, '', ?, ?)
	`
	hash := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	payload := []byte{}
	_, err = db.Exec(insert, 1, hash, hash, hash, payload, hash, 0)
	require.NoError(t, err)
	_, err = db.Exec(insert, 2, hash, hash, hash, payload, hash, 0)
	require.NoError(t, err, "the same content key must be isolated by tenant")
	_, err = db.Exec(insert, 1, hash, hash, hash, payload, hash, 0)
	require.Error(t, err, "the tenant-scoped immutable key must be unique")

	_, err = db.Exec(
		"INSERT INTO knowledge_attempt_counters (knowledge_id, last_attempt) VALUES (?, ?)",
		"knowledge-1",
		1,
	)
	require.NoError(t, err)
	_, err = db.Exec(
		"INSERT INTO knowledge_attempt_counters (knowledge_id, last_attempt) VALUES (?, ?)",
		"knowledge-1",
		2,
	)
	require.Error(t, err, "each knowledge must have exactly one attempt allocator row")

	down, err := os.ReadFile("../../migrations/sqlite/000001_processing_artifacts.down.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(down))
	require.NoError(t, err)
	_, err = db.Exec("SELECT 1 FROM processing_artifacts LIMIT 1")
	require.Error(t, err)
	_, err = db.Exec("SELECT 1 FROM knowledge_attempt_counters LIMIT 1")
	require.Error(t, err)
}
