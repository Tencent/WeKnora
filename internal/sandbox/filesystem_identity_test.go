package sandbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCubeFilesystemIdentityIsRequestScoped(t *testing.T) {
	for _, route := range []string{"/files?path=/workspace/a", "/filesystem.Filesystem/MakeDir"} {
		recorder := &recordingRoundTripper{}
		transport := &cubeFilesystemTransport{next: recorder}
		for _, maintenance := range []bool{false, true, false} {
			ctx := context.Background()
			user := DefaultSandboxExecUser
			if maintenance {
				ctx = withMaintenanceFilesystem(ctx)
				user = "root"
			}
			req := httptest.NewRequest(http.MethodGet, "https://sandbox.example"+route, nil).WithContext(ctx)
			req.Header.Set("Authorization", basicAuthorizationFor("root"))
			_, err := transport.RoundTrip(req)
			require.NoError(t, err)
			require.Equal(t, basicAuthorizationFor(user), recorder.request.Header.Get("Authorization"))
			require.Equal(t, basicAuthorizationFor("root"), req.Header.Get("Authorization"), "input request must not be mutated")
		}
	}
	for _, route := range []string{"/sandboxes", "/process.Process/Start"} {
		recorder := &recordingRoundTripper{}
		req := httptest.NewRequest(http.MethodPost, "https://sandbox.example"+route, nil)
		req.Header.Set("Authorization", "preserve-me")
		_, err := (&cubeFilesystemTransport{next: recorder}).RoundTrip(req)
		require.NoError(t, err)
		require.Equal(t, "preserve-me", recorder.request.Header.Get("Authorization"))
	}
}

func TestEnvdMaintenanceFilesystemIdentity(t *testing.T) {
	recorder := &recordingRoundTripper{}
	transport := NewEnvdCompatTransport(recorder, DefaultSandboxExecUser)
	req := httptest.NewRequest(http.MethodGet, "https://sandbox.example/files", nil).
		WithContext(withMaintenanceFilesystem(context.Background()))
	_, err := transport.RoundTrip(req)
	require.NoError(t, err)
	require.Equal(t, basicAuthorizationFor("root"), recorder.request.Header.Get("Authorization"))
}

func TestWorkspaceBootstrapDoesNotRemoveOrMoveFiles(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "existing-file")
	require.NoError(t, os.WriteFile(blocked, []byte("keep this"), 0600))
	cmd := exec.Command("/bin/sh", "-c", workspaceBootstrapCommand(blocked))
	require.Error(t, cmd.Run())
	content, err := os.ReadFile(blocked)
	require.NoError(t, err)
	require.Equal(t, "keep this", string(content))
	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(root, link))
	require.Error(t, exec.Command("/bin/sh", "-c", workspaceBootstrapCommand(link)).Run())
	_, err = os.Lstat(link)
	require.NoError(t, err)
	valid := filepath.Join(root, "space ' quote", "nested")
	require.NoError(t, exec.Command("/bin/sh", "-c", workspaceBootstrapCommand(valid)).Run())
	info, err := os.Stat(valid)
	require.NoError(t, err)
	require.True(t, info.IsDir())
}

func TestFileTransferSkipsShortHTTPTimeout(t *testing.T) {
	parent, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	assertBudget := func(t *testing.T, transport http.RoundTripper, route string, short bool) {
		t.Helper()
		recorder := &recordingRoundTripper{}
		switch typed := transport.(type) {
		case *cubeFilesystemTransport:
			typed.next = recorder
		case *e2bRPCTimeoutTransport:
			typed.next = recorder
		default:
			t.Fatalf("unexpected transport %T", transport)
		}
		req := httptest.NewRequest(http.MethodGet, "https://sandbox.example"+route, nil).WithContext(parent)
		_, err := transport.RoundTrip(req)
		require.NoError(t, err)
		deadline, ok := recorder.request.Context().Deadline()
		require.True(t, ok)
		remaining := time.Until(deadline)
		if short {
			require.Less(t, remaining, 200*time.Millisecond)
			return
		}
		require.Greater(t, remaining, time.Second)
	}

	cube := &cubeFilesystemTransport{timeout: 50 * time.Millisecond}
	e2b := &e2bRPCTimeoutTransport{timeout: 50 * time.Millisecond}
	assertBudget(t, cube, "/files?path=/workspace/a", false)
	assertBudget(t, cube, "/filesystem.Filesystem/WriteFile", false)
	assertBudget(t, cube, "/filesystem.Filesystem/MakeDir", true)
	assertBudget(t, cube, "/process.Process/Start", false)
	assertBudget(t, e2b, "/files", false)
	assertBudget(t, e2b, "/filesystem.Filesystem/Read", false)
	assertBudget(t, e2b, "/filesystem.Filesystem/MakeDir", true)
	assertBudget(t, e2b, "/process.Process/List", true)
	assertBudget(t, e2b, "/process.Process/Start", false)
}
