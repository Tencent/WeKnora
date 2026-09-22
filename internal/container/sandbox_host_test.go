//go:build desktop

package container

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/dig"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/localsandbox"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"gorm.io/gorm"
)

var errUnsupportedForTest = errors.New("unsupported for test")

type stubBackend struct{}

func (stubBackend) Name() string                      { return "stub" }
func (stubBackend) Available() error                  { return nil }
func (stubBackend) EnsureReady(context.Context) error { return nil }
func (stubBackend) TearDown(context.Context) error    { return nil }
func (stubBackend) Prepare(context.Context, localsandbox.Policy) (localsandbox.Prepared, error) {
	return nil, errors.New("unused")
}
func (stubBackend) Spawn(context.Context, localsandbox.Prepared, localsandbox.Command) (localsandbox.Process, error) {
	return nil, errors.New("unused")
}

type unavailableBackend struct{ stubBackend }

func (unavailableBackend) Available() error { return errUnsupportedForTest }

// An unsupported platform must stay off rather than run unsandboxed.
func TestHostSandboxManagerNilWhenBackendUnavailable(t *testing.T) {
	require.Nil(t, buildHostSandboxManager(hostSandboxDeps{
		newBackend: func() (localsandbox.Backend, error) { return nil, errUnsupportedForTest },
	}))
}

func TestHostSandboxManagerNilWhenAvailableFails(t *testing.T) {
	require.Nil(t, buildHostSandboxManager(hostSandboxDeps{
		newBackend: func() (localsandbox.Backend, error) { return unavailableBackend{}, nil },
	}))
}

func TestDefaultHostSessionRootUsesWeKnoraLite(t *testing.T) {
	require.Equal(t, "/Users/dev/Documents/WeKnoraLite", defaultHostSessionRoot("/Users/dev"))
}

func TestHostSandboxManagerEnabledWhenBackendAvailable(t *testing.T) {
	mgr := buildHostSandboxManager(hostSandboxDeps{
		newBackend: func() (localsandbox.Backend, error) { return stubBackend{}, nil },
		homeDir:    "/Users/dev",
		appDataDir: "/Users/dev/App Support/WeKnora",
	})
	require.NotNil(t, mgr)
	require.Equal(t, sandbox.SandboxTypeHost, mgr.GetType())
}

func TestProvideHostSandboxManagerAcceptsNilLookups(t *testing.T) {
	c := dig.New()
	require.NoError(t, c.Provide(func() *gorm.DB { return nil }))
	require.NoError(t, c.Provide(provideHostApprovalModeLoader))
	require.NoError(t, c.Provide(provideHostProjectDirsLoader))
	require.NoError(t, c.Provide(hostProjectLookup))
	require.NoError(t, c.Provide(hostModeLookup))
	require.NoError(t, c.Provide(provideHostSandboxManager))
	require.NoError(t, c.Invoke(func(host service.HostSandboxManager) {
		if host.Manager == nil {
			return
		}
		require.Equal(t, sandbox.SandboxTypeHost, host.Manager.GetType())
	}))
}

func TestHostModeLookupReadsApprovalModeLoader(t *testing.T) {
	lookup := hostModeLookup(func() string { return "auto" })
	require.NotNil(t, lookup)
	require.Equal(t, localsandbox.ModeAuto, lookup.ModeForSession(context.Background(), "session-1"))
}

func TestHostModeLookupDelegatesToParseApprovalMode(t *testing.T) {
	lookup := func(stored string) localsandbox.ApprovalMode {
		return hostModeLookup(func() string { return stored }).ModeForSession(context.Background(), "session-1")
	}
	require.Equal(t, localsandbox.ModeAsk, lookup("ask"))
	require.Equal(t, localsandbox.ModeFull, lookup("full"))
	require.Equal(t, localsandbox.ModeAuto, lookup("yolo"))
	require.Equal(t, localsandbox.ModeAuto, lookup(""))
}

func TestHostModeLookupNilLoaderMeansAuto(t *testing.T) {
	require.Nil(t, hostModeLookup(nil))
}

func TestHostLookupsComeFromProvidedLoaders(t *testing.T) {
	c := dig.New()
	require.NoError(t, c.Provide(func() *gorm.DB { return nil }))
	require.NoError(t, c.Provide(func() HostApprovalModeLoader {
		return func() string { return "auto" }
	}))
	require.NoError(t, c.Provide(func() HostProjectDirsLoader {
		return func() []string { return []string{"/Users/dev/My Project"} }
	}))
	require.NoError(t, c.Provide(hostProjectLookup))
	require.NoError(t, c.Provide(hostModeLookup))
	require.NoError(t, c.Invoke(func(modes localsandbox.ModeLookup, projects localsandbox.ProjectLookup) {
		require.Equal(t, localsandbox.ModeAuto, modes.ModeForSession(context.Background(), "session-1"))
		require.NotNil(t, projects)
	}))
}
