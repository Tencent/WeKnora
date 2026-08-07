// Package sandbox: Cube adapter for the provider-neutral RemoteSandboxClient.
//
// CubeRemoteClient implements RemoteSandboxClient on top of the Cube SDK and
// envd transport. Callers speak RemoteSandboxClient; Cube-specific types,
// HTTP status codes, and workarounds never leak past this file — every return
// value is either a neutral DTO or a RemoteError with a stable Kind.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	cubesandbox "github.com/tencentcloud/CubeSandbox/sdk/go"
)

// CubeRemoteClient implements RemoteSandboxClient on top of the Cube SDK and
// envd transport. It is the only Cube backend the manager and lifecycle
// coordinator see.
type CubeRemoteClient struct {
	config        *Config
	client        *cubesandbox.Client
	sandboxDomain string
	httpTimeout   time.Duration
}

// NewCubeRemoteClient constructs a Cube-backed RemoteSandboxClient using the
// SDK default HTTP clients (separate control/data pools). Suitable for the
// process-wide default manager and for throwaway connectivity probes, neither
// of which benefits from an externally owned pool.
func NewCubeRemoteClient(config *Config) (*CubeRemoteClient, error) {
	return NewCubeRemoteClientWithPool(config, nil)
}

// NewCubeRemoteClientWithPool builds a client whose connections come from a
// caller-owned pool. Named configs construct a client per request, so the pool
// is what keeps connections alive across requests; it routes control-plane
// traffic onto the transport shared with E2B while preserving the SDK's
// proxy dial rewrite for the data plane. A nil pool keeps the SDK defaults.
func NewCubeRemoteClientWithPool(
	config *Config,
	pool *CubeTransportPool,
) (*CubeRemoteClient, error) {
	if config == nil {
		return nil, errors.New("cube remote client config is required")
	}
	httpTimeout := config.CubeHTTPTimeout
	if httpTimeout <= 0 {
		httpTimeout = DefaultCubeHTTPTimeout
	}
	sdkCfg := cubesandbox.Config{
		APIURL:         config.CubeAPIURL,
		APIKey:         config.CubeAPIKey,
		TemplateID:     config.CubeTemplate,
		SandboxDomain:  config.CubeSandboxDomain,
		Timeout:        config.CubeHTTPTimeout,
		RequestTimeout: config.CubeHTTPTimeout,
	}

	if proxyHost, proxyPort, proxyScheme, ok := parseProxyURL(config.CubeProxyURL); ok {
		sdkCfg.ProxyNodeIP = proxyHost
		sdkCfg.ProxyPortHTTP = proxyPort
		sdkCfg.ProxyScheme = proxyScheme
	}

	var opts []cubesandbox.ClientOption
	if pool != nil {
		httpClient := &http.Client{
			Timeout:   httpTimeout,
			Transport: pool.RoundTripperFor(config),
		}
		opts = append(opts, cubesandbox.WithHTTPClient(httpClient))
	}

	return &CubeRemoteClient{
		config: config,
		client: cubesandbox.NewClient(
			sdkCfg,
			opts...,
		),
		sandboxDomain: config.CubeSandboxDomain,
		httpTimeout:   httpTimeout,
	}, nil
}

// cubeRemoteHandle is the RemoteSandboxHandle Cube returns. It wraps the SDK's
// *cubesandbox.Sandbox directly; all envd calls (WriteFile / RunCommand / …)
// dispatch through sb. Metadata is cached at creation time so Metadata()
// can return it without a network round-trip.
type cubeRemoteHandle struct {
	sb       *cubesandbox.Sandbox
	metadata map[string]string
}

func (h *cubeRemoteHandle) ID() string {
	if h == nil || h.sb == nil {
		return ""
	}
	return h.sb.SandboxID
}

func (h *cubeRemoteHandle) Provider() RemoteProvider { return SandboxTypeCube }

func (h *cubeRemoteHandle) Metadata() map[string]string {
	if h == nil {
		return nil
	}
	return cloneMetadata(h.metadata)
}

// --- RemoteSandboxClient ------------------------------------------------------

func (c *CubeRemoteClient) Provider() RemoteProvider { return SandboxTypeCube }

