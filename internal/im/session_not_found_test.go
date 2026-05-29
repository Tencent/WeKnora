package im

import (
	"errors"
	"fmt"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"gorm.io/gorm"
)

// TestIsSessionNotFound guards the recovery path for issue #1499.
//
// The session repository translates gorm.ErrRecordNotFound into
// apperrors.ErrSessionNotFound, so an `errors.Is(err, gorm.ErrRecordNotFound)`
// check on the value returned by SessionService.GetSession would silently
// miss — leaving the IM bot permanently unresponsive after the user deletes
// the underlying session from the UI.
func TestIsSessionNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"app sentinel as returned by sessionService.GetSession today", apperrors.ErrSessionNotFound, true},
		{"wrapped app sentinel", fmt.Errorf("get session: %w", apperrors.ErrSessionNotFound), true},
		{"raw gorm sentinel (safety net)", gorm.ErrRecordNotFound, true},
		{"wrapped gorm sentinel", fmt.Errorf("query session: %w", gorm.ErrRecordNotFound), true},
		{"unrelated error", errors.New("boom"), false},
		{"nil", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSessionNotFound(tt.err); got != tt.want {
				t.Errorf("isSessionNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// TestErrSessionNotFoundIsNotGormErrRecordNotFound documents the invariant
// that motivated the bug: the two sentinels are distinct error values, so
// callers must explicitly match the application sentinel.
func TestErrSessionNotFoundIsNotGormErrRecordNotFound(t *testing.T) {
	if errors.Is(apperrors.ErrSessionNotFound, gorm.ErrRecordNotFound) {
		t.Fatal("apperrors.ErrSessionNotFound must not unwrap to gorm.ErrRecordNotFound; " +
			"if this changes, the IM recovery path can be simplified accordingly")
	}
}
