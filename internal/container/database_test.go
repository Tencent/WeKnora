package container

import (
	"strings"
	"testing"
)

func TestMySQLMigrationDSNEncodesCredentials(t *testing.T) {
	dsn := mysqlMigrationDSN("user:name", "p@ss#word!", "127.0.0.1:3306", "WeKnora")

	if !strings.Contains(dsn, "user%3Aname:p%40ss%23word%21@tcp(127.0.0.1:3306)/WeKnora") {
		t.Fatalf("mysqlMigrationDSN did not URL-encode credentials: %s", dsn)
	}
	if strings.Contains(dsn, "p@ss#word!") {
		t.Fatalf("mysqlMigrationDSN leaked raw password characters: %s", dsn)
	}
}