func (c *CubeRemoteClient) Capabilities() RemoteSandboxCapabilities {
	return RemoteSandboxCapabilities{
		SupportsReconnect:             true,
		SupportsMetadata:              true,
		SupportsListSandboxes:         true,
		SupportsPauseResume:           true,
		SupportsTimeoutRefresh:        true,
		SupportsFilesystemEnumeration: true,
		// Cube's CreateOptions has no volume-mount field yet; revisit when the
		// official Go SDK reaches v0.6.0.
		SupportsVolumes: false,
	}
}

func (c *CubeRemoteClient) Health(ctx context.Context) error {
	if _, err := c.client.Health(ctx); err != nil {
		logger.Errorf(ctx, "cube remote client health check failed: %v", err)
		return normalizeCubeError("Health", err)
	}
	return nil
}

func (c *CubeRemoteClient) Create(
	ctx context.Context,
	request RemoteCreateRequest,
) (RemoteSandboxHandle, error) {
	if strings.TrimSpace(request.TemplateID) == "" {
		return nil, cubeInvalidRequest("Create", "template ID is required", nil)
	}
	timeout, err := cubeTimeout(request.Timeout)
	if err != nil {
		return nil, cubeInvalidRequest("Create", err.Error(), err)
	}
	action := request.Timeout.Action
	autoResume := request.Timeout.AutoResume
	if action == "" {
		action = RemoteOnTimeoutKill
	}
	if action != RemoteOnTimeoutKill && action != RemoteOnTimeoutPause {
		return nil, cubeInvalidRequest(
			"Create",
			fmt.Sprintf("unsupported timeout action %q", action),
			nil,
		)
	}
	if request.Timeout.AutoResume && action != RemoteOnTimeoutPause {
		return nil, cubeInvalidRequest(
			"Create",
			"auto resume requires pause on timeout",
			nil,
		)
	}
	// Cube honours two independent switches for outbound reachability and
	// both must be on for "curl https://example.com" to work from inside
	// the VM:
	//
	//   * AllowInternetAccess (top-level, cluster egress switch): when
	//     nil the SDK omits the field and the server falls back to the
	//     template's default. Setting it explicitly makes the intent
	//     obvious to anyone reading a captured payload and keeps
	//     behaviour identical when the template default flips.
	//   * Network.AllowPublicTraffic (per-sandbox routing switch): tells
	//     CubeProxy to attach the public-egress interface to this MicroVM.
	//     Without it the VM has no outbound route regardless of the
	//     cluster-level allow.
	//
	// Callers can override either switch via cubeCreateRequest.Network.
	// The permissive (both on) defaults preserve the pre-refactor
	// behaviour for callers that leave the policy unset.
	//
	// See docs/guide/network-policy.md ("示例 7: 混合 L3 allow 和 L7 rules").
	network := request.Network
	if network.AllowInternetAccess == nil {
		defaultOn := true
		network.AllowInternetAccess = &defaultOn
	}
	if network.AllowPublicTraffic == nil {
		defaultOn := true
		network.AllowPublicTraffic = &defaultOn
	}
	opts := cubesandbox.CreateOptions{
		TemplateID:          request.TemplateID,
		Timeout:             timeout,
		EnvVars:             cloneMetadata(request.EnvVars),
		Metadata:            cloneMetadata(request.Metadata),
		AllowInternetAccess: network.AllowInternetAccess,
		Network: cubesandbox.NetworkOptions{
			AllowPublicTraffic: network.AllowPublicTraffic,
			AllowOut:           append([]string(nil), network.AllowOut...),
			DenyOut:            append([]string(nil), network.DenyOut...),
		},
	}
	if action != "" {
		opts.Extra = map[string]any{
			"lifecycle": map[string]any{
				"onTimeout":  string(action),
				"autoResume": autoResume,
			},
		}
	}
	sb, err := c.client.Create(ctx, opts)
	if err != nil {
		return nil, normalizeCubeError("Create", err)
	}
	if sb == nil || sb.SandboxID == "" {
		return nil, errors.New("cube api: create sandbox: empty sandboxID")
	}
	return &cubeRemoteHandle{
		sb:       sb,
		metadata: cloneMetadata(request.Metadata),
	}, nil
}

