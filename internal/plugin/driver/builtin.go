package driver

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/manifest"
)

// Builtin serves plugins compiled into WeKnora: there is nothing to start,
// every node runs them in process, and they are ready as long as the node is.
type Builtin struct {
	node    string
	started time.Time
	lookup  func(pluginID string) (*manifest.Manifest, bool)
}

// NewBuiltin returns the builtin driver. lookup finds a registered plugin.
func NewBuiltin(lookup func(pluginID string) (*manifest.Manifest, bool)) *Builtin {
	node, err := os.Hostname()
	if err != nil || node == "" {
		node = "local"
	}
	return &Builtin{node: node, started: time.Now(), lookup: lookup}
}

// Type implements Driver.
func (b *Builtin) Type() manifest.RuntimeType { return manifest.RuntimeBuiltin }

// Ensure implements Driver. Builtins are ready by construction; anything else
// is a misrouted manifest.
func (b *Builtin) Ensure(_ context.Context, m *manifest.Manifest) error {
	if !m.Builtin {
		return errors.New("the builtin driver only runs builtin plugins")
	}
	return nil
}

// Remove implements Driver. Builtins cannot be removed, only disabled.
func (b *Builtin) Remove(context.Context, string, string) error {
	return errors.New("builtin plugins cannot be removed")
}

// Resolve implements Driver.
func (b *Builtin) Resolve(_ context.Context, pluginID string, _ uint64) (Endpoint, error) {
	if _, ok := b.lookup(pluginID); !ok {
		return Endpoint{}, errors.New("unknown builtin plugin")
	}
	return Endpoint{Kind: EndpointInProcess}, nil
}

// Status implements Driver: one ready instance, this node.
func (b *Builtin) Status(_ context.Context, pluginID string) ([]InstanceStatus, error) {
	m, ok := b.lookup(pluginID)
	if !ok {
		return nil, errors.New("unknown builtin plugin")
	}
	return []InstanceStatus{{Node: b.node, Version: m.Version, State: StateReady, UpdatedAt: b.started}}, nil
}
