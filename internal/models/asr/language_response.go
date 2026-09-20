package asr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// asrLanguageTransport 在 SDK 解码前规范化转写响应的语言字段，保留原有请求和安全传输层。
type asrLanguageTransport struct {
	base http.RoundTripper // 原有传输层，负责连接及 SSRF 校验。
}

// RoundTrip 仅适配成功的转写响应；上游 HTTP 错误仍交给 SDK 按原规则处理。
func (t *asrLanguageTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices ||
		!strings.HasSuffix(req.URL.Path, "/audio/transcriptions") {
		return resp, nil
	}
	defer func(body io.ReadCloser) {
		// 读取错误单独返回；关闭已读取的响应流不应覆盖主要结果。
		_ = body.Close()
	}(resp.Body)

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read ASR response: %w", err)
	}
	body, err = normalizeASRLanguage(body)
	if err != nil {
		return nil, fmt.Errorf("decode ASR response: %w", err)
	}
	resp.Body = io.NopCloser(bytes.NewReader(body))
	resp.ContentLength = int64(len(body))
	resp.Header.Set("Content-Length", strconv.Itoa(len(body)))
	return resp, nil
}

// normalizeASRLanguage 保留字符串并展开单元素字符串数组；多语言或未知类型置为 null。
// 当前转写业务不消费语言元数据，不能猜测主要语言；正文和时间戳仍由 SDK 校验。
func normalizeASRLanguage(body []byte) ([]byte, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	language, exists := fields["language"]
	if !exists {
		return body, nil
	}
	var single string
	if json.Unmarshal(language, &single) == nil {
		// JSON null 与合法字符串均可直接交给 SDK。
		return body, nil
	}
	var languages []json.RawMessage
	fields["language"] = json.RawMessage("null")
	if json.Unmarshal(language, &languages) == nil && len(languages) == 1 {
		if json.Unmarshal(languages[0], &single) == nil {
			fields["language"] = languages[0]
		}
	}
	return json.Marshal(fields)
}
