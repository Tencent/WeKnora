//go:build !desktop

package container

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/dig"

	"github.com/Tencent/WeKnora/internal/application/service"
	"gorm.io/gorm"
)

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	require.NoError(t, err)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

// The host OS sandbox is a desktop-only compile. cmd/server (standard and
// make build-lite) must not even link the package.
func TestCmdServerDoesNotDependOnLocalSandbox(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "github.com/Tencent/WeKnora/cmd/server")
	cmd.Dir = moduleRoot(t)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	require.NotContains(t, string(out), "internal/localsandbox",
		"localsandbox must stay behind the desktop build tag")
}

func TestStubHostWiringRegistersWithoutLocalSandbox(t *testing.T) {
	c := dig.New()
	require.NoError(t, c.Provide(func() *gorm.DB { return nil }))
	require.NoError(t, c.Provide(provideHostApprovalModeLoader))
	require.NoError(t, c.Provide(provideHostProjectDirsLoader))
	require.NoError(t, c.Provide(hostProjectLookup))
	require.NoError(t, c.Provide(hostModeLookup))
	require.NoError(t, c.Provide(provideHostSandboxManager))
	require.NoError(t, c.Invoke(func(host service.HostSandboxManager) {
		require.Nil(t, host.Manager)
	}))
}

func TestContainerPackageImportListOmitsLocalSandbox(t *testing.T) {
	cmd := exec.Command(
		"go", "list", "-f", "{{join .Imports \"\\n\"}}",
		"github.com/Tencent/WeKnora/internal/container",
	)
	cmd.Dir = moduleRoot(t)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	for _, line := range strings.Split(string(out), "\n") {
		require.NotContains(t, line, "internal/localsandbox")
	}
}
