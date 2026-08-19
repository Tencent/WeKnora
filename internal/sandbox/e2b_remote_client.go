// Package sandbox: E2B adapter for the provider-neutral RemoteSandboxClient.
//
// E2BRemoteClient wraps go-e2b and keeps provider-specific configuration,
// lifecycle, access-token, and filesystem details behind RemoteSandboxClient.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	e2b "github.com/matiasinsaurralde/go-e2b"

	"github.com/Tencent/WeKnora/internal/logger"
)

// E2BRemoteClient implements RemoteSandboxClient on top of the go-e2b client.
type E2BRemoteClient struct {
	client *e2b.Client

	templateID string
	timeout    time.Duration
}

// NewE2BRemoteClient builds an E2B-backed RemoteSandboxClient from Config.
// The API key is required; the caller may leave APIURL / SandboxDomain empty
// to fall back to the SDK's built-in defaults.
func NewE2BRemoteClient(cfg *Config) (*E2BRemoteClient, error) {
	return NewE2BRemoteClientWithTransport(cfg, nil)
}

// NewE2BRemoteClientWithTransport builds the client with an injected HTTP
// transport. Per-tenant clients are constructed per request, so sharing one
// transport across tenants is what preserves connection pooling: transports
// pool per host:port and the API key travels in per-request headers, making
// the sharing both safe and effective. A nil transport uses the http default.
func NewE2BRemoteClientWithTransport(
	cfg *Config,
	transport *http.Transport,
) (*E2BRemoteClient, error) {
	if transport == nil {
		return newE2BRemoteClient(cfg, nil)
	}
	return newE2BRemoteClient(cfg, transport)
}

// NewE2BRemoteClientWithPool builds the client on top of the shared gateway
// pool. It is what self-hosted E2B-compatible control planes need: the pool
// keeps control-plane traffic on the process-wide transport while dialling
// data-plane traffic at the configured gateway (see gateway_transport.go).
// A nil pool falls back to the SDK defaults.
func NewE2BRemoteClientWithPool(
	cfg *Config,
	pool *SandboxGatewayTransportPool,
) (*E2BRemoteClient, error) {
	if pool == nil {
		return newE2BRemoteClient(cfg, nil)
	}
	return newE2BRemoteClient(cfg, pool.RoundTripperFor(cfg))
}

func newE2BRemoteClient(
	cfg *Config,
	transport http.RoundTripper,
) (*E2BRemoteClient, error) {
	if cfg == nil {
		return nil, errors.New("e2b remote client config is required")
	}
	if strings.TrimSpace(cfg.E2BAPIKey) == "" {
		return nil, errors.New("E2BAPIKey is required for the E2B backend")
	}
	timeout := cfg.E2BHTTPTimeout
	if timeout <= 0 {
		timeout = DefaultE2BHTTPTimeout
	}
	// Every E2B client speaks to envd through the compatibility shim, whether
	// or not a gateway is configured: the two details it rewrites belong to the
	// envd protocol itself, not to any one deployment. See envd_compat_transport.go.
	httpClient := &http.Client{
		Timeout:   timeout,
		Transport: NewEnvdCompatTransport(transport, DefaultSandboxExecUser),
	}
	client, err := e2b.NewClient(e2b.ClientConfig{
		APIKey:        cfg.E2BAPIKey,
		APIBaseURL:    strings.TrimSpace(cfg.E2BAPIURL),
		SandboxDomain: strings.TrimSpace(cfg.E2BSandboxDomain),
		HTTPClient:    httpClient,
	})
	if err != nil {
		return nil, fmt.Errorf("build e2b client: %w", err)
	}

	ttl := cfg.E2BSandboxTTL
	if ttl <= 0 {
		ttl = DefaultE2BSandboxTTL
	}
	return &E2BRemoteClient{
		client:     client,
		templateID: strings.TrimSpace(cfg.E2BTemplate),
		timeout:    ttl,
	}, nil
}

// e2bRemoteHandle is the RemoteSandboxHandle E2B returns. It carries the
// *e2b.Sandbox so subsequent envd calls can reuse its access token.
type e2bRemoteHandle struct {
	sandbox  *e2b.Sandbox
	metadata map[string]string
}

func (h *e2bRemoteHandle) ID() string {
	if h == nil || h.sandbox == nil {
		return ""
	}
	return h.sandbox.ID
}

func (h *e2bRemoteHandle) Provider() RemoteProvider { return SandboxTypeE2B }

func (h *e2bRemoteHandle) Metadata() map[string]string {
	if h == nil {
		return nil
	}
	return cloneMetadata(h.metadata)
}

// --- RemoteSandboxClient ------------------------------------------------------

func (c *E2BRemoteClient) Provider() RemoteProvider { return SandboxTypeE2B }

func (c *E2BRemoteClient) Capabilities() RemoteSandboxCapabilities {
	return RemoteSandboxCapabilities{
		SupportsReconnect:             true,
		SupportsMetadata:              true,
		SupportsListSandboxes:         true,
		SupportsPauseResume:           true,
		SupportsTimeoutRefresh:        true,
		SupportsFilesystemEnumeration: true,
		// E2B stores snapshots as templates, so a snapshot ID can be handed
		// straight back as CreateOptions.TemplateID.
		SupportsSnapshots: true,
		SupportsVolumes:   true,
	}
}

