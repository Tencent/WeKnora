// Package sandbox: Cube Sandbox client.
//
// This file is a thin façade over the official Tencent CubeSandbox Go SDK
// (github.com/tencentcloud/CubeSandbox/sdk/go). Earlier versions of WeKnora
// hand-rolled the Connect-RPC envelope framing, host-header routing and
// process event decoding directly against envd. Now that the SDK is
// publicly available we delegate the wire-level work to it and keep only
// the small adapter layer that:
//
//   - Exposes the same cubeClient / SandboxInfo / CommandResult / DirEntry /
//     StatEntry / SandboxSummary surface that cube.go, session_manager.go and
//     the existing tests already depend on, so callers don't have to change.
//   - Bridges WeKnora's Config (which uses separate CubeAPIURL / CubeProxyURL
//     values with host-header routing on the proxy) to the SDK's Config
//     (which uses APIURL for control-plane and ProxyNodeIP/ProxyPortHTTP for
//     data-plane).
package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	cubesandbox "github.com/tencentcloud/CubeSandbox/sdk/go"
)

// cubeClient is a thin wrapper around *cubesandbox.Client. It keeps the
// original method signatures (Health / CreateSandbox / KillSandbox / …) so
// upstream code and tests continue to compile without change, while
// delegating every RPC to the official SDK.
//
// A note on the envdHTTP field: the SDK's Commands.Run currently sends the
// process.Process/Start request body as raw JSON, but the envd server on the
// data plane expects a Connect-RPC framed envelope (1 flag byte + 4 big-
// endian length bytes + payload). Real deployments (like the one this test
// suite talks to on port 80) reject the bare JSON with
// "invalid_argument: protocol error: promised … bytes". Until the SDK adds
// framing on the request path we send that specific call ourselves through
// envdHTTP, which is dialled the same way the SDK's data-plane client is:
// TCP straight to <ProxyNodeIP>:<ProxyPortHTTP> while preserving the
// virtual sandbox Host header.
type cubeClient struct {
	cfg           *Config
	sdk           *cubesandbox.Client
	envdHTTP      *http.Client
	envdProxyAddr string // "<ProxyNodeIP>:<ProxyPortHTTP>", empty when unset
	envdScheme    string // "http" or "https"
	sandboxDomain string
	envdPort      int
	httpTimeout   time.Duration
}

// SandboxInfo is the metadata WeKnora carries around for one live remote
// sandbox. It wraps the SDK's *cubesandbox.Sandbox so subsequent envd calls
// (WriteFile / RunCommand / …) can dispatch back through the SDK.
//
// The exported fields (ID / ClientID / Domain / EnvdVersion) are kept for
// backwards compatibility with the persistence code in cube.go and with the
// existing tests. sb is unexported so nothing outside this package accidentally
// depends on the SDK's concrete type.
type SandboxInfo struct {
	ID          string
	ClientID    string
	Domain      string
	EnvdVersion string

	sb *cubesandbox.Sandbox
}

// SandboxSummary mirrors one row of CubeAPI's GET /sandboxes response. Only
// the fields WeKnora actually consumes are kept, so a future SDK upgrade that
// adds new fields to cubesandbox.SandboxInfo does not force a schema change
// here.
type SandboxSummary struct {
	SandboxID  string
	TemplateID string
	ClientID   string
	Alias      string
	Domain     string
	StartedAt  time.Time
	EndAt      time.Time
	State      string
	CPUCount   int
	MemoryMB   int
}

// CommandResult is the aggregated output of one RunCommand call. Killed is
// synthesised by the caller when it detects a context cancellation, since the
// SDK does not expose that distinction directly.
type CommandResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Killed   bool
}

// DirEntry mirrors one row of the SDK's FileEntry, projected into the field
// shape the rest of WeKnora already expects.
type DirEntry struct {
	Name       string
	Path       string
	Type       string
	Size       int64
	Mode       uint32
	SymlinkTo  string
	ModifiedAt string
}

// StatEntry is the resolved metadata for a single filesystem entry. Its shape
// is identical to DirEntry because envd returns the same struct for Stat and
// ListDir; we keep two names for readability at call sites.
type StatEntry = DirEntry

