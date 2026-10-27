package doc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDownloadInterruptedClobberPreservesExistingFile(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/knowledge/doc-test/download", r.URL.Path)
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("partial"))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "report.pdf")
	require.NoError(t, os.WriteFile(path, []byte("previous complete download"), 0o640))
	err := runDownload(
		context.Background(), &DownloadOptions{Output: path, Clobber: true},
		textFopts(), sdk.NewClient(server.URL), "doc-test",
	)
	require.Error(t, err)
	got, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	assert.Equal(t, "previous complete download", string(got))
	entries, listErr := os.ReadDir(filepath.Dir(path))
	require.NoError(t, listErr)
	assert.Len(t, entries, 1, "temporary partial download must be removed")
}

func TestDownloadClobberReplacesCompleteFileAndPreservesMode(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("complete replacement"))
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "report.pdf")
	require.NoError(t, os.WriteFile(path, []byte("old"), 0o640))
	original, statErr := os.Stat(path)
	require.NoError(t, statErr)
	require.NoError(t, runDownload(
		context.Background(), &DownloadOptions{Output: path, Clobber: true},
		textFopts(), sdk.NewClient(server.URL), "doc-test",
	))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "complete replacement", string(got))
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, original.Mode().Perm(), info.Mode().Perm())
}

func TestDownloadClobberPreservesSymlink(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "interrupted"}[interrupted], func(t *testing.T) {
			_, _ = iostreams.SetForTest(t)
			dir := t.TempDir()
			target := filepath.Join(dir, "target.pdf")
			link := filepath.Join(dir, "report.pdf")
			require.NoError(t, os.WriteFile(target, []byte("original"), 0o640))
			if err := os.Symlink(target, link); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if interrupted {
					w.Header().Set("Content-Length", "100")
				}
				_, _ = w.Write([]byte("replacement"))
			}))
			defer server.Close()
			err := runDownload(
				context.Background(), &DownloadOptions{Output: link, Clobber: true},
				textFopts(), sdk.NewClient(server.URL), "doc-test",
			)
			expected := "replacement"
			if interrupted {
				require.Error(t, err)
				expected = "original"
			} else {
				require.NoError(t, err)
			}
			got, err := os.ReadFile(target)
			require.NoError(t, err)
			assert.Equal(t, expected, string(got))
			resolved, err := os.Readlink(link)
			require.NoError(t, err)
			assert.Equal(t, target, resolved)
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			assert.Len(t, entries, 2)
		})
	}
}

func TestDownloadRenameFailurePreservesDestination(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	dir := t.TempDir()
	dest := filepath.Join(dir, "existing-directory")
	require.NoError(t, os.Mkdir(dest, 0o750))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("replacement"))
	}))
	defer server.Close()
	require.Error(t, runDownload(
		context.Background(), &DownloadOptions{Output: dest, Clobber: true},
		textFopts(), sdk.NewClient(server.URL), "doc-test",
	))
	info, err := os.Stat(dest)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Len(t, entries, 1)
}

func TestDownloadCreatesTargetThroughDanglingSymlink(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	dir := t.TempDir()
	target := filepath.Join(dir, "new.pdf")
	link := filepath.Join(dir, "report.pdf")
	if err := os.Symlink("new.pdf", link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("complete"))
	}))
	defer server.Close()
	err := runDownload(context.Background(), &DownloadOptions{Output: link, Clobber: true},
		textFopts(), sdk.NewClient(server.URL), "doc-test")
	require.NoError(t, err)
	got, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "complete", string(got))
	resolved, err := os.Readlink(link)
	require.NoError(t, err)
	assert.Equal(t, "new.pdf", resolved)
}
