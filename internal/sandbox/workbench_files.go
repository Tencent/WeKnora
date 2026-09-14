package sandbox

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Workbench filesystem limits bound decoded content, directory entries and RPCs.
const (
	WorkbenchMaxFileBytes = 8 << 20
	WorkbenchMaxEntries   = 500
	workbenchMaxPathBytes = 4096
	workbenchDirectBytes  = 32 << 10
	workbenchChunkBytes   = 64 << 10
	workbenchMaxRequest   = 96 << 10
	workbenchMaxResponse  = ((WorkbenchMaxFileBytes + 2) / 3 * 4) + (64 << 10)
	workbenchTimeout      = 3 * time.Minute
	workbenchExecTimeout  = 15 * time.Second
)

// Workbench filesystem errors omit provider output and caller file contents.
var (
	ErrWorkbenchPath        = errors.New("sandbox: invalid workbench path or file type")
	ErrWorkbenchNotFound    = errors.New("sandbox: workbench path not found")
	ErrWorkbenchConflict    = errors.New("sandbox: workbench destination exists or path changed")
	ErrWorkbenchTooLarge    = errors.New("sandbox: workbench size limit exceeded")
	ErrWorkbenchUnavailable = errors.New("sandbox: workbench files unavailable")
)

// SessionWorkbenchFileProvider is an optional capability, independent of the
// unrestricted agent/maintenance filesystem APIs. It never provisions a sandbox.
type SessionWorkbenchFileProvider interface {
	WorkbenchFiles(ctx context.Context, sessionID string, req WorkbenchFileRequest) (*WorkbenchFileResult, error)
}

// WorkbenchFileRequest addresses paths relative to /workspace/output. Empty Path
// is allowed only for list. Paths are validated, not cleaned. Write creates a
// new regular file, mkdir creates one directory, and rename never replaces.
// Remove accepts only a regular file or an empty directory; it is not recursive.
type WorkbenchFileRequest struct {
	Operation string `json:"operation"`
	Path      string `json:"path"`
	NewPath   string `json:"new_path,omitempty"`
	Content   []byte `json:"content,omitempty"`
}

// WorkbenchFileResult carries a bounded listing or file payload.
type WorkbenchFileResult struct {
	Path    string               `json:"path"`
	Entries []WorkbenchFileEntry `json:"entries"`
	Content []byte               `json:"content,omitempty"`
}

// WorkbenchFileEntry describes one regular file or directory below the output root.
type WorkbenchFileEntry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Type       string    `json:"type"` // "file" or "dir"
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
}

// WorkbenchFileProviderFrom preserves optional capability discovery through
// wrappers that expose an accessor or the existing SessionFileStore capability.
func WorkbenchFileProviderFrom(mgr Manager) (SessionWorkbenchFileProvider, bool) {
	if mgr == nil {
		return nil, false
	}
	if accessor, ok := mgr.(interface {
		SessionWorkbenchFileProvider() SessionWorkbenchFileProvider
	}); ok {
		provider := accessor.SessionWorkbenchFileProvider()
		return provider, provider != nil
	}
	if provider, ok := mgr.(SessionWorkbenchFileProvider); ok {
		return provider, true
	}
	if accessor, ok := mgr.(SessionCapabilityProvider); ok {
		provider, ok := accessor.SessionFileStore().(SessionWorkbenchFileProvider)
		return provider, ok
	}
	return nil, false
}

func (m *SessionBoundManager) workbenchEnabled() bool {
	return m != nil && !m.remoteDisabled() && m.client != nil &&
		workbenchPrivateExecSupported(m.client) && m.GetType() == m.client.Provider()
}

type workbenchPrivateExecCapability interface {
	SupportsPrivateWorkbenchExec() bool
}

func workbenchPrivateExecSupported(client RemoteSandboxClient) bool {
	switch wrapped := client.(type) {
	case *langfuseRemoteClient:
		return workbenchPrivateExecSupported(wrapped.inner)
	case *langfuseSnapshotClient:
		return workbenchPrivateExecSupported(wrapped.inner)
	}
	capability, ok := client.(workbenchPrivateExecCapability)
	return ok && capability.SupportsPrivateWorkbenchExec()
}

// SessionWorkbenchFileProvider exposes descriptor-relative file operations when
// the backend provides payload-safe exec. Runtime probes remain per sandbox.
func (m *SessionBoundManager) SessionWorkbenchFileProvider() SessionWorkbenchFileProvider {
	if !m.workbenchEnabled() {
		return nil
	}
	return m
}