// newCubeClient constructs a cubeClient from a WeKnora Config. It normalises
// the CubeProxy URL into the SDK's expected ProxyNodeIP + ProxyPortHTTP +
// ProxyScheme trio so the SDK can produce the correct
// "<port>-<sandboxID>.<domain>" host header while dialling the on-prem
// CubeProxy IP directly.
func newCubeClient(cfg *Config) *cubeClient {
	httpTimeout := cfg.CubeHTTPTimeout
	if httpTimeout <= 0 {
		httpTimeout = DefaultCubeHTTPTimeout
	}
	envdPort := cfg.CubeEnvdPort
	if envdPort <= 0 {
		envdPort = DefaultCubeEnvdPort
	}

	sdkCfg := cubesandbox.Config{
		APIURL:         strings.TrimRight(cfg.CubeAPIURL, "/"),
		APIKey:         cfg.CubeAPIKey,
		TemplateID:     cfg.CubeTemplate,
		SandboxDomain:  cfg.CubeSandboxDomain,
		RequestTimeout: httpTimeout,
	}

	// Break the CubeProxy URL into (host, port, scheme). The SDK then dials
	// <ProxyNodeIP>:<ProxyPortHTTP> while preserving the virtual sandbox
	// hostname in the request's Host header.
	var envdAddr, envdScheme string
	if proxyHost, proxyPort, proxyScheme, ok := parseProxyURL(cfg.CubeProxyURL); ok {
		sdkCfg.ProxyNodeIP = proxyHost
		sdkCfg.ProxyPortHTTP = proxyPort
		sdkCfg.ProxyScheme = proxyScheme
		envdAddr = net.JoinHostPort(proxyHost, strconv.Itoa(proxyPort))
		envdScheme = proxyScheme
	}

	ttl := cfg.CubeSandboxTTL
	if ttl <= 0 {
		ttl = DefaultCubeSandboxTTL
	}

	// Inject a single HTTP client that both routes control/data-plane traffic
	// and patches the SDK's connect request. This CubeAPI build (v0.5.11)
	// rejects the SDK's empty "/sandboxes/{id}/connect" body with HTTP 422
	// ("missing field `timeout`"); the rewriter fills the mandatory field in.
	sdkHTTP := newCubeSDKHTTPClient(envdAddr, cfg.CubeSandboxDomain, ttl, httpTimeout)

	return &cubeClient{
		cfg:           cfg,
		sdk:           cubesandbox.NewClient(sdkCfg, cubesandbox.WithHTTPClient(sdkHTTP)),
		envdHTTP:      newEnvdDataPlaneClient(envdAddr, httpTimeout),
		envdProxyAddr: envdAddr,
		envdScheme:    envdScheme,
		sandboxDomain: cfg.CubeSandboxDomain,
		envdPort:      envdPort,
		httpTimeout:   httpTimeout,
	}
}

// newCubeSDKHTTPClient builds the HTTP client injected into the SDK. Its
// transport serves both the SDK's control-plane calls (dialled straight to the
// CubeAPI host in the request URL) and its data-plane calls (dialled to the
// fixed CubeProxy address while keeping the virtual
// "<port>-<sandboxID>.<domain>" Host header). Data-plane requests are
// recognised by the sandbox-domain suffix in the URL host.
//
// The transport is wrapped in connectBodyRewriter so the SDK's
// "/sandboxes/{id}/connect" call carries the timeout field this deployment
// requires.
func newCubeSDKHTTPClient(envdProxyAddr, sandboxDomain string, ttl, timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		host := addr
		if h, _, err := net.SplitHostPort(addr); err == nil {
			host = h
		}
		// Requests whose host lands in the sandbox routing domain are
		// data-plane (envd) calls and must be tunnelled through CubeProxy.
		if envdProxyAddr != "" && sandboxDomain != "" && strings.HasSuffix(host, "."+sandboxDomain) {
			return dialer.DialContext(ctx, network, envdProxyAddr)
		}
		return dialer.DialContext(ctx, network, addr)
	}
	return &http.Client{
		Transport: &connectBodyRewriter{
			base:       transport,
			ttlSeconds: int(ttl / time.Second),
		},
	}
}

