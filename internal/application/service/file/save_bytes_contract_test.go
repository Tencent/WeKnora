package file

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteNewLocalObjectNeverOverwritesExistingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "candidate.bin")
	require.NoError(t, os.WriteFile(path, []byte("winner"), 0o644))

	err := writeNewLocalObject(path, []byte("candidate"))
	require.Error(t, err)
	stored, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, []byte("winner"), stored)
}

func TestLocalSaveBytesCreatesUniqueIndependentObjects(t *testing.T) {
	service := NewLocalFileService(t.TempDir(), "")
	const callers = 32

	start := make(chan struct{})
	paths := make(chan string, callers)
	errs := make(chan error, callers)
	var ready sync.WaitGroup
	ready.Add(callers)
	for i := range callers {
		go func() {
			ready.Done()
			<-start
			path, err := service.SaveBytes(context.Background(), []byte{byte(i)}, 7, "artifact.bin", false)
			paths <- path
			errs <- err
		}()
	}
	ready.Wait()
	close(start)

	seen := make(map[string]struct{}, callers)
	for range callers {
		require.NoError(t, <-errs)
		path := <-paths
		assert.NotContains(t, seen, path)
		seen[path] = struct{}{}
	}
	require.Len(t, seen, callers)
	for path := range seen {
		require.NoError(t, service.DeleteFile(context.Background(), path))
	}
}

func TestDummySaveBytesCreatesReadableIndependentlyDeletableObject(t *testing.T) {
	service := NewDummyFileService()
	first := []byte("first")
	second := []byte("second")

	firstPath, err := service.SaveBytes(context.Background(), first, 7, "artifact.bin", false)
	require.NoError(t, err)
	secondPath, err := service.SaveBytes(context.Background(), second, 7, "artifact.bin", false)
	require.NoError(t, err)
	require.NotEqual(t, firstPath, secondPath)

	firstReader, err := service.GetFile(context.Background(), firstPath)
	require.NoError(t, err)
	firstBytes, err := io.ReadAll(firstReader)
	require.NoError(t, err)
	require.NoError(t, firstReader.Close())
	assert.Equal(t, first, firstBytes)

	require.NoError(t, service.DeleteFile(context.Background(), firstPath))
	_, err = service.GetFile(context.Background(), firstPath)
	assert.ErrorIs(t, err, os.ErrNotExist)

	secondReader, err := service.GetFile(context.Background(), secondPath)
	require.NoError(t, err)
	secondBytes, err := io.ReadAll(secondReader)
	require.NoError(t, err)
	require.NoError(t, secondReader.Close())
	assert.True(t, bytes.Equal(second, secondBytes))
}

func TestDummyFileServiceZeroValueSupportsSaveBytes(t *testing.T) {
	var service DummyFileService
	want := []byte("zero-value")

	path, err := service.SaveBytes(context.Background(), want, 7, "artifact.bin", false)
	require.NoError(t, err)

	reader, err := service.GetFile(context.Background(), path)
	require.NoError(t, err)
	got, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	assert.Equal(t, want, got)

	require.NoError(t, service.DeleteFile(context.Background(), path))
	_, err = service.GetFile(context.Background(), path)
	assert.ErrorIs(t, err, os.ErrNotExist)
}
