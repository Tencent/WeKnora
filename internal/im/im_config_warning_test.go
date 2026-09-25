package im

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// imImageConfigWarning warns only when IM channels are active AND APP_EXTERNAL_URL
// is unset — the config state that silently breaks resource:// images in IM.
func TestIMImageConfigWarning(t *testing.T) {
	t.Run("no channels -> silent", func(t *testing.T) {
		assert.Empty(t, imImageConfigWarning(0, ""))
	})
	t.Run("external URL set -> silent", func(t *testing.T) {
		assert.Empty(t, imImageConfigWarning(3, "https://weknora.example.com"))
	})
	t.Run("external URL whitespace-only -> warns", func(t *testing.T) {
		assert.NotEmpty(t, imImageConfigWarning(1, "   "))
	})
	t.Run("channels active but external URL empty -> warns with count", func(t *testing.T) {
		msg := imImageConfigWarning(2, "")
		assert.Contains(t, msg, "APP_EXTERNAL_URL")
		assert.Contains(t, msg, "2 IM channel")
		assert.Contains(t, msg, "/r/")
	})
}
