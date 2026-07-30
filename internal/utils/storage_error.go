package utils

import "strings"

// SanitizeStorageConnectivityError converts a raw storage connectivity error
// into a safe, user-facing message. It deliberately avoids echoing the raw
// driver/network error so responses never leak internal hostnames, IPs, ports
// or TLS/certificate details. Callers that need the full error must log it
// server-side instead of returning it to the client.
func SanitizeStorageConnectivityError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "Endpoint url cannot have fully qualified paths"):
		return "Endpoint 地址格式错误：请去除 http:// 或 https:// 前缀，只填写域名或 IP 地址和端口（例如：minio.example.com:9000）"
	case strings.Contains(msg, "no such host"):
		return "DNS 解析失败，请检查地址是否正确"
	case strings.Contains(msg, "connection refused"):
		return "连接被拒绝，请确认服务已启动且端口正确"
	case strings.Contains(msg, "no route to host"):
		return "无法路由到目标地址，请检查网络配置"
	case strings.Contains(msg, "i/o timeout") || strings.Contains(msg, "deadline exceeded") || strings.Contains(msg, "context deadline"):
		return "连接超时，请检查网络或服务状态"
	case strings.Contains(msg, "403") || strings.Contains(msg, "AccessDenied") || strings.Contains(msg, "access denied"):
		return "认证失败，请检查访问凭证是否正确"
	case strings.Contains(msg, "certificate") || strings.Contains(msg, "tls") || strings.Contains(msg, "x509"):
		return "TLS/SSL 证书错误，请检查 SSL 配置"
	case strings.Contains(msg, "404") || strings.Contains(msg, "NoSuchBucket"):
		return "Bucket 不存在，请检查名称和 Region"
	default:
		return "连接失败，请检查配置参数是否正确"
	}
}
