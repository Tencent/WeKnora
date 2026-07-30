package service

import "gorm.io/gorm"

// supportsRowLevelLocking reports whether SELECT ... FOR UPDATE/SHARE is
// supported by the active primary database. SQLite serializes writes through
// its single-connection configuration and must not receive these clauses.
func supportsRowLevelLocking(db *gorm.DB) bool {
	if db == nil || db.Dialector == nil {
		return false
	}
	switch db.Dialector.Name() {
	case "postgres", "mysql":
		return true
	default:
		return false
	}
}
