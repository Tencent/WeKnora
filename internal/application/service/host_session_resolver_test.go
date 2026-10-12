package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type namedPinReader struct {
	configID string
}

func (n namedPinReader) Read(context.Context, string) (SandboxPin, error) {
	return SandboxPin{ConfigID: n.configID}, nil
}

func TestHostManagerForOnDesktopIgnoresNamedPin(t *testing.T) {
	host := stubHostManager{}
	r := NewHostSessionResolver(namedPinReader{configID: "cfg-remote"}, host, true)
	require.Equal(t, host, r.HostManagerFor(context.Background(), "s1"))

	web := NewHostSessionResolver(namedPinReader{configID: "cfg-remote"}, host, false)
	require.Nil(t, web.HostManagerFor(context.Background(), "s1"))
}
