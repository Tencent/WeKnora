package database

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type bridgeFixture struct {
	dsn   string
	opts  MigrationOptions
	m     *migrationConnection
	files *migrationFiles
}

func newBridgeFixture(t *testing.T, dialect string) *bridgeFixture {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
	opts := MigrationOptions{MigrationsRoot: filepath.Join(root, "migrations"), BackupID: "x03-isolated-fixture"}
	dsn := "sqlite3://unused"
	if dialect == "sqlite" {
		opts.SQLiteDBPath = filepath.Join(t.TempDir(), "测试 path with spaces.sqlite")
	} else {
		dsn = os.Getenv("TEST_POSTGRES_MIGRATION_DSN")
		if dsn == "" {
			if os.Getenv("REQUIRE_POSTGRES_TESTS") == "1" {
				t.Fatal("PostgreSQL tests require explicit isolated TEST_POSTGRES_MIGRATION_DSN")
			}
			t.Skip("TEST_POSTGRES_MIGRATION_DSN is not configured")
		}
		u, err := url.Parse(dsn)
		require.NoError(t, err)
		require.True(
			t,
			strings.HasPrefix(strings.TrimPrefix(u.Path, "/"), "weknora_x03_"),
			"test target must be an isolated weknora_x03_ database",
		)
		dsn, err = createIsolatedMigrationDatabase(context.Background(), dsn, "case")
		require.NoError(t, err)
		u, err = url.Parse(dsn)
		require.NoError(t, err)
		q := u.Query()
		q.Set("x-skip-embedding", "true")
		u.RawQuery = q.Encode()
		dsn = u.String()
	}
	m, err := openMigrationConnection(context.Background(), dsn, opts, false)
	require.NoError(t, err)
	t.Cleanup(m.close)
	files, err := loadMigrationFiles(opts.MigrationsRoot)
	require.NoError(t, err)
	return &bridgeFixture{dsn: dsn, opts: opts, m: m, files: files}
}

func (f *bridgeFixture) seed(t *testing.T, official, legacyTopic int) {
	t.Helper()
	ctx := context.Background()
	if f.m.dialect == "postgres" {
		_, err := f.m.conn.ExecContext(ctx, "SELECT set_config('app.skip_embedding','true',false)")
		require.NoError(t, err)
	}
	for _, file := range f.files.chain(f.m.dialect, "official") {
		if file.Version <= official {
			_, err := f.m.conn.ExecContext(ctx, file.SQL)
			require.NoError(t, err, file.Path)
		}
	}
	for _, file := range f.files.chain(f.m.dialect, "topic3") {
		if file.Version <= legacyTopic {
			_, err := f.m.conn.ExecContext(ctx, file.SQL)
			require.NoError(t, err, file.Path)
		}
	}
	if official >= 0 {
		version := official
		if legacyTopic > 0 {
			offset := 89
			if f.m.dialect == "sqlite" {
				offset = 12
			}
			version = offset + legacyTopic
		}
		_, err := f.m.conn.ExecContext(ctx, fmt.Sprintf(
			"CREATE TABLE schema_migrations(version BIGINT NOT NULL PRIMARY KEY,"+
				"dirty BOOLEAN NOT NULL); INSERT INTO schema_migrations VALUES (%d,"+
				"false)", version,
		))
		require.NoError(t, err)
	}
}

func (f *bridgeFixture) apply(t *testing.T) *MigrationStatus {
	t.Helper()
	state, err := ApplyMigrations(context.Background(), f.dsn, f.opts)
	require.NoError(t, err)
	require.True(t, state.Ready)
	require.Equal(
		t,
		f.files.chain(f.m.dialect, "topic3")[len(f.files.chain(f.m.dialect, "topic3"))-1].Version,
		state.Topic3.Version,
	)
	return state
}

func (f *bridgeFixture) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	_, err := f.m.conn.ExecContext(context.Background(), query, args...)
	require.NoError(t, err)
}

func TestMigrationBridgeSourceMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		baseline, target := 12, 14
		if dialect == "postgres" {
			baseline, target = 89, 93
		}
		cases := []struct {
			name            string
			official, topic int
		}{
			{"empty", -1, 0},
			{"baseline", baseline, 0},
			{"official", target, 0},
			{"topic3-endpoint", baseline, 14},
			{"ambiguous-version-topic3", baseline, 1},
		}
		if dialect == "postgres" {
			cases = append(cases, struct {
				name            string
				official, topic int
			}{"official-79", 79, 0})
		}
		for _, test := range cases {
			t.Run(dialect+"/"+test.name, func(t *testing.T) {
				f := newBridgeFixture(t, dialect)
				f.seed(t, test.official, test.topic)
				if test.official >= 0 {
					f.exec(t, "INSERT INTO tenants(id,name,business) VALUES(7001,'x03-sentinel',"+"'fixture')")
				}
				before, err := InspectMigrations(context.Background(), f.dsn, f.opts)
				require.NoError(t, err)
				require.False(t, before.Ready)
				state := f.apply(t)
				officialChain := f.files.chain(dialect, "official")
				require.Equal(t, officialChain[len(officialChain)-1].Version, state.Official.Version)
				if test.official >= 0 {
					var name string
					require.NoError(
						t,
						f.m.conn.QueryRowContext(
							context.Background(), "SELECT name FROM tenants WHERE id=7001",
						).Scan(&name),
					)
					require.Equal(t, "x03-sentinel", name)
				}
				again := f.apply(t)
				require.Equal(t, state.RunID, again.RunID)
				require.NoError(t, ValidateMigrationReadiness(context.Background(), f.dsn, f.opts))
			})
		}
	}
}

func TestMigrationBridgeRejectsUnknownAndPartial(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		baseline := 12
		if dialect == "postgres" {
			baseline = 89
		}
		for _, test := range []struct{ name, sql string }{
			{"dirty", "UPDATE schema_migrations SET dirty=true"},
			{"unknown-version", "UPDATE schema_migrations SET version=999"},
			{"missing-column", "ALTER TABLE models RENAME COLUMN display_name TO unexpected_name"},
			{"missing-index", "DROP INDEX idx_models_type"},
			{"multiple-versions", "INSERT INTO schema_migrations VALUES(1,false)"},
			{"unowned-topic-table", "CREATE TABLE evaluation_tasks(id TEXT)"},
		} {
			t.Run(dialect+"/"+test.name, func(t *testing.T) {
				f := newBridgeFixture(t, dialect)
				f.seed(t, baseline, 0)
				f.exec(t, test.sql)
				_, err := ApplyMigrations(context.Background(), f.dsn, f.opts)
				require.Error(t, err)
				_, tables, err := describeSchema(context.Background(), f.m.conn, dialect)
				require.NoError(t, err)
				require.False(t, tables[MigrationBridgeTable])
				require.False(t, tables[Topic3MigrationTable])
			})
		}
	}
}

func TestMigrationBridgeResumeCommittedPhases(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		official := 13
		if dialect == "postgres" {
			official = 91
		}
		for _, phase := range []string{"accepted", fmt.Sprintf("official:%d", official), "topic3:10"} {
			t.Run(dialect+"/"+phase, func(t *testing.T) {
				f := newBridgeFixture(t, dialect)
				opts := f.opts
				opts.testCheckpoint = func(current string) error {
					if current == phase {
						return errors.New("injected process interruption")
					}
					return nil
				}
				_, err := ApplyMigrations(context.Background(), f.dsn, opts)
				require.ErrorContains(t, err, "injected process interruption")
				partial, err := InspectMigrations(context.Background(), f.dsn, f.opts)
				require.NoError(t, err)
				require.False(t, partial.Ready)
				state := f.apply(t)
				require.Equal(t, partial.RunID, state.RunID)
				var count int
				require.NoError(
					t,
					f.m.conn.QueryRowContext(context.Background(), "SELECT count(*) FROM migration_bridge_runs").Scan(
						&count,
					),
				)
				require.Equal(t, 1, count)
			})
		}
	}
}

