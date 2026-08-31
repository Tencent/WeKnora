package sandbox

import (
	"context"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
)

const sandboxSpanPreviewRunes = 256

// wrapLangfuseRemoteClient records provider-neutral sandbox RPCs as Langfuse
// spans (sandbox.exec / sandbox.connect / …) so LiteFuse shows a product-level
// tree instead of a pile of Docker Engine HTTP calls parented to whatever
// agent.round happened to be recording. No-op when Langfuse is disabled.
//
// Snapshot capability is forwarded: wrapping must not hide RemoteSnapshotManager
// from SnapshotManagerFrom.
func wrapLangfuseRemoteClient(inner RemoteSandboxClient) RemoteSandboxClient {
	if inner == nil {
		return nil
	}
	if _, ok := inner.(*langfuseRemoteClient); ok {
		return inner
	}
	if _, ok := inner.(*langfuseSnapshotClient); ok {
		return inner
	}
	wrapped := langfuseRemoteClient{inner: inner}
	if _, ok := inner.(RemoteSnapshotManager); ok {
		return &langfuseSnapshotClient{langfuseRemoteClient: wrapped}
	}
	return &wrapped
}

type langfuseRemoteClient struct {
	inner RemoteSandboxClient
}

func (c *langfuseRemoteClient) Provider() RemoteProvider { return c.inner.Provider() }

func (c *langfuseRemoteClient) Capabilities() RemoteSandboxCapabilities {
	return c.inner.Capabilities()
}

func (c *langfuseRemoteClient) Health(ctx context.Context) error {
	return c.inner.Health(ctx)
}

func (c *langfuseRemoteClient) Create(
	ctx context.Context, req RemoteCreateRequest,
) (RemoteSandboxHandle, error) {
	ctx, span := startSandboxSpan(ctx, "sandbox.create", map[string]interface{}{
		"template_id": req.TemplateID,
	}, nil)
	handle, err := c.inner.Create(ctx, req)
	span.Finish(sandboxHandleOut(handle), nil, err)
	return handle, err
}

func (c *langfuseRemoteClient) Connect(
	ctx context.Context, sandboxID string,
) (RemoteSandboxHandle, error) {
	ctx, span := startSandboxSpan(ctx, "sandbox.connect", map[string]interface{}{
		"sandbox_id": sandboxID,
	}, nil)
	handle, err := c.inner.Connect(ctx, sandboxID)
	span.Finish(sandboxHandleOut(handle), nil, err)
	return handle, err
}

func (c *langfuseRemoteClient) Get(
	ctx context.Context, sandboxID string,
) (*RemoteSandboxSummary, error) {
	return c.inner.Get(ctx, sandboxID)
}

func (c *langfuseRemoteClient) List(
	ctx context.Context, filter RemoteListFilter,
) ([]RemoteSandboxSummary, error) {
	return c.inner.List(ctx, filter)
}

func (c *langfuseRemoteClient) Delete(ctx context.Context, sandboxID string) error {
	ctx, span := startSandboxSpan(ctx, "sandbox.delete", map[string]interface{}{
		"sandbox_id": sandboxID,
	}, nil)
	err := c.inner.Delete(ctx, sandboxID)
	span.Finish(nil, nil, err)
	return err
}

func (c *langfuseRemoteClient) Exec(
	ctx context.Context,
	handle RemoteSandboxHandle,
	req RemoteExecRequest,
) (*RemoteExecResult, error) {
	ctx, span := startSandboxSpan(ctx, "sandbox.exec", map[string]interface{}{
		"command":    truncateSandboxPreview(req.Command),
		"shell":      req.Shell,
		"work_dir":   req.WorkDir,
		"user":       req.User,
		"timeout_ms": req.Timeout.Milliseconds(),
	}, sandboxHandleMeta(handle))
	result, err := c.inner.Exec(ctx, handle, req)
	out := map[string]interface{}{}
	if result != nil {
		out["exit_code"] = result.ExitCode
		out["killed"] = result.Killed
		out["duration_ms"] = result.Duration.Milliseconds()
		out["stdout_bytes"] = len(result.Stdout)
		out["stderr_bytes"] = len(result.Stderr)
	}
	span.Finish(out, nil, err)
	return result, err
}

func (c *langfuseRemoteClient) WriteFile(
	ctx context.Context, handle RemoteSandboxHandle, path string, content []byte,
) error {
	ctx, span := startSandboxSpan(ctx, "sandbox.write_file", map[string]interface{}{
		"path":  path,
		"bytes": len(content),
	}, sandboxHandleMeta(handle))
	err := c.inner.WriteFile(ctx, handle, path, content)
	span.Finish(nil, nil, err)
	return err
}

func (c *langfuseRemoteClient) ReadFile(
	ctx context.Context, handle RemoteSandboxHandle, path string,
) ([]byte, error) {
	ctx, span := startSandboxSpan(ctx, "sandbox.read_file", map[string]interface{}{
		"path": path,
	}, sandboxHandleMeta(handle))
	data, err := c.inner.ReadFile(ctx, handle, path)
	span.Finish(map[string]interface{}{"bytes": len(data)}, nil, err)
	return data, err
}