// Health probes the control plane by listing sandboxes. The protocol has no
// dedicated health endpoint, and a list is the smallest authenticated call
// that detects a bad API key or a dead API.
//
// It deliberately uses the v2 listing rather than the legacy one: v2 is what
// every other call in this client already depends on, and E2B-compatible
// control planes (Agent-Sandbox, for one) implement only that one — probing
// the legacy path would report a perfectly healthy backend as unreachable.
func (c *E2BRemoteClient) Health(ctx context.Context) error {
	if _, err := c.client.ListSandboxesV2(ctx, e2b.WithSandboxLimit(1)); err != nil {
		return normalizeE2BError("Health", err)
	}
	return nil
}

func (c *E2BRemoteClient) ListTemplates(ctx context.Context) ([]RemoteTemplate, error) {
	items, err := c.client.ListTemplates(ctx)
	if err != nil {
		return nil, normalizeE2BError("ListTemplates", err)
	}
	result := make([]RemoteTemplate, 0, len(items))
	for _, item := range items {
		name := item.TemplateID
		if len(item.Names) > 0 && strings.TrimSpace(item.Names[0]) != "" {
			name = strings.TrimSpace(item.Names[0])
		} else if len(item.Aliases) > 0 && strings.TrimSpace(item.Aliases[0]) != "" {
			name = strings.TrimSpace(item.Aliases[0])
		}
		standard := isStandardTemplate(name)
		for _, candidate := range append(append([]string(nil), item.Aliases...), item.Names...) {
			if isStandardTemplate(candidate) {
				standard = true
				break
			}
		}
		status := normalizeE2BTemplateBuildStatus(item.BuildStatus)
		// E2B's template list keeps reporting a pending build status after the
		// template already became spawnable, so reconcile our standard template
		// against the build endpoints before telling the UI to keep waiting
		// forever. Limit these extra requests to the standard template; polling
		// every public template would turn one catalog refresh into an
		// unbounded N+1 request pattern.
		if standard && isE2BTemplateBuildPending(status) {
			if reconciled := c.reconcileE2BTemplateStatus(ctx, item); reconciled != "" {
				status = reconciled
			}
		}
		result = append(result, RemoteTemplate{
			ID:        item.TemplateID,
			Name:      name,
			Status:    status,
			Version:   item.EnvdVersion,
			CreatedAt: item.CreatedAt,
			UpdatedAt: item.UpdatedAt,
			Standard:  standard,
		})
	}
	return result, nil
}

// reconcileE2BTemplateStatus resolves the real build state of a template whose
// list entry still looks pending, by asking the per-build endpoint that owns
// that answer.
//
// Only the template's *current* build counts as evidence. E2B spawns from that
// build alone, so neither an older successful build nor a non-zero spawn count
// proves the template can boot now — both happily coexist with a template that
// answers every sandbox creation with a 404. It returns an empty string when
// nothing more authoritative than the list status could be established.
func (c *E2BRemoteClient) reconcileE2BTemplateStatus(
	ctx context.Context,
	item e2b.TemplateDetail,
) string {
	templateID := strings.TrimSpace(item.TemplateID)
	buildID := strings.TrimSpace(item.BuildID)
	if templateID == "" {
		return ""
	}
	// buildID is "the last successful build", so the all-zero UUID means the
	// provider sees none under the tag it resolves. When builds did finish, the
	// template is not building at all — its builds are untagged, and no amount
	// of waiting will change that.
	if isBlankE2BUUID(buildID) {
		if item.BuildCount > 0 && c.hasSuccessfulE2BBuild(ctx, templateID) {
			return TemplateStatusUntagged
		}
		return ""
	}
	build, err := c.client.GetBuildStatus(ctx, templateID, buildID)
	if err != nil {
		logger.Warnf(ctx,
			"e2b build status lookup failed for template %s build %s: %v",
			templateID, buildID, err,
		)
		return ""
	}
	if isE2BTemplateBuildPending(build.Status) {
		return ""
	}
	reconciled := normalizeE2BTemplateBuildStatus(build.Status)
	if reconciled == "" {
		return ""
	}
	logger.Debugf(ctx,
		"e2b template %s list status %q reconciled to %q from build %s",
		templateID, item.BuildStatus, reconciled, buildID,
	)
	return reconciled
}

// hasSuccessfulE2BBuild reports whether the template's build history contains a
// finished build. It separates "the first build is still running" from "builds
// succeeded but none is reachable under the default tag".
func (c *E2BRemoteClient) hasSuccessfulE2BBuild(ctx context.Context, templateID string) bool {
	result, err := c.client.GetTemplate(ctx, templateID, e2b.WithTemplateBuildsLimit(100))
	if err != nil {
		logger.Warnf(ctx, "e2b template build history lookup failed for %s: %v", templateID, err)
		return false
	}
	for _, build := range result.Template.Builds {
		if normalizeE2BTemplateBuildStatus(build.Status) == "ready" {
			return true
		}
	}
	return false
}

// isBlankE2BUUID reports whether id carries no build reference. E2B returns the
// all-zero UUID, not an empty string, for a template without a build.
func isBlankE2BUUID(id string) bool {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return true
	}
	for _, char := range trimmed {
		if char != '0' && char != '-' {
			return false
		}
	}
	return true
}

func normalizeE2BTemplateBuildStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ready", "success", "succeeded", "complete", "completed":
		return "ready"
	case "building", "processing":
		return "building"
	case "waiting", "queued", "pending", "uploaded":
		return "waiting"
	case "error", "failed", "failure", "cancelled", "canceled":
		return "error"
	default:
		return strings.ToLower(strings.TrimSpace(status))
	}
}

func isE2BTemplateBuildPending(status string) bool {
	switch normalizeE2BTemplateBuildStatus(status) {
	case "building", "waiting":
		return true
	default:
		return false
	}
}

