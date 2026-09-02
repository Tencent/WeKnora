package dingtalk

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/logger"
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
