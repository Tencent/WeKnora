// Package sandbox: Cube adapter for the provider-neutral RemoteSandboxClient.
//
// CubeRemoteClient is the Cube backend behind the single sandbox abstraction:
// callers speak RemoteSandboxClient; this file translates each call into the
// Cube SDK / envd transport that already lives in cube_client.go. Cube SDK
// types, HTTP status codes and Cube-specific workarounds never leak past this
// file — every return value is either a neutral DTO or a RemoteError with a
// stable Kind.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	cubesandbox "github.com/tencentcloud/CubeSandbox/sdk/go"
)

// cubeCreateRequest carries the subset of RemoteCreateRequest that
// cubeClient.createRemoteSandbox needs. It lives here so the adapter can shape
// the SDK call while cubeClient stays free of RemoteSandboxClient types.
type cubeCreateRequest struct {
	TemplateID string
	Timeout    *time.Duration
	OnTimeout  RemoteTimeoutAction
	AutoResume bool
	Metadata   map[string]string
	EnvVars    map[string]string
}

// CubeRemoteClient implements RemoteSandboxClient on top of the Cube SDK and
// envd transport. It is the only Cube backend the manager and lifecycle
// coordinator see.
type CubeRemoteClient struct {
	transport *cubeClient
}

// NewCubeRemoteClient constructs a Cube-backed RemoteSandboxClient from the
// WeKnora sandbox Config. It does not probe the control plane; callers may
// invoke Health() as part of startup policy.
func NewCubeRemoteClient(config *Config) (*CubeRemoteClient, error) {
	if config == nil {
		return nil, errors.New("cube remote client config is required")
	}
	return &CubeRemoteClient{transport: newCubeClient(config)}, nil
}

// cubeRemoteHandle is the RemoteSandboxHandle Cube returns. It wraps a
// *SandboxInfo so subsequent envd calls can reuse the connected SDK sandbox.
type cubeRemoteHandle struct {
	info *SandboxInfo
}

func (h *cubeRemoteHandle) ID() string {
	if h == nil || h.info == nil {
		return ""
	}
	return h.info.ID
}

func (h *cubeRemoteHandle) Provider() RemoteProvider { return SandboxTypeCube }

func (h *cubeRemoteHandle) Metadata() map[string]string {
	if h == nil || h.info == nil {
		return nil
	}
	return cloneMetadata(h.info.Metadata)
}

// --- RemoteSandboxClient ------------------------------------------------------

func (c *CubeRemoteClient) Provider() RemoteProvider { return SandboxTypeCube }

func (c *CubeRemoteClient) Capabilities() RemoteSandboxCapabilities {
	return RemoteSandboxCapabilities{
		SupportsReconnect:      true,
		SupportsMetadata:       true,
		SupportsListSandboxes:  true,
		SupportsPauseResume:    true,
		SupportsTimeoutRefresh: true,
	}
}

