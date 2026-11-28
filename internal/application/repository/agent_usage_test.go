package repository

import (
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestAgentUsageScope(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			driver := sqlite.Open(":memory:")
			if dialect == "postgres" {
				dsn := os.Getenv("WEKNORA_TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("no PostgreSQL test database")
				}
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver)
			require.NoError(t, err)
			// Temporary tables isolate fixtures even on a shared test database.
			tx := db.Begin()
			t.Cleanup(func() { tx.Rollback() })
			require.NoError(t, tx.Exec(`CREATE TEMP TABLE sessions (
                id text, tenant_id bigint, user_id text,
                owner_auth_source text, deleted_at timestamp, parent_session_id text, created_at timestamp)`).Error)
			require.NoError(t, tx.Exec(`CREATE TEMP TABLE messages (
                agent_id text, agent_tenant_id bigint, session_id text,
                role text, is_completed boolean, created_at timestamp, deleted_at timestamp)`).Error)
			require.NoError(t, tx.Exec(`INSERT INTO sessions (id,tenant_id,user_id,owner_auth_source,deleted_at)
                VALUES ('one',1,'u','web',NULL),('two',1,'u','web',NULL),
                       ('anon',1,'key','api_tenant_key',NULL),('other',2,'u','web',NULL)`).Error)
			end := time.Now().UTC().Truncate(time.Second)
			start := end.AddDate(0, 0, -30)
			add := func(id string, owner int, session string, completed bool, when time.Time) {
				require.NoError(t, tx.Exec("INSERT INTO messages VALUES (?,?,?,'assistant',?,?,NULL)",
					id, owner, session, completed, when).Error)
			}
			// Forked history preserves the parent's timestamps. Only answers
			// produced after creating this fork belong in its usage totals.
			require.NoError(t, tx.Exec("INSERT INTO sessions VALUES ('fork',1,'u','web',NULL,'one',?)",
				end.Add(-time.Hour)).Error)
			add("a", 1, "fork", true, start)
			add("a", 1, "fork", true, end.Add(-time.Minute))
			add("a", 0, "one", true, start)
			add("b", 2, "one", true, start)
			add("a", 1, "two", true, start)
			add("a", 1, "anon", true, start)
			add("a", 1, "one", false, start)
			add("a", 1, "one", true, end)
			add("a", 1, "one", true, start.Add(-time.Second))
			add("a", 1, "other", true, start)
			rows, err := NewCustomAgentRepository(tx).AgentUsage(
				t.Context(), 1, []string{"a", "b", "unused"}, start, end)
			require.NoError(t, err)
			require.ElementsMatch(t, []types.AgentUsageRow{
				{
					AgentID: "a", AgentTenantID: 1,
					AgentUsageSummary: types.AgentUsageSummary{Answers: 4, Sessions: 4, Users: 1},
				},
				{
					AgentID: "b", AgentTenantID: 2,
					AgentUsageSummary: types.AgentUsageSummary{Answers: 1, Sessions: 1, Users: 1},
				},
			}, rows)
		})
	}
}
