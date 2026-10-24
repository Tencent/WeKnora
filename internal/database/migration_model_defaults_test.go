package database

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const modelDefaultsFirstTenant = 99013401

type modelDefaultHistoryRow struct {
	IsDefault  bool
	Parameters string
}

// Both database upgrades use the same history, including conflicting defaults,
// inherited builtin defaults, implicit fallbacks and explicit resource bindings.
func seedModelDefaultHistory(t *testing.T, db *sql.DB) map[uint64]map[types.ModelType]string {
	t.Helper()
	cleanup := func() {
		_, _ = db.Exec("DELETE FROM model_defaults WHERE tenant_id BETWEEN 99013401 AND 99013405")
		_, _ = db.Exec("DELETE FROM knowledge_bases WHERE id = 'mdm-kb'")
		_, _ = db.Exec("DELETE FROM custom_agents WHERE id = 'mdm-agent'")
		_, _ = db.Exec("DELETE FROM models WHERE id LIKE 'mdm-%'")
		_, _ = db.Exec("DELETE FROM tenants WHERE id BETWEEN 99013401 AND 99013405")
	}
	cleanup()
	t.Cleanup(cleanup)
	for i := range 5 {
		var deleted any
		status := "active"
		if i == 3 {
			deleted = "2020-01-01 00:00:00"
		}
		if i == 4 {
			status = "inactive"
		}
		_, err := db.Exec(`INSERT INTO tenants (id, name, business, status, deleted_at)
			VALUES ($1, $2, 'migration-test', $3, $4)`,
			modelDefaultsFirstTenant+i, "model-default-history", status, deleted)
		require.NoError(t, err)
	}
	type legacyModel struct {
		id, modelType     string
		tenantID          uint64
		isBuiltin, chosen bool
		status            string
		created           any
		deleted           any
	}
	models := []legacyModel{
		// A builtin's owning tenant does not need to exist for it to be visible.
		{"mdm-chat-default", "KnowledgeQA", 99013400, true, true, "active", "2020-01-01 00:00:00", nil},
		{"mdm-chat-own-later", "KnowledgeQA", 99013401, false, true, "active", "2021-01-01 00:00:00", nil},
		{"mdm-chat-foreign-default", "KnowledgeQA", 99013402, false, true, "active", "2018-01-01 00:00:00", nil},
		{
			"mdm-chat-deleted", "KnowledgeQA", 99013400, true, true, "active",
			"2010-01-01 00:00:00", "2020-01-01 00:00:00",
		},
		{"mdm-embed-first", "Embedding", 99013400, true, false, "active", "2018-01-01 00:00:00", nil},
		{"mdm-embed-own-default", "Embedding", 99013401, false, true, "active", "2022-01-01 00:00:00", nil},
		{"mdm-embed-orphan", "Embedding", 99013400, false, true, "active", "2017-01-01 00:00:00", nil},
		// Equal timestamps and NULL timestamps have the same resolution in both DBs.
		{"mdm-rerank-z", "Rerank", 99013400, true, true, "active", "2020-01-01 00:00:00", nil},
		{"mdm-rerank-a", "Rerank", 99013400, true, true, "active", "2020-01-01 00:00:00", nil},
		{"mdm-rerank-null", "Rerank", 99013400, true, true, "active", nil, nil},
		{"mdm-asr-later", "ASR", 99013400, true, false, "active", "2022-01-01 00:00:00", nil},
		{"mdm-asr-first", "ASR", 99013400, true, false, "active", "2018-01-01 00:00:00", nil},
		{"mdm-vision-downloading", "VLLM", 99013400, true, true, "downloading", "2017-01-01 00:00:00", nil},
		{"mdm-vision-failed", "VLLM", 99013402, false, true, "download_failed", "2018-01-01 00:00:00", nil},
		{"mdm-vision-active", "VLLM", 99013401, false, false, "active", "2022-01-01 00:00:00", nil},
		{"mdm-deleted-tenant-default", "KnowledgeQA", 99013404, false, true, "active", "2016-01-01 00:00:00", nil},
	}
	for _, model := range models {
		_, err := db.Exec(`INSERT INTO models
			(id, tenant_id, name, type, source, parameters, is_default, is_builtin, status, created_at, deleted_at)
			VALUES ($1, $2, $1, $3, 'remote', '{"api_key":"legacy-test-key"}', $4, $5, $6, $7, $8)`,
			model.id, model.tenantID, model.modelType, model.chosen,
			model.isBuiltin, model.status, model.created, model.deleted)
		require.NoError(t, err, model.id)
	}
	_, err := db.Exec(`INSERT INTO knowledge_bases
		(id, name, tenant_id, embedding_model_id, summary_model_id)
		VALUES ('mdm-kb', 'Existing bindings', 99013401, 'mdm-embed-first', 'mdm-chat-own-later')`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO custom_agents (id, name, tenant_id, config)
		VALUES ('mdm-agent', 'Existing bindings', 99013401,
			'{"model_id":"mdm-chat-own-later","rerank_model_id":"mdm-rerank-z"}')`)
	require.NoError(t, err)

	want := make(map[uint64]map[types.ModelType]string)
	for _, tenantID := range []uint64{99013401, 99013402, 99013403, 99013405} {
		want[tenantID] = map[types.ModelType]string{
			types.ModelTypeKnowledgeQA: "mdm-chat-default",
			types.ModelTypeEmbedding:   "mdm-embed-first",
			types.ModelTypeRerank:      "mdm-rerank-a",
			types.ModelTypeASR:         "mdm-asr-first",
		}
	}
	want[99013401][types.ModelTypeEmbedding] = "mdm-embed-own-default"
	want[99013401][types.ModelTypeVLLM] = "mdm-vision-active"
	want[99013402][types.ModelTypeKnowledgeQA] = "mdm-chat-foreign-default"
	return want
}

func modelDefaultHistorySnapshot(t *testing.T, db *sql.DB) map[string]modelDefaultHistoryRow {
	t.Helper()
	rows, err := db.Query("SELECT id, is_default, parameters FROM models WHERE id LIKE 'mdm-%'")
	require.NoError(t, err)
	defer func() { _ = rows.Close() }()
	snapshot := make(map[string]modelDefaultHistoryRow)
	for rows.Next() {
		var id string
		var row modelDefaultHistoryRow
		require.NoError(t, rows.Scan(&id, &row.IsDefault, &row.Parameters))
		snapshot[id] = row
	}
	require.NoError(t, rows.Err())
	return snapshot
}

func assertMigratedModelDefaults(t *testing.T, db *sql.DB, dialect, migrationPath string,
	want map[uint64]map[types.ModelType]string,
) {
	t.Helper()
	var dialector gorm.Dialector = &sqlite.Dialector{Conn: db}
	if dialect == "postgres" {
		dialector = postgres.New(postgres.Config{Conn: db})
	}
	orm, err := gorm.Open(dialector, &gorm.Config{})
	require.NoError(t, err)
	repo := repository.NewModelRepository(orm)
	assertChoices := func() {
		t.Helper()
		rows, err := db.Query("SELECT tenant_id, model_type, model_id FROM model_defaults " +
			"WHERE tenant_id BETWEEN 99013400 AND 99013405")
		require.NoError(t, err)
		got := make(map[uint64]map[types.ModelType]string)
		for rows.Next() {
			var tenantID uint64
			var modelType types.ModelType
			var id string
			require.NoError(t, rows.Scan(&tenantID, &modelType, &id))
			if got[tenantID] == nil {
				got[tenantID] = make(map[types.ModelType]string)
			}
			got[tenantID][modelType] = id
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
		require.Equal(t, want, got, "backfill every live workspace, including those without their own models")
		for tenantID, expected := range want {
			models, err := repo.List(context.Background(), tenantID, "", "")
			require.NoError(t, err)
			effective := make(map[types.ModelType]string)
			for _, model := range models {
				if model.IsDefault {
					require.Empty(t, effective[model.Type])
					effective[model.Type] = model.ID
				}
			}
			require.Equal(t, expected, effective, "runtime must honor the migrated choices for workspace %d", tenantID)
		}
	}
	assertChoices()

	var embeddingID, chatID, agentConfig string
	require.NoError(t, db.QueryRow(`SELECT embedding_model_id, summary_model_id FROM knowledge_bases
		WHERE id = 'mdm-kb'`).
		Scan(&embeddingID, &chatID))
	require.Equal(t, []string{"mdm-embed-first", "mdm-chat-own-later"}, []string{embeddingID, chatID})
	require.NoError(t, db.QueryRow("SELECT config FROM custom_agents WHERE id = 'mdm-agent'").Scan(&agentConfig))
	require.JSONEq(t, `{"model_id":"mdm-chat-own-later","rerank_model_id":"mdm-rerank-z"}`, agentConfig)

	// Once migrated, legacy flags cannot change selection. Replaying the SQL
	// also preserves an explicit choice made through the new model settings.
	require.NoError(t, repo.SetDefault(context.Background(), 99013401,
		types.ModelTypeKnowledgeQA, "mdm-chat-own-later"))
	want[99013401][types.ModelTypeKnowledgeQA] = "mdm-chat-own-later"
	_, err = db.Exec("UPDATE models SET is_default = FALSE WHERE id LIKE 'mdm-%'")
	require.NoError(t, err)
	assertChoices()
	script, err := os.ReadFile(migrationPath)
	require.NoError(t, err)
	_, err = db.Exec(string(script))
	require.NoError(t, err)
	assertChoices()
}
