package sandbox

import (
	"context"
	"fmt"
)

func connectRemoteSession(
	ctx context.Context, client RemoteSandboxClient, req RemoteConnectRequest,
) (RemoteSandboxHandle, error) {
	if connector, ok := client.(RemoteSessionConnector); ok {
		return connector.ConnectSession(ctx, req)
	}
	// Compatibility for providers that do not offer a combined connect/probe.
	summary, err := client.Get(ctx, req.SandboxID)
	if err != nil {
		return nil, err
	}
	if err := validateSessionSummary(client.Provider(), req.SandboxID, summary); err != nil {
		return nil, err
	}
	return client.Connect(ctx, req)
}

func validateSessionSummary(provider RemoteProvider, id string, summary *RemoteSandboxSummary) error {
	if summary == nil {
		return NewRemoteError(provider, "ConnectSession", RemoteErrorKindInternal,
			"provider returned no sandbox summary", nil)
	}
	if summary.ID != id {
		return NewRemoteError(provider, "ConnectSession", RemoteErrorKindInternal,
			fmt.Sprintf("provider returned sandbox %q for binding %q", summary.ID, id), nil)
	}
	if summary.State == RemoteStateTerminal {
		return NewRemoteError(provider, "ConnectSession", RemoteErrorKindTerminal, "sandbox is terminal", nil)
	}
	return nil
}
