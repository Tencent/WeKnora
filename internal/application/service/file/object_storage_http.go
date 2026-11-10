package file

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/utils"
)

const (
	// objectStorageSetupTimeout bounds the bucket probe a file service runs
	// when it is built. Services are rebuilt per resolution, so the probe
	// runs before many transfers, not just at startup.
	objectStorageSetupTimeout = 30 * time.Second

	// objectStorageTransferTimeout is the fallback when the caller did not
	// set a deadline (asynq workers and the main HTTP server both omit one).
	// Long enough for a slow 40 MB COS PUT; short enough that a stalled body
	// cannot pin a worker forever.
	objectStorageTransferTimeout = 30 * time.Minute

	// objectStorageResponseHeaderTimeout is longer than a typical GET/PUT
	// header wait because CopyObject / Object.Copy are synchronous: the
	// server does not send headers until the copy finishes. GET headers
	// still arrive immediately, so a silent peer fails in this window
	// rather than the full transfer budget.
	objectStorageResponseHeaderTimeout = 10 * time.Minute
)

// objectStorageHTTPClientConfig is the SSRF-safe client config for the object
// storage SDKs. The default config's Timeout is Go's whole-request timeout,
// body included, which capped every transfer at what a link moves in
// 30 seconds (#3306). The object storage clients bound connection setup on
// their transport instead and leave the transfer to the request context.
func objectStorageHTTPClientConfig() utils.SSRFSafeHTTPClientConfig {
	config := utils.DefaultSSRFSafeHTTPClientConfig()
	config.Timeout = 0
	return config
}

// objectStorageTransport is the SSRF-safe transport with the setup bounds the
// whole-request timeout used to provide: a peer that accepts the connection
// and then goes silent fails within the header wait instead of holding the
// request until its context ends. The header wait starts after the request
// body is written, so it does not cap an upload.
func objectStorageTransport() *http.Transport {
	transport := utils.NewSSRFSafeTransport(objectStorageHTTPClientConfig())
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = objectStorageResponseHeaderTimeout
	transport.ExpectContinueTimeout = time.Second
	transport.IdleConnTimeout = 90 * time.Second
	return transport
}

// objectStorageHTTPClient is the client for the SDKs that take a whole
// http.Client (S3, KS3, OBS, OSS). COS wraps objectStorageTransport in its
// own signing transport.
func objectStorageHTTPClient() *http.Client {
	return utils.NewSSRFSafeHTTPClientWithTransport(objectStorageHTTPClientConfig(), objectStorageTransport())
}

// objectStorageSetupContext bounds one constructor probe (HeadBucket or
// CreateBucket). Callers must not reuse the same context for both: a slow
// Head that then reports "missing" would leave Create with no time left.
func objectStorageSetupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), objectStorageSetupTimeout)
}

// objectStorageTransferContext adds a fallback deadline only when the caller
// did not set one, matching models/chat.withLLMTimeout.
func objectStorageTransferContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, objectStorageTransferTimeout)
}

type objectStorageBoundReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func objectStorageBoundReader(body io.ReadCloser, cancel context.CancelFunc) io.ReadCloser {
	if body == nil {
		cancel()
		return nil
	}
	return &objectStorageBoundReadCloser{ReadCloser: body, cancel: cancel}
}

func (r *objectStorageBoundReadCloser) Close() error {
	err := r.ReadCloser.Close()
	r.cancel()
	return err
}
