package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMigrationCommandExplicitTargetAndSecrets(t *testing.T) {
	for _, key := range []string{"MIGRATION_DSN", "DB_URL", "DB_DRIVER", "DB_PATH"} {
		t.Setenv(key, "")
	}
	t.Setenv("DB_PASSWORD", "password-sentinel")
	var out, errout bytes.Buffer
	require.Equal(t, 2, run([]string{"inspect"}, &out, &errout))
	require.Contains(t, errout.String(), "no implicit database target")
	for _, action := range []string{"force", "down", "goto"} {
		out.Reset()
		errout.Reset()
		require.Equal(t, 2, run([]string{action}, &out, &errout))
		require.Empty(t, out.String())
		require.NotContains(t, errout.String(), "password-sentinel")
	}
	for _, dsn := range []string{
		"postgres://user:password-sentinel@%invalid/db",
		"unrecognized://user:password-sentinel@localhost/db",
	} {
		t.Setenv("MIGRATION_DSN", dsn)
		out.Reset()
		errout.Reset()
		require.Equal(t, 1, run([]string{"inspect", "--root", "../../migrations"}, &out, &errout))
		require.NotContains(t, out.String()+errout.String(), "password-sentinel")
	}
}

func TestMigrationCommandCreatesDualPairsAndRejectsMismatch(t *testing.T) {
	root := t.TempDir()
	for _, dialect := range []string{"postgres", "sqlite"} {
		require.NoError(t, os.MkdirAll(filepath.Join(root, "topic3", dialect), 0o755))
		for _, direction := range []string{"up", "down"} {
			require.NoError(
				t,
				os.WriteFile(
					filepath.Join(root, "topic3", dialect, "000015_existing."+direction+".sql"),
					[]byte("existing"),
					0o644,
				),
			)
		}
	}
	var out, errout bytes.Buffer
	require.Zero(t, run([]string{"create", "--root", root, "feature_name"}, &out, &errout), errout.String())
	for _, dialect := range []string{"postgres", "sqlite"} {
		for _, direction := range []string{"up", "down"} {
			content, err := os.ReadFile(
				filepath.Join(root, "topic3", dialect, "000016_feature_name."+direction+".sql"),
			)
			require.NoError(t, err)
			require.NotEmpty(t, content)
		}
	}
	require.Error(t, createMigrationPair(root, "../escape"))
	require.NoError(
		t,
		os.WriteFile(
			filepath.Join(root, "topic3", "postgres", "000017_partial.up.sql"),
			[]byte("existing"),
			0o644,
		),
	)
	require.ErrorContains(t, createMigrationPair(root, "next_feature"), "versions differ")
}

func TestMigrationCommandSQLiteLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "command.sqlite")
	var out, errout bytes.Buffer
	require.Zero(
		t,
		run([]string{"apply", "--root", "../../migrations", "--sqlite-path", path}, &out, &errout),
		errout.String(),
	)
	require.Contains(t, out.String(), `"ready": true`)
	out.Reset()
	errout.Reset()
	require.Zero(
		t,
		run([]string{"inspect", "--root", "../../migrations", "--sqlite-path", path}, &out, &errout),
		errout.String(),
	)
	require.Contains(t, out.String(), `"topic3"`)
}