// EnsureStandardTemplate returns the cluster's WeKnora template, building it
// when absent. A failed or untagged template is not returned as is: it can
// never spawn a sandbox, so it falls through to the build below. E2B resolves
// the build by name, so that is a rebuild of the same template rather than a
// second entry in the catalog.
func (c *E2BRemoteClient) EnsureStandardTemplate(ctx context.Context) (*RemoteTemplate, error) {
	items, err := c.ListTemplates(ctx)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if items[i].Standard && !IsTemplateBuildFailed(items[i].Status) {
			return &items[i], nil
		}
	}
	builder := e2b.NewTemplate().FromImage(DefaultDockerImage)
	build, err := builder.BuildInBackground(ctx, c.client, e2b.BuildConfig{
		Name: StandardTemplateName,
		// Creating a sandbox from a plain template name or ID resolves the
		// "default" tag, so a build tagged anything else is unreachable: E2B
		// answers every creation with "tag 'default' does not exist for
		// template ...". Naming the tag explicitly keeps that contract visible
		// instead of relying on the server-side default.
		Tags:     []string{DefaultE2BTemplateTag},
		CPUCount: 2,
		MemoryMB: 2048,
	})
	if err != nil {
		return nil, normalizeE2BError("EnsureStandardTemplate", err)
	}
	return &RemoteTemplate{
		ID:       build.TemplateID,
		Name:     StandardTemplateName,
		Status:   "building",
		Image:    DefaultDockerImage,
		Standard: true,
	}, nil
}

func (c *E2BRemoteClient) Create(
	ctx context.Context,
	request RemoteCreateRequest,
) (RemoteSandboxHandle, error) {
	template := strings.TrimSpace(request.TemplateID)
	if template == "" {
		template = c.templateID
	}
	if template == "" {
		return nil, e2bInvalidRequest("Create", "template ID is required", nil)
	}
	timeoutSeconds, err := e2bTimeoutSeconds(request.Timeout, c.timeout)
	if err != nil {
		return nil, e2bInvalidRequest("Create", err.Error(), err)
	}
	action := request.Timeout.Action
	if action == "" {
		action = RemoteOnTimeoutKill
	}
	if action != RemoteOnTimeoutKill && action != RemoteOnTimeoutPause {
		return nil, e2bInvalidRequest(
			"Create",
			fmt.Sprintf("unsupported timeout action %q", action),
			nil,
		)
	}
	if request.Timeout.AutoResume && action != RemoteOnTimeoutPause {
		return nil, e2bInvalidRequest(
			"Create",
			"auto resume requires pause on timeout",
			nil,
		)
	}

	// Translate the neutral RemoteNetworkPolicy to E2B's SDK types. When
	// the caller supplied no policy at all we fall back to the same
	// permissive defaults the Cube adapter uses (public egress + public
	// URL reachability) so upgrading WeKnora deployments do not silently
	// lose `curl` / `pip` access from inside the sandbox.
	policy := request.Network
	if policy.AllowInternetAccess == nil {
		defaultOn := true
		policy.AllowInternetAccess = &defaultOn
	}
	if policy.AllowPublicTraffic == nil {
		defaultOn := true
		policy.AllowPublicTraffic = &defaultOn
	}
	config := e2b.SandboxConfig{
		Template:            template,
		Timeout:             timeoutSeconds,
		EnvVars:             cloneMetadata(request.EnvVars),
		Metadata:            cloneMetadata(request.Metadata),
		Secure:              true,
		AllowInternetAccess: policy.AllowInternetAccess,
		Network: &e2b.NetworkConfig{
			AllowPublicTraffic: policy.AllowPublicTraffic,
			AllowOut:           append([]string(nil), policy.AllowOut...),
			DenyOut:            append([]string(nil), policy.DenyOut...),
		},
		AutoPause:    action == RemoteOnTimeoutPause,
		VolumeMounts: toE2BVolumeMounts(request.VolumeMounts),
	}
	if request.Timeout.AutoResume {
		autoPauseMemory := true
		config.AutoPauseMemory = &autoPauseMemory
		config.AutoResume = &e2b.AutoResumeConfig{Enabled: true}
	}
	sandbox, err := c.client.NewSandbox(ctx, config)
	if err != nil {
		return nil, normalizeE2BError("Create", err)
	}
	if sandbox == nil || strings.TrimSpace(sandbox.ID) == "" {
		return nil, NewRemoteError(
			SandboxTypeE2B, "Create", RemoteErrorKindInternal,
			"e2b returned an empty sandbox handle", nil,
		)
	}
	return &e2bRemoteHandle{
		sandbox:  sandbox,
		metadata: cloneMetadata(request.Metadata),
	}, nil
}

func (c *E2BRemoteClient) Connect(
	ctx context.Context,
	sandboxID string,
) (RemoteSandboxHandle, error) {
	if strings.TrimSpace(sandboxID) == "" {
		return nil, e2bInvalidRequest("Connect", "sandbox ID is required", nil)
	}
	timeoutSeconds, err := e2bTimeoutSeconds(
		RemoteTimeoutPolicy{Mode: RemoteTimeoutServerDefault},
		c.timeout,
	)
	if err != nil {
		return nil, e2bInvalidRequest("Connect", err.Error(), err)
	}
	sandbox, err := c.client.Connect(ctx, sandboxID, timeoutSeconds)
	if err != nil {
		return nil, normalizeE2BError("Connect", err)
	}
	if sandbox == nil || strings.TrimSpace(sandbox.ID) == "" ||
		sandbox.ID != sandboxID {
		return nil, NewRemoteError(
			SandboxTypeE2B, "Connect", RemoteErrorKindInternal,
			"e2b returned a mismatched sandbox handle", nil,
		)
	}
	return &e2bRemoteHandle{sandbox: sandbox}, nil
}

