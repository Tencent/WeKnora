package container

import (
	"io"
	stdlog "log"
	"time"

	gormlogger "gorm.io/gorm/logger"
)

// gormSlowSQLThreshold is GORM's default slow-query threshold. Statements
// faster than this are not logged at Warn.
const gormSlowSQLThreshold = 200 * time.Millisecond

// newGormLogger is the application database logger. It matches GORM's
// default slow-SQL threshold (200ms), Warn level, and record-not-found
// handling. ParameterizedQueries is set so slow-query logs keep the SQL
// text and timing but omit bound values such as chunk text and vectors.
func newGormLogger(w io.Writer) gormlogger.Interface {
	return gormlogger.New(stdlog.New(w, "\r\n", stdlog.LstdFlags), gormlogger.Config{
		SlowThreshold:             gormSlowSQLThreshold,
		LogLevel:                  gormlogger.Warn,
		IgnoreRecordNotFoundError: false,
		Colorful:                  true,
		ParameterizedQueries:      true,
	})
}