// connectBodyRewriter is an http.RoundTripper that injects a "timeout" field
// into "/sandboxes/{id}/connect" POST bodies. The SDK sends "{}" for connect
// (deliberately, to defer to server policy), but CubeAPI v0.5.11 rejects that
// with HTTP 422. All other requests pass through unchanged.
type connectBodyRewriter struct {
	base       http.RoundTripper
	ttlSeconds int
}

func (t *connectBodyRewriter) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/connect") {
		if newReq, err := t.rewriteConnect(req); err == nil {
			req = newReq
		}
	}
	return t.base.RoundTrip(req)
}

// rewriteConnect returns a clone of req whose JSON body is guaranteed to carry
// a "timeout" field. If the original body already sets one, it is left alone.
func (t *connectBodyRewriter) rewriteConnect(req *http.Request) (*http.Request, error) {
	body := map[string]any{}
	if req.Body != nil {
		raw, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &body)
		}
	}
	if _, ok := body["timeout"]; !ok {
		ttl := t.ttlSeconds
		if ttl <= 0 {
			ttl = int(DefaultCubeSandboxTTL / time.Second)
		}
		body["timeout"] = ttl
	}
	patched, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	clone := req.Clone(req.Context())
	clone.Body = io.NopCloser(bytes.NewReader(patched))
	clone.ContentLength = int64(len(patched))
	clone.Header.Set("Content-Type", "application/json")
	return clone, nil
}

// newEnvdDataPlaneClient creates an HTTP client that always dials
// proxyAddr (e.g. "127.0.0.1:80") regardless of what host is written in the
// request URL. That's how CubeProxy routes traffic into a specific MicroVM:
// the URL host stays as "<port>-<sandboxID>.<domain>" (so nginx's Lua host
// parser can identify the sandbox), while the TCP connection is opened to
// the fixed CubeProxy IP.
func newEnvdDataPlaneClient(proxyAddr string, timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	if proxyAddr != "" {
		transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, proxyAddr)
		}
	} else {
		transport.DialContext = dialer.DialContext
	}
	return &http.Client{Transport: transport}
}

// parseProxyURL turns "http://127.0.0.1:80" into ("127.0.0.1", 80, "http").
// A missing port defaults to 80/443 depending on the scheme; an unparseable
// URL returns ok=false so callers can fall back to the SDK's defaults.
func parseProxyURL(raw string) (host string, port int, scheme string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", 0, "", false
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" {
		return "", 0, "", false
	}
	scheme = strings.ToLower(parsed.Scheme)
	if scheme == "" {
		scheme = "http"
	}
	h, p, err := net.SplitHostPort(parsed.Host)
	if err != nil {
		h = parsed.Host
		if scheme == "https" {
			p = "443"
		} else {
			p = "80"
		}
	}
	portInt, err := strconv.Atoi(p)
	if err != nil || portInt <= 0 {
		return "", 0, "", false
	}
	return h, portInt, scheme, true
}

// Health probes the CubeAPI /health endpoint. Returns nil when the API is
// reachable.
func (c *cubeClient) Health(ctx context.Context) error {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	if _, err := c.sdk.Health(ctx); err != nil {
		return fmt.Errorf("cube api: health: %w", err)
	}
	return nil
}

// CreateSandbox provisions a new sandbox and returns its metadata.
func (c *cubeClient) CreateSandbox(ctx context.Context, templateID string, ttl time.Duration) (*SandboxInfo, error) {
	if templateID == "" {
		return nil, errors.New("cube api: templateID is required")
	}
	if ttl <= 0 {
		ttl = DefaultCubeSandboxTTL
	}
	// Explicitly opt this sandbox into public internet egress. Cube honours
	// two independent switches here and both must be on for
	// "curl https://example.com" style calls to work from inside the VM:
	//
	//   * AllowInternetAccess (top-level, cluster egress switch): when nil
	//     or true the SDK omits the field and the server falls back to the
	//     template's default. When the template already ticks "允许公网访问"
	//     this defaulting is fine, but sending true explicitly makes the
	//     intent obvious to anyone reading a captured payload and keeps
	//     behaviour identical when the template default flips.
	//   * Network.AllowPublicTraffic (per-sandbox routing switch): tells
	//     CubeProxy to attach the public-egress interface to this MicroVM.
	//     Without it the VM has no outbound route regardless of the
	//     cluster-level allow.
	//
	// See docs/guide/network-policy.md ("示例 7: 混合 L3 allow 和 L7 rules").
	allowInternet := true
	allowPublicTraffic := true
	opts := cubesandbox.CreateOptions{
		TemplateID:          templateID,
		Timeout:             cubesandbox.DurationPtr(ttl),
		AllowInternetAccess: &allowInternet,
		Network: cubesandbox.NetworkOptions{
			AllowPublicTraffic: &allowPublicTraffic,
		},
	}
	sb, err := c.sdk.Create(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("cube api: create sandbox: %w", err)
	}
	if sb == nil || sb.SandboxID == "" {
		return nil, errors.New("cube api: create sandbox: empty sandboxID")
	}
	domain := sb.Domain
	if domain == "" {
		domain = c.sandboxDomain
	}
	return &SandboxInfo{
		ID:          sb.SandboxID,
		ClientID:    sb.ClientID,
		Domain:      domain,
		EnvdVersion: sb.EnvdVersion,
		sb:          sb,
	}, nil
}

