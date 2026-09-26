package activate

import (
	"context"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/plugin/configschema"
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
	oauth      OAuthResolver
}

// OAuthResolver turns a form field's "oauth:<id>" into an access token.
type OAuthResolver interface {
	AccessToken(ctx context.Context, pluginID string, tenantID uint64, ref string) (string, error)
}

// SetOAuth lets calls carry fresh access tokens in place of OAuth
// references.
func (iv *Invoker) SetOAuth(r OAuthResolver) {
	iv.mu.Lock()
	iv.oauth = r
	iv.mu.Unlock()
}

// resolveOAuth returns a configuration with every "oauth:<id>" string
// replaced by the connection's access token, copying what it changes so a
// caller's cached configuration keeps its references. A connection that is
// gone or cannot be refreshed becomes "": the plugin sees no token and
// reports unauthorized.
func (iv *Invoker) resolveOAuth(
	ctx context.Context, pluginID string, tenantID uint64, cfg map[string]any,
) map[string]any {
	iv.mu.RLock()
	r := iv.oauth
	iv.mu.RUnlock()
	if r == nil || cfg == nil {
		return cfg
	}
	var walk func(v any) any
	walk = func(v any) any {
		switch t := v.(type) {
		case string:
			if !configschema.IsOAuthRef(t) {
				return t
			}
			tok, err := r.AccessToken(ctx, pluginID, tenantID, t)
			if err != nil {
				logger.Warnf(ctx, "[plugin] %s: OAuth connection %s: %v", pluginID, t, err)
				return ""
			}
			return tok
		case map[string]any:
			out := make(map[string]any, len(t))
			for k, x := range t {
				out[k] = walk(x)
			}
			return out
		case []any:
			out := make([]any, len(t))
			for i, x := range t {
				out[i] = walk(x)
			}
			return out
		}
		return v
	}
	return walk(cfg).(map[string]any)
}

// SetWebhooks lets calls carry the workspace's webhook URLs, when WeKnora's
// public address (publicBase) is known.
func (iv *Invoker) SetWebhooks(tokens *webhook.Tokens, publicBase string) {
	iv.mu.Lock()
	iv.webhooks, iv.publicBase = tokens, publicBase
	iv.mu.Unlock()
}

// TokenIssuer signs the Host API token of one call, valid until the call's
// deadline (zero: none).
type TokenIssuer interface {
	IssueUntil(
		pluginID, version string, tenantID uint64, scopes []string, deadline time.Time,
	) (string, time.Time, error)
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
		var deadline time.Time
		if env.Context.Deadline != nil {
			deadline = *env.Context.Deadline
		}
		token, _, err := tokens.IssueUntil(m.ID, m.Version, env.Context.TenantID, m.Permissions.HostAPI, deadline)
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
	// System connections belong to the platform (tenant 0).
	env.Config.System = iv.resolveOAuth(ctx, m.ID, 0, env.Config.System)
	env.Config.Tenant = iv.resolveOAuth(ctx, m.ID, env.Context.TenantID, env.Config.Tenant)
	env.Config.Instance = iv.resolveOAuth(ctx, m.ID, env.Context.TenantID, env.Config.Instance)
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

// ConfigOverride replaces configuration scopes for one call: a form being
// edited asks the plugin with what it holds, not what is stored. A nil scope
// keeps the stored one.
type ConfigOverride struct {
	System map[string]any
	Tenant map[string]any
}

// CallOverriding is Call with some configuration scopes replaced.
func (iv *Invoker) CallOverriding(
	ctx context.Context, m *manifest.Manifest, path string, instance map[string]any, override ConfigOverride,
	input, out any,
) error {
	c, err := iv.clients.Client(ctx, m)
	if err != nil {
		return present(m.ID, err)
	}
	env, err := iv.Envelope(ctx, m, instance)
	if err != nil {
		return err
	}
	if override.System != nil {
		env.Config.System = iv.resolveOAuth(ctx, m.ID, 0, override.System)
	}
	if override.Tenant != nil {
		env.Config.Tenant = iv.resolveOAuth(ctx, m.ID, env.Context.TenantID, override.Tenant)
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
