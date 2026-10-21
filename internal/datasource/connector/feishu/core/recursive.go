package core

import (
	"net/url"
	"os"
	"strconv"
	"strings"
)

const (
	envRecursiveLinks    = "FEISHU_RECURSIVE_LINKS"
	envRecursiveMaxDepth = "FEISHU_RECURSIVE_MAX_DEPTH"

	defaultRecursiveMaxDepth = 3
	maxRecursiveMaxDepth     = 10
)

// LinkedResource 表示从 Bitable / Sheet 单元格中发现的飞书资源。
type LinkedResource struct {
	Type    string
	Token   string
	URL     string
	TableID string
	ViewID  string
}

// RecursiveLinksEnabled 控制飞书内容中链接递归。
// 默认关闭，灰度验证通过后在生产 .env 中显式开启。
func RecursiveLinksEnabled() bool {
	raw := strings.TrimSpace(
		strings.ToLower(os.Getenv(envRecursiveLinks)),
	)

	switch raw {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// RecursiveMaxDepth 返回最大递归深度。
func RecursiveMaxDepth() int {
	raw := strings.TrimSpace(
		os.Getenv(envRecursiveMaxDepth),
	)

	if raw == "" {
		return defaultRecursiveMaxDepth
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return defaultRecursiveMaxDepth
	}

	if n > maxRecursiveMaxDepth {
		return maxRecursiveMaxDepth
	}

	return n
}

// ParseLinkedResourceURL 识别公司飞书文档中的常见资源链接。
func ParseLinkedResourceURL(raw string) (LinkedResource, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return LinkedResource{}, false
	}

	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return LinkedResource{}, false
	}

	host := strings.ToLower(u.Hostname())

	if u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return LinkedResource{}, false
	}
	if !strings.HasSuffix(host, ".feishu.cn") &&
		!strings.HasSuffix(host, ".larksuite.com") {
		return LinkedResource{}, false
	}

	parts := strings.Split(
		strings.Trim(u.Path, "/"),
		"/",
	)

	if len(parts) < 2 {
		return LinkedResource{}, false
	}

	resource := LinkedResource{
		URL:     raw,
		TableID: u.Query().Get("table"),
		ViewID:  u.Query().Get("view"),
	}

	switch parts[0] {
	case "base":
		resource.Type = "bitable"
		resource.Token = parts[1]

	case "wiki":
		resource.Type = "wiki"
		resource.Token = parts[1]

	case "docx":
		resource.Type = "docx"
		resource.Token = parts[1]

	case "sheets":
		resource.Type = "sheet"
		resource.Token = parts[1]

	case "file":
		resource.Type = "file"
		resource.Token = parts[1]

	case "drive":
		if len(parts) >= 3 && parts[1] == "folder" {
			resource.Type = "folder"
			resource.Token = parts[2]
		}

	default:
		return LinkedResource{}, false
	}

	if resource.Type == "" || resource.Token == "" {
		return LinkedResource{}, false
	}

	return resource, true
}