// KillSandbox tears down the sandbox by ID. Idempotent: a 404 (already gone)
// is treated as success so that reaper races don't produce noisy errors.
//
// This path first re-attaches to the remote sandbox via Client.Connect so
// the SDK has a live handle to call Kill against; callers that already hold
// a fresh SandboxInfo should prefer killSandboxByInfo to avoid the extra
// round-trip.
func (c *cubeClient) KillSandbox(ctx context.Context, sandboxID string) error {
	if sandboxID == "" {
		return nil
	}
	sb, err := c.reattach(ctx, sandboxID)
	if err != nil {
		if isSDKNotFound(err) {
			return nil
		}
		return fmt.Errorf("cube api: kill sandbox %s: %w", sandboxID, err)
	}
	if err := sb.Kill(ctx); err != nil {
		if isSDKNotFound(err) {
			return nil
		}
		return fmt.Errorf("cube api: kill sandbox %s: %w", sandboxID, err)
	}
	return nil
}

// killSandboxByInfo tears down a sandbox we already have an attached handle
// for, avoiding the extra /sandboxes/{id}/connect round-trip that
// KillSandbox performs. It is the preferred path for CubeSandbox.Cleanup
// and the ephemeral disposer since they both hold a fresh *SandboxInfo.
func (c *cubeClient) killSandboxByInfo(ctx context.Context, info *SandboxInfo) error {
	if info == nil || info.ID == "" {
		return nil
	}
	if info.sb == nil {
		return c.KillSandbox(ctx, info.ID)
	}
	if err := info.sb.Kill(ctx); err != nil {
		if isSDKNotFound(err) {
			return nil
		}
		return fmt.Errorf("cube api: kill sandbox %s: %w", info.ID, err)
	}
	return nil
}

// WriteFile uploads a script (or arbitrary blob) into the sandbox filesystem
// via envd's /files REST endpoint.
func (c *cubeClient) WriteFile(ctx context.Context, info *SandboxInfo, path string, content []byte) error {
	if info == nil || info.ID == "" {
		return errors.New("cube api: sandbox info required for WriteFile")
	}
	if path == "" {
		return errors.New("cube api: path required for WriteFile")
	}
	sb, err := c.sandboxFrom(ctx, info)
	if err != nil {
		return err
	}
	if err := sb.Files().Write(ctx, path, content); err != nil {
		return fmt.Errorf("envd: write file %s: %w", path, err)
	}
	return nil
}

// ReadFile downloads a file from the sandbox at the given absolute path.
func (c *cubeClient) ReadFile(ctx context.Context, info *SandboxInfo, path string) ([]byte, error) {
	if info == nil || info.ID == "" {
		return nil, errors.New("cube api: sandbox info required for ReadFile")
	}
	if path == "" {
		return nil, errors.New("cube api: path required for ReadFile")
	}
	sb, err := c.sandboxFrom(ctx, info)
	if err != nil {
		return nil, err
	}
	content, err := sb.Files().Read(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("envd: read file %s: %w", path, err)
	}
	return []byte(content), nil
}