func TestMigrationBridgeReadOnlyAndStartupGate(t *testing.T) {
	f := newBridgeFixture(t, "sqlite")
	require.Error(t, PrepareDatabaseSchema(context.Background(), f.dsn, f.opts, false))
	f.apply(t)
	before, err := os.ReadFile(f.opts.SQLiteDBPath)
	require.NoError(t, err)
	state, err := InspectMigrations(context.Background(), f.dsn, f.opts)
	require.NoError(t, err)
	require.True(t, state.Ready)
	require.NoError(t, PrepareDatabaseSchema(context.Background(), f.dsn, f.opts, false))
	after, err := os.ReadFile(f.opts.SQLiteDBPath)
	require.NoError(t, err)
	require.Equal(t, sha256.Sum256(before), sha256.Sum256(after))
	f.exec(t, "UPDATE topic3_schema_migrations SET dirty=true")
	require.Error(t, PrepareDatabaseSchema(context.Background(), f.dsn, f.opts, false))
	require.Error(t, PrepareDatabaseSchema(context.Background(), f.dsn, f.opts, true))
	opts := f.opts
	opts.AutoRecoverDirty = true
	require.Error(t, PrepareDatabaseSchema(context.Background(), f.dsn, opts, true))
	opts = f.opts
	opts.SQLiteDBPath = filepath.Join(t.TempDir(), "absent.sqlite")
	_, err = InspectMigrations(context.Background(), f.dsn, opts)
	require.Error(t, err)
	_, err = os.Stat(opts.SQLiteDBPath)
	require.True(t, os.IsNotExist(err))
	_, err = os.Stat(opts.SQLiteDBPath + ".migration.lock")
	require.True(t, os.IsNotExist(err))
}

func TestMigrationBridgeRequiresBackupIdentity(t *testing.T) {
	f := newBridgeFixture(t, "sqlite")
	f.seed(t, 12, 14)
	opts := f.opts
	opts.BackupID = ""
	_, err := ApplyMigrations(context.Background(), f.dsn, opts)
	require.ErrorContains(t, err, "BackupID")
	var version int
	require.NoError(
		t,
		f.m.conn.QueryRowContext(context.Background(), "SELECT version FROM schema_migrations").Scan(&version),
	)
	require.Equal(t, 26, version)
}

func TestMigrationBridgeSubprocessWorker(t *testing.T) {
	if os.Getenv("X03_SUBPROCESS") != "1" {
		return
	}
	opts := MigrationOptions{
		SQLiteDBPath:   os.Getenv("X03_SQLITE_PATH"),
		MigrationsRoot: os.Getenv("X03_MIGRATION_ROOT"),
		BackupID:       "x03-subprocess",
	}
	opts.testCheckpoint = func(phase string) error {
		if phase == "accepted" {
			time.Sleep(150 * time.Millisecond)
		}
		return nil
	}
	_, err := ApplyMigrations(context.Background(), os.Getenv("X03_MIGRATION_DSN"), opts)
	require.NoError(t, err)
}

func TestMigrationBridgeCrossProcessLock(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			f := newBridgeFixture(t, dialect)
			binary, err := os.Executable()
			require.NoError(t, err)
			commands := []*exec.Cmd{}
			for range 2 {
				cmd := exec.Command(binary, "-test.run=^TestMigrationBridgeSubprocessWorker$", "-test.v")
				cmd.Env = append(
					os.Environ(),
					"X03_SUBPROCESS=1",
					"X03_SQLITE_PATH="+f.opts.SQLiteDBPath,
					"X03_MIGRATION_ROOT="+f.opts.MigrationsRoot,
					"X03_MIGRATION_DSN="+f.dsn,
				)
				commands = append(commands, cmd)
			}
			errors := make(chan error, 2)
			for _, cmd := range commands {
				go func(cmd *exec.Cmd) {
					output, err := cmd.CombinedOutput()
					if err != nil {
						err = fmt.Errorf("%w: %s", err, output)
					}
					errors <- err
				}(cmd)
			}
			require.NoError(t, <-errors)
			require.NoError(t, <-errors)
			state, err := InspectMigrations(context.Background(), f.dsn, f.opts)
			require.NoError(t, err)
			require.True(t, state.Ready)
		})
	}
}

