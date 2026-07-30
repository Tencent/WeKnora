package service

import (
	"testing"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

type rowLockTestDialector string

func (d rowLockTestDialector) Name() string {
	return string(d)
}

func (d rowLockTestDialector) Initialize(*gorm.DB) error {
	return nil
}

func (d rowLockTestDialector) Migrator(*gorm.DB) gorm.Migrator {
	return nil
}

func (d rowLockTestDialector) DataTypeOf(*schema.Field) string {
	return ""
}

func (d rowLockTestDialector) DefaultValueOf(*schema.Field) clause.Expression {
	return clause.Expr{SQL: "DEFAULT"}
}

func (d rowLockTestDialector) BindVarTo(clause.Writer, *gorm.Statement, interface{}) {
}

func (d rowLockTestDialector) QuoteTo(clause.Writer, string) {
}

func (d rowLockTestDialector) Explain(sql string, _ ...interface{}) string {
	return sql
}

func rowLockTestDB(name string) *gorm.DB {
	return &gorm.DB{Config: &gorm.Config{Dialector: rowLockTestDialector(name)}}
}

func TestSupportsRowLevelLockingIncludesMySQL(t *testing.T) {
	for _, dialect := range []string{"postgres", "mysql"} {
		if !supportsRowLevelLocking(rowLockTestDB(dialect)) {
			t.Fatalf("%s should support row-level locking", dialect)
		}
	}
	for _, dialect := range []string{"sqlite", ""} {
		if supportsRowLevelLocking(rowLockTestDB(dialect)) {
			t.Fatalf("%s should not emit row-level locking clauses", dialect)
		}
	}
}
