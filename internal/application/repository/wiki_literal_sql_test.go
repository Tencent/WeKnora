package repository

import (
	"context"
	"database/sql/driver"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func TestWikiSearchLiteralPostgresUsesBoundEscapedPattern(t *testing.T) {
	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sqlDB.Close() }()
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: sqlDB}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewWikiPageRepository(db)
	for _, tc := range []struct {
		query   string
		literal bool
	}{
		{`C++`, true}, {`a.b`, true}, {`dir\file`, true}, {`x' OR 1=1 --`, true}, {`stardust|skyvault`, false},
	} {
		t.Run(tc.query, func(t *testing.T) {
			pattern := tc.query
			if tc.literal {
				pattern = regexp.QuoteMeta(pattern)
			}
			args := []driver.Value{
				pattern, pattern, pattern, pattern, "kb-1",
				pattern, pattern, pattern, pattern, "archived", int64(10),
			}
			expected := mock.ExpectQuery(`SELECT .*~\* \$1`).WithArgs(args...)
			expected.WillReturnRows(sqlmock.NewRows([]string{"id"}))
			if tc.literal {
				_, err = repo.SearchLiteral(context.Background(), "kb-1", tc.query, 10)
			} else {
				_, err = repo.Search(context.Background(), "kb-1", tc.query, 10)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
