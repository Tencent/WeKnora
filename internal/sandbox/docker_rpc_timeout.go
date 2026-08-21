package sandbox

import (
	"context"
	"time"

	"github.com/moby/moby/client"
)

// withDockerRPCTimeout bounds short Engine API calls. Streaming methods are
// left on the caller's context: http.Client.Timeout (and a blanket RPC
// deadline) would abort a pull or hijacked exec after the budget, which is
// exactly how the previous 30s client timeout broke cold image pulls.
func withDockerRPCTimeout(inner dockerEngineAPI, timeout time.Duration) dockerEngineAPI {
	if inner == nil || timeout <= 0 {
		return inner
	}
	return &dockerRPCTimeoutAPI{inner: inner, timeout: timeout}
}

type dockerRPCTimeoutAPI struct {
	inner   dockerEngineAPI
	timeout time.Duration
}

func (a *dockerRPCTimeoutAPI) rpcCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, a.timeout)
}

func (a *dockerRPCTimeoutAPI) Ping(
	ctx context.Context, options client.PingOptions,
) (client.PingResult, error) {
	rpcCtx, cancel := a.rpcCtx(ctx)
	defer cancel()
	return a.inner.Ping(rpcCtx, options)
}

func (a *dockerRPCTimeoutAPI) ContainerCreate(
	ctx context.Context, options client.ContainerCreateOptions,
) (client.ContainerCreateResult, error) {
	rpcCtx, cancel := a.rpcCtx(ctx)
	defer cancel()
	return a.inner.ContainerCreate(rpcCtx, options)
}

func (a *dockerRPCTimeoutAPI) ContainerStart(
	ctx context.Context, containerID string, options client.ContainerStartOptions,
) (client.ContainerStartResult, error) {
	rpcCtx, cancel := a.rpcCtx(ctx)
	defer cancel()
	return a.inner.ContainerStart(rpcCtx, containerID, options)
}

func (a *dockerRPCTimeoutAPI) ContainerUnpause(
	ctx context.Context, containerID string, options client.ContainerUnpauseOptions,
) (client.ContainerUnpauseResult, error) {
	rpcCtx, cancel := a.rpcCtx(ctx)
	defer cancel()
	return a.inner.ContainerUnpause(rpcCtx, containerID, options)
}

func (a *dockerRPCTimeoutAPI) ContainerInspect(
	ctx context.Context, containerID string, options client.ContainerInspectOptions,
) (client.ContainerInspectResult, error) {
	rpcCtx, cancel := a.rpcCtx(ctx)
	defer cancel()
	return a.inner.ContainerInspect(rpcCtx, containerID, options)
}

func (a *dockerRPCTimeoutAPI) ContainerList(
	ctx context.Context, options client.ContainerListOptions,
) (client.ContainerListResult, error) {
	rpcCtx, cancel := a.rpcCtx(ctx)
	defer cancel()
	return a.inner.ContainerList(rpcCtx, options)
}

func (a *dockerRPCTimeoutAPI) ContainerRemove(
	ctx context.Context, containerID string, options client.ContainerRemoveOptions,
) (client.ContainerRemoveResult, error) {
	rpcCtx, cancel := a.rpcCtx(ctx)
	defer cancel()
	return a.inner.ContainerRemove(rpcCtx, containerID, options)
}

func (a *dockerRPCTimeoutAPI) ExecCreate(
	ctx context.Context, containerID string, options client.ExecCreateOptions,
) (client.ExecCreateResult, error) {
	rpcCtx, cancel := a.rpcCtx(ctx)
	defer cancel()
	return a.inner.ExecCreate(rpcCtx, containerID, options)
}

func (a *dockerRPCTimeoutAPI) ExecAttach(
	ctx context.Context, execID string, options client.ExecAttachOptions,
) (client.ExecAttachResult, error) {
	return a.inner.ExecAttach(ctx, execID, options)
}

func (a *dockerRPCTimeoutAPI) ExecInspect(
	ctx context.Context, execID string, options client.ExecInspectOptions,
) (client.ExecInspectResult, error) {
	rpcCtx, cancel := a.rpcCtx(ctx)
	defer cancel()
	return a.inner.ExecInspect(rpcCtx, execID, options)
}

func (a *dockerRPCTimeoutAPI) CopyToContainer(
	ctx context.Context, containerID string, options client.CopyToContainerOptions,
) (client.CopyToContainerResult, error) {
	return a.inner.CopyToContainer(ctx, containerID, options)
}

func (a *dockerRPCTimeoutAPI) CopyFromContainer(
	ctx context.Context, containerID string, options client.CopyFromContainerOptions,
) (client.CopyFromContainerResult, error) {
	return a.inner.CopyFromContainer(ctx, containerID, options)
}

func (a *dockerRPCTimeoutAPI) ContainerStatPath(
	ctx context.Context, containerID string, options client.ContainerStatPathOptions,
) (client.ContainerStatPathResult, error) {
	rpcCtx, cancel := a.rpcCtx(ctx)
	defer cancel()
	return a.inner.ContainerStatPath(rpcCtx, containerID, options)
}

func (a *dockerRPCTimeoutAPI) ImageInspect(
	ctx context.Context, imageID string, opts ...client.ImageInspectOption,
) (client.ImageInspectResult, error) {
	rpcCtx, cancel := a.rpcCtx(ctx)
	defer cancel()
	return a.inner.ImageInspect(rpcCtx, imageID, opts...)
}

func (a *dockerRPCTimeoutAPI) ImagePull(
	ctx context.Context, refStr string, options client.ImagePullOptions,
) (client.ImagePullResponse, error) {
	return a.inner.ImagePull(ctx, refStr, options)
}

func (a *dockerRPCTimeoutAPI) ImageList(
	ctx context.Context, options client.ImageListOptions,
) (client.ImageListResult, error) {
	rpcCtx, cancel := a.rpcCtx(ctx)
	defer cancel()
	return a.inner.ImageList(rpcCtx, options)
}

var _ dockerEngineAPI = (*dockerRPCTimeoutAPI)(nil)
