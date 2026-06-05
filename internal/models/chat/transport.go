package chat

import (
	"context"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// LLM 调用超时配置。仅作为"上层未设置 deadline 时"的兜底，避免 hung 请求
// 永久阻塞 worker。如果上层 ctx 已经设置了 deadline（无论比默认更短还是更长），
// 都会原样尊重，不再叠加默认超时。可通过环境变量覆盖：
//   - WEKNORA_LLM_CHAT_TIMEOUT_SECONDS    非流式调用兜底超时（默认 600s）
//   - WEKNORA_LLM_STREAM_TIMEOUT_SECONDS  流式调用兜底超时（默认 1800s）
var (
	defaultChatTimeout   = envDurationSeconds("WEKNORA_LLM_CHAT_TIMEOUT_SECONDS", 300*time.Second)
	defaultStreamTimeout = envDurationSeconds("WEKNORA_LLM_STREAM_TIMEOUT_SECONDS", 600*time.Second)
)

// envDurationSeconds 读取以"秒"为单位的环境变量，解析失败或非正值时回退到 fallback。
func envDurationSeconds(key string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return time.Duration(n) * time.Second
}

// withLLMTimeout 仅在上层 ctx 没有 deadline 时附加一个兜底超时；
// 如果上层已显式设置 deadline（无论更短或更长），则原样返回，
// 让调用方对自己的超时策略拥有最终决定权。
func withLLMTimeout(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, d)
}

// rawHTTPClient is a shared HTTP client for raw HTTP LLM calls with connection-level timeouts.
// Per-request timeout is enforced via context deadline (see defaultChatTimeout / defaultStreamTimeout)
// rather than http.Client.Timeout, so streaming calls are not prematurely terminated.
// Uses SSRFSafeDialContext to prevent DNS rebinding attacks at the connection layer.
var rawHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DialContext:         secutils.SSRFSafeDialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		IdleConnTimeout:     90 * time.Second,
		MaxIdleConnsPerHost: 5,
	},
}