func (c *langfuseRemoteClient) ListDir(
	ctx context.Context, handle RemoteSandboxHandle, path string,
) ([]RemoteDirEntry, error) {
	ctx, span := startSandboxSpan(ctx, "sandbox.list_dir", map[string]interface{}{
		"path": path,
	}, sandboxHandleMeta(handle))
	entries, err := c.inner.ListDir(ctx, handle, path)
	span.Finish(map[string]interface{}{"entries": len(entries)}, nil, err)
	return entries, err
}

func (c *langfuseRemoteClient) MakeDir(
	ctx context.Context, handle RemoteSandboxHandle, path string,
) error {
	ctx, span := startSandboxSpan(ctx, "sandbox.make_dir", map[string]interface{}{
		"path": path,
	}, sandboxHandleMeta(handle))
	err := c.inner.MakeDir(ctx, handle, path)
	span.Finish(nil, nil, err)
	return err
}

func (c *langfuseRemoteClient) Remove(
	ctx context.Context, handle RemoteSandboxHandle, path string,
) error {
	ctx, span := startSandboxSpan(ctx, "sandbox.remove", map[string]interface{}{
		"path": path,
	}, sandboxHandleMeta(handle))
	err := c.inner.Remove(ctx, handle, path)
	span.Finish(nil, nil, err)
	return err
}

func (c *langfuseRemoteClient) Stat(
	ctx context.Context, handle RemoteSandboxHandle, path string,
) (*RemoteStatEntry, error) {
	ctx, span := startSandboxSpan(ctx, "sandbox.stat", map[string]interface{}{
		"path": path,
	}, sandboxHandleMeta(handle))
	stat, err := c.inner.Stat(ctx, handle, path)
	span.Finish(nil, nil, err)
	return stat, err
}

type langfuseSnapshotClient struct {
	langfuseRemoteClient
}

func (c *langfuseSnapshotClient) CreateSnapshot(
	ctx context.Context, sandboxID string, name string,
) (RemoteSnapshotRef, error) {
	inner, ok := c.inner.(RemoteSnapshotManager)
	if !ok {
		return RemoteSnapshotRef{}, &RemoteError{
			Kind:    RemoteErrorKindUnsupported,
			Op:      "CreateSnapshot",
			Message: "inner client has no snapshot manager",
		}
	}
	ctx, span := startSandboxSpan(ctx, "sandbox.create_snapshot", map[string]interface{}{
		"sandbox_id": sandboxID,
		"name":       name,
	}, nil)
	ref, err := inner.CreateSnapshot(ctx, sandboxID, name)
	span.Finish(map[string]interface{}{"snapshot_id": ref.ID}, nil, err)
	return ref, err
}

func (c *langfuseSnapshotClient) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	inner, ok := c.inner.(RemoteSnapshotManager)
	if !ok {
		return nil
	}
	ctx, span := startSandboxSpan(ctx, "sandbox.delete_snapshot", map[string]interface{}{
		"snapshot_id": snapshotID,
	}, nil)
	err := inner.DeleteSnapshot(ctx, snapshotID)
	span.Finish(nil, nil, err)
	return err
}

func (c *langfuseSnapshotClient) ListSnapshots(
	ctx context.Context, sandboxID string,
) ([]RemoteSnapshotRef, error) {
	inner, ok := c.inner.(RemoteSnapshotManager)
	if !ok {
		return nil, nil
	}
	return inner.ListSnapshots(ctx, sandboxID)
}

func startSandboxSpan(
	ctx context.Context,
	name string,
	input, extraMeta map[string]interface{},
) (context.Context, *langfuse.Span) {
	return langfuse.GetManager().StartSpan(ctx, langfuse.SpanOptions{
		Name:     name,
		Input:    input,
		Metadata: extraMeta,
	})
}

func sandboxHandleMeta(handle RemoteSandboxHandle) map[string]interface{} {
	if handle == nil {
		return nil
	}
	return map[string]interface{}{
		"sandbox_id": handle.ID(),
		"provider":   string(handle.Provider()),
	}
}

func sandboxHandleOut(handle RemoteSandboxHandle) map[string]interface{} {
	if handle == nil {
		return nil
	}
	return map[string]interface{}{"sandbox_id": handle.ID()}
}

func truncateSandboxPreview(s string) string {
	if s == "" || utf8.RuneCountInString(s) <= sandboxSpanPreviewRunes {
		return s
	}
	runes := []rune(s)
	return string(runes[:sandboxSpanPreviewRunes]) + "…"
}

var (
	_ RemoteSandboxClient   = (*langfuseRemoteClient)(nil)
	_ RemoteSnapshotManager = (*langfuseSnapshotClient)(nil)
)
