package activate

import (
	"context"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/plugin/install"
	"github.com/Tencent/WeKnora/internal/plugin/manifest"
	"github.com/Tencent/WeKnora/internal/plugin/tenancy"
	"github.com/Tencent/WeKnora/internal/plugin/webhook"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/pluginsdk/client"
	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// ClientSource reaches running code plugins wherever they run: this node's
// plugin host, a standalone plugin host, a remote service.
type ClientSource interface {
	Client(ctx context.Context, m *manifest.Manifest) (*client.Client, error)
	// OnThisNode reports whether a plugin runs on this node, where the
	// loopback Host API address reaches WeKnora.
	OnThisNode(pluginID string) bool
}

// Invoker is how the remote adapters call a code plugin: it finds the
// process and builds the protocol envelope from the request context and the
// plugin's decrypted system and tenant configuration.
type Invoker struct {
	clients ClientSource

	mu      sync.RWMutex
	tenancy *tenancy.Service
	plugins interfaces.PluginRepository
	// tokens and hostURL give plugins granted Host API scopes a way back;
	// plugins off this node (remote, on a plugin host) use publicHostURL.
	tokens        TokenIssuer
	hostURL       string
	publicHostURL string
	// webhooks and publicBase give plugins their workspace's webhook URLs.
	webhooks   *webhook.Tokens
	publicBase string
}

// SetWebhooks lets calls carry the workspace's webhook URLs, when WeKnora's
// public address (publicBase) is known.
func (iv *Invoker) SetWebhooks(tokens *webhook.Tokens, publicBase string) {
	iv.mu.Lock()
	iv.webhooks, iv.publicBase = tokens, publicBase
	iv.mu.Unlock()
}

// TokenIssuer signs the Host API token of one call.
type TokenIssuer interface {
	Issue(pluginID, version string, tenantID uint64, scopes []string) (string, time.Time, error)
}

// SetHostAPI lets calls carry Host API access: where plugins reach WeKnora
// and how their tokens are signed.
func (iv *Invoker) SetHostAPI(tokens TokenIssuer, url string) {
	iv.mu.Lock()
	iv.tokens, iv.hostURL = tokens, url
	iv.mu.Unlock()
}

// SetPublicHostAPI is where remote plugins reach the Host API. Without it
// their calls carry no Host API access.
func (iv *Invoker) SetPublicHostAPI(url string) {
	iv.mu.Lock()
	iv.publicHostURL = url
	iv.mu.Unlock()
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
	t, plugins, tokens, hostURL := iv.tenancy, iv.plugins, iv.tokens, iv.hostURL
	if !iv.clients.OnThisNode(m.ID) {
		hostURL = iv.publicHostURL
	}
	hooks, publicBase := iv.webhooks, iv.publicBase
	iv.mu.RUnlock()
	if hooks != nil && publicBase != "" && env.Context.TenantID != 0 {
		for _, c := range m.Contributes[manifest.PointWebhooks] {
			if env.Context.Webhooks == nil {
				env.Context.Webhooks = map[string]string{}
			}
			env.Context.Webhooks[c.ID] = publicBase + hooks.Path(m.ID, c.ID, env.Context.TenantID)
		}
	}
	if tokens != nil && hostURL != "" && env.Context.TenantID != 0 && len(m.Permissions.HostAPI) > 0 {
		token, _, err := tokens.Issue(m.ID, m.Version, env.Context.TenantID, m.Permissions.HostAPI)
		if err != nil {
			return env, err
		}
		env.Context.Host = &pluginapi.HostAccess{URL: hostURL, Token: token}
	}
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

// callError presents a plugin's error by its message, the way WeKnora's own
// errors read in the UI; the protocol code stays reachable through
// errors.As(err, **pluginapi.Error).
type callError struct {
	pluginID string
	err      *pluginapi.Error
}

func (e *callError) Error() string {
	if e.err.Code == pluginapi.CodeUnavailable {
		return "plugin " + e.pluginID + " is unavailable: " + e.err.Message
	}
	return e.err.Message
}

func (e *callError) Unwrap() error { return e.err }

func present(pluginID string, err error) error {
	if pe, ok := pluginapi.AsError(err); ok {
		return &callError{pluginID: pluginID, err: pe}
	}
	return err
}

// Call makes a unary call to a plugin.
func (iv *Invoker) Call(
	ctx context.Context, m *manifest.Manifest, path string, instance map[string]any, input, out any,
) error {
	c, err := iv.clients.Client(ctx, m)
	if err != nil {
		return present(m.ID, err)
	}
	env, err := iv.Envelope(ctx, m, instance)
	if err != nil {
		return err
	}
	return present(m.ID, c.Call(ctx, path, env, input, out))
}

// Stream makes a streaming call to a plugin.
func (iv *Invoker) Stream(
	ctx context.Context, m *manifest.Manifest, path string, instance map[string]any, input any,
	fn func(pluginapi.Event) error,
) ([]byte, error) {
	c, err := iv.clients.Client(ctx, m)
	if err != nil {
		return nil, present(m.ID, err)
	}
	env, err := iv.Envelope(ctx, m, instance)
	if err != nil {
		return nil, err
	}
	end, err := c.Stream(ctx, path, env, input, fn)
	return end, present(m.ID, err)
}