// ListDir enumerates the contents of the given directory inside the sandbox.
func (c *cubeClient) ListDir(ctx context.Context, info *SandboxInfo, path string) ([]DirEntry, error) {
	if info == nil || info.ID == "" {
		return nil, errors.New("cube api: sandbox info required for ListDir")
	}
	if path == "" {
		path = "/"
	}
	sb, err := c.sandboxFrom(ctx, info)
	if err != nil {
		return nil, err
	}
	entries, err := sb.Files().List(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("envd: list dir %s: %w", path, err)
	}
	return convertFileEntries(entries), nil
}

// MakeDir recursively creates a directory (mkdir -p semantics).
func (c *cubeClient) MakeDir(ctx context.Context, info *SandboxInfo, path string) error {
	if path == "" {
		return errors.New("cube api: path required for MakeDir")
	}
	sb, err := c.sandboxFrom(ctx, info)
	if err != nil {
		return err
	}
	if _, err := sb.Files().MakeDir(ctx, path); err != nil {
		return fmt.Errorf("envd: mkdir %s: %w", path, err)
	}
	return nil
}

// Remove deletes a file or directory. Recursive on directories.
func (c *cubeClient) Remove(ctx context.Context, info *SandboxInfo, path string) error {
	if path == "" {
		return errors.New("cube api: path required for Remove")
	}
	sb, err := c.sandboxFrom(ctx, info)
	if err != nil {
		return err
	}
	if err := sb.Files().Remove(ctx, path); err != nil {
		return fmt.Errorf("envd: remove %s: %w", path, err)
	}
	return nil
}

// Move renames/moves an entry from src to dst.
func (c *cubeClient) Move(ctx context.Context, info *SandboxInfo, src, dst string) error {
	if src == "" || dst == "" {
		return errors.New("cube api: source and destination required for Move")
	}
	sb, err := c.sandboxFrom(ctx, info)
	if err != nil {
		return err
	}
	if _, err := sb.Files().Rename(ctx, src, dst); err != nil {
		return fmt.Errorf("envd: move %s -> %s: %w", src, dst, err)
	}
	return nil
}

// Stat returns metadata for a single filesystem entry. Returns (nil, nil)
// when the path does not exist so callers can distinguish "missing" from
// "error".
func (c *cubeClient) Stat(ctx context.Context, info *SandboxInfo, path string) (*StatEntry, error) {
	if path == "" {
		return nil, errors.New("cube api: path required for Stat")
	}
	sb, err := c.sandboxFrom(ctx, info)
	if err != nil {
		return nil, err
	}
	entry, err := sb.Files().Stat(ctx, path)
	if err != nil {
		if isSDKNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("envd: stat %s: %w", path, err)
	}
	converted := convertFileEntry(*entry)
	return &converted, nil
}

// RunCommand invokes envd's process.Process/Start on the sandbox and returns
// the aggregated result.
//
// Why we don't use the SDK's Commands().Run here: process.Process/Start is a
// Connect server-streaming RPC, so envd expects the *request* body to be a
// Connect envelope (1 flag byte + 4 big-endian length bytes + JSON payload),
// not bare JSON. The SDK (v0.0.0-20260709) marshals the request as raw JSON,
// which envd rejects with "protocol error: promised <N> bytes in enveloped
// message" because it reads the first five JSON bytes as an envelope header.
// Until the SDK frames the request path, we send that one call ourselves
// through envdHTTP — dialled straight to <ProxyNodeIP>:<ProxyPortHTTP> while
// preserving the virtual "<port>-<sandboxID>.<domain>" Host header, exactly
// how the SDK's data-plane client is configured. The response stream is
// already framed by envd, so we decode envelopes on the way back.
//
// If the context is cancelled mid-flight the Killed flag is set and the
// context error is returned alongside a partially-populated result, matching
// the original hand-rolled implementation.
func (c *cubeClient) RunCommand(
	ctx context.Context,
	info *SandboxInfo,
	cmd string,
	args []string,
	stdin string,
	env map[string]string,
	cwd string,
) (*CommandResult, error) {

	logger.Info(ctx, "[func (c *cubeClient) RunCommand] : ")

	if info == nil || info.ID == "" {
		return nil, errors.New("cube api: sandbox info required for RunCommand")
	}
	if cmd == "" {
		return nil, errors.New("cube api: cmd required for RunCommand")
	}
	// Resolve the SDK handle so we can reuse its metadata (domain, access
	// token) and lazily reattach when the caller resurrected info from
	// persistence.
	sb, err := c.sandboxFrom(ctx, info)
	if err != nil {
		return nil, err
	}

	// Mirror the SDK's Commands.Run contract: it runs "/bin/bash -l -c <line>"
	// so we assemble argv into a single shell line, resolving the interpreter
	// against $PATH inside the sandbox image.
	line := buildShellLine(cmd, args)
	if stdin != "" {
		// stdin has no first-class field on process.Process/Start; funnel it
		// via a heredoc prelude so the child still reads it. Rare in practice.
		line = wrapWithStdin(line, stdin)
	}
	logger.Info(ctx, "line : ", line)
	envs := env
	if envs == nil {
		envs = map[string]string{}
	}
	stdinFlag := false
	payload := envdProcessStartRequest{
		Process: envdProcessConfig{
			Cmd:  "/bin/bash",
			Args: []string{"-l", "-c", line},
			Envs: envs,
			Cwd:  cwd,
		},
		Stdin: &stdinFlag,
	}

	result, err := c.startProcess(ctx, sb, payload)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return &CommandResult{Killed: true, ExitCode: -1}, ctxErr
		}
		return nil, fmt.Errorf("envd: /process.Process/Start: %w", err)
	}
	return result, nil
}

