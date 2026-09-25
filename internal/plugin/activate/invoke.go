package activate

import (
	"context"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/install"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/tenancy"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// ClientSource reaches running code plugins (the node's plugin host).
type ClientSource interface {
	Client(pluginID string) (*client.Client, error)
}

// Invoker is how the remote adapters call a code plugin: it finds the
// process and builds the protocol envelope from the request context and the
// plugin's decrypted system and tenant configuration.
type Invoker struct {
	clients ClientSource

	mu      sync.RWMutex
	tenancy *tenancy.Service
	plugins interfaces.PluginRepository
}

// NewInvoker creates an Invoker; Bind completes it.
func NewInvoker(clients ClientSource) *Invoker { return &Invoker{clients: clients} }

// Bind supplies the tenant configuration and installed plugin rows.
func (iv *Invoker) Bind(t *tenancy.Service, plugins interfaces.PluginRepository) {
	iv.mu.Lock()
	iv.tenancy, iv.plugins = t, plugins
	iv.mu.Unlock()
}

// Default timeouts per call kind, used when the caller set no deadline.
const (
	searchTimeout   = 10 * time.Second
	metadataTimeout = time.Minute
)

// withDefaultTimeout bounds ctx unless it already has a deadline.
func withDefaultTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, d)
}

// Envelope builds the call envelope for a plugin with an instance config.
func (iv *Invoker) Envelope(
	ctx context.Context,
	m *manifest.Manifest,
	instance map[string]any,
) (pluginapi.Envelope, error) {
	env := pluginapi.Envelope{Config: pluginapi.Config{Instance: instance}}
	if tenantID, ok := types.TenantIDFromContext(ctx); ok {
		env.Context.TenantID = tenantID
	}
	if userID, ok := ctx.Value(types.UserIDContextKey).(string); ok {
		env.Context.UserID = userID
	}
	if lang, ok := types.LanguageFromContext(ctx); ok {
		env.Context.Locale = lang
	}
	if rid, ok := ctx.Value(types.RequestIDContextKey).(string); ok {
		env.Context.RequestID = rid
	}
	if d, ok := ctx.Deadline(); ok {
		env.Context.Deadline = &d
	}
	iv.mu.RLock()
	t, plugins := iv.tenancy, iv.plugins
	iv.mu.RUnlock()
	if plugins != nil && len(m.Config.SystemSchema) > 0 {
		sys, _, err := install.OpenSystemConfig(ctx, plugins, m)
		if err != nil {
			return env, err
		}
		env.Config.System = sys
	}
	if t != nil && env.Context.TenantID != 0 && len(m.Config.TenantSchema) > 0 {
		cfg, _, err := t.OpenConfig(ctx, env.Context.TenantID, m.ID)
		if err != nil {
			return env, err
		}
		env.Config.Tenant = cfg
	}
	return env, nil
}

// Call makes a unary call to a plugin.
func (iv *Invoker) Call(
	ctx context.Context, m *manifest.Manifest, path string, instance map[string]any, input, out any,
) error {
	c, err := iv.clients.Client(m.ID)
	if err != nil {
		return err
	}
	env, err := iv.Envelope(ctx, m, instance)
	if err != nil {
		return err
	}
	return c.Call(ctx, path, env, input, out)
}

// Stream makes a streaming call to a plugin.
func (iv *Invoker) Stream(
	ctx context.Context, m *manifest.Manifest, path string, instance map[string]any, input any,
	fn func(pluginapi.Event) error,
) ([]byte, error) {
	c, err := iv.clients.Client(m.ID)
	if err != nil {
		return nil, err
	}
	env, err := iv.Envelope(ctx, m, instance)
	if err != nil {
		return nil, err
	}
	return c.Stream(ctx, path, env, input, fn)
}