// Get returns a single sandbox summary by ID. E2B's control plane exposes no
// read-only "fetch sandbox by ID" endpoint, so Get reattaches to the sandbox
// via Connect and reads its full metadata through the per-sandbox info
// endpoint (GET /sandboxes/{id}). This mirrors the Cube adapter's
// Connect+GetInfo shape and yields a strongly-consistent, O(1) lookup with a
// clean NotFound, instead of scanning the whole account-wide sandbox list
// (which is O(N) and can return a stale NotFound for a just-created sandbox).
//
// Get's only caller is the lifecycle coordinator's connectBinding pre-check,
// which Connects — and therefore wakes — the sandbox immediately afterwards.
// Resuming a paused sandbox here is thus the intended behaviour, not a side
// effect to avoid.
func (c *E2BRemoteClient) Get(
	ctx context.Context,
	sandboxID string,
) (*RemoteSandboxSummary, error) {
	if strings.TrimSpace(sandboxID) == "" {
		return nil, e2bInvalidRequest("Get", "sandbox ID is required", nil)
	}
	timeoutSeconds, err := e2bTimeoutSeconds(
		RemoteTimeoutPolicy{Mode: RemoteTimeoutServerDefault},
		c.timeout,
	)
	if err != nil {
		return nil, e2bInvalidRequest("Get", err.Error(), err)
	}
	sandbox, err := c.client.Connect(ctx, sandboxID, timeoutSeconds)
	if err != nil {
		return nil, normalizeE2BError("Get", err)
	}
	if sandbox == nil || strings.TrimSpace(sandbox.ID) == "" ||
		sandbox.ID != sandboxID {
		return nil, NewRemoteError(
			SandboxTypeE2B, "Get", RemoteErrorKindInternal,
			"e2b returned a mismatched sandbox handle", nil,
		)
	}
	info, err := sandbox.InfoWithContext(ctx)
	if err != nil {
		return nil, normalizeE2BError("Get", err)
	}
	if info == nil {
		return nil, NewRemoteError(
			SandboxTypeE2B, "Get", RemoteErrorKindNotFound,
			"sandbox not found", nil,
		)
	}
	summary := e2bRemoteSummary(*info)
	return &summary, nil
}

func (c *E2BRemoteClient) List(
	ctx context.Context,
	filter RemoteListFilter,
) ([]RemoteSandboxSummary, error) {
	if len(filter.States) > 0 && len(e2bListStates(filter.States)) == 0 {
		return []RemoteSandboxSummary{}, nil
	}
	infos, err := c.listSandboxesV2(ctx, "List", filter)
	if err != nil {
		return nil, err
	}
	result := make([]RemoteSandboxSummary, 0, len(infos))
	for _, info := range infos {
		converted := e2bRemoteSummary(info)
		if !metadataMatches(converted.Metadata, filter.Metadata) ||
			!StateMatches(converted.State, filter.States) {
			continue
		}
		result = append(result, converted)
	}
	return result, nil
}

func (c *E2BRemoteClient) listSandboxesV2(
	ctx context.Context,
	op string,
	filter RemoteListFilter,
) ([]e2b.SandboxInfo, error) {
	baseOptions := []e2b.ListSandboxesV2Option{e2b.WithSandboxLimit(100)}
	if serverMetadata := e2bServerMetadataFilter(filter.Metadata); len(serverMetadata) > 0 {
		baseOptions = append(baseOptions, e2b.WithSandboxMetadata(serverMetadata))
	}
	if states := e2bListStates(filter.States); len(states) > 0 {
		baseOptions = append(baseOptions, e2b.WithSandboxState(states...))
	}

	var sandboxes []e2b.SandboxInfo
	nextToken := ""
	seenTokens := make(map[string]struct{})
	for {
		options := append([]e2b.ListSandboxesV2Option(nil), baseOptions...)
		if nextToken != "" {
			options = append(options, e2b.WithSandboxNextToken(nextToken))
		}
		page, err := c.client.ListSandboxesV2(ctx, options...)
		if err != nil {
			return nil, normalizeE2BError(op, err)
		}
		if page == nil {
			return nil, NewRemoteError(
				SandboxTypeE2B,
				op,
				RemoteErrorKindInternal,
				"e2b returned an empty sandbox list page",
				nil,
			)
		}
		sandboxes = append(sandboxes, page.Sandboxes...)
		if page.NextToken == "" {
			return sandboxes, nil
		}
		if !e2bQuerySafe(page.NextToken) {
			return nil, NewRemoteError(
				SandboxTypeE2B,
				op,
				RemoteErrorKindInternal,
				"e2b returned an unsafe sandbox list pagination token",
				nil,
			)
		}
		if _, repeated := seenTokens[page.NextToken]; repeated {
			return nil, NewRemoteError(
				SandboxTypeE2B,
				op,
				RemoteErrorKindInternal,
				"e2b returned a repeated sandbox list pagination token",
				nil,
			)
		}
		seenTokens[page.NextToken] = struct{}{}
		nextToken = page.NextToken
	}
}