func TestMigrationBridgeAppendOnlyUpgrade(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			f := newBridgeFixture(t, dialect)
			baseline := 12
			if dialect == "postgres" {
				baseline = 89
			}
			f.seed(t, baseline, 14)
			blob := []byte{0, 0, 128, 63}
			marks := []string{}
			for i := 1; i <= 6; i++ {
				marks = append(marks, parameter(dialect, i))
			}
			f.exec(t, "INSERT INTO embedding_cache_entries(tenant_id,model_id,"+
				"model_fingerprint,request_options_sha256,text_sha256,embedding,"+
				"dimension,expires_at,accessed_at,created_at,updated_at) VALUES(7001,"+strings.Join(marks, ",")+
				",CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,"+"CURRENT_TIMESTAMP)",
				"x03-model", strings.Repeat("a", 64), strings.Repeat("b", 64), strings.Repeat("c", 64), blob, 1,
			)
			opts := f.opts
			opts.testCheckpoint = func(phase string) error {
				if phase == "topic3:15" {
					return errors.New("stop before appended migration")
				}
				return nil
			}
			_, err := ApplyMigrations(context.Background(), f.dsn, opts)
			require.ErrorContains(t, err, "stop before")
			prior := map[string]string{}
			for path, digest := range f.files.Hashes {
				if !strings.HasPrefix(path, "topic3/") || !strings.Contains(path, "000016_") {
					prior[path] = digest
				}
			}
			encoded, err := json.Marshal(prior)
			require.NoError(t, err)
			f.exec(
				t,
				"UPDATE migration_bridge_runs SET input_sha256="+parameter(dialect, 1)+",input_manifest="+
					parameter(dialect, 2)+
					",topic3_target=15,phase='complete',completed_at=updated_at",
				hashJSON(prior),
				string(encoded),
			)
			pending, err := InspectMigrations(context.Background(), f.dsn, f.opts)
			require.NoError(t, err)
			require.False(t, pending.Ready)
			require.Len(t, pending.Pending, 1)
			state := f.apply(t)
			require.Equal(t, pending.RunID, state.RunID)
			var actual []byte
			require.NoError(
				t,
				f.m.conn.QueryRowContext(
					context.Background(),
					"SELECT embedding FROM embedding_cache_entries WHERE tenant_id=7001",
				).Scan(
					&actual,
				),
			)
			require.Equal(t, blob, actual)
			var stored string
			require.NoError(
				t,
				f.m.conn.QueryRowContext(context.Background(), "SELECT input_sha256 FROM migration_bridge_runs").Scan(
					&stored,
				),
			)
			require.Equal(t, f.files.SHA256, stored)
		})
	}
	prior := map[string]string{"topic3/sqlite/000015_a.up.sql": "a", "topic3/sqlite/000015_a.down.sql": "b"}
	next := map[string]string{}
	for k, v := range prior {
		next[k] = v
	}
	next["topic3/sqlite/000016_b.up.sql"] = "c"
	next["topic3/sqlite/000016_b.down.sql"] = "d"
	require.NoError(t, validateAppendOnlyManifest(prior, next))
	next["topic3/sqlite/000015_a.up.sql"] = "changed"
	require.Error(t, validateAppendOnlyManifest(prior, next))
	next["topic3/sqlite/000015_a.up.sql"] = "a"
	next["topic3/sqlite/000014_c.up.sql"] = "insert"
	require.Error(t, validateAppendOnlyManifest(prior, next))
}

