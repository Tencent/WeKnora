package codeinterpreter

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// SandboxOpts are options for creating or connecting to a Sandbox.
type SandboxOpts struct {
	// APIKey to use. Falls back to the E2B_API_KEY env variable.
	APIKey string
	// AccessToken to use.
	AccessToken string
	// Domain to use (defaults to e2b.app).
	Domain string
	// Debug, if true, uses plain http:// against the sandbox.
	Debug bool
	// RequestTimeout default HTTP request timeout.
	RequestTimeout time.Duration
	// Timeout is the sandbox lifetime in seconds (not the request timeout).
	Timeout time.Duration
	// Template id/alias to use when creating a sandbox. Defaults to the
	// code-interpreter template.
	Template string
	// Metadata attached to the sandbox.
	Metadata map[string]string
	// EnvVars passed to the sandbox at startup.
	EnvVars map[string]string
	// HTTPClient allows overriding the underlying http.Client.
	HTTPClient *http.Client
	// Headers are additional headers to send on every request.
	Headers map[string]string
}

// SandboxInfo is the JSON shape returned by the API when listing sandboxes.
type SandboxInfo struct {
	SandboxID  string            `json:"sandboxID"`
	ClientID   string            `json:"clientID"`
	TemplateID string            `json:"templateID"`
	Alias      string            `json:"alias,omitempty"`
	Metadata   map[string]string `json:"metadata,omitempty"`
	StartedAt  string            `json:"startedAt,omitempty"`
	EndAt      string            `json:"endAt,omitempty"`
	State      string            `json:"state,omitempty"`
}

// Sandbox is a running E2B sandbox with code-interpreter capabilities.
type Sandbox struct {
	id         string
	clientID   string
	template   string
	envdPort   int
	connection *ConnectionConfig
}

// SandboxID returns the ID of this sandbox.
func (s *Sandbox) SandboxID() string { return s.id }

// ClientID returns the client id (envd worker) running this sandbox.
func (s *Sandbox) ClientID() string { return s.clientID }

// fullID returns "<sandboxID>-<clientID>" which is the form used in the
// sandbox hostnames.
func (s *Sandbox) fullID() string {
	if s.clientID == "" {
		return s.id
	}
	return fmt.Sprintf("%s-%s", s.id, s.clientID)
}

// getHost returns the public host for a port exposed by the sandbox.
func (s *Sandbox) getHost(port int) string {
	return fmt.Sprintf("%d-%s.%s", port, s.fullID(), s.connection.Domain)
}

// jupyterURL returns the URL to the internal Jupyter/Code-Interpreter server.
func (s *Sandbox) jupyterURL() string {
	scheme := "https"
	if s.connection.Debug {
		scheme = "http"
	}
	return fmt.Sprintf("%s://%s", scheme, s.getHost(JupyterPort))
}

// Create starts a new sandbox. `opts` may be nil, in which case sensible
// defaults are used (template = code-interpreter-v1).
func Create(ctx context.Context, opts *SandboxOpts) (*Sandbox, error) {
	if opts == nil {
		opts = &SandboxOpts{}
	}

	cfg := &ConnectionConfig{
		APIKey:         opts.APIKey,
		AccessToken:    opts.AccessToken,
		Domain:         opts.Domain,
		Debug:          opts.Debug,
		RequestTimeout: opts.RequestTimeout,
		HTTPClient:     opts.HTTPClient,
		Headers:        opts.Headers,
	}
	cfg.init()

	if cfg.APIKey == "" {
		return nil, &AuthenticationError{Message: "API key is required; set E2B_API_KEY or SandboxOpts.APIKey"}
	}

	template := opts.Template
	if template == "" {
		template = DefaultTemplate
	}

	timeoutSec := int(opts.Timeout / time.Second)
	if timeoutSec == 0 {
		timeoutSec = DefaultSandboxTimeout
	}

	body := map[string]any{
		"templateID": template,
		"timeout":    timeoutSec,
	}
	if len(opts.Metadata) > 0 {
		body["metadata"] = opts.Metadata
	}
	if len(opts.EnvVars) > 0 {
		body["envVars"] = opts.EnvVars
	}

	var out struct {
		SandboxID  string `json:"sandboxID"`
		ClientID   string `json:"clientID"`
		TemplateID string `json:"templateID"`
		EnvdPort   int    `json:"envdPort"`
	}
	if err := cfg.do(ctx, "POST", "/sandboxes", body, &out); err != nil {
		return nil, err
	}

	return &Sandbox{
		id:         out.SandboxID,
		clientID:   out.ClientID,
		template:   out.TemplateID,
		envdPort:   out.EnvdPort,
		connection: cfg,
	}, nil
}