// E2B's V2 endpoint accepts one metadata query parameter even though the SDK
// option accepts a map. Send the most selective stable ownership key available;
// List still applies the complete metadata filter locally before returning.
func e2bServerMetadataFilter(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	for _, key := range []string{
		remoteMetadataSessionID,
		remoteMetadataTenantID,
		remoteMetadataProvider,
		remoteMetadataBindingVersion,
		remoteMetadataConfigID,
	} {
		if value, ok := metadata[key]; ok {
			return e2bSafeMetadataFilter(key, value)
		}
	}
	keys := make([]string, 0, len(metadata))
	for key := range metadata {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	key := keys[0]
	return e2bSafeMetadataFilter(key, metadata[key])
}

func e2bSafeMetadataFilter(key, value string) map[string]string {
	if !e2bQuerySafe(key) || !e2bQuerySafe(value) {
		return nil
	}
	return map[string]string{key: value}
}

func e2bQuerySafe(value string) bool {
	return url.QueryEscape(value) == value
}

func e2bListStates(states []RemoteSandboxState) []string {
	result := make([]string, 0, len(states))
	for _, state := range states {
		switch state {
		case RemoteStateRunning:
			result = append(result, "running")
		case RemoteStatePaused:
			result = append(result, "paused")
		}
	}
	return result
}

func (c *E2BRemoteClient) Delete(ctx context.Context, sandboxID string) error {
	if strings.TrimSpace(sandboxID) == "" {
		return e2bInvalidRequest("Delete", "sandbox ID is required", nil)
	}
	timeoutSeconds, err := e2bTimeoutSeconds(
		RemoteTimeoutPolicy{Mode: RemoteTimeoutServerDefault},
		c.timeout,
	)
	if err != nil {
		return e2bInvalidRequest("Delete", err.Error(), err)
	}
	sandbox, err := c.client.Connect(ctx, sandboxID, timeoutSeconds)
	if err != nil {
		return normalizeE2BError("Delete", err)
	}
	if sandbox == nil || strings.TrimSpace(sandbox.ID) == "" ||
		sandbox.ID != sandboxID {
		return NewRemoteError(
			SandboxTypeE2B, "Delete", RemoteErrorKindInternal,
			"e2b returned a mismatched sandbox handle", nil,
		)
	}
	if err := sandbox.CloseWithContext(ctx); err != nil {
		return normalizeE2BError("Delete", err)
	}
	return nil
}

func (c *E2BRemoteClient) Exec(
	ctx context.Context,
	handle RemoteSandboxHandle,
	request RemoteExecRequest,
) (*RemoteExecResult, error) {
	sandbox, err := e2bHandleSandbox("Exec", handle)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(request.Command) == "" {
		return nil, e2bInvalidRequest("Exec", "command is required", nil)
	}
	if request.Shell && len(request.Args) != 0 {
		return nil, e2bInvalidRequest(
			"Exec", "shell execution cannot include argv arguments", nil,
		)
	}
	if request.Timeout < 0 {
		return nil, e2bInvalidRequest("Exec", "execution timeout cannot be negative", nil)
	}

	// The SDK runs every command through `/bin/bash -l -c <cmd>`, so argv mode
	// is lowered to a quoted shell line and stdin (when present) is piped in
	// via a heredoc. This mirrors the Cube adapter exactly, keeping argv+stdin
	// working identically across both remote backends.
	cmd := request.Command
	if !request.Shell {
		cmd = buildShellLine(request.Command, request.Args)
	}
	if request.Stdin != "" {
		cmd = wrapWithStdin(cmd, request.Stdin)
	}

	execCtx := ctx
	cancel := context.CancelFunc(func() {})
	if request.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, request.Timeout)
	}
	defer cancel()

	start := time.Now()
	options := []e2b.RunOption{}
	if request.WorkDir != "" {
		options = append(options, e2b.WithCwd(request.WorkDir))
	}
	if len(request.Env) > 0 {
		options = append(options, e2b.WithEnv(cloneMetadata(request.Env)))
	}
	if request.User != "" {
		options = append(options, e2b.WithUser(request.User))
	}
	if request.Timeout > 0 {
		options = append(options, e2b.WithTimeout(request.Timeout))
	}
	result, err := sandbox.Commands.Run(execCtx, cmd, options...)
	duration := time.Since(start)

	if err != nil {
		// A timeout is a normal RemoteExecResult with Killed=true, never a
		// hard error. Detection cannot rely on execCtx.Err() alone: the SDK
		// derives its own timeout context (from request.Timeout, or from its
		// DefaultCommandTimeout when we pass none), and when *that* timer
		// fires it cancels the SDK's child context rather than our execCtx.
		// The SDK then surfaces every deadline as *e2b.TimeoutError, so we
		// key off that plus our own deadline to cover whichever timer wins.
		if isE2BExecTimeout(execCtx, err) {
			return &RemoteExecResult{
				Duration: duration,
				Killed:   true,
				ExitCode: -1,
			}, nil
		}
		// A non-zero exit code is reported by the SDK as *CommandExitError
		// with the captured output still attached. This is a normal
		// execution signal (a failing script, `grep` with no match,
		// `exit 1`), not a wire failure, so lower it to a RemoteExecResult
		// exactly like the Cube adapter — whose SDK returns the exit code
		// inline. Only genuine transport/RPC errors fall through to
		// normalizeE2BError below.
		var exitErr *e2b.CommandExitError
		if errors.As(err, &exitErr) {
			return &RemoteExecResult{
				Stdout:   exitErr.Stdout,
				Stderr:   exitErr.Stderr,
				ExitCode: exitErr.ExitCode,
				Duration: duration,
			}, nil
		}
		return nil, normalizeE2BError("Exec", err)
	}
	if result == nil {
		return nil, NewRemoteError(
			SandboxTypeE2B, "Exec", RemoteErrorKindInternal,
			"e2b returned an empty command result", nil,
		)
	}
	return &RemoteExecResult{
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
		Duration: duration,
	}, nil
}