func TestMigrationBridgeSQLRollbackAndRecovery(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			f := newBridgeFixture(t, dialect)
			opts := f.opts
			opts.testCheckpoint = func(phase string) error {
				if phase != "accepted" {
					return nil
				}
				if dialect == "sqlite" {
					f.exec(t, "CREATE TRIGGER x03_fail_progress BEFORE UPDATE ON "+
						"migration_bridge_runs WHEN NEW.phase <> OLD.phase BEGIN SELECT "+
						"RAISE(ABORT,'x03 injected receipt failure'); END")
				} else {
					f.exec(t, "CREATE FUNCTION x03_fail_progress() RETURNS trigger LANGUAGE plpgsql "+
						"AS $$ BEGIN IF NEW.phase <> OLD.phase THEN RAISE EXCEPTION 'x03 "+
						"injected receipt failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER "+
						"x03_fail_progress BEFORE UPDATE ON migration_bridge_runs FOR EACH ROW "+
						"EXECUTE FUNCTION x03_fail_progress()")
				}
				return nil
			}
			_, err := ApplyMigrations(context.Background(), f.dsn, opts)
			require.ErrorContains(t, err, "x03 injected receipt failure")
			_, tables, err := describeSchema(context.Background(), f.m.conn, dialect)
			require.NoError(t, err)
			require.False(t, tables["tenants"], "DDL must roll back together with receipt failure")
			version, _, err := readChainVersion(context.Background(), f.m.conn, OfficialMigrationTable, true)
			require.NoError(t, err)
			require.Equal(t, -1, version)
			var code string
			require.NoError(
				t,
				f.m.conn.QueryRowContext(context.Background(), "SELECT error_code FROM migration_bridge_runs").Scan(
					&code,
				),
			)
			require.Equal(t, "official_0_transaction_failed", code)
			if dialect == "sqlite" {
				f.exec(t, "DROP TRIGGER x03_fail_progress")
			} else {
				f.exec(t, "DROP TRIGGER x03_fail_progress ON migration_bridge_runs; DROP "+
					"FUNCTION x03_fail_progress()")
			}
			f.apply(t)
		})
	}
}

func TestMigrationBridgeRejectsReceiptAndIndexPollution(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			f := newBridgeFixture(t, dialect)
			f.apply(t)
			f.exec(t, "DROP INDEX topic3_schema_migrations_version_unique; CREATE UNIQUE "+
				"INDEX topic3_schema_migrations_version_unique ON "+
				"topic3_schema_migrations(dirty)")
			_, err := InspectMigrations(context.Background(), f.dsn, f.opts)
			require.ErrorContains(t, err, "version index")
			f.exec(t, "DROP INDEX topic3_schema_migrations_version_unique; CREATE UNIQUE "+
				"INDEX topic3_schema_migrations_version_unique ON "+
				"topic3_schema_migrations(version)")
			f.exec(t, "UPDATE migration_bridge_runs SET source_version=999")
			_, err = ApplyMigrations(context.Background(), f.dsn, f.opts)
			require.Error(t, err)
		})
	}
}

func TestMigrationBridgeRejectsOrphans(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			f := newBridgeFixture(t, dialect)
			baseline := 12
			if dialect == "postgres" {
				baseline = 89
			}
			f.seed(t, baseline, 0)
			if dialect == "sqlite" {
				f.exec(t, "PRAGMA foreign_keys=OFF")
			} else {
				f.exec(t, "SET session_replication_role=replica")
			}
			f.exec(t, "INSERT INTO mcp_tool_approvals(id,tenant_id,service_id,tool_name) "+
				"VALUES('x03-orphan',7001,'missing-service','tool')")
			if dialect == "sqlite" {
				f.exec(t, "PRAGMA foreign_keys=ON")
			} else {
				f.exec(t, "SET session_replication_role=origin")
			}
			_, err := ApplyMigrations(context.Background(), f.dsn, f.opts)
			require.ErrorContains(t, err, "orphan")
			_, tables, err := describeSchema(context.Background(), f.m.conn, dialect)
			require.NoError(t, err)
			require.False(t, tables[MigrationBridgeTable])
		})
	}
}

