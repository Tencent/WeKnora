package allocation

import "fmt"

const (
	// DefaultEmptyRetentionDays is how long a user machine with no live
	// session is kept when the config leaves the value unset.
	DefaultEmptyRetentionDays = 7
	// MaxEmptyRetentionDays bounds the setting; there is no "keep forever".
	MaxEmptyRetentionDays = 90
)

// EffectiveRetentionDays maps the stored value (0 = unset) onto days.
func EffectiveRetentionDays(stored int) int {
	if stored <= 0 {
		return DefaultEmptyRetentionDays
	}
	return stored
}

// ValidateRetentionDays accepts 0 (use the default) through 90.
func ValidateRetentionDays(stored int) error {
	if stored < 0 || stored > MaxEmptyRetentionDays {
		return fmt.Errorf("user_sandbox_empty_retention_days must be between 1 and %d, or 0 for the default of %d",
			MaxEmptyRetentionDays, DefaultEmptyRetentionDays)
	}
	return nil
}
