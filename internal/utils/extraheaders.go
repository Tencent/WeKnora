package utils

import (
	"net/http"
	"strings"
)

// reservedHeaderKeys 列出不允许被用户自定义头覆盖的关键请求头。
// 这些头由各 provider 的签名、鉴权或 SSE 流程控制，覆盖后可能直接导致调用失败。
var reservedHeaderKeys = map[string]struct{}{
	"authorization":     {},
	"api-key":           {},
	"x-api-key":         {},
	"content-type":      {},
	"content-length":    {},
	"accept-encoding":   {},
	"host":              {},
	"connection":        {},
	"transfer-encoding": {},
}

// IsReservedHeader 判断某个 header key 是否为保留 header，保留 header 不允许被自定义头覆盖。
func IsReservedHeader(key string) bool {
	_, ok := reservedHeaderKeys[strings.ToLower(strings.TrimSpace(key))]
	return ok
}

// ApplyCustomHeaders 将用户自定义的 header 写入 http.Request。
// 保留 header（Authorization、api-key、Content-Type 等）会被跳过以避免破坏鉴权/签名。
// 其它 header 会直接覆盖同名条目，允许用户替换默认值（例如 Accept）。
func ApplyCustomHeaders(req *http.Request, headers map[string]string) {
	if req == nil || len(headers) == 0 {
		return
	}
	for k, v := range headers {
		name := strings.TrimSpace(k)
		if name == "" {
			continue
		}
		if IsReservedHeader(name) {
			continue
		}
		req.Header.Set(name, v)
	}
}

// CustomHeadersRoundTripper 是一个 http.RoundTripper 包装器，
// 会在每个 HTTP 请求发出前注入用户自定义的 header。
// 用于无法直接拿到底层 *http.Request 的场景（如 go-openai SDK）。
type CustomHeadersRoundTripper struct {
	Headers map[string]string
	Base    http.RoundTripper
}

// RoundTrip 实现 http.RoundTripper 接口。
func (t *CustomHeadersRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if len(t.Headers) > 0 {
		// 复制请求以避免修改调用方传入的对象。
		cloned := req.Clone(req.Context())
		ApplyCustomHeaders(cloned, t.Headers)
		return base.RoundTrip(cloned)
	}
	return base.RoundTrip(req)
}

// WrapHTTPClientWithHeaders 返回一个新的 *http.Client，在原有 client 基础上注入自定义 header。
// 如果 headers 为空则直接返回原 client，避免不必要的开销。
func WrapHTTPClientWithHeaders(client *http.Client, headers map[string]string) *http.Client {
	if len(headers) == 0 {
		return client
	}
	if client == nil {
		client = &http.Client{}
	}
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	wrapped := *client
	wrapped.Transport = &CustomHeadersRoundTripper{Headers: headers, Base: base}
	return &wrapped
}