func (c *CubeRemoteClient) Connect(
	ctx context.Context,
	sandboxID string,
) (RemoteSandboxHandle, error) {
	if strings.TrimSpace(sandboxID) == "" {
		return nil, cubeInvalidRequest("Connect", "sandbox ID is required", nil)
	}
	sb, err := c.client.Connect(ctx, sandboxID)
	if err != nil {
		return nil, normalizeCubeError("Connect", err)
	}
	if sb == nil || sb.SandboxID == "" {
		return nil, NewRemoteError(
			SandboxTypeCube, "Connect", RemoteErrorKindInternal,
			"cube returned an empty sandbox handle", nil,
		)
	}
	return &cubeRemoteHandle{sb: sb}, nil
}

func (c *CubeRemoteClient) Get(
	ctx context.Context,
	sandboxID string,
) (*RemoteSandboxSummary, error) {
	if strings.TrimSpace(sandboxID) == "" {
		return nil, cubeInvalidRequest("Get", "sandbox ID is required", nil)
	}
	sb, err := c.client.Connect(ctx, sandboxID)
	if err != nil {
		return nil, normalizeCubeError("Get", err)
	}
	info, err := sb.GetInfo(ctx)
	if err != nil {
		if isSDKNotFound(err) {
			return nil, NewRemoteError(
				SandboxTypeCube, "Get", RemoteErrorKindNotFound,
				"sandbox not found", nil,
			)
		}
		return nil, normalizeCubeError("Get", err)
	}
	if info == nil {
		return nil, NewRemoteError(
			SandboxTypeCube, "Get", RemoteErrorKindNotFound,
			"sandbox not found", nil,
		)
	}
	return cubeRemoteSummary(*info), nil
}

func (c *CubeRemoteClient) List(
	ctx context.Context,
	filter RemoteListFilter,
) ([]RemoteSandboxSummary, error) {
	sandboxInfos, err := c.client.List(ctx)
	if err != nil {
		return nil, normalizeCubeError("List", err)
	}
	result := make([]RemoteSandboxSummary, 0, len(sandboxInfos))
	for _, summary := range sandboxInfos {
		converted := cubeRemoteSummary(summary)
		if !metadataMatches(converted.Metadata, filter.Metadata) ||
			!cubeStateMatches(converted.State, filter.States) {
			continue
		}
		result = append(result, *converted)
	}
	return result, nil
}

func (c *CubeRemoteClient) Delete(ctx context.Context, sandboxID string) error {
	if strings.TrimSpace(sandboxID) == "" {
		return cubeInvalidRequest("Delete", "sandbox ID is required", nil)
	}
	sb, err := c.client.Connect(ctx, sandboxID)
	if err != nil {
		return normalizeCubeError("Delete", err)
	}
	if err := sb.Kill(ctx); err != nil {
		return normalizeCubeError("Delete", err)
	}
	return nil
}

func (c *CubeRemoteClient) Exec(
	ctx context.Context,
	handle RemoteSandboxHandle,
	request RemoteExecRequest,
) (*RemoteExecResult, error) {
	sb, err := cubeHandleSandbox("Exec", handle)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.Command) == "" {
		return nil, cubeInvalidRequest("Exec", "command is required", nil)
	}
	if request.Shell && len(request.Args) != 0 {
		return nil, cubeInvalidRequest(
			"Exec", "shell execution cannot include argv arguments", nil,
		)
	}
	if request.Timeout < 0 {
		return nil, cubeInvalidRequest("Exec", "execution timeout cannot be negative", nil)
	}

	execCtx := ctx
	cancel := func() {}
	if request.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, request.Timeout)
	}
	defer cancel()

	line := request.Command
	if !request.Shell {
		line = buildShellLine(request.Command, request.Args)
	}
	if request.Stdin != "" {
		line = wrapWithStdin(line, request.Stdin)
	}
	envs := cloneMetadata(request.Env)
	if envs == nil {
		envs = map[string]string{}
	}

	startedAt := time.Now()
	// User comes from the neutral request rather than being hardcoded: running
	// everything as root silently defeats file-mode protections on shared
	// volumes, and made this adapter behave differently from E2B's.
	sdkResult, execErr := sb.Commands().Run(execCtx, line, cubesandbox.CommandOptions{
		Timeout: request.Timeout,
		Envs:    envs,
		Cwd:     request.WorkDir,
		User:    request.User,
	})
	duration := time.Since(startedAt)

	if execErr != nil {
		if request.Timeout > 0 &&
			errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			return &RemoteExecResult{
				Duration: duration,
				Killed:   true,
				ExitCode: -1,
			}, nil
		}
		return nil, normalizeCubeError("Exec", execErr)
	}
	if sdkResult == nil {
		return nil, NewRemoteError(
			SandboxTypeCube, "Exec", RemoteErrorKindInternal,
			"cube returned an empty command result", nil,
		)
	}
	return &RemoteExecResult{
		Stdout:   sdkResult.Stdout,
		Stderr:   sdkResult.Stderr,
		ExitCode: sdkResult.ExitCode,
		Duration: duration,
	}, nil
}

