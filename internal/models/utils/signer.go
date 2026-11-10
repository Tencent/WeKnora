package utils

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	nonceChars  = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	nonceLength = 16
)

// Sign 按 WeKnoraCloud 参考实现生成请求头。
// appID: 上游 APPID
// apiKey: 上游 API Key（当前沿用 AppSecret 字段承载）
// requestID: 每次请求唯一的 UUID 字符串
// bodyJSON: 请求体 JSON 字符串，空请求体传 "{}"
func Sign(appID, apiKey, requestID, bodyJSON string) map[string]string {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := generateNonce(nonceLength)

	bodyForHash := bodyJSON
	if bodyForHash == "" {
		bodyForHash = "{}"
	}
	bodyMD5 := md5Hex(bodyForHash)

	params := map[string]string{
		"x-appid":      appID,
		"x-api-key":    apiKey,
		"x-request-id": requestID,
		"x-timestamp":  timestamp,
		"x-nonce":      nonce,
		"body":         bodyMD5,
	}

	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, rfc3986Encode(k)+"="+rfc3986Encode(params[k]))
	}
	signature := md5Hex(strings.Join(parts, "&"))

	return map[string]string{
		"X-APPID":      appID,
		"X-API-Key":    apiKey,
		"X-Request-ID": requestID,
		"X-Timestamp":  timestamp,
		"X-Nonce":      nonce,
		"X-Signature":  signature,
	}
}

func md5Hex(s string) string {
	h := md5.New()
	h.Write([]byte(s))
	return fmt.Sprintf("%x", h.Sum(nil))
}

func generateNonce(length int) string {
	b := make([]byte, length)
	for i := range b {
		b[i] = nonceChars[rand.Intn(len(nonceChars))]
	}
	return string(b)
}

// rfc3986Encode 对字符串做 RFC3986 编码
// 保留字符：A-Z a-z 0-9 - _ . ~
func rfc3986Encode(s string) string {
	var buf bytes.Buffer
	for _, r := range s {
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' ||
			r == '.' || r == '~' {
			buf.WriteRune(r)
		} else {
			fmt.Fprintf(&buf, "%%%02X", r)
		}
	}
	return buf.String()
}
