package database

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLearningSQLiteMigrationConstraintsAndRollback(t *testing.T) {
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "learning.db")+"?_foreign_keys=on")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	up, err := os.ReadFile("../../migrations/sqlite/000014_learning.up.sql")
	require.NoError(t, err)
	down, err := os.ReadFile("../../migrations/sqlite/000014_learning.down.sql")
	require.NoError(t, err)
	for range 2 {
		_, err = db.Exec(string(up))
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO learning_profiles(tenant_id,subject_id) VALUES (1,'web_user:a')`)
		require.NoError(t, err)
		var enabled bool
		var epoch int
		require.NoError(t, db.QueryRow(`SELECT enabled,epoch FROM learning_profiles`).Scan(&enabled, &epoch))
		require.False(t, enabled)
		require.Equal(t, 1, epoch)
		_, err = db.Exec(
			`INSERT INTO learning_quizzes(
				id,tenant_id,subject_id,page_id,knowledge_base_id,epoch,source_stamp,status,created_at,updated_at)
			VALUES ('q1',1,'web_user:a','page','kb',1,'stamp','pending',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		)
		require.NoError(t, err)
		_, err = db.Exec(
			`INSERT INTO learning_quizzes(
				id,tenant_id,subject_id,page_id,knowledge_base_id,epoch,source_stamp,status,created_at,updated_at)
			VALUES ('q2',1,'web_user:a','page','kb',1,'stamp','running',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
		)
		require.Error(t, err)
		_, err = db.Exec(`UPDATE learning_quizzes SET status='ready'`)
		require.NoError(t, err)
		_, err = db.Exec(
			`INSERT INTO learning_questions(
				id,quiz_id,position,prompt,options,correct_option,explanation,evidence,fingerprint)
			VALUES ('question','q1',0,'prompt','[]','0','explanation','[]','fp')`,
		)
		require.NoError(t, err)
		_, err = db.Exec(
			`INSERT INTO learning_attempts(
				tenant_id,subject_id,attempt_id,question_id,quiz_id,page_id,knowledge_base_id,
				fingerprint,source_stamp,before,after,credited,algorithm_version,result,created_at)
			VALUES (
				1,'web_user:a','a1','question','q1','page','kb','fp','stamp',0.2,0.55,
				TRUE,'bkt-v1','{}',CURRENT_TIMESTAMP)`,
		)
		require.NoError(t, err)
		_, err = db.Exec(
			`INSERT INTO learning_attempts SELECT
				tenant_id,subject_id,'a2',question_id,quiz_id,page_id,knowledge_base_id,
				fingerprint,source_stamp,before,after,credited,algorithm_version,result,created_at
			FROM learning_attempts`,
		)
		require.Error(t, err)
		_, err = db.Exec(`DELETE FROM learning_quizzes`)
		require.NoError(t, err)
		var count int
		require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM learning_attempts`).Scan(&count))
		require.Zero(t, count)
		_, err = db.Exec(string(down))
		require.NoError(t, err)
		for _, table := range []string{
			"learning_profiles", "learning_mastery", "learning_quizzes", "learning_questions", "learning_attempts",
		} {
			require.False(t, sqliteTableExists(t, db, table))
		}
	}
}
