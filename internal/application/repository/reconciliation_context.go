package repository

import (
	"context"

	"gorm.io/gorm"
)

type reconciliationDBContextKey struct{}

func withReconciliationDB(ctx context.Context, db *gorm.DB) context.Context {
	return context.WithValue(ctx, reconciliationDBContextKey{}, db)
}

// DBFromContext returns the reconciliation transaction when one owns the
// current callback, otherwise it returns the repository's regular handle.
func DBFromContext(ctx context.Context, fallback *gorm.DB) *gorm.DB {
	if db, ok := ctx.Value(reconciliationDBContextKey{}).(*gorm.DB); ok && db != nil {
		return db.WithContext(ctx)
	}
	return fallback.WithContext(ctx)
}

// IsReconciliationTransaction reports whether ctx carries the transaction
// that serializes a destructive knowledge reconciliation.
func IsReconciliationTransaction(ctx context.Context) bool {
	_, ok := ctx.Value(reconciliationDBContextKey{}).(*gorm.DB)
	return ok
}
