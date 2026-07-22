package database

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/golang-migrate/migrate/v4"
	"github.com/stretchr/testify/require"
)

type dirtyMigrationRecoveryStub struct {
	forceCalls       []int
	forceErr         error
	recoveredVersion uint
	recoveredDirty   bool
	recoveredErr     error
	versionCalls     int
}

func (s *dirtyMigrationRecoveryStub) Force(version int) error {
	s.forceCalls = append(s.forceCalls, version)
	return s.forceErr
}

func (s *dirtyMigrationRecoveryStub) Version() (uint, bool, error) {
	s.versionCalls++
	return s.recoveredVersion, s.recoveredDirty, s.recoveredErr
}

func assertDirtyVersionZeroRemediation(t *testing.T, message string) {
	t.Helper()
	require.Contains(t, message, "no-version")
	require.Contains(t, message, "Force(-1)")
	require.Contains(t, message, "re-run version 0")
	require.Contains(t, message, "restore the migration state")
	require.Contains(t, message, "Only if you have manually verified")
	require.Contains(t, message, "Force(0)")
	require.Contains(t, message, "skips re-running version 0")
	require.NotContains(t, message, "previous successful version 0")
	require.True(t, strings.Index(message, "Only if") < strings.Index(message, "Force(0)"))
}

func TestValidateFinalMigrationState(t *testing.T) {
	require.NoError(t, validateFinalMigrationState(1, false, nil))

	t.Run("dirty after up", func(t *testing.T) {
		err := validateFinalMigrationState(1, true, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "dirty state")
	})

	t.Run("dirty after recovery", func(t *testing.T) {
		err := validateFinalMigrationState(1, true, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "dirty state")
	})

	versionErr := errors.New("version read failed")
	err := validateFinalMigrationState(0, false, versionErr)
	require.ErrorIs(t, err, versionErr)

	err = validateFinalMigrationState(0, false, migrate.ErrNilVersion)
	require.ErrorIs(t, err, migrate.ErrNilVersion)
}

func TestDirtyMigrationRemediation(t *testing.T) {
	t.Run("version zero distinguishes retry and completed cases", func(t *testing.T) {
		assertDirtyVersionZeroRemediation(t, dirtyMigrationRemediation(0))
	})

	t.Run("positive version uses exact previous version", func(t *testing.T) {
		remediation := dirtyMigrationRemediation(5)
		require.Contains(t, remediation, "previous successful version 4")
		require.Contains(t, remediation, "Force(4)")
		require.Contains(t, remediation, "version 5 is re-run")
	})
}

func TestRecoverDirtyMigrationWithRemediation(t *testing.T) {
	forceErr := errors.New("force failed")
	stub := &dirtyMigrationRecoveryStub{forceErr: forceErr}

	_, err := recoverDirtyMigrationWithRemediation(context.Background(), stub, 0)

	require.ErrorIs(t, err, forceErr)
	assertDirtyVersionZeroRemediation(t, err.Error())
	require.Equal(t, []int{-1}, stub.forceCalls)
	require.Zero(t, stub.versionCalls)
}

func TestRecoverFromDirtyStateVersionZero(t *testing.T) {
	t.Run("no version is the expected intermediate state", func(t *testing.T) {
		stub := &dirtyMigrationRecoveryStub{recoveredErr: migrate.ErrNilVersion}

		version, err := recoverFromDirtyState(context.Background(), stub, 0)

		require.NoError(t, err)
		require.Equal(t, uint(0), version)
		require.Equal(t, []int{-1}, stub.forceCalls)
		require.Equal(t, 1, stub.versionCalls)
	})

	t.Run("force failure preserves root cause", func(t *testing.T) {
		forceErr := errors.New("force failed")
		stub := &dirtyMigrationRecoveryStub{forceErr: forceErr}

		_, err := recoverFromDirtyState(context.Background(), stub, 0)

		require.ErrorIs(t, err, forceErr)
		require.Equal(t, []int{-1}, stub.forceCalls)
		require.Zero(t, stub.versionCalls)
	})

	t.Run("version read failure preserves root cause", func(t *testing.T) {
		versionErr := errors.New("version read failed")
		stub := &dirtyMigrationRecoveryStub{recoveredErr: versionErr}

		_, err := recoverFromDirtyState(context.Background(), stub, 0)

		require.ErrorIs(t, err, versionErr)
	})

	t.Run("dirty formal version is rejected", func(t *testing.T) {
		stub := &dirtyMigrationRecoveryStub{
			recoveredVersion: 0,
			recoveredDirty:   true,
		}

		_, err := recoverFromDirtyState(context.Background(), stub, 0)

		require.Error(t, err)
		require.Contains(t, err.Error(), "remains dirty")
	})

	t.Run("clean formal version is rejected", func(t *testing.T) {
		stub := &dirtyMigrationRecoveryStub{recoveredVersion: 0}

		_, err := recoverFromDirtyState(context.Background(), stub, 0)

		require.Error(t, err)
		require.Contains(t, err.Error(), "unexpected migration version")
	})
}

func TestRecoverFromDirtyStatePositiveVersion(t *testing.T) {
	t.Run("exact previous version is accepted", func(t *testing.T) {
		stub := &dirtyMigrationRecoveryStub{recoveredVersion: 4}

		version, err := recoverFromDirtyState(context.Background(), stub, 5)

		require.NoError(t, err)
		require.Equal(t, uint(4), version)
		require.Equal(t, []int{4}, stub.forceCalls)
	})

	t.Run("no version is rejected", func(t *testing.T) {
		stub := &dirtyMigrationRecoveryStub{recoveredErr: migrate.ErrNilVersion}

		_, err := recoverFromDirtyState(context.Background(), stub, 5)

		require.ErrorIs(t, err, migrate.ErrNilVersion)
	})

	t.Run("dirty state is rejected", func(t *testing.T) {
		stub := &dirtyMigrationRecoveryStub{
			recoveredVersion: 4,
			recoveredDirty:   true,
		}

		_, err := recoverFromDirtyState(context.Background(), stub, 5)

		require.Error(t, err)
		require.Contains(t, err.Error(), "remains dirty")
	})

	t.Run("wrong previous version is rejected", func(t *testing.T) {
		stub := &dirtyMigrationRecoveryStub{recoveredVersion: 3}

		_, err := recoverFromDirtyState(context.Background(), stub, 5)

		require.Error(t, err)
		require.Contains(t, err.Error(), "unexpected migration version")
	})
}