// Filesystem operations name DefaultSandboxExecUser explicitly rather than
// relying on the daemon's default account. It keeps ownership aligned with the
// account scripts run as, and it is required for interoperability: E2B Cloud
// falls back to "user" when the request omits it, while other E2B-compatible
// control planes reject the call outright.
func (c *E2BRemoteClient) WriteFile(
	ctx context.Context,
	handle RemoteSandboxHandle,
	path string,
	content []byte,
) error {
	sandbox, err := e2bHandleSandbox("WriteFile", handle)
	if err != nil {
		return err
	}
	if strings.TrimSpace(path) == "" {
		return e2bInvalidRequest("WriteFile", "path is required", nil)
	}
	if _, err := sandbox.Filesystem.WriteBytes(ctx, path, content, e2b.WithFileUser(DefaultSandboxExecUser)); err != nil {
		return normalizeE2BError("WriteFile", err)
	}
	return nil
}

func (c *E2BRemoteClient) ReadFile(
	ctx context.Context,
	handle RemoteSandboxHandle,
	path string,
) ([]byte, error) {
	sandbox, err := e2bHandleSandbox("ReadFile", handle)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(path) == "" {
		return nil, e2bInvalidRequest("ReadFile", "path is required", nil)
	}
	content, err := sandbox.Filesystem.ReadBytes(ctx, path, e2b.WithFileUser(DefaultSandboxExecUser))
	if err != nil {
		return nil, normalizeE2BError("ReadFile", err)
	}
	return content, nil
}

func (c *E2BRemoteClient) ListDir(
	ctx context.Context,
	handle RemoteSandboxHandle,
	path string,
) ([]RemoteDirEntry, error) {
	sandbox, err := e2bHandleSandbox("ListDir", handle)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(path) == "" {
		return nil, e2bInvalidRequest("ListDir", "path is required", nil)
	}
	entries, err := sandbox.Filesystem.List(ctx, path, e2b.WithFileUser(DefaultSandboxExecUser))
	if err != nil {
		return nil, normalizeE2BError("ListDir", err)
	}
	result := make([]RemoteDirEntry, 0, len(entries))
	for _, e := range entries {
		result = append(result, RemoteDirEntry{
			Name:    e.Name,
			Path:    e.Path,
			Type:    e2bRemoteEntryType(e.Type),
			Size:    e.Size,
			ModTime: e.ModTime,
		})
	}
	return result, nil
}

func (c *E2BRemoteClient) MakeDir(
	ctx context.Context,
	handle RemoteSandboxHandle,
	path string,
) error {
	sandbox, err := e2bHandleSandbox("MakeDir", handle)
	if err != nil {
		return err
	}
	if strings.TrimSpace(path) == "" {
		return e2bInvalidRequest("MakeDir", "path is required", nil)
	}
	if err := sandbox.Filesystem.MakeDir(ctx, path, e2b.WithFileUser(DefaultSandboxExecUser)); err != nil {
		return normalizeE2BError("MakeDir", err)
	}
	return nil
}

func (c *E2BRemoteClient) Remove(
	ctx context.Context,
	handle RemoteSandboxHandle,
	path string,
) error {
	sandbox, err := e2bHandleSandbox("Remove", handle)
	if err != nil {
		return err
	}
	if strings.TrimSpace(path) == "" {
		return e2bInvalidRequest("Remove", "path is required", nil)
	}
	if err := sandbox.Filesystem.Remove(ctx, path, e2b.WithFileUser(DefaultSandboxExecUser)); err != nil {
		return normalizeE2BError("Remove", err)
	}
	return nil
}

func (c *E2BRemoteClient) Stat(
	ctx context.Context,
	handle RemoteSandboxHandle,
	path string,
) (*RemoteStatEntry, error) {
	sandbox, err := e2bHandleSandbox("Stat", handle)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(path) == "" {
		return nil, e2bInvalidRequest("Stat", "path is required", nil)
	}
	info, err := sandbox.Filesystem.Stat(ctx, path, e2b.WithFileUser(DefaultSandboxExecUser))
	if err != nil {
		return nil, normalizeE2BError("Stat", err)
	}
	if info == nil {
		return nil, NewRemoteError(
			SandboxTypeE2B, "Stat", RemoteErrorKindNotFound,
			"path not found", nil,
		)
	}
	return &RemoteStatEntry{
		Path:    info.Path,
		Type:    e2bRemoteEntryType(info.Type),
		Size:    info.Size,
		ModTime: info.ModTime,
	}, nil
}

