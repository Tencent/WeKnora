package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestDatabaseQueryToolRejectsMySQLBeforeParsingOrExecutingSQL(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = sqlDB.Close() })

	db, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      sqlDB,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{})
	require.NoError(t, err)

	tool := NewDatabaseQueryTool(db, nil)
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"sql":"SELECT id FROM knowledge_bases"}`))
	require.Error(t, err)
	require.NotNil(t, result)
	require.False(t, result.Success)
	require.True(t, strings.Contains(strings.ToLower(result.Error), "mysql"))
	require.True(t, strings.Contains(strings.ToLower(result.Error), "unavailable"))
	require.NoError(t, mock.ExpectationsWereMet(), "unsupported dialect must not reach the database")
}

func TestDatabaseQuerySupportedDialects(t *testing.T) {
	tests := []struct {
		dialect string
		want    bool
	}{
		{dialect: "postgres", want: true},
		{dialect: "sqlite", want: true},
		{dialect: "mysql", want: false},
		{dialect: "", want: false},
	}

	for _, testCase := range tests {
		t.Run(testCase.dialect, func(t *testing.T) {
			require.Equal(t, testCase.want, DatabaseQuerySupported(testCase.dialect))
		})
	}
}
