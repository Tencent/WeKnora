package file

import (
	"context"
	"net/http"
	"time"

	"github.com/Tencent/WeKnora/internal/utils"
)

// objectStorageSetupTimeout bounds the bucket probe a file service runs when
// it is built. Services are rebuilt per resolution, so the probe runs before
// many transfers, not just at startup.
const objectStorageSetupTimeout = 30 * time.Second

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
// and then goes silent fails within about a minute instead of holding the
// request until its context ends. The header wait starts after the request
// body is written, so it does not cap an upload.
func objectStorageTransport() *http.Transport {
	transport := utils.NewSSRFSafeTransport(objectStorageHTTPClientConfig())
	transport.TLSHandshakeTimeout = 10 * time.Second
	transport.ResponseHeaderTimeout = 60 * time.Second
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

// objectStorageSetupContext bounds a constructor's bucket probe.
func objectStorageSetupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), objectStorageSetupTimeout)
}
