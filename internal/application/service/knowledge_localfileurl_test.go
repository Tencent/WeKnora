package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withLocalFileURLRoots resets the sync.Once + roots slice, sets the env
// to `dirs` (colon-joined), and triggers a fresh load. Tests must call
// the returned cleanup to restore previous state — sync.Once is reset
// again so other tests aren't affected by this one's env.
func withLocalFileURLRoots(t *testing.T, dirs ...string) func() {
	t.Helper()
	prevEnv, prevSet := os.LookupEnv("WEKNORA_LOCAL_FILE_URL_ROOTS")
	prevOnce := localFileURLRootsOnce
	prevRoots := localFileURLRoots

	localFileURLRootsOnce = sync.Once{}
	localFileURLRoots = nil
	require.NoError(t, os.Setenv("WEKNORA_LOCAL_FILE_URL_ROOTS", strings.Join(dirs, string(os.PathListSeparator))))
	// Force load now so the test gets a deterministic snapshot.
	localFileURLRootsOnce.Do(loadLocalFileURLRoots)

	return func() {
		if prevSet {
			_ = os.Setenv("WEKNORA_LOCAL_FILE_URL_ROOTS", prevEnv)
		} else {
			_ = os.Unsetenv("WEKNORA_LOCAL_FILE_URL_ROOTS")
		}
		localFileURLRootsOnce = prevOnce
		localFileURLRoots = prevRoots
	}
}

// TestResolveLocalFileURL_DisabledByDefault asserts that without the env
// var the feature stays off — protecting fresh deployments from
// accidental LFI even if a frontend tries to fall back to file:// URLs.
func TestResolveLocalFileURL_DisabledByDefault(t *testing.T) {
	prev, set := os.LookupEnv("WEKNORA_LOCAL_FILE_URL_ROOTS")
	_ = os.Unsetenv("WEKNORA_LOCAL_FILE_URL_ROOTS")
	prevOnce := localFileURLRootsOnce
	prevRoots := localFileURLRoots
	localFileURLRootsOnce = sync.Once{}
	localFileURLRoots = nil
	t.Cleanup(func() {
		if set {
			_ = os.Setenv("WEKNORA_LOCAL_FILE_URL_ROOTS", prev)
		}
		localFileURLRootsOnce = prevOnce
		localFileURLRoots = prevRoots
	})

	_, err := resolveLocalFileURL("file:///tmp/anything.html")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "WEKNORA_LOCAL_FILE_URL_ROOTS",
		"error message should point operators at the env var that enables this")
}

// TestResolveLocalFileURL_AllowedPath: a file inside the configured root
// canonicalises and is returned.
func TestResolveLocalFileURL_AllowedPath(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "sub", "doc.html")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0o755))
	require.NoError(t, os.WriteFile(target, []byte("<html>ok</html>"), 0o644))

	defer withLocalFileURLRoots(t, root)()

	got, err := resolveLocalFileURL("file://" + target)
	require.NoError(t, err)
	// EvalSymlinks may normalise /private prefix on macOS — compare via
	// EvalSymlinks on both sides.
	want, _ := filepath.EvalSymlinks(target)
	assert.Equal(t, want, got)
}

// TestResolveLocalFileURL_RejectsTraversal: ../ escape is blocked.
func TestResolveLocalFileURL_RejectsTraversal(t *testing.T) {
	root := t.TempDir()
	defer withLocalFileURLRoots(t, root)()

	// Try to read /etc/hosts via a traversal-encoded path. The path is
	// canonicalised first, so this becomes /etc/hosts which is outside
	// the temp root.
	_, err := resolveLocalFileURL("file://" + root + "/../../../../../etc/hosts")
	require.Error(t, err)
	assert.True(t,
		strings.Contains(err.Error(), "not under any") ||
			strings.Contains(err.Error(), "evaluate symlinks"),
		"expected rejection or symlink-resolve error, got: %v", err)
}

// TestResolveLocalFileURL_RejectsOutsideRoot: an absolute path outside
// any configured root is rejected even without traversal markers.
func TestResolveLocalFileURL_RejectsOutsideRoot(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir() // a different temp dir not in the allowlist
	target := filepath.Join(other, "secret.txt")
	require.NoError(t, os.WriteFile(target, []byte("secret"), 0o600))

	defer withLocalFileURLRoots(t, root)()

	_, err := resolveLocalFileURL("file://" + target)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not under any")
}

// TestResolveLocalFileURL_RejectsSymlinkEscape: a symlink inside the
// allowed root that points outside the root is rejected — this is the
// key reason resolveLocalFileURL calls EvalSymlinks before the prefix
// check.
func TestResolveLocalFileURL_RejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret.txt")
	require.NoError(t, os.WriteFile(outsideFile, []byte("secret"), 0o600))

	// Place a symlink inside `root` that points to a file outside.
	linkPath := filepath.Join(root, "escape.html")
	require.NoError(t, os.Symlink(outsideFile, linkPath))

	defer withLocalFileURLRoots(t, root)()

	_, err := resolveLocalFileURL("file://" + linkPath)
	require.Error(t, err, "symlink escape must be rejected")
	assert.Contains(t, err.Error(), "not under any")
}

// TestResolveLocalFileURL_RejectsRemoteHost: file://server/share-style
// URLs are not allowed (would be a remote SMB import path on Windows).
func TestResolveLocalFileURL_RejectsRemoteHost(t *testing.T) {
	root := t.TempDir()
	defer withLocalFileURLRoots(t, root)()

	_, err := resolveLocalFileURL("file://otherhost/etc/passwd")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-local host")
}

// TestResolveLocalFileURL_LocalhostAccepted: file://localhost/<path> is
// equivalent to file:///<path>.
func TestResolveLocalFileURL_LocalhostAccepted(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "doc.html")
	require.NoError(t, os.WriteFile(target, []byte("ok"), 0o644))

	defer withLocalFileURLRoots(t, root)()

	got, err := resolveLocalFileURL("file://localhost" + target)
	require.NoError(t, err)
	want, _ := filepath.EvalSymlinks(target)
	assert.Equal(t, want, got)
}

// TestReadLocalFileURL_PopulatesFilenameAndType: round-trip through the
// integration point downloadFileFromURL uses (file:// short-circuit).
func TestReadLocalFileURL_PopulatesFilenameAndType(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "page.html")
	body := []byte("<html><body><h1>Hi</h1></body></html>")
	require.NoError(t, os.WriteFile(target, body, 0o644))

	defer withLocalFileURLRoots(t, root)()

	var name, typ string
	got, err := readLocalFileURL(context.Background(), "file://"+target, &name, &typ)
	require.NoError(t, err)
	assert.Equal(t, body, got)
	assert.Equal(t, "page.html", name)
	assert.Equal(t, "html", typ)
}

// TestIsValidURL_AcceptsFileScheme proves the URL-shape gate is in sync
// with the local-file path so a file:// URL doesn't get blocked at the
// service entrypoint before reaching resolveLocalFileURL.
func TestIsValidURL_AcceptsFileScheme(t *testing.T) {
	assert.True(t, isValidURL("file:///tmp/x.html"))
	assert.True(t, isValidURL("file://localhost/tmp/x.html"))
	assert.True(t, isValidURL("https://example.com/x.html"))
	assert.False(t, isValidURL("ftp://example.com/x.html"))
	assert.False(t, isValidURL("/tmp/x.html"))
	assert.False(t, isValidURL(""))
}