//go:embed workbench_files.py
var workbenchFileHelper string

// WorkbenchFiles serializes with session teardown/replacement, checks ownership,
// and reconnects an existing binding. It never calls the provider's path-based
// filesystem APIs or executes on the application host.
func (m *SessionBoundManager) WorkbenchFiles(
	ctx context.Context, sessionID string, req WorkbenchFileRequest,
) (*WorkbenchFileResult, error) {
	if !m.workbenchEnabled() || m.bindings == nil || m.checker == nil {
		return nil, ErrWorkbenchUnavailable
	}
	if err := validateWorkbenchRequest(req); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, workbenchTimeout)
	defer cancel()
	key, err := m.sessionKey(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrWorkbenchUnavailable, err)
	}
	var result *WorkbenchFileResult
	err = m.bindings.WithLifecycleLock(ctx, key, func(lockCtx context.Context) error {
		if !m.workbenchEnabled() {
			return ErrWorkbenchUnavailable
		}
		exists, err := m.checker.SessionExists(lockCtx, key)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrWorkbenchUnavailable, err)
		}
		if !exists {
			return errors.Join(ErrWorkbenchUnavailable, ErrSandboxSessionDeleted)
		}
		handle, ok, err := m.lookupSessionHandle(lockCtx, sessionID)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrWorkbenchUnavailable, err)
		}
		if !ok {
			return ErrWorkbenchUnavailable
		}
		if err := m.ensureWorkbenchRuntime(lockCtx, handle, false, workbenchRuntimeFiles); err != nil {
			return err
		}
		if req.Operation == "write" && len(req.Content) > workbenchDirectBytes {
			result, err = m.workbenchUpload(lockCtx, handle, req)
			return err
		}
		reply, err := m.workbenchExec(lockCtx, handle, workbenchWireRequest{WorkbenchFileRequest: req})
		if err != nil {
			return err
		}
		result, err = validateWorkbenchResult(req, reply)
		return err
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil, errors.Join(ErrWorkbenchUnavailable, ctx.Err())
		}
		if !isWorkbenchError(err) {
			err = fmt.Errorf("%w: %w", ErrWorkbenchUnavailable, err)
		}
		return nil, err
	}
	return result, nil
}

func validateWorkbenchRequest(req WorkbenchFileRequest) error {
	switch req.Operation {
	case "list", "read", "write", "mkdir", "rename", "remove":
	default:
		return fmt.Errorf("%w: unknown operation", ErrWorkbenchPath)
	}
	if err := validateWorkbenchPath(req.Path, req.Operation == "list"); err != nil {
		return err
	}
	if req.Operation == "rename" {
		if err := validateWorkbenchPath(req.NewPath, false); err != nil {
			return err
		}
	} else if req.NewPath != "" {
		return fmt.Errorf("%w: new_path requires rename", ErrWorkbenchPath)
	}
	if len(req.Content) > WorkbenchMaxFileBytes {
		return ErrWorkbenchTooLarge
	}
	if req.Operation != "write" && len(req.Content) != 0 {
		return fmt.Errorf("%w: content requires write", ErrWorkbenchPath)
	}
	return nil
}

func validateWorkbenchPath(p string, allowRoot bool) error {
	if p == "" {
		if allowRoot {
			return nil
		}
		return ErrWorkbenchPath
	}
	if len(p) > workbenchMaxPathBytes || !utf8.ValidString(p) || strings.ContainsAny(p, "\\:") {
		return ErrWorkbenchPath
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return ErrWorkbenchPath
		}
	}
	parts := strings.Split(p, "/")
	if len(parts) > 128 {
		return ErrWorkbenchPath
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || len(part) > 255 ||
			strings.HasPrefix(part, ".weknora-workbench-") {
			return ErrWorkbenchPath
		}
	}
	return nil
}