func TestMigrationBridgeSkillCatalogBackfill(t *testing.T) {
	f := newBridgeFixture(t, "postgres")
	f.seed(t, 89, 0)
	f.exec(t, "INSERT INTO tenant_skills(id,tenant_id,name,sandbox_config_id,"+
		"bundle_ref,updated_at,status) VALUES('x03-archive',7001,'same-skill',"+
		"'sandbox-one','archive-ref','2026-01-01','ready'),('x03-recent',7001,"+
		"'same-skill','sandbox-two','','2026-02-01','ready'),('x03-other',7002,"+
		"'same-skill','sandbox-three','other-ref','2026-01-01','ready')")
	u, err := url.Parse(f.dsn)
	require.NoError(t, err)
	q := u.Query()
	q.Del("x-skip-embedding")
	q.Set("options", "-c app.skip_embedding=true")
	u.RawQuery = q.Encode()
	f.dsn = u.String()
	f.apply(t)
	var count int
	require.NoError(
		t,
		f.m.conn.QueryRowContext(context.Background(), "SELECT count(*) FROM tenant_skill_catalog").Scan(&count),
	)
	require.Equal(t, 2, count)
	var catalog string
	require.NoError(
		t,
		f.m.conn.QueryRowContext(
			context.Background(),
			"SELECT catalog_id FROM tenant_skills WHERE id='x03-recent'",
		).Scan(
			&catalog,
		),
	)
	require.Equal(t, "x03-archive", catalog)
	f.exec(t, "UPDATE tenant_skills SET catalog_id='x03-other' WHERE id='x03-recent'")
	_, err = InspectMigrations(context.Background(), f.dsn, f.opts)
	require.ErrorContains(t, err, "skill")
}

func TestMigrationBridgePostgresVectorAndExtensionOwnership(t *testing.T) {
	f := newBridgeFixture(t, "postgres")
	u, err := url.Parse(f.dsn)
	require.NoError(t, err)
	q := u.Query()
	q.Set("x-skip-embedding", "false")
	u.RawQuery = q.Encode()
	f.dsn = u.String()
	var installed bool
	require.NoError(t, f.m.conn.QueryRowContext(
		context.Background(), "SELECT EXISTS(SELECT 1 FROM pg_available_extensions WHERE "+"name='postgis')",
	).Scan(&installed))
	if installed {
		f.exec(t, "CREATE EXTENSION postgis")
	}
	f.apply(t)
	_, tables, err := describeSchema(context.Background(), f.m.conn, "postgres")
	require.NoError(t, err)
	require.True(t, tables["embeddings"])
	require.False(t, tables["spatial_ref_sys"])
	f.exec(t, "CREATE TABLE x03_unknown_business(id INTEGER)")
	_, err = InspectMigrations(context.Background(), f.dsn, f.opts)
	require.ErrorContains(t, err, "x03_unknown_business")
}

func TestMigrationBridgeDatabaseWideLock(t *testing.T) {
	f := newBridgeFixture(t, "postgres")
	f.exec(t, "CREATE SCHEMA x03_other_lock_schema")
	unlock, err := f.m.lock(context.Background(), time.Second)
	require.NoError(t, err)
	defer unlock()
	u, err := url.Parse(f.dsn)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", "x03_other_lock_schema")
	u.RawQuery = q.Encode()
	other, err := openMigrationConnection(context.Background(), u.String(), f.opts, false)
	require.NoError(t, err)
	defer other.close()
	_, err = other.lock(context.Background(), 80*time.Millisecond)
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestPostgresIndexJSONSemanticComparison(t *testing.T) {
	a := schemaObjects{
		"embeddings/index/embeddings_search_idx": "CREATE INDEX embeddings_search_idx ON embeddings USING bm25 (id, " +
			"knowledge_base_id, content, knowledge_id, chunk_id) WITH " +
			"(key_field=id, text_fields='{ \"content\": { \"tokenizer\": { \"type\": " +
			"\"chinese_lindera\" } } }')|true",
	}
	b := schemaObjects{
		"embeddings/index/embeddings_search_idx": "CREATE INDEX embeddings_search_idx ON embeddings USING bm25 (id, " +
			"knowledge_base_id, content, knowledge_id, chunk_id) WITH " +
			"(key_field=id, text_fields='{\"content\":{\"tokenizer\":{\"type\":\"chinese_l" +
			"indera\"}}}')|true",
	}
	require.Empty(t, schemaDifference(a, b))
	require.Equal(t, hashSchema(a), hashSchema(b))
	for _, replace := range [][2]string{
		{"chinese_lindera", "unicode"},
		{"key_field=id", "key_field=source_id"},
		{"content, knowledge_id", "content, source_id"},
	} {
		changed := schemaObjects{}
		for key, value := range b {
			changed[key] = strings.ReplaceAll(value, replace[0], replace[1])
		}
		require.NotEmpty(t, schemaDifference(a, changed))
	}
}
