package container

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseAutoRecoverDirty(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		present bool
		want    bool
		wantErr bool
	}{
		{name: "unset", present: false, want: false},
		{name: "empty", value: "", present: true, want: false},
		{name: "true", value: "true", present: true, want: true},
		{name: "false", value: "false", present: true, want: false},
		{name: "trimmed", value: "  true  ", present: true, want: true},
		{name: "uppercase", value: "TRUE", present: true, want: true},
		{name: "numeric true", value: "1", present: true, want: true},
		{name: "numeric false", value: "0", present: true, want: false},
		{name: "invalid", value: "enabled", present: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAutoRecoverDirty(tt.value, tt.present)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "AUTO_RECOVER_DIRTY")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestRunStartupMigrations(t *testing.T) {
	t.Run("disabled skips runner and post migration", func(t *testing.T) {
		runnerCalled := false
		postCalled := false
		ran, err := runStartupMigrations(
			"false",
			"invalid but ignored while disabled",
			true,
			"sqlite",
			func(bool) error {
				runnerCalled = true
				return nil
			},
			func() { postCalled = true },
		)
		require.NoError(t, err)
		assert.False(t, ran)
		assert.False(t, runnerCalled)
		assert.False(t, postCalled)
	})

	t.Run("invalid recovery configuration skips runner", func(t *testing.T) {
		runnerCalled := false
		ran, err := runStartupMigrations(
			"",
			"invalid",
			true,
			"sqlite",
			func(bool) error {
				runnerCalled = true
				return nil
			},
			nil,
		)
		require.Error(t, err)
		assert.False(t, ran)
		assert.False(t, runnerCalled)
	})

	for _, driver := range []string{"postgres", "sqlite"} {
		t.Run(driver+" failure stops post migration", func(t *testing.T) {
			migrationErr := errors.New("migration failure")
			postCalled := false
			ran, err := runStartupMigrations(
				"",
				"",
				false,
				driver,
				func(autoRecover bool) error {
					assert.False(t, autoRecover)
					return migrationErr
				},
				func() { postCalled = true },
			)
			require.ErrorIs(t, err, migrationErr)
			assert.False(t, ran)
			assert.False(t, postCalled)
		})
	}

	t.Run("success runs post migration", func(t *testing.T) {
		postCalled := false
		ran, err := runStartupMigrations(
			"",
			" true ",
			true,
			"sqlite",
			func(autoRecover bool) error {
				assert.True(t, autoRecover)
				return nil
			},
			func() { postCalled = true },
		)
		require.NoError(t, err)
		assert.True(t, ran)
		assert.True(t, postCalled)
	})
}