type workbenchUploadRef struct {
	Name   string `json:"name"`
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

type workbenchWireRequest struct {
	WorkbenchFileRequest
	Upload *workbenchUploadRef `json:"upload,omitempty"`
	Offset int                 `json:"offset,omitempty"`
	Size   int                 `json:"size,omitempty"`
	SHA256 string              `json:"sha256,omitempty"`
}

type workbenchWireReply struct {
	OK     bool                 `json:"ok"`
	Code   string               `json:"code,omitempty"`
	Result *WorkbenchFileResult `json:"result,omitempty"`
	Upload *workbenchUploadRef  `json:"upload,omitempty"`
}

func (m *SessionBoundManager) workbenchExec(
	ctx context.Context, handle RemoteSandboxHandle, req workbenchWireRequest,
) (*workbenchWireReply, error) {
	if err := ctx.Err(); err != nil {
		return nil, errors.Join(ErrWorkbenchUnavailable, err)
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, ErrWorkbenchPath
	}
	// Cube/E2B's existing stdin adapter strips its heredoc delimiter. Escaping
	// underscores in JSON prevents that adapter from corrupting literal paths.
	stdin := strings.ReplaceAll(string(payload), "_", `\u005f`) + "\n"
	if len(stdin) > workbenchMaxRequest {
		return nil, ErrWorkbenchTooLarge
	}
	execCtx, cancel := context.WithTimeout(ctx, workbenchExecTimeout)
	defer cancel()
	out, err := workbenchPrivateExecClient(m.client).Exec(execCtx, handle, RemoteExecRequest{
		Command: "python3",
		Args:    []string{"-I", "-c", workbenchFileHelper},
		Stdin:   stdin,
		WorkDir: "/",
		User:    DefaultSandboxExecUser,
		Env:     map[string]string{"PATH": "/usr/local/bin:/usr/bin:/bin", "LC_ALL": "C.UTF-8"},
		Timeout: workbenchExecTimeout,
	})
	if err != nil {
		return nil, err
	}
	if execCtx.Err() != nil {
		return nil, errors.Join(ErrWorkbenchUnavailable, execCtx.Err())
	}
	if out == nil || out.Killed || out.ExitCode != 0 {
		return nil, ErrWorkbenchUnavailable
	}
	// The fixed helper bounds output at the source. Check before JSON/base64
	// decoding as well; never echo remote stdout/stderr into errors or audits.
	if len(out.Stdout) > workbenchMaxResponse || len(out.Stderr) > 4096 {
		return nil, ErrWorkbenchTooLarge
	}
	var reply workbenchWireReply
	if err := decodeWorkbenchJSON(out.Stdout, &reply); err != nil {
		return nil, ErrWorkbenchUnavailable
	}
	if !reply.OK {
		if reply.Result != nil || reply.Upload != nil {
			return nil, ErrWorkbenchUnavailable
		}
		switch reply.Code {
		case "path":
			return nil, ErrWorkbenchPath
		case "not_found":
			return nil, ErrWorkbenchNotFound
		case "conflict":
			return nil, ErrWorkbenchConflict
		case "too_large":
			return nil, ErrWorkbenchTooLarge
		default:
			return nil, ErrWorkbenchUnavailable
		}
	}
	if reply.Code != "" {
		return nil, ErrWorkbenchUnavailable
	}
	return &reply, nil
}

// Keep the existing sandbox.exec metadata span, but sanitize provider failures
// underneath it: SDK errors may echo the complete argv/stdin. Wrapping outside
// the tracing client would be too late. This never mutates m.client or changes
// tracing/error behavior for ordinary agent execution.
func workbenchPrivateExecClient(client RemoteSandboxClient) RemoteSandboxClient {
	switch traced := client.(type) {
	case *langfuseRemoteClient:
		return &langfuseRemoteClient{inner: &workbenchExecRedactor{traced.inner}}
	case *langfuseSnapshotClient:
		return &langfuseRemoteClient{inner: &workbenchExecRedactor{traced.inner}}
	default:
		return &workbenchExecRedactor{client}
	}
}

type workbenchExecRedactor struct{ RemoteSandboxClient }

func (c *workbenchExecRedactor) Exec(
	ctx context.Context, handle RemoteSandboxHandle, req RemoteExecRequest,
) (*RemoteExecResult, error) {
	result, err := c.RemoteSandboxClient.Exec(ctx, handle, req)
	if err == nil {
		return result, nil
	}
	if ctx.Err() != nil {
		return nil, errors.Join(ErrWorkbenchUnavailable, ctx.Err())
	}
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, cause) {
			return nil, errors.Join(ErrWorkbenchUnavailable, cause)
		}
	}
	// Do not retain the raw error as a wrapped cause: outer audit/tracing
	// layers must not recover payloads via Error(), errors.Unwrap or errors.As.
	return nil, ErrWorkbenchUnavailable
}

