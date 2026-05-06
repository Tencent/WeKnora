package safepaths

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidate(t *testing.T) {
	t.Run("rejects empty", func(t *testing.T) {
		_, err := Validate("")
		require.ErrorIs(t, err, ErrEmptyPath)
	})
	t.Run("rejects bare dot", func(t *testing.T) {
		_, err := Validate(".")
		require.ErrorIs(t, err, ErrSuspiciousPath)
	})
	t.Run("rejects bare double-dot", func(t *testing.T) {
		_, err := Validate("..")
		require.ErrorIs(t, err, ErrSuspiciousPath)
	})
	t.Run("rejects relative path that ascends past start", func(t *testing.T) {
		// "../foo" preserves the ".." after Clean.
		_, err := Validate("../foo")
		require.ErrorIs(t, err, ErrSuspiciousPath)
	})
	t.Run("cleans benign relative", func(t *testing.T) {
		// "a/b/../c" cleans to "a/c" — fine.
		got, err := Validate("a/b/../c")
		require.NoError(t, err)
		assert.Equal(t, filepath.Clean("a/c"), got)
	})
	t.Run("cleans cancelling relative", func(t *testing.T) {
		// "a/../b" cleans to "b" — also fine; the ascent fully resolves.
		got, err := Validate("a/../b")
		require.NoError(t, err)
		assert.Equal(t, "b", got)
	})
}

func TestWithinRoot(t *testing.T) {
	t.Run("inside root", func(t *testing.T) {
		err := WithinRoot("/srv/data/file.md", "/srv/data")
		require.NoError(t, err)
	})
	t.Run("equal to root", func(t *testing.T) {
		err := WithinRoot("/srv/data", "/srv/data")
		require.NoError(t, err)
	})
	t.Run("escapes via parent", func(t *testing.T) {
		err := WithinRoot("/srv/data/../../etc/passwd", "/srv/data")
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrPathEscapes))
	})
	t.Run("sibling outside", func(t *testing.T) {
		err := WithinRoot("/srv/other", "/srv/data")
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrPathEscapes))
	})
	t.Run("dotdot-prefix sibling is not within", func(t *testing.T) {
		// /srv/data..foo lexically starts with "/srv/data" but is a different dir.
		// Without separator-awareness this would be a false negative.
		err := WithinRoot("/srv/data..foo/x", "/srv/data")
		require.Error(t, err)
		assert.True(t, errors.Is(err, ErrPathEscapes))
	})
}
