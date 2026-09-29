package mcp

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// disconnectClient is an MCPClient whose Disconnect blocks until release is
// closed, standing in for a remote server that stopped answering.
type disconnectClient struct {
	MCPClient
	release      chan struct{}
	disconnected atomic.Bool
}

func (c *disconnectClient) Disconnect() error {
	if c.release != nil {
		<-c.release
	}
	c.disconnected.Store(true)
	return nil
}

func TestCloseAllIsConcurrentAndBounded(t *testing.T) {
	saved := closeAllTimeout
	closeAllTimeout = 100 * time.Millisecond
	t.Cleanup(func() { closeAllTimeout = saved })

	m := NewMCPManager(nil)
	defer m.cancel()
	hung := &disconnectClient{release: make(chan struct{})}
	defer close(hung.release)
	fast := make([]*disconnectClient, 3)
	m.clients["hung"] = hung
	for i := range fast {
		fast[i] = &disconnectClient{}
		m.clients[string(rune('a'+i))] = fast[i]
	}

	start := time.Now()
	m.CloseAll()

	require.Less(t, time.Since(start), 2*time.Second, "a hung server must not hold up shutdown")
	for _, c := range fast {
		require.True(t, c.disconnected.Load(), "the other clients close regardless of the hung one")
	}
	require.False(t, hung.disconnected.Load())
	_, ok := m.GetClient("a")
	require.False(t, ok, "closed clients are detached from the manager")
}