// startProcess sends a Connect-framed process.Process/Start request to envd
// through the data-plane client and aggregates the streamed response. It is
// the framing-correct replacement for the SDK's Commands().Run request path.
func (c *cubeClient) startProcess(
	ctx context.Context,
	sb *cubesandbox.Sandbox,
	payload envdProcessStartRequest,
) (*CommandResult, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("envd: marshal process request: %w", err)
	}

	scheme := c.envdScheme
	if scheme == "" {
		scheme = "http"
	}
	target := url.URL{
		Scheme: scheme,
		Host:   c.envdHostForSandbox(sb),
		Path:   "/process.Process/Start",
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target.String(),
		bytes.NewReader(encodeConnectEnvelope(0, raw)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", connectContentType)
	req.Header.Set("Connect-Protocol-Version", connectProtocolVersion)
	req.Header.Set("Authorization", envdBasicAuth(defaultEnvdUser))
	if sb != nil && sb.EnvdAccessToken != "" {
		req.Header.Set("X-Access-Token", sb.EnvdAccessToken)
	}

	client := c.envdHTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		logger.Info(ctx, "resp.StatusCode : ", resp.StatusCode)
		return nil, fmt.Errorf("envd http %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return parseProcessStartStream(resp.Body)
}

// envdHostForSandbox builds the CubeProxy routing host
// "<envdPort>-<sandboxID>.<domain>" from the SDK handle, falling back to the
// client's configured domain when the handle omits it.
func (c *cubeClient) envdHostForSandbox(sb *cubesandbox.Sandbox) string {
	domain := ""
	id := ""
	if sb != nil {
		domain = sb.Domain
		id = sb.SandboxID
	}
	if domain == "" {
		domain = c.sandboxDomain
	}
	return strconv.Itoa(c.envdPort) + "-" + id + "." + domain
}

// ListSandboxes returns every sandbox the CubeAPI currently knows about.
func (c *cubeClient) ListSandboxes(ctx context.Context) ([]SandboxSummary, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	infos, err := c.sdk.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("cube api: list sandboxes: %w", err)
	}
	out := make([]SandboxSummary, 0, len(infos))
	for _, i := range infos {
		out = append(out, summaryFromSDK(i))
	}
	return out, nil
}

// GetSandbox fetches metadata for a single sandbox. Returns (nil, nil) if the
// sandbox no longer exists so callers can treat missing sandboxes as a benign
// condition.
func (c *cubeClient) GetSandbox(ctx context.Context, sandboxID string) (*SandboxSummary, error) {
	if sandboxID == "" {
		return nil, errors.New("cube api: sandboxID required for GetSandbox")
	}
	sb, err := c.reattach(ctx, sandboxID)
	if err != nil {
		if isSDKNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cube api: get sandbox %s: %w", sandboxID, err)
	}
	info, err := sb.GetInfo(ctx)
	if err != nil {
		if isSDKNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cube api: get sandbox %s: %w", sandboxID, err)
	}
	summary := summaryFromSDK(*info)
	return &summary, nil
}

// RefreshTimeout resets the sandbox TTL. Cube evicts sandboxes past their
// deadline, so long-running sessions must periodically call this.
//
// The SDK does not currently expose a first-class RefreshTimeout call, so we
// implement it by re-connecting to the sandbox (which by design revalidates
// the TTL on the server side). Any error is surfaced to the caller.
func (c *cubeClient) RefreshTimeout(ctx context.Context, sandboxID string, ttl time.Duration) error {
	if sandboxID == "" {
		return errors.New("cube api: sandboxID required for RefreshTimeout")
	}
	_ = ttl // reserved for future SDK support
	if _, err := c.sdk.Connect(ctx, sandboxID); err != nil {
		if isSDKNotFound(err) {
			return fmt.Errorf("cube api: refresh timeout: sandbox %s not found", sandboxID)
		}
		return fmt.Errorf("cube api: refresh timeout %s: %w", sandboxID, err)
	}
	return nil
}

// PauseSandbox asks CubeAPI to snapshot the MicroVM and free its runtime
// resources; the sandbox can later be brought back with ResumeSandbox or
// Client.Connect.
func (c *cubeClient) PauseSandbox(ctx context.Context, sandboxID string) error {
	if sandboxID == "" {
		return errors.New("cube api: sandboxID required for PauseSandbox")
	}
	sb, err := c.reattach(ctx, sandboxID)
	if err != nil {
		if isSDKNotFound(err) {
			return nil
		}
		return fmt.Errorf("cube api: pause sandbox %s: %w", sandboxID, err)
	}
	if err := sb.Pause(ctx, cubesandbox.PauseOptions{}); err != nil {
		if isSDKNotFound(err) {
			return nil
		}
		return fmt.Errorf("cube api: pause sandbox %s: %w", sandboxID, err)
	}
	return nil
}

// ResumeSandbox restores a previously paused sandbox. The SDK's recommended
// path is Client.Connect, which auto-resumes paused sandboxes.
func (c *cubeClient) ResumeSandbox(ctx context.Context, sandboxID string, ttl time.Duration) error {
	if sandboxID == "" {
		return errors.New("cube api: sandboxID required for ResumeSandbox")
	}
	_ = ttl // reserved for future SDK support
	if _, err := c.sdk.Connect(ctx, sandboxID); err != nil {
		return fmt.Errorf("cube api: resume sandbox %s: %w", sandboxID, err)
	}
	return nil
}

// hostHeader builds the CubeProxy routing host, e.g.
//
//	49983-8cbb6469eaf5432e81df0ecd575ad65d.cube.app
//
// Kept for backwards compatibility with the existing tests; the SDK builds
// the header internally, so production code no longer relies on this helper.
func (c *cubeClient) hostHeader(info *SandboxInfo) string {
	domain := info.Domain
	if domain == "" {
		domain = c.sandboxDomain
	}
	return strconv.Itoa(c.envdPort) + "-" + info.ID + "." + domain
}

// -----------------------------------------------------------------------------
// helpers
// -----------------------------------------------------------------------------

// sandboxFrom returns the SDK Sandbox handle for info. When info was created
// by this process we already have a live handle; otherwise (e.g. when the
// caller resurrects a SandboxInfo from persistence) we reconnect through the
// SDK so filesystem/process RPCs still work.
func (c *cubeClient) sandboxFrom(ctx context.Context, info *SandboxInfo) (*cubesandbox.Sandbox, error) {
	if info == nil {
		return nil, errors.New("cube api: sandbox info required")
	}
	if info.sb != nil {
		return info.sb, nil
	}
	if info.ID == "" {
		return nil, errors.New("cube api: sandbox info missing ID")
	}
	sb, err := c.reattach(ctx, info.ID)
	if err != nil {
		return nil, err
	}
	info.sb = sb
	if info.Domain == "" {
		info.Domain = sb.Domain
	}
	return sb, nil
}

// reattach binds the SDK client to a sandbox identified only by its ID. It
// is the SDK-blessed way to construct a *cubesandbox.Sandbox for a MicroVM
// this process didn't create.
func (c *cubeClient) reattach(ctx context.Context, sandboxID string) (*cubesandbox.Sandbox, error) {
	return c.sdk.Connect(ctx, sandboxID)
}

// withTimeout wraps ctx in a bounded timeout derived from the configured
// CubeHTTPTimeout. The returned cancel function must be deferred.
func (c *cubeClient) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok || c.httpTimeout <= 0 {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.httpTimeout)
}

