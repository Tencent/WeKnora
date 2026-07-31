package repository

import (
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func TestSystemSettingQueriesQuoteReservedKeyForMySQL(t *testing.T) {
	sqlDB, _, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}
	defer sqlDB.Close()

	db, err := gorm.Open(mysql.New(mysql.Config{
		Conn:                      sqlDB,
		SkipInitializeWithVersion: true,
	}), &gorm.Config{})
	if err != nil {
		t.Fatalf("gorm.Open() error = %v", err)
	}

	getStmt := db.Session(&gorm.Session{DryRun: true}).
		Where(systemSettingKeyEquals("model.max_concurrency")).
		First(&types.SystemSetting{}).Statement
	if sql := getStmt.SQL.String(); !strings.Contains(sql, "`key` = ?") {
		t.Fatalf("Get SQL does not quote reserved key column: %s", sql)
	}

	listStmt := db.Session(&gorm.Session{DryRun: true}).
		Order(systemSettingListOrder()).
		Find(&[]*types.SystemSetting{}).Statement
	if sql := listStmt.SQL.String(); !strings.Contains(sql, "ORDER BY `category`,`key`") {
		t.Fatalf("List SQL does not quote reserved key column: %s", sql)
	}

	if expr := systemSettingKeyEquals("model.max_concurrency"); expr == nil {
		t.Fatal("systemSettingKeyEquals returned nil")
	}
}
