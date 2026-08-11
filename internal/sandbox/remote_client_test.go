package sandbox

import (
	"context"
	"testing"
	"time"
)

type contractHandle struct {
	id       string
	provider RemoteProvider
	metadata map[string]string
}

func (h *contractHandle) ID() string                  { return h.id }
func (h *contractHandle) Provider() RemoteProvider    { return h.provider }
func (h *contractHandle) Metadata() map[string]string { return h.metadata }

type contractClient struct {
	provider     RemoteProvider
	capabilities RemoteSandboxCapabilities
}

func (c *contractClient) Provider() RemoteProvider { return c.provider }
func (c *contractClient) Capabilities() RemoteSandboxCapabilities {
	return c.capabilities
}
func (c *contractClient) Health(context.Context) error { return nil }
func (c *contractClient) Create(_ context.Context, req RemoteCreateRequest) (RemoteSandboxHandle, error) {
	return &contractHandle{id: "sandbox-1", provider: c.provider, metadata: req.Metadata}, nil
}
func (c *contractClient) Connect(context.Context, string) (RemoteSandboxHandle, error) {
	return &contractHandle{id: "sandbox-1", provider: c.provider}, nil
}
func (c *contractClient) Get(context.Context, string) (*RemoteSandboxSummary, error) {
	return &RemoteSandboxSummary{ID: "sandbox-1", State: RemoteStateRunning}, nil
}
func (c *contractClient) List(context.Context, RemoteListFilter) ([]RemoteSandboxSummary, error) {
	return []RemoteSandboxSummary{{ID: "sandbox-1", State: RemoteStateRunning}}, nil
}
func (c *contractClient) Delete(context.Context, string) error { return nil }
func (c *contractClient) Exec(
	context.Context,
	RemoteSandboxHandle,
	RemoteExecRequest,
) (*RemoteExecResult, error) {
	return &RemoteExecResult{ExitCode: 0}, nil
}
func (c *contractClient) WriteFile(context.Context, RemoteSandboxHandle, string, []byte) error {
	return nil
}
func (c *contractClient) ReadFile(context.Context, RemoteSandboxHandle, string) ([]byte, error) {
	return []byte("content"), nil
}
func (c *contractClient) ListDir(context.Context, RemoteSandboxHandle, string) ([]RemoteDirEntry, error) {
	return []RemoteDirEntry{{Name: "file", Type: RemoteEntryFile}}, nil
}
func (c *contractClient) MakeDir(context.Context, RemoteSandboxHandle, string) error {
	return nil
}
func (c *contractClient) Remove(context.Context, RemoteSandboxHandle, string) error {
	return nil
}
func (c *contractClient) Stat(context.Context, RemoteSandboxHandle, string) (*RemoteStatEntry, error) {
	return &RemoteStatEntry{Path: "/workspace/file", Type: RemoteEntryFile}, nil
}

var _ RemoteSandboxClient = (*contractClient)(nil)

func TestRemoteClientContractExposesProviderAndReconnectCapability(t *testing.T) {
	t.Parallel()

	client := &contractClient{
		provider: SandboxTypeE2B,
		capabilities: RemoteSandboxCapabilities{
			SupportsReconnect: true,
		},
	}

	if got := client.Provider(); got != SandboxTypeE2B {
		t.Fatalf("Provider() = %q, want %q", got, SandboxTypeE2B)
	}
	if !client.Capabilities().SupportsReconnect {
		t.Fatal("reconnect capability must be explicit")
	}
}

func TestRemoteTypesRepresentProviderNeutralSemantics(t *testing.T) {
	t.Parallel()

	timeout := RemoteTimeoutPolicy{
		Mode:       RemoteTimeoutExplicit,
		Value:      15 * time.Minute,
		Action:     RemoteOnTimeoutPause,
		AutoResume: true,
	}
	if timeout.Mode != RemoteTimeoutExplicit || timeout.Action != RemoteOnTimeoutPause {
		t.Fatalf("unexpected timeout policy: %+v", timeout)
	}

	direct := RemoteExecRequest{Command: "python3", Args: []string{"-V"}}
	if direct.Shell || len(direct.Args) != 1 {
		t.Fatalf("direct execution lost argv semantics: %+v", direct)
	}

	shell := RemoteExecRequest{Command: "printf '%s' ok", Shell: true}
	if !shell.Shell || len(shell.Args) != 0 {
		t.Fatalf("shell execution representation is ambiguous: %+v", shell)
	}

	states := []RemoteSandboxState{
		RemoteStateRunning,
		RemoteStatePaused,
		RemoteStateTransitioning,
		RemoteStateTerminal,
		RemoteStateUnknown,
	}
	if got, want := len(states), 5; got != want {
		t.Fatalf("normalized state count = %d, want %d", got, want)
	}
}