// Connect attaches to an already running sandbox by its ID. The caller must
// supply at least the API key (via opts or the env var).
func Connect(ctx context.Context, sandboxID string, opts *SandboxOpts) (*Sandbox, error) {
	if opts == nil {
		opts = &SandboxOpts{}
	}
	cfg := &ConnectionConfig{
		APIKey:         opts.APIKey,
		AccessToken:    opts.AccessToken,
		Domain:         opts.Domain,
		Debug:          opts.Debug,
		RequestTimeout: opts.RequestTimeout,
		HTTPClient:     opts.HTTPClient,
		Headers:        opts.Headers,
	}
	cfg.init()

	var info SandboxInfo
	if err := cfg.do(ctx, "GET", "/sandboxes/"+sandboxID, nil, &info); err != nil {
		return nil, err
	}

	return &Sandbox{
		id:         info.SandboxID,
		clientID:   info.ClientID,
		template:   info.TemplateID,
		connection: cfg,
	}, nil
}

// Kill terminates the sandbox.
func (s *Sandbox) Kill(ctx context.Context) error {
	return s.connection.do(ctx, "DELETE", "/sandboxes/"+s.id, nil, nil)
}

// SetTimeout updates the remaining lifetime of the sandbox. Pass the desired
// wall-clock time-until-expiration.
func (s *Sandbox) SetTimeout(ctx context.Context, timeout time.Duration) error {
	body := map[string]int{
		"timeout": int(timeout / time.Second),
	}
	return s.connection.do(ctx, "POST", "/sandboxes/"+s.id+"/timeout", body, nil)
}

// IsRunning checks whether the sandbox is still reachable.
func (s *Sandbox) IsRunning(ctx context.Context) (bool, error) {
	err := s.connection.do(ctx, "GET", "/sandboxes/"+s.id, nil, nil)
	if err == nil {
		return true, nil
	}
	if _, ok := err.(*NotFoundError); ok {
		return false, nil
	}
	return false, err
}

// List returns all sandboxes currently running under the configured API key.
func List(ctx context.Context, opts *SandboxOpts) ([]SandboxInfo, error) {
	if opts == nil {
		opts = &SandboxOpts{}
	}
	cfg := &ConnectionConfig{
		APIKey:         opts.APIKey,
		AccessToken:    opts.AccessToken,
		Domain:         opts.Domain,
		Debug:          opts.Debug,
		RequestTimeout: opts.RequestTimeout,
		HTTPClient:     opts.HTTPClient,
		Headers:        opts.Headers,
	}
	cfg.init()

	var out []SandboxInfo
	if err := cfg.do(ctx, "GET", "/sandboxes", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetInfo returns information about this sandbox, including metadata and
// start/end times.
func (s *Sandbox) GetInfo(ctx context.Context) (*SandboxInfo, error) {
	var info SandboxInfo
	if err := s.connection.do(ctx, "GET", "/sandboxes/"+s.id, nil, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// GetHost returns a routable hostname for a port exposed by the sandbox. This
// lets callers build URLs to user-exposed services.
func (s *Sandbox) GetHost(port int) string {
	return s.getHost(port)
}

// addAuthHeaders adds authentication headers used by direct-to-sandbox HTTP
// calls (jupyterURL/envd).
func (s *Sandbox) addAuthHeaders(h http.Header) {
	h.Set("Content-Type", "application/json")
	if s.connection.AccessToken != "" {
		h.Set("X-Access-Token", s.connection.AccessToken)
	}
	if s.connection.TrafficAccessToken != "" {
		h.Set("E2B-Traffic-Access-Token", s.connection.TrafficAccessToken)
	}
}
