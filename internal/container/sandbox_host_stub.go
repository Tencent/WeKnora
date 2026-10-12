//go:build !desktop

package container

import "github.com/Tencent/WeKnora/internal/application/service"

// Dummy dig types so BuildContainer can Provide the same constructor names
// without importing localsandbox. The desktop-tagged files replace these.

type (
	hostLookupDisabled struct{}
	hostModeDisabled   struct{}
)

func hostProjectLookup() hostLookupDisabled { return hostLookupDisabled{} }

func hostModeLookup() hostModeDisabled { return hostModeDisabled{} }

func provideHostSandboxManager() service.HostSandboxManager {
	return service.HostSandboxManager{}
}
