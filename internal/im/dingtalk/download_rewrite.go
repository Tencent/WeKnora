package dingtalk

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// downloadRewrite rewrites temporary DingTalk file download URLs that point
// to dedicated-line hosts (unreachable from the intranet) to a fixed
// intranet base address configured by the operator
// (im.dingtalk.download_url_rewrite).
type downloadRewrite struct {
	prefixes []string // matched URL prefixes (scheme://host[:port][path])
	to       string   // replacement base URL
}

// parseDownloadRewrite validates and normalizes the rewrite configuration.
// It returns nil (feature disabled) when cfg is nil, when `to` is missing or
// has no scheme, or when no valid prefix remains; every dropped piece is
// reported with a warning so misconfiguration is visible in logs.
func parseDownloadRewrite(cfg *config.DingTalkURLRewriteConfig) *downloadRewrite {
	if cfg == nil {
		return nil
	}
	to := strings.TrimSpace(cfg.To)
	if to == "" {
		logger.Warnf(context.Background(), "[DingTalk] im.dingtalk.download_url_rewrite.to is empty; url rewrite disabled")
		return nil
	}
	if !strings.Contains(to, "://") {
		logger.Warnf(context.Background(), "[DingTalk] im.dingtalk.download_url_rewrite.to %q has no scheme://; url rewrite disabled", to)
		return nil
	}

	var prefixes []string
	for _, raw := range strings.Split(cfg.From, ",") {
		prefix := strings.TrimSpace(raw)
		if prefix == "" {
			continue
		}
		if !strings.Contains(prefix, "://") {
			logger.Warnf(context.Background(), "[DingTalk] im.dingtalk.download_url_rewrite.from entry %q has no scheme://; entry dropped", prefix)
			continue
		}
		prefixes = append(prefixes, prefix)
	}
	// Longest-first so the most specific prefix always wins, decoupling
	// match results from the order entries appear in the config.
	slices.SortFunc(prefixes, func(a, b string) int { return len(b) - len(a) })
	if len(prefixes) == 0 {
		logger.Warnf(context.Background(), "[DingTalk] im.dingtalk.download_url_rewrite.from has no valid entries; url rewrite disabled")
		return nil
	}
	return &downloadRewrite{prefixes: prefixes, to: to}
}

// apply returns the rewritten URL when rawURL starts with one of the
// configured prefixes. The prefix must end on a URL boundary — the next
// character is the end of the string, '/', '?' or '#' — so a host:port
// prefix such as "...:15443" cannot match "...:154439". Everything after
// the prefix (path, query, fragment) is preserved; matching is exact and
// case-sensitive (DingTalk download URLs are machine-generated lowercase).
func (r *downloadRewrite) apply(rawURL string) (rewritten, matchedPrefix string, ok bool) {
	for _, prefix := range r.prefixes {
		if !strings.HasPrefix(rawURL, prefix) {
			continue
		}
		rest := rawURL[len(prefix):]
		if rest != "" && !strings.HasPrefix(rest, "/") && !strings.HasPrefix(rest, "?") && !strings.HasPrefix(rest, "#") {
			continue
		}
		return r.to + rest, prefix, true
	}
	return rawURL, "", false
}

// downloadRewriteTimeout matches the timeout of the shared package httpClient.
const downloadRewriteTimeout = 15 * time.Second

// downloadMaxRedirects caps redirects for the trusted download client,
// mirroring MaxRedirects of the shared SSRF-safe client.
const downloadMaxRedirects = 5

// newTrustedDownloadClient builds the HTTP client used for download URLs
// that were rewritten to the operator-configured intranet base. The target
// host is admin-controlled (same trust level as SSRF_WHITELIST entries), so
// the SSRF dial guard and validating round-tripper are intentionally
// omitted — they would reject private intranet addresses. Redirects are
// capped but not re-validated: the source is a trusted intranet mirror
// serving DingTalk-signed URLs.
func newTrustedDownloadClient(skipTLSVerify bool) *http.Client {
	return &http.Client{
		Timeout: downloadRewriteTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: skipTLSVerify},
		},
		CheckRedirect: func(_ *http.Request, via []*http.Request) error {
			if len(via) >= downloadMaxRedirects {
				return fmt.Errorf("stopped after %d redirects", downloadMaxRedirects)
			}
			return nil
		},
	}
}

// logSnippet truncates s to at most max bytes for log output.
func logSnippet(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

// newSkipVerifySSRFSafeDownloadClient builds the client used for
// non-rewritten download URLs when im.dingtalk.download_insecure_skip_verify
// is enabled. It keeps every SSRF safeguard (per-request validation,
// per-hop redirect validation, dial-time IP pinning) and only relaxes
// certificate verification.
func newSkipVerifySSRFSafeDownloadClient() *http.Client {
	transport := &http.Transport{
		DialContext:     secutils.SSRFSafeDialContext,
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec — operator opt-in flag (im.dingtalk.download_insecure_skip_verify)
	}
	return secutils.NewSSRFSafeHTTPClientWithTransport(
		secutils.SSRFSafeHTTPClientConfig{
			Timeout:      downloadRewriteTimeout,
			MaxRedirects: downloadMaxRedirects,
		},
		transport,
	)
}