func decodeWorkbenchJSON(raw string, dst any) error {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return ErrWorkbenchUnavailable
	}
	return nil
}

func validateWorkbenchResult(req WorkbenchFileRequest, reply *workbenchWireReply) (*WorkbenchFileResult, error) {
	if reply == nil || reply.Result == nil || reply.Upload != nil {
		return nil, ErrWorkbenchUnavailable
	}
	result := reply.Result
	expectedPath := req.Path
	if req.Operation == "rename" {
		expectedPath = req.NewPath
	}
	if result.Path != expectedPath || result.Entries == nil {
		return nil, ErrWorkbenchUnavailable
	}
	if len(result.Entries) > WorkbenchMaxEntries || len(result.Content) > WorkbenchMaxFileBytes {
		return nil, ErrWorkbenchTooLarge
	}
	if req.Operation != "list" && len(result.Entries) != 0 ||
		req.Operation != "read" && len(result.Content) != 0 {
		return nil, ErrWorkbenchUnavailable
	}
	seen := make(map[string]bool, len(result.Entries))
	for _, entry := range result.Entries {
		p := entry.Name
		if req.Path != "" {
			p = req.Path + "/" + p
		}
		if strings.Contains(entry.Name, "/") || validateWorkbenchPath(p, false) != nil ||
			entry.Path != p || seen[p] || entry.Size < 0 || entry.ModifiedAt.IsZero() ||
			(entry.Type != "file" && entry.Type != "dir") {
			return nil, ErrWorkbenchUnavailable
		}
		seen[p] = true
	}
	return result, nil
}

func (m *SessionBoundManager) workbenchUpload(
	ctx context.Context, handle RemoteSandboxHandle, req WorkbenchFileRequest,
) (*WorkbenchFileResult, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, ErrWorkbenchUnavailable
	}
	ref := &workbenchUploadRef{Name: ".weknora-workbench-" + hex.EncodeToString(nonce[:])}
	begin := workbenchWireRequest{
		WorkbenchFileRequest: WorkbenchFileRequest{Operation: "_upload_begin"},
		Upload:               ref,
	}
	reply, err := m.workbenchExec(ctx, handle, begin)
	if err != nil {
		return nil, err
	}
	if reply.Upload == nil || reply.Result != nil || reply.Upload.Name != ref.Name || reply.Upload.Inode == 0 {
		return nil, ErrWorkbenchUnavailable
	}
	ref = reply.Upload
	defer func() {
		// Cleanup owns only this inode and never recursively removes anything.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), workbenchExecTimeout)
		defer cancel()
		_, _ = m.workbenchExec(cleanupCtx, handle, workbenchWireRequest{
			WorkbenchFileRequest: WorkbenchFileRequest{Operation: "_upload_abort"},
			Upload:               ref,
		})
	}()
	for offset := 0; offset < len(req.Content); offset += workbenchChunkBytes {
		end := min(offset+workbenchChunkBytes, len(req.Content))
		reply, err = m.workbenchExec(ctx, handle, workbenchWireRequest{
			WorkbenchFileRequest: WorkbenchFileRequest{Operation: "_upload_chunk", Content: req.Content[offset:end]},
			Upload:               ref,
			Offset:               offset,
		})
		if err != nil {
			return nil, err
		}
		if reply.Result != nil || reply.Upload != nil {
			return nil, ErrWorkbenchUnavailable
		}
	}
	digest := sha256.Sum256(req.Content)
	commit := req
	commit.Content = nil
	reply, err = m.workbenchExec(ctx, handle, workbenchWireRequest{
		WorkbenchFileRequest: commit,
		Upload:               ref,
		Size:                 len(req.Content),
		SHA256:               hex.EncodeToString(digest[:]),
	})
	if err != nil {
		return nil, err
	}
	return validateWorkbenchResult(req, reply)
}

func isWorkbenchError(err error) bool {
	return errors.Is(err, ErrWorkbenchPath) || errors.Is(err, ErrWorkbenchNotFound) ||
		errors.Is(err, ErrWorkbenchConflict) || errors.Is(err, ErrWorkbenchTooLarge) ||
		errors.Is(err, ErrWorkbenchUnavailable) || errors.Is(err, ErrWorkbenchRuntimeIncompatible)
}

var _ SessionWorkbenchFileProvider = (*SessionBoundManager)(nil)
