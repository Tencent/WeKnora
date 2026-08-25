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
	pool *SandboxGatewayTransportPool,
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
		// Cube stores snapshots as templates, so a snapshot ID can be handed
		// straight back as CreateOptions.TemplateID.
		SupportsSnapshots: true,
		// CreateOptions still has no volume-mount field; skills ride on
		// snapshots instead, so this stays false.
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

func (c *CubeRemoteClient) ListTemplates(ctx context.Context) ([]RemoteTemplate, error) {
	items, err := c.client.ListTemplates(ctx)
	if err != nil {
		return nil, normalizeCubeError("ListTemplates", err)
	}
	// Cube stores snapshots in the same template store that GET /templates
	// returns, so skill-image snapshots would otherwise show up in the
	// settings "pick a base template" step. Subtract them; E2B's catalog
	// already keeps the two lists apart.
	snapshotIDs := c.cubeSnapshotIDsForCatalog(ctx)
	result := make([]RemoteTemplate, 0, len(items))
	for _, item := range items {
		if cubeTemplateIsSnapshot(item.TemplateID, snapshotIDs) {
			continue
		}
		name := strings.TrimSpace(item.Name)
		// Cube only reports a name when the template carries an alias, so fall
		// back to the image before falling back to the opaque ID: recognising
		// our own template is what keeps EnsureStandardTemplate idempotent.
		standard := isStandardTemplate(name) || isStandardTemplateImage(item.ImageInfo)
		if name == "" {
			if standard {
				name = StandardTemplateName
			} else {
				name = item.TemplateID
			}
		}
		result = append(result, RemoteTemplate{
			ID:        item.TemplateID,
			Name:      name,
			Status:    item.Status,
			Version:   item.Version,
			Image:     item.ImageInfo,
			CreatedAt: item.CreatedAt,
			Standard:  standard,
			Error:     strings.TrimSpace(item.LastError),
		})
	}
	return result, nil
}

// cubeSnapshotIDsForCatalog lists snapshot IDs so ListTemplates can hide them.
// A listing failure must not fail the catalog: the settings UI still needs
// real templates, and cubeTemplateIsSnapshot still drops the snap- prefix.
func (c *CubeRemoteClient) cubeSnapshotIDsForCatalog(ctx context.Context) map[string]struct{} {
	refs, err := c.ListSnapshots(ctx, "")
	if err != nil {
		logger.Warnf(ctx, "cube ListTemplates: listing snapshots to exclude them failed: %v", err)
		return nil
	}
	out := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		if id := strings.TrimSpace(ref.ID); id != "" {
			out[id] = struct{}{}
		}
	}
	return out
}

func cubeTemplateIsSnapshot(templateID string, snapshotIDs map[string]struct{}) bool {
	id := strings.TrimSpace(templateID)
	if id == "" {
		return false
	}
	if strings.HasPrefix(strings.ToLower(id), "snap-") {
		return true
	}
	_, ok := snapshotIDs[id]
	return ok
}

// EnsureStandardTemplate makes the cluster hold exactly one WeKnora template.
// A healthy or still-building one is returned as is; a failed one is rebuilt in
// place, because building a second template would leave the failed one behind
// and repeat on every refresh.
func (c *CubeRemoteClient) EnsureStandardTemplate(ctx context.Context) (*RemoteTemplate, error) {
	items, err := c.ListTemplates(ctx)
	if err != nil {
		return nil, err
	}
	var failed *RemoteTemplate
	for i := range items {
		if !items[i].Standard {
			continue
		}
		if !IsTemplateBuildFailed(items[i].Status) {
			return &items[i], nil
		}
		if failed == nil {
			failed = &items[i]
		}
	}
	if failed != nil {
		return c.rebuildStandardTemplate(ctx, *failed)
	}
	job, err := c.client.BuildTemplate(ctx, cubesandbox.BuildTemplateOptions{
		Image: DefaultCubeTemplateImage,
		Extra: cubeStandardTemplateSpec(),
	})
	if err != nil {
		return nil, normalizeCubeError("EnsureStandardTemplate", err)
	}
	return &RemoteTemplate{
		ID:       job.TemplateID,
		Name:     StandardTemplateName,
		Status:   job.Status,
		Image:    DefaultCubeTemplateImage,
		Standard: true,
		Error:    strings.TrimSpace(job.ErrorMessage),
	}, nil
}