func (c *CubeRemoteClient) WriteFile(
	ctx context.Context,
	handle RemoteSandboxHandle,
	path string,
	content []byte,
) error {
	sb, err := cubeHandleSandbox("WriteFile", handle)
	if err != nil {
		return err
	}
	if err := sb.Files().Write(ctx, path, content); err != nil {
		return normalizeCubeError("WriteFile", err)
	}
	return nil
}

func (c *CubeRemoteClient) ReadFile(
	ctx context.Context,
	handle RemoteSandboxHandle,
	path string,
) ([]byte, error) {
	sb, err := cubeHandleSandbox("ReadFile", handle)
	if err != nil {
		return nil, err
	}
	content, err := sb.Files().Read(ctx, path)
	if err != nil {
		return nil, normalizeCubeError("ReadFile", err)
	}
	return []byte(content), nil
}

func (c *CubeRemoteClient) ListDir(
	ctx context.Context,
	handle RemoteSandboxHandle,
	path string,
) ([]RemoteDirEntry, error) {
	sb, err := cubeHandleSandbox("ListDir", handle)
	if err != nil {
		return nil, err
	}
	if path == "" {
		path = "/"
	}
	entries, err := sb.Files().List(ctx, path)
	if err != nil {
		return nil, normalizeCubeError("ListDir", err)
	}
	result := make([]RemoteDirEntry, 0, len(entries))
	for _, e := range entries {
		result = append(result, RemoteDirEntry{
			Name:    e.Name,
			Path:    e.Path,
			Type:    cubeRemoteEntryType(normaliseFileType(e.Type)),
			Size:    e.Size,
			ModTime: cubeModTime(e.ModifiedTime),
		})
	}
	return result, nil
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

func (c *CubeRemoteClient) MakeDir(
	ctx context.Context,
	handle RemoteSandboxHandle,
	path string,
) error {
	sb, err := cubeHandleSandbox("MakeDir", handle)
	if err != nil {
		return err
	}
	if _, err := sb.Files().MakeDir(ctx, path); err != nil {
		return normalizeCubeError("MakeDir", err)
	}
	return nil
}

func (c *CubeRemoteClient) Remove(
	ctx context.Context,
	handle RemoteSandboxHandle,
	path string,
) error {
	sb, err := cubeHandleSandbox("Remove", handle)
	if err != nil {
		return err
	}
	if err := sb.Files().Remove(ctx, path); err != nil {
		return normalizeCubeError("Remove", err)
	}
	return nil
}

func (c *CubeRemoteClient) Stat(
	ctx context.Context,
	handle RemoteSandboxHandle,
	path string,
) (*RemoteStatEntry, error) {
	sb, err := cubeHandleSandbox("Stat", handle)
	if err != nil {
		return nil, err
	}
	entry, err := sb.Files().Stat(ctx, path)
	if err != nil {
		if isSDKNotFound(err) {
			return nil, NewRemoteError(
				SandboxTypeCube, "Stat", RemoteErrorKindNotFound,
				"path not found", nil,
			)
		}
		return nil, normalizeCubeError("Stat", err)
	}
	if entry == nil {
		return nil, NewRemoteError(
			SandboxTypeCube, "Stat", RemoteErrorKindNotFound,
			"path not found", nil,
		)
	}
	return &RemoteStatEntry{
		Path:    entry.Path,
		Type:    cubeRemoteEntryType(normaliseFileType(entry.Type)),
		Size:    entry.Size,
		ModTime: cubeModTime(entry.ModifiedTime),
	}, nil
}

// --- helpers -----------------------------------------------------------------

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

func cubeTimeout(policy RemoteTimeoutPolicy) (*time.Duration, error) {
	switch policy.Mode {
	case "", RemoteTimeoutServerDefault:
		return nil, nil
	case RemoteTimeoutExplicit:
		value := policy.Value
		if value < 0 {
			value = cubesandbox.NeverTimeout
		}
		return &value, nil
	default:
		return nil, fmt.Errorf("unsupported timeout mode %q", policy.Mode)
	}
}

// cubeHandleSandbox extracts the SDK *cubesandbox.Sandbox from an opaque
// RemoteSandboxHandle. Returns an error when the handle is nil, not Cube,
// or has an empty sandbox ID.
func cubeHandleSandbox(op string, handle RemoteSandboxHandle) (*cubesandbox.Sandbox, error) {
	cubeHandle, ok := handle.(*cubeRemoteHandle)
	if !ok || cubeHandle == nil || cubeHandle.sb == nil ||
		strings.TrimSpace(cubeHandle.sb.SandboxID) == "" {
		return nil, cubeInvalidRequest(op, "handle was not issued by Cube", nil)
	}
	return cubeHandle.sb, nil
}

// cubeRemoteSummary converts the SDK's SandboxInfo (from List / GetInfo) into
// the provider-neutral RemoteSandboxSummary DTO.
func cubeRemoteSummary(info cubesandbox.SandboxInfo) *RemoteSandboxSummary {
	out := &RemoteSandboxSummary{
		ID:         info.SandboxID,
		TemplateID: info.TemplateID,
		State:      normalizeCubeState(info.State),
		RawState:   info.State,
		Metadata:   cloneMetadata(info.Metadata),
		StartedAt:  info.StartedAt,
	}
	if info.EndAt != nil {
		out.EndAt = *info.EndAt
	}
	return out
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
func normalizeCubeState(state string) RemoteSandboxState {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "running", "available":
		return RemoteStateRunning
	case "paused":
		return RemoteStatePaused
	case "pending", "creating", "provisioning", "starting", "pausing", "resuming":
		return RemoteStateTransitioning
	case "killing", "killed", "terminated", "stopped", "deleted", "failed", "error":
		return RemoteStateTerminal
	default:
		return RemoteStateUnknown
	}
}

func cubeRemoteEntryType(entryType string) RemoteDirEntryType {
	switch strings.ToLower(strings.TrimSpace(entryType)) {
	case "file":
		return RemoteEntryFile
	case "directory", "dir":
		return RemoteEntryDir
	default:
		return RemoteEntryOther
	}
}

func cubeModTime(value string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		parsed, err := time.Parse(layout, value)
		if err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func cubeStateMatches(candidate RemoteSandboxState, allowed []RemoteSandboxState) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, state := range allowed {
		if candidate == state {
			return true
		}
	}
	return false
}

func cubeInvalidRequest(op, message string, cause error) error {
	return NewRemoteError(
		SandboxTypeCube, op, RemoteErrorKindInvalidRequest, message, cause,
	)
}

// normalizeCubeError projects a Cube-native error (SDK sentinel, APIError,
// net.Error, context cancellation) onto a RemoteError with a stable Kind. The
// original error is preserved via errors.Unwrap for diagnostics.
func normalizeCubeError(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("cube %s: %w", op, err)
	}

	kind := RemoteErrorKindInternal
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		kind = RemoteErrorKindTimeout
	case errors.Is(err, cubesandbox.ErrAuthentication):
		kind = RemoteErrorKindAuthentication
	case errors.Is(err, cubesandbox.ErrTemplateNotFound):
		kind = RemoteErrorKindInvalidRequest
	case errors.Is(err, cubesandbox.ErrSandboxNotFound):
		kind = RemoteErrorKindNotFound
	default:
		var pathNotFound *cubesandbox.NotFoundError
		var apiErr *cubesandbox.APIError
		var netErr net.Error
		switch {
		case errors.As(err, &pathNotFound):
			kind = RemoteErrorKindNotFound
		case errors.As(err, &apiErr):
			kind = httpErrorKind(op, apiErr.StatusCode)
		case errors.As(err, &netErr) && netErr.Timeout():
			kind = RemoteErrorKindTimeout
		case errors.As(err, &netErr):
			kind = RemoteErrorKindUnavailable
		}
	}
	return NewRemoteError(SandboxTypeCube, op, kind, err.Error(), err)
}

var (
	_ RemoteSandboxClient = (*CubeRemoteClient)(nil)
	_ RemoteSandboxHandle = (*cubeRemoteHandle)(nil)
)