// CreateSnapshot snapshots a running sandbox. E2B exposes snapshots on the
// connected sandbox handle, so the adapter reattaches first like Delete does.
func (c *E2BRemoteClient) CreateSnapshot(
	ctx context.Context, sandboxID string, name string,
) (RemoteSnapshotRef, error) {
	if strings.TrimSpace(sandboxID) == "" {
		return RemoteSnapshotRef{}, e2bInvalidRequest("CreateSnapshot", "sandbox ID is required", nil)
	}
	timeoutSeconds, err := e2bTimeoutSeconds(
		RemoteTimeoutPolicy{Mode: RemoteTimeoutServerDefault},
		c.timeout,
	)
	if err != nil {
		return RemoteSnapshotRef{}, e2bInvalidRequest("CreateSnapshot", err.Error(), err)
	}
	sandbox, err := c.client.Connect(ctx, sandboxID, timeoutSeconds)
	if err != nil {
		return RemoteSnapshotRef{}, normalizeE2BError("CreateSnapshot", err)
	}
	if sandbox == nil || strings.TrimSpace(sandbox.ID) == "" ||
		sandbox.ID != sandboxID {
		return RemoteSnapshotRef{}, NewRemoteError(
			SandboxTypeE2B, "CreateSnapshot", RemoteErrorKindInternal,
			"e2b returned a mismatched sandbox handle", nil,
		)
	}

	var info *e2b.SnapshotInfo
	if trimmed := strings.TrimSpace(name); trimmed != "" {
		info, err = sandbox.CreateSnapshot(ctx, trimmed)
	} else {
		info, err = sandbox.CreateSnapshot(ctx)
	}
	if err != nil {
		return RemoteSnapshotRef{}, normalizeE2BError("CreateSnapshot", err)
	}
	if info == nil || strings.TrimSpace(info.SnapshotID) == "" {
		return RemoteSnapshotRef{}, e2bInvalidRequest(
			"CreateSnapshot", "provider returned an empty snapshot ID", nil)
	}
	return RemoteSnapshotRef{ID: info.SnapshotID, Names: info.Names}, nil
}

// DeleteSnapshot removes a snapshot. go-e2b reports a missing snapshot as
// (false, nil), so cleanup stays idempotent while genuine provider errors
// still surface through the neutral error taxonomy.
func (c *E2BRemoteClient) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	if strings.TrimSpace(snapshotID) == "" {
		return e2bInvalidRequest("DeleteSnapshot", "snapshot ID is required", nil)
	}
	if _, err := c.client.DeleteSnapshot(ctx, snapshotID); err != nil {
		normalized := normalizeE2BError("DeleteSnapshot", err)
		if IsRemoteNotFound(normalized) {
			return nil
		}
		return normalized
	}
	return nil
}

// ListSnapshots pages through every snapshot, optionally filtered by source
// sandbox. Used only by the skill-image orphan-reconciliation task.
func (c *E2BRemoteClient) ListSnapshots(
	ctx context.Context, sandboxID string,
) ([]RemoteSnapshotRef, error) {
	baseOptions := []e2b.ListSnapshotsOption{e2b.WithSnapshotLimit(100)}
	if trimmed := strings.TrimSpace(sandboxID); trimmed != "" {
		baseOptions = append(baseOptions, e2b.WithSnapshotSandboxID(trimmed))
	}

	var (
		out   []RemoteSnapshotRef
		token string
	)
	for {
		options := append([]e2b.ListSnapshotsOption(nil), baseOptions...)
		if token != "" {
			options = append(options, e2b.WithSnapshotNextToken(token))
		}
		page, err := c.client.ListSnapshots(ctx, options...)
		if err != nil {
			return nil, normalizeE2BError("ListSnapshots", err)
		}
		if page == nil {
			return nil, NewRemoteError(
				SandboxTypeE2B,
				"ListSnapshots",
				RemoteErrorKindInternal,
				"e2b returned an empty snapshot list page",
				nil,
			)
		}
		for _, item := range page.Snapshots {
			out = append(out, RemoteSnapshotRef{ID: item.SnapshotID, Names: item.Names})
		}
		if page.NextToken == "" {
			return out, nil
		}
		// A provider/SDK bug that echoes the same non-empty pagination token
		// would otherwise spin forever and block skill-image orphan cleanup.
		// Fail fast instead of hanging until the context is cancelled.
		if page.NextToken == token {
			return nil, e2bInvalidRequest("ListSnapshots",
				"provider returned the same pagination token", nil)
		}
		token = page.NextToken
	}
}

// --- helpers -----------------------------------------------------------------

// e2bTimeoutSeconds maps the neutral RemoteTimeoutPolicy onto E2B's integer
// timeout. E2B only supports a positive TTL; "never" is rejected as
// unsupported so the lifecycle coordinator can pick a different policy.
func e2bTimeoutSeconds(policy RemoteTimeoutPolicy, fallback time.Duration) (int, error) {
	toSeconds := func(value time.Duration) (int, error) {
		if value > 0 && value < time.Second {
			return 0, errors.New("E2B timeout must be at least one second")
		}
		seconds := value / time.Second
		if value%time.Second != 0 {
			seconds++
		}
		return int(seconds), nil
	}

	switch policy.Mode {
	case "", RemoteTimeoutServerDefault:
		if fallback <= 0 {
			return 0, nil
		}
		return toSeconds(fallback)
	case RemoteTimeoutExplicit:
		if policy.Value < 0 {
			return 0, errors.New("E2B backend does not support NeverTimeout")
		}
		return toSeconds(policy.Value)
	default:
		return 0, fmt.Errorf("unsupported timeout mode %q", policy.Mode)
	}
}

// toE2BVolumeMounts converts the provider-neutral RemoteVolumeMount slice to
// E2B SDK VolumeMount values. Nil and empty inputs both produce nil.
func toE2BVolumeMounts(src []RemoteVolumeMount) []e2b.VolumeMount {
	if len(src) == 0 {
		return nil
	}
	result := make([]e2b.VolumeMount, len(src))
	for i, mount := range src {
		result[i] = e2b.VolumeMount{
			Name: mount.Name,
			Path: mount.Path,
		}
	}
	return result
}

