package repository

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"testing"

	applogger "github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/mattn/go-sqlite3"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func learningErrorContext(output *bytes.Buffer) context.Context {
	log := logrus.New()
	log.SetOutput(output)
	log.SetFormatter(&logrus.JSONFormatter{})
	ctx := context.WithValue(context.Background(), types.LoggerContextKey, logrus.NewEntry(log))
	return applogger.WithRequestID(ctx, "learning-error-test")
}

func TestLearningDatabaseErrorClassificationAndRedaction(t *testing.T) {
	for _, test := range []struct {
		name, class, sqlstate string
		err, want             error
	}{
		{name: "nil"},
		{name: "not found", err: gorm.ErrRecordNotFound, want: types.ErrLearningNotFound},
		{name: "disabled", err: types.ErrLearningDisabled, want: types.ErrLearningDisabled},
		{name: "conflict", err: types.ErrLearningConflict, want: types.ErrLearningConflict},
		{name: "capacity", err: types.ErrLearningBusy, want: types.ErrLearningBusy},
		{name: "canceled", err: context.Canceled, want: context.Canceled},
		{name: "deadline", err: context.DeadlineExceeded, want: context.DeadlineExceeded},
		{name: "sqlite busy", err: sqlite3.Error{Code: sqlite3.ErrBusy}, want: types.ErrLearningBusy},
		{name: "sqlite locked", err: sqlite3.Error{Code: sqlite3.ErrLocked}, want: types.ErrLearningBusy},
		{name: "serialization", err: &pgconn.PgError{Code: "40001"}, want: types.ErrLearningBusy},
		{name: "deadlock", err: &pgconn.PgError{Code: "40P01"}, want: types.ErrLearningBusy},
		{
			name: "postgres missing table", class: "postgres", sqlstate: "42P01",
			err:  &pgconn.PgError{Code: "42P01", Message: "PRIVATE_ANSWER", Detail: "SECRET_SQL"},
			want: types.ErrLearningUnavailable,
		},
		{
			name: "invalid sqlstate", class: "postgres",
			err: &pgconn.PgError{Code: "PRIVATE_ANSWER\nSQL"}, want: types.ErrLearningUnavailable,
		},
		{
			name: "sqlite syntax", class: "sqlite",
			err: sqlite3.Error{Code: sqlite3.ErrError}, want: types.ErrLearningUnavailable,
		},
		{name: "connection", class: "connection", err: driver.ErrBadConn, want: types.ErrLearningUnavailable},
		{name: "closed connection", class: "connection", err: sql.ErrConnDone, want: types.ErrLearningUnavailable},
		{
			name: "network", class: "network",
			err: &net.OpError{Op: "dial", Err: errors.New("PRIVATE_ANSWER")}, want: types.ErrLearningUnavailable,
		},
		{
			name: "unknown", class: "database",
			err: errors.New("PRIVATE_ANSWER SECRET_SQL"), want: types.ErrLearningUnavailable,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			err := test.err
			if err != nil {
				err = fmt.Errorf("PRIVATE_ANSWER SECRET_SQL: %w", err)
			}
			got := learningDBError(learningErrorContext(&output), err)
			require.ErrorIs(t, got, test.want)
			if got != nil {
				require.NotContains(t, got.Error(), "PRIVATE_ANSWER")
				require.NotContains(t, got.Error(), "SECRET_SQL")
			}
			if test.class == "" {
				require.Empty(t, output.String())
				return
			}
			var record map[string]any
			require.NoError(t, json.Unmarshal(output.Bytes(), &record))
			require.Equal(t, test.class, record["error_class"])
			require.Equal(t, "learning-error-test", record["request_id"])
			require.Equal(t, "learning_repository", record["component"])
			if test.sqlstate == "" {
				require.NotContains(t, record, "sqlstate")
			} else {
				require.Equal(t, test.sqlstate, record["sqlstate"])
			}
			require.NotContains(t, output.String(), "PRIVATE_ANSWER")
			require.NotContains(t, output.String(), "SECRET_SQL")
		})
	}
}

func TestLearningDatabaseFailuresAreNotRetriedAsBusy(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	conn, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	repo := NewLearningRepository(db).(*learningRepository)
	var output bytes.Buffer
	ctx := learningErrorContext(&output)
	scope := interfaces.LearningScope{TenantID: 7, SubjectID: "web_user:review"}
	_, err = repo.Settings(ctx, scope)
	require.ErrorIs(t, err, types.ErrLearningUnavailable)
	require.Contains(t, output.String(), `"error_class":"sqlite"`)
	require.NotContains(t, output.String(), "learning_profiles")

	calls := 0
	err = repo.transaction(ctx, func(*gorm.DB) error {
		calls++
		return errors.New("PRIVATE_ANSWER")
	})
	require.ErrorIs(t, err, types.ErrLearningUnavailable)
	require.Equal(t, 1, calls)

	calls = 0
	err = repo.transaction(ctx, func(*gorm.DB) error {
		calls++
		return sqlite3.Error{Code: sqlite3.ErrBusy}
	})
	require.ErrorIs(t, err, types.ErrLearningBusy)
	require.Equal(t, 8, calls)
	require.NoError(t, conn.Close())
	_, err = repo.Settings(ctx, scope)
	require.ErrorIs(t, err, types.ErrLearningUnavailable)
	require.NotContains(t, output.String(), "PRIVATE_ANSWER")
}
