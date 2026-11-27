package runtime

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateStartupEnv(t *testing.T) {
	t.Run("allows unset SYSTEM_AES_KEY", func(t *testing.T) {
		t.Setenv("SYSTEM_AES_KEY", "")
		assert.NoError(t, ValidateStartupEnv())
	})

	t.Run("allows a raw 32-byte SYSTEM_AES_KEY", func(t *testing.T) {
		t.Setenv("SYSTEM_AES_KEY", "01234567890123456789012345678901")
		assert.NoError(t, ValidateStartupEnv())
	})

	t.Run("allows a 64-char hex SYSTEM_AES_KEY", func(t *testing.T) {
		t.Setenv("SYSTEM_AES_KEY", hex.EncodeToString([]byte("01234567890123456789012345678901")))
		assert.NoError(t, ValidateStartupEnv())
	})

	t.Run("rejects a set-but-invalid SYSTEM_AES_KEY", func(t *testing.T) {
		t.Setenv("SYSTEM_AES_KEY", "too-short")
		err := ValidateStartupEnv()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SYSTEM_AES_KEY")
	})

	t.Run("rejects 64 chars that are not hex", func(t *testing.T) {
		t.Setenv("SYSTEM_AES_KEY", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz")
		err := ValidateStartupEnv()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "SYSTEM_AES_KEY")
	})
}