// convertFileEntry projects the SDK's FileEntry into WeKnora's DirEntry
// shape. Type is normalised to the plain "file"/"directory"/"symlink" strings
// the existing callers expect.
func convertFileEntry(e cubesandbox.FileEntry) DirEntry {
	return DirEntry{
		Name:       e.Name,
		Path:       e.Path,
		Type:       normaliseFileType(e.Type),
		Size:       e.Size,
		Mode:       uint32(e.Mode),
		ModifiedAt: e.ModifiedTime,
	}
}

func convertFileEntries(in []cubesandbox.FileEntry) []DirEntry {
	out := make([]DirEntry, 0, len(in))
	for _, e := range in {
		out = append(out, convertFileEntry(e))
	}
	return out
}

// normaliseFileType maps envd's proto enum strings ("FILE_TYPE_FILE",
// "FILE_TYPE_DIRECTORY", …) onto the short lowercase names WeKnora already
// stores.
func normaliseFileType(t string) string {
	switch strings.ToUpper(t) {
	case "FILE_TYPE_FILE", "FILE":
		return "file"
	case "FILE_TYPE_DIRECTORY", "DIRECTORY":
		return "directory"
	case "FILE_TYPE_SYMLINK", "SYMLINK":
		return "symlink"
	default:
		if t == "" {
			return ""
		}
		return strings.ToLower(t)
	}
}

