package video

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

const (
	// MaxUploadBytes is the vendor's documented ceiling for one temporary
	// file. It is also the most the pipeline will hand over in memory for one
	// video, so the ceiling is enforced before anything is uploaded.
	MaxUploadBytes = 1 << 30
	// ossURLPrefix marks an object in the vendor's temporary bucket.
	ossURLPrefix = "oss://"
	// defaultUploadBaseURL is the public DashScope root used when the model's
	// own base URL is not a DashScope host (for example a gateway).
	defaultUploadBaseURL = "https://dashscope.aliyuncs.com"
	// defaultHTTPTimeout bounds one upload or one model call. A video file can
	// be hundreds of megabytes and the answer is generated, so ten minutes
	// leaves room for both without a hung request holding a worker for the
	// whole document-process budget.
	defaultHTTPTimeout = 10 * time.Minute
	// uploadBaseEnv lets an operator point the temporary-file service at
	// another DashScope domain (an international account, a private gateway).
	uploadBaseEnv = "VIDEO_UPLOAD_BASE_URL"
	// timeoutEnv overrides defaultHTTPTimeout, in seconds.
	timeoutEnv = "VIDEO_HTTP_TIMEOUT_SECONDS"
)

// videoHTTPTimeout returns the per-phase budget: the video model call and the
// upload each get this long.
func videoHTTPTimeout() time.Duration {
	if raw := strings.TrimSpace(os.Getenv(timeoutEnv)); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return defaultHTTPTimeout
}

// uploadBaseURL decides where the temporary-file API lives. An explicit
// override wins (config, then the environment); otherwise the model's own
// DashScope host is reused so an international account uploads to the domain
// it calls, and anything else falls back to the public Beijing endpoint.
func uploadBaseURL(configured, baseURL string) string {
	if value := strings.TrimRight(strings.TrimSpace(configured), "/"); value != "" {
		return value
	}
	if value := strings.TrimRight(strings.TrimSpace(os.Getenv(uploadBaseEnv)), "/"); value != "" {
		return value
	}
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err == nil {
		host := strings.ToLower(parsed.Host)
		if strings.HasSuffix(host, ".aliyuncs.com") && strings.Contains(host, "dashscope") {
			return parsed.Scheme + "://" + parsed.Host
		}
	}
	return defaultUploadBaseURL
}

// validateVideoBaseURL applies the deployment's SSRF policy to an outbound
// URL. It is used for the model endpoint and for the upload host the vendor
// answers with.
func validateVideoBaseURL(rawURL string) error {
	if strings.TrimSpace(rawURL) == "" {
		return fmt.Errorf("URL is empty")
	}
	if err := secutils.ValidateURLForSSRF(rawURL); err != nil {
		return fmt.Errorf("URL SSRF check failed: %w", err)
	}
	return nil
}

// newVideoHTTPClient builds the SSRF-safe client the upload and the call use.
// Timeouts are carried by the request context, so the client itself has none:
// one client serves both a small policy request and a gigabyte upload.
func newVideoHTTPClient() *http.Client {
	config := secutils.DefaultSSRFSafeHTTPClientConfig()
	config.Timeout = 0
	return secutils.NewSSRFSafeHTTPClient(config)
}
