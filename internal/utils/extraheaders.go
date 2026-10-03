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
	"x-goog-api-key":    {},
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