// summaryFromSDK adapts the SDK's SandboxInfo to WeKnora's leaner
// SandboxSummary struct. Fields the SDK returns via pointer (EndAt) are
// dereferenced defensively.
func summaryFromSDK(in cubesandbox.SandboxInfo) SandboxSummary {
	out := SandboxSummary{
		SandboxID:  in.SandboxID,
		TemplateID: in.TemplateID,
		ClientID:   in.ClientID,
		Alias:      in.Alias,
		Domain:     in.Domain,
		StartedAt:  in.StartedAt,
		State:      in.State,
		CPUCount:   in.CPUCount,
		MemoryMB:   in.MemoryMB,
	}
	if in.EndAt != nil {
		out.EndAt = *in.EndAt
	}
	return out
}

// isSDKNotFound spots the SDK's "resource missing" errors without having to
// import its internal error types. Both explicit NotFoundError values and
// textual "not found" mentions in wrapped errors are recognised.
func isSDKNotFound(err error) bool {
	if err == nil {
		return false
	}
	var nfe *cubesandbox.NotFoundError
	if errors.As(err, &nfe) {
		return true
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "not found") ||
		strings.Contains(lower, "no such file") ||
		strings.Contains(lower, "sandbox_not_found") ||
		strings.Contains(lower, "http 404")
}

// buildShellLine turns argv into a single shell-safe command line. It matches
// the semantics of the old hand-rolled path, which relied on envd's bash to
// resolve `python3` (or similar) against $PATH inside the sandbox image.
func buildShellLine(cmd string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, shellQuote(cmd))
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

// wrapWithStdin funnels a caller-supplied stdin payload into the child
// process by prepending a heredoc. This keeps the SDK's Commands.Run contract
// (which does not take an explicit stdin argument) usable in the rare case
// callers actually need to pipe data.
func wrapWithStdin(line, stdin string) string {
	// Use a heredoc delimiter unlikely to appear in caller data.
	const delim = "WEKNORA_STDIN_EOF"
	// Escape lines containing the delimiter defensively.
	safe := strings.ReplaceAll(stdin, delim, "")
	return "cat <<'" + delim + "' | " + line + "\n" + safe + "\n" + delim
}

// shellQuote wraps s in single quotes, escaping any embedded quotes. Suitable
// for building a /bin/bash -c line where every argv element should be treated
// as literal text.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	// Only bare tokens (alnum, dash, underscore, slash, dot, comma, colon,
	// equals, plus) can be passed unquoted; everything else gets single
	// quotes.
	if isShellSafe(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func isShellSafe(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '/' || r == '.' || r == ',' ||
			r == ':' || r == '=' || r == '+':
		default:
			return false
		}
	}
	return true
}
