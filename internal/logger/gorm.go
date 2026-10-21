package logger

import (
	"io"
	"log"
	"os"
	"time"

	gormlogger "gorm.io/gorm/logger"
)

// NewGORM returns the logger GORM should use for SQL diagnostics. Bind values
// are withheld: a slow embedding batch would otherwise write chunk text and
// vector payloads into the application log (Tencent/WeKnora#4543), while the
// statement and duration still support slow-query triage.
func NewGORM() gormlogger.Interface {
	return NewGORMTo(os.Stdout)
}

// NewGORMTo behaves like NewGORM but writes to w, which keeps the logger
// testable without a live database.
func NewGORMTo(w io.Writer) gormlogger.Interface {
	return gormlogger.New(log.New(w, "\r\n", log.LstdFlags), gormlogger.Config{
		SlowThreshold:             200 * time.Millisecond,
		LogLevel:                  gormlogger.Warn,
		IgnoreRecordNotFoundError: false,
		ParameterizedQueries:      true,
	})
}