// rebuildStandardTemplate restarts the build of a template that already exists,
// keeping its ID so a retry never adds to the catalog.
func (c *CubeRemoteClient) rebuildStandardTemplate(
	ctx context.Context,
	current RemoteTemplate,
) (*RemoteTemplate, error) {
	logger.Infof(ctx, "cube standard template %s failed (%s), rebuilding in place",
		current.ID, current.Status)
	job, err := c.client.RebuildTemplate(ctx, current.ID, cubeStandardTemplateSpec())
	if err != nil {
		return nil, normalizeCubeError("EnsureStandardTemplate", err)
	}
	rebuilt := current
	rebuilt.Status = job.Status
	rebuilt.Error = strings.TrimSpace(job.ErrorMessage)
	if strings.TrimSpace(job.TemplateID) != "" {
		rebuilt.ID = job.TemplateID
	}
	return &rebuilt, nil
}

// cubeStandardTemplateSpec is the single definition of how the WeKnora template
// is built. Both the first build and every rebuild send it verbatim — the
// rebuild endpoint takes a raw payload rather than BuildTemplateOptions, and
// two hand-kept copies of the spec would eventually disagree.
func cubeStandardTemplateSpec() map[string]any {
	return map[string]any{
		"image":             DefaultCubeTemplateImage,
		"name":              StandardTemplateName,
		"writableLayerSize": "1G",
		"exposedPorts":      []uint16{CubeEnvdPort},
		// Cube defaults to probing envd, but naming the probe keeps the reason
		// this image must ship envd visible at the call site.
		"probePort": uint16(CubeEnvdPort),
		"probePath": CubeEnvdHealthPath,
	}
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
	logCubeSandboxCreated(ctx, c, sb, network.AllowPublicTraffic)
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
			!StateMatches(converted.State, filter.States) {
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

	logCubeDataPlaneExec(ctx, c, sb, request.User, line)

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
		normalized := normalizeCubeError("Exec", execErr)
		logger.Warnf(ctx,
			"[CubeRemote] data-plane exec failed sandbox=%s detail=%s",
			sb.SandboxID, RemoteErrorDiagnostics(normalized),
		)
		return nil, normalized
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
		return ignoreExistingDir(normalizeCubeError("MakeDir", err))
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

// CreateSnapshot snapshots a running sandbox. Cube exposes this on *Sandbox,
// so we connect by ID first (same shape as Delete).
func (c *CubeRemoteClient) CreateSnapshot(
	ctx context.Context, sandboxID string, name string,
) (RemoteSnapshotRef, error) {
	if strings.TrimSpace(sandboxID) == "" {
		return RemoteSnapshotRef{}, cubeInvalidRequest("CreateSnapshot", "sandbox ID is required", nil)
	}
	sb, err := c.client.Connect(ctx, sandboxID)
	if err != nil {
		return RemoteSnapshotRef{}, normalizeCubeError("CreateSnapshot", err)
	}
	info, err := sb.CreateSnapshot(ctx, strings.TrimSpace(name))
	if err != nil {
		return RemoteSnapshotRef{}, normalizeCubeError("CreateSnapshot", err)
	}
	if info == nil || strings.TrimSpace(info.SnapshotID) == "" {
		return RemoteSnapshotRef{}, cubeInvalidRequest(
			"CreateSnapshot", "provider returned an empty snapshot ID", nil)
	}
	return RemoteSnapshotRef{ID: info.SnapshotID, Names: info.Names}, nil
}

// DeleteSnapshot removes a snapshot. Cube returns an API error for a missing
// template; we map not-found to success so cleanup is idempotent.
func (c *CubeRemoteClient) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	if strings.TrimSpace(snapshotID) == "" {
		return cubeInvalidRequest("DeleteSnapshot", "snapshot ID is required", nil)
	}
	if err := c.client.DeleteSnapshot(ctx, snapshotID); err != nil {
		normalized := normalizeCubeError("DeleteSnapshot", err)
		if IsRemoteNotFound(normalized) {
			return nil
		}
		return normalized
	}
	return nil
}

// ListSnapshots pages through every snapshot, optionally filtered by source
// sandbox. Used only by the orphan-reconciliation task.
func (c *CubeRemoteClient) ListSnapshots(
	ctx context.Context, sandboxID string,
) ([]RemoteSnapshotRef, error) {
	var (
		out   []RemoteSnapshotRef
		token string
		seen  = map[string]struct{}{"": {}}
	)
	for {
		page, next, err := c.client.ListSnapshots(ctx, cubesandbox.ListSnapshotsOptions{
			SandboxID: strings.TrimSpace(sandboxID),
			Limit:     100,
			NextToken: token,
		})
		if err != nil {
			return nil, normalizeCubeError("ListSnapshots", err)
		}
		for _, item := range page {
			out = append(out, RemoteSnapshotRef{ID: item.SnapshotID, Names: item.Names})
		}
		if next == "" {
			return out, nil
		}
		// A provider/SDK bug that repeats a pagination token (the same one,
		// or a cycle A→B→A) would otherwise spin forever and block skill-
		// image orphan cleanup. Fail fast instead of hanging until cancel.
		if _, dup := seen[next]; dup {
			return nil, cubeInvalidRequest("ListSnapshots",
				"provider returned a repeated pagination token", nil)
		}
		seen[next] = struct{}{}
		token = next
	}
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
	parts = append(parts, ShellQuote(cmd))
	for _, a := range args {
		parts = append(parts, ShellQuote(a))
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

func StateMatches(candidate RemoteSandboxState, allowed []RemoteSandboxState) bool {
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
	status := 0
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		kind = RemoteErrorKindTimeout
	case errors.Is(err, cubesandbox.ErrAuthentication):
		kind = RemoteErrorKindAuthentication
	case errors.Is(err, cubesandbox.ErrTemplateNotFound):
		if op == "DeleteSnapshot" {
			kind = RemoteErrorKindNotFound
		} else {
			kind = RemoteErrorKindInvalidRequest
		}
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
			status = apiErr.StatusCode
		case errors.As(err, &netErr) && netErr.Timeout():
			kind = RemoteErrorKindTimeout
		case errors.As(err, &netErr):
			kind = RemoteErrorKindUnavailable
		}
	}
	remoteErr := NewRemoteError(SandboxTypeCube, op, kind, err.Error(), err)
	remoteErr.StatusCode = status
	return remoteErr
}

// logCubeSandboxCreated records create-time data-plane fields operators need
// when exec fails with a generic auth error. Token values are never logged.
func logCubeSandboxCreated(
	ctx context.Context,
	client *CubeRemoteClient,
	sb *cubesandbox.Sandbox,
	allowPublicTraffic *bool,
) {
	if sb == nil || client == nil {
		return
	}
	publicTraffic := "server_default"
	if allowPublicTraffic != nil {
		publicTraffic = fmt.Sprintf("%t", *allowPublicTraffic)
	}
	logger.Infof(ctx,
		"[CubeRemote] sandbox created id=%s template=%s domain=%s envd_host=%s "+
			"api_url=%s proxy_url=%s api_key=%s envd_token=%s traffic_token=%s "+
			"allow_public_traffic=%s",
		sb.SandboxID,
		sb.TemplateID,
		cubeSandboxDomain(client, sb),
		sb.GetHost(CubeEnvdPort),
		client.config.CubeAPIURL,
		client.config.CubeProxyURL,
		cubeCredentialPresence(client.config.CubeAPIKey),
		cubeCredentialPresence(sb.EnvdAccessToken),
		cubeCredentialPresence(sb.TrafficAccessToken),
		publicTraffic,
	)
}

func logCubeDataPlaneExec(
	ctx context.Context,
	client *CubeRemoteClient,
	sb *cubesandbox.Sandbox,
	execUser, commandLine string,
) {
	if sb == nil || client == nil {
		return
	}
	logger.Infof(ctx,
		"[CubeRemote] data-plane exec sandbox=%s envd_host=%s exec_user=%s "+
			"api_key=%s envd_token=%s traffic_token=%s cmd=%q",
		sb.SandboxID,
		sb.GetHost(CubeEnvdPort),
		strings.TrimSpace(execUser),
		cubeCredentialPresence(client.config.CubeAPIKey),
		cubeCredentialPresence(sb.EnvdAccessToken),
		cubeCredentialPresence(sb.TrafficAccessToken),
		commandLine,
	)
}

func cubeSandboxDomain(client *CubeRemoteClient, sb *cubesandbox.Sandbox) string {
	if sb != nil && strings.TrimSpace(sb.Domain) != "" {
		return sb.Domain
	}
	if client != nil && client.sandboxDomain != "" {
		return client.sandboxDomain
	}
	return ""
}

func cubeCredentialPresence(value string) string {
	if strings.TrimSpace(value) == "" {
		return "absent"
	}
	return "present"
}

var (
	_ RemoteSandboxClient   = (*CubeRemoteClient)(nil)
	_ RemoteSnapshotManager = (*CubeRemoteClient)(nil)
	_ RemoteSandboxHandle   = (*cubeRemoteHandle)(nil)
)