func (c *CubeRemoteClient) Health(ctx context.Context) error {
	if err := c.transport.Health(ctx); err != nil {
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

	info, err := c.transport.createRemoteSandbox(ctx, cubeCreateRequest{
		TemplateID: request.TemplateID,
		Timeout:    timeout,
		OnTimeout:  action,
		AutoResume: request.Timeout.AutoResume,
		Metadata:   cloneMetadata(request.Metadata),
		EnvVars:    cloneMetadata(request.EnvVars),
	})
	if err != nil {
		return nil, normalizeCubeError("Create", err)
	}
	if info == nil || strings.TrimSpace(info.ID) == "" {
		return nil, NewRemoteError(
			SandboxTypeCube, "Create", RemoteErrorKindInternal,
			"cube returned an empty sandbox handle", nil,
		)
	}
	if info.Metadata == nil {
		info.Metadata = cloneMetadata(request.Metadata)
	}
	return &cubeRemoteHandle{info: info}, nil
}

func (c *CubeRemoteClient) Connect(
	ctx context.Context,
	sandboxID string,
) (RemoteSandboxHandle, error) {
	if strings.TrimSpace(sandboxID) == "" {
		return nil, cubeInvalidRequest("Connect", "sandbox ID is required", nil)
	}
	info, err := c.transport.connectSandbox(ctx, sandboxID)
	if err != nil {
		return nil, normalizeCubeError("Connect", err)
	}
	if info == nil || info.ID == "" {
		return nil, NewRemoteError(
			SandboxTypeCube, "Connect", RemoteErrorKindInternal,
			"cube returned an empty sandbox handle", nil,
		)
	}
	return &cubeRemoteHandle{info: info}, nil
}

func (c *CubeRemoteClient) Get(
	ctx context.Context,
	sandboxID string,
) (*RemoteSandboxSummary, error) {
	if strings.TrimSpace(sandboxID) == "" {
		return nil, cubeInvalidRequest("Get", "sandbox ID is required", nil)
	}
	summary, err := c.transport.GetSandbox(ctx, sandboxID)
	if err != nil {
		return nil, normalizeCubeError("Get", err)
	}
	if summary == nil {
		return nil, NewRemoteError(
			SandboxTypeCube, "Get", RemoteErrorKindNotFound,
			"sandbox not found", nil,
		)
	}
	return cubeRemoteSummary(*summary), nil
}

func (c *CubeRemoteClient) List(
	ctx context.Context,
	filter RemoteListFilter,
) ([]RemoteSandboxSummary, error) {
	summaries, err := c.transport.ListSandboxes(ctx)
	if err != nil {
		return nil, normalizeCubeError("List", err)
	}
	result := make([]RemoteSandboxSummary, 0, len(summaries))
	for _, summary := range summaries {
		converted := cubeRemoteSummary(summary)
		if !cubeMetadataMatches(converted.Metadata, filter.Metadata) ||
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
	if err := c.transport.deleteSandbox(ctx, sandboxID); err != nil {
		return normalizeCubeError("Delete", err)
	}
	return nil
}

func (c *CubeRemoteClient) Exec(
	ctx context.Context,
	handle RemoteSandboxHandle,
	request RemoteExecRequest,
) (*RemoteExecResult, error) {
	info, err := cubeHandleInfo("Exec", handle)
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

	startedAt := time.Now()
	var commandResult *CommandResult
	if request.Shell {
		line := request.Command
		if request.Stdin != "" {
			line = wrapWithStdin(line, request.Stdin)
		}
		commandResult, err = c.transport.RunShell(
			execCtx, info, line,
			cloneMetadata(request.Env), request.WorkDir,
		)
	} else {
		commandResult, err = c.transport.RunCommand(
			execCtx, info, request.Command,
			append([]string(nil), request.Args...),
			request.Stdin, cloneMetadata(request.Env), request.WorkDir,
		)
	}
	duration := time.Since(startedAt)
	result := cubeRemoteExecResult(commandResult, duration)

	if err != nil {
		// Execution timeout keeps the application contract: return a
		// Killed=true, ExitCode=-1 result with nil error. Only the outer
		// request.Timeout counts as an execution timeout; a canceled caller
		// or provider transport failure is a transport error.
		if request.Timeout > 0 &&
			errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			if result == nil {
				result = &RemoteExecResult{Duration: duration}
			}
			result.Killed = true
			result.ExitCode = -1
			return result, nil
		}
		return result, normalizeCubeError("Exec", err)
	}
	if result == nil {
		return nil, NewRemoteError(
			SandboxTypeCube, "Exec", RemoteErrorKindInternal,
			"cube returned an empty command result", nil,
		)
	}
	return result, nil
}

func (c *CubeRemoteClient) WriteFile(
	ctx context.Context,
	handle RemoteSandboxHandle,
	path string,
	content []byte,
) error {
	info, err := cubeHandleInfo("WriteFile", handle)
	if err != nil {
		return err
	}
	if err := c.transport.WriteFile(ctx, info, path, content); err != nil {
		return normalizeCubeError("WriteFile", err)
	}
	return nil
}

func (c *CubeRemoteClient) ReadFile(
	ctx context.Context,
	handle RemoteSandboxHandle,
	path string,
) ([]byte, error) {
	info, err := cubeHandleInfo("ReadFile", handle)
	if err != nil {
		return nil, err
	}
	content, err := c.transport.ReadFile(ctx, info, path)
	if err != nil {
		return nil, normalizeCubeError("ReadFile", err)
	}
	return content, nil
}

func (c *CubeRemoteClient) ListDir(
	ctx context.Context,
	handle RemoteSandboxHandle,
	path string,
) ([]RemoteDirEntry, error) {
	info, err := cubeHandleInfo("ListDir", handle)
	if err != nil {
		return nil, err
	}
	entries, err := c.transport.ListDir(ctx, info, path)
	if err != nil {
		return nil, normalizeCubeError("ListDir", err)
	}
	result := make([]RemoteDirEntry, 0, len(entries))
	for _, entry := range entries {
		result = append(result, RemoteDirEntry{
			Name: entry.Name,
			Path: entry.Path,
			Type: cubeRemoteEntryType(entry.Type),
			Size: entry.Size,
		})
	}
	return result, nil
}

func (c *CubeRemoteClient) MakeDir(
	ctx context.Context,
	handle RemoteSandboxHandle,
	path string,
) error {
	info, err := cubeHandleInfo("MakeDir", handle)
	if err != nil {
		return err
	}
	if err := c.transport.MakeDir(ctx, info, path); err != nil {
		return normalizeCubeError("MakeDir", err)
	}
	return nil
}

func (c *CubeRemoteClient) Remove(
	ctx context.Context,
	handle RemoteSandboxHandle,
	path string,
) error {
	info, err := cubeHandleInfo("Remove", handle)
	if err != nil {
		return err
	}
	if err := c.transport.Remove(ctx, info, path); err != nil {
		return normalizeCubeError("Remove", err)
	}
	return nil
}

func (c *CubeRemoteClient) Stat(
	ctx context.Context,
	handle RemoteSandboxHandle,
	path string,
) (*RemoteStatEntry, error) {
	info, err := cubeHandleInfo("Stat", handle)
	if err != nil {
		return nil, err
	}
	entry, err := c.transport.Stat(ctx, info, path)
	if err != nil {
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
		Type:    cubeRemoteEntryType(entry.Type),
		Size:    entry.Size,
		ModTime: cubeModTime(entry.ModifiedAt),
	}, nil
}

// --- helpers -----------------------------------------------------------------

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

func cubeHandleInfo(op string, handle RemoteSandboxHandle) (*SandboxInfo, error) {
	cubeHandle, ok := handle.(*cubeRemoteHandle)
	if !ok || cubeHandle == nil || cubeHandle.info == nil ||
		strings.TrimSpace(cubeHandle.info.ID) == "" {
		return nil, cubeInvalidRequest(op, "handle was not issued by Cube", nil)
	}
	return cubeHandle.info, nil
}

func cubeRemoteSummary(summary SandboxSummary) *RemoteSandboxSummary {
	return &RemoteSandboxSummary{
		ID:         summary.SandboxID,
		TemplateID: summary.TemplateID,
		State:      normalizeCubeState(summary.State),
		RawState:   summary.State,
		Metadata:   cloneMetadata(summary.Metadata),
		StartedAt:  summary.StartedAt,
		EndAt:      summary.EndAt,
	}
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

func cubeRemoteExecResult(result *CommandResult, duration time.Duration) *RemoteExecResult {
	if result == nil {
		return nil
	}
	return &RemoteExecResult{
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
		ExitCode: result.ExitCode,
		Duration: duration,
		Killed:   result.Killed,
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

func cubeMetadataMatches(candidate, required map[string]string) bool {
	for key, value := range required {
		if candidate[key] != value {
			return false
		}
	}
	return true
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
			kind = cubeHTTPErrorKind(op, apiErr.StatusCode)
		case errors.As(err, &netErr) && netErr.Timeout():
			kind = RemoteErrorKindTimeout
		case errors.As(err, &netErr):
			kind = RemoteErrorKindUnavailable
		}
	}
	return NewRemoteError(SandboxTypeCube, op, kind, err.Error(), err)
}

func cubeHTTPErrorKind(op string, status int) RemoteErrorKind {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return RemoteErrorKindInvalidRequest
	case http.StatusUnauthorized, http.StatusForbidden:
		return RemoteErrorKindAuthentication
	case http.StatusNotFound:
		if op == "Create" {
			return RemoteErrorKindInvalidRequest
		}
		return RemoteErrorKindNotFound
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return RemoteErrorKindTimeout
	case http.StatusConflict:
		return RemoteErrorKindConflict
	case http.StatusGone:
		return RemoteErrorKindTerminal
	case http.StatusTooManyRequests, http.StatusInsufficientStorage:
		return RemoteErrorKindCapacity
	default:
		if status >= 500 {
			return RemoteErrorKindUnavailable
		}
		return RemoteErrorKindInternal
	}
}

var (
	_ RemoteSandboxClient = (*CubeRemoteClient)(nil)
	_ RemoteSandboxHandle = (*cubeRemoteHandle)(nil)
)