// isE2BExecTimeout reports whether a failed command Run should be treated as
// a timeout kill (Killed=true) rather than a transport error. It recognises:
//   - our own execCtx deadline (request.Timeout);
//   - a raw context deadline returned before the stream was established;
//   - *e2b.TimeoutError, which the SDK returns for both its internal command
//     timeout and any Connect deadline surfaced from the envd stream.
func isE2BExecTimeout(execCtx context.Context, err error) bool {
	if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
		return true
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var timeoutErr *e2b.TimeoutError
	return errors.As(err, &timeoutErr)
}

func e2bHandleSandbox(op string, handle RemoteSandboxHandle) (*e2b.Sandbox, error) {
	h, ok := handle.(*e2bRemoteHandle)
	if !ok || h == nil || h.sandbox == nil {
		return nil, e2bInvalidRequest(op, "handle was not issued by E2B", nil)
	}
	return h.sandbox, nil
}

func e2bRemoteSummary(info e2b.SandboxInfo) RemoteSandboxSummary {
	startedAt, _ := time.Parse(time.RFC3339, info.StartedAt)
	endAt, _ := time.Parse(time.RFC3339, info.EndAt)
	return RemoteSandboxSummary{
		ID:         info.ID,
		TemplateID: info.Template,
		State:      normalizeE2BState(info.State),
		RawState:   info.State,
		Metadata:   cloneMetadata(info.Metadata),
		StartedAt:  startedAt,
		EndAt:      endAt,
	}
}

func normalizeE2BState(state string) RemoteSandboxState {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "running":
		return RemoteStateRunning
	case "paused":
		return RemoteStatePaused
	case "pending", "creating", "provisioning", "starting", "pausing", "resuming":
		return RemoteStateTransitioning
	case "killed", "stopped", "terminated", "deleted", "failed", "error":
		return RemoteStateTerminal
	default:
		return RemoteStateUnknown
	}
}

func e2bInvalidRequest(op, message string, cause error) error {
	return NewRemoteError(
		SandboxTypeE2B, op, RemoteErrorKindInvalidRequest, message, cause,
	)
}

func normalizeE2BError(op string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return fmt.Errorf("e2b %s: %w", op, err)
	}
	kind := RemoteErrorKindInternal
	status := 0
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		kind = RemoteErrorKindTimeout
	default:
		var sandboxNotFound *e2b.SandboxNotFoundError
		var fileNotFound *e2b.FileNotFoundError
		var templateNotFound *e2b.TemplateNotFoundError
		var invalidArg *e2b.InvalidArgumentError
		var authErr *e2b.AuthenticationError
		var apiErr *e2b.Error
		var timeoutErr *e2b.TimeoutError
		var connectErr *connect.Error
		var networkErr net.Error
		switch {
		case errors.As(err, &sandboxNotFound):
			kind = RemoteErrorKindNotFound
		case errors.As(err, &fileNotFound):
			kind = RemoteErrorKindNotFound
		case errors.As(err, &templateNotFound):
			kind = RemoteErrorKindInvalidRequest
		// The envd process/filesystem RPC layer converts Connect codes into
		// these typed errors (see the SDK's mapProcessRPCError), so they
		// never reach the *connect.Error branch below. Match them explicitly
		// or they would fall through to the default RemoteErrorKindInternal.
		case errors.As(err, &invalidArg):
			kind = RemoteErrorKindInvalidRequest
		case errors.As(err, &authErr):
			kind = RemoteErrorKindAuthentication
		case errors.As(err, &timeoutErr):
			kind = RemoteErrorKindTimeout
		case errors.As(err, &apiErr):
			kind = httpErrorKind(op, apiErr.StatusCode)
			status = apiErr.StatusCode
		case errors.As(err, &connectErr):
			switch connectErr.Code() {
			case connect.CodeCanceled, connect.CodeDeadlineExceeded:
				kind = RemoteErrorKindTimeout
			case connect.CodeUnavailable:
				kind = RemoteErrorKindUnavailable
			case connect.CodeUnauthenticated, connect.CodePermissionDenied:
				kind = RemoteErrorKindAuthentication
			case connect.CodeResourceExhausted:
				kind = RemoteErrorKindCapacity
			case connect.CodeInvalidArgument, connect.CodeOutOfRange:
				kind = RemoteErrorKindInvalidRequest
			case connect.CodeNotFound:
				kind = RemoteErrorKindNotFound
			case connect.CodeAlreadyExists, connect.CodeAborted, connect.CodeFailedPrecondition:
				kind = RemoteErrorKindConflict
			case connect.CodeUnimplemented:
				kind = RemoteErrorKindUnsupported
			case connect.CodeInternal, connect.CodeDataLoss, connect.CodeUnknown:
				kind = RemoteErrorKindInternal
			}
		case errors.As(err, &networkErr):
			if networkErr.Timeout() {
				kind = RemoteErrorKindTimeout
			} else {
				kind = RemoteErrorKindUnavailable
			}
		}
	}
	remoteErr := NewRemoteError(SandboxTypeE2B, op, kind, err.Error(), err)
	remoteErr.StatusCode = status
	return remoteErr
}

// e2bRemoteEntryType maps the E2B SDK FileInfo.Type onto the provider-neutral
// RemoteDirEntryType.
func e2bRemoteEntryType(fileType string) RemoteDirEntryType {
	switch strings.ToLower(strings.TrimSpace(fileType)) {
	case "file":
		return RemoteEntryFile
	case "directory", "dir":
		return RemoteEntryDir
	default:
		return RemoteEntryOther
	}
}

var (
	_ RemoteSandboxClient   = (*E2BRemoteClient)(nil)
	_ RemoteSnapshotManager = (*E2BRemoteClient)(nil)
	_ RemoteSandboxHandle   = (*e2bRemoteHandle)(nil)
)
