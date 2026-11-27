package utils

import (
	"errors"
	"strings"
	"testing"
)

func TestSanitizeStorageConnectivityError(t *testing.T) {
	// Kept in a variable so the table stays under the 120-char line limit.
	endpointFormat := "Endpoint 地址格式错误：请去除 http:// 或 https:// 前缀，只填写域名或 IP 地址和端口（例如：minio.example.com:9000）"
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil error", nil, ""},
		{"endpoint format", errors.New("Endpoint url cannot have fully qualified paths"), endpointFormat},
		{"dns failure", errors.New("dial tcp: lookup minio.internal: no such host"), "DNS 解析失败，请检查地址是否正确"},
		{"connection refused", errors.New("dial tcp 10.0.0.5:9000: connection refused"), "连接被拒绝，请确认服务已启动且端口正确"},
		{"no route", errors.New("dial tcp 10.0.0.5:9000: no route to host"), "无法路由到目标地址，请检查网络配置"},
		{"io timeout", errors.New("read tcp: i/o timeout"), "连接超时，请检查网络或服务状态"},
		{"deadline exceeded", errors.New("rpc error: deadline exceeded"), "连接超时，请检查网络或服务状态"},
		{"context deadline", errors.New("context deadline exceeded"), "连接超时，请检查网络或服务状态"},
		{"http 403", errors.New("403 Forbidden"), "认证失败，请检查访问凭证是否正确"},
		{"access denied", errors.New("AccessDenied: access denied"), "认证失败，请检查访问凭证是否正确"},
		{"tls certificate", errors.New("certificate signed by unknown authority"), "TLS/SSL 证书错误，请检查 SSL 配置"},
		{"x509", errors.New("x509: certificate has expired"), "TLS/SSL 证书错误，请检查 SSL 配置"},
		{"http 404", errors.New("404 Not Found"), "Bucket 不存在，请检查名称和 Region"},
		{"no such bucket", errors.New("NoSuchBucket: the specified bucket does not exist"), "Bucket 不存在，请检查名称和 Region"},
		{"unknown error falls back to default", errors.New("boom"), "连接失败，请检查配置参数是否正确"},
	}
	for _, tt := range tests {
		if got := SanitizeStorageConnectivityError(tt.err); got != tt.want {
			t.Errorf("SanitizeStorageConnectivityError(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}

// The sanitizer must never echo internal hostnames, IPs or ports: the
// whole point of the function is to keep them out of client responses.
func TestSanitizeStorageConnectivityErrorHidesInternals(t *testing.T) {
	raw := errors.New("dial tcp 10.0.0.5:9000: connection refused (host minio.internal)")
	got := SanitizeStorageConnectivityError(raw)
	for _, leaked := range []string{"10.0.0.5", "9000", "minio.internal"} {
		if strings.Contains(got, leaked) {
			t.Errorf("SanitizeStorageConnectivityError(%v) = %q, leaks %q", raw, got, leaked)
		}
	}
}
