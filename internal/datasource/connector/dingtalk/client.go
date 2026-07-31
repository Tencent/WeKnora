package dingtalk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
)

const userAgent = "WeKnora-DingTalk-Connector/1.0"

type client struct {
	baseURL    string
	clientID   string
	appSecret  string
	unionID    string
	httpClient *http.Client
}

func newClient(cfg *Config) *client {
	return &client{
		baseURL:    cfg.BaseURL,
		clientID:   cfg.ClientID,
		appSecret:  cfg.AppSecret,
		unionID:    cfg.UnionID,
		httpClient: datasource.NewConnectorHTTPClient(90 * time.Second),
	}
}

func (c *client) accessToken(ctx context.Context) (string, error) {
	var resp map[string]interface{}
	if err := c.doJSON(ctx, http.MethodPost, "/v1.0/oauth2/accessToken", "", map[string]string{
		"appKey":    c.clientID,
		"appSecret": c.appSecret,
	}, &resp); err != nil {
		return "", err
	}
	token := firstString(resp, "access_token", "accessToken")
	if token == "" {
		return "", fmt.Errorf("dingtalk access_token missing")
	}
	return token, nil
}

func (c *client) listSpaces(ctx context.Context, token string) ([]space, error) {
	var out []space
	next := ""
	for {
		q := url.Values{}
		q.Set("unionId", c.unionID)
		q.Set("spaceType", "org")
		q.Set("maxResults", "50")
		if next != "" {
			q.Set("nextToken", next)
		}
		var resp map[string]interface{}
		if err := c.doJSON(ctx, http.MethodGet, "/v1.0/drive/spaces?"+q.Encode(), token, nil, &resp); err != nil {
			return nil, err
		}
		for _, raw := range firstArray(resp, "spaces", "items", "data", "result") {
			item := unwrap(raw)
			id := firstString(item, "spaceId", "id")
			if id == "" {
				continue
			}
			name := firstString(item, "spaceName", "name", "title")
			if name == "" {
				name = id
			}
			out = append(out, space{ID: id, Name: name})
		}
		next = firstString(resp, "nextToken")
		if next == "" {
			next = firstString(asMap(resp["result"]), "nextToken")
		}
		if next == "" {
			break
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no DingTalk drive spaces found for unionId %s", c.unionID)
	}
	return out, nil
}

func (c *client) listAllEntries(ctx context.Context, token, spaceID string, limit int) ([]driveEntry, error) {
	entries, err := c.listAllStorageEntries(ctx, token, spaceID, limit)
	if err == nil {
		return entries, nil
	}
	legacy, legacyErr := c.listAllDriveEntries(ctx, token, spaceID, limit)
	if legacyErr != nil {
		return nil, fmt.Errorf("storage API failed: %v; drive API failed: %w", err, legacyErr)
	}
	return legacy, nil
}

func (c *client) listAllStorageEntries(ctx context.Context, token, spaceID string, limit int) ([]driveEntry, error) {
	var out []driveEntry
	next := ""
	for {
		option := map[string]interface{}{
			"maxResults":    50,
			"order":         "DESC",
			"withThumbnail": false,
		}
		if next != "" {
			option["nextToken"] = next
		}
		path := "/v1.0/storage/spaces/" + url.PathEscape(spaceID) +
			"/dentries/listAll?unionId=" + url.QueryEscape(c.unionID)
		var resp map[string]interface{}
		if err := c.doJSON(ctx, http.MethodPost, path, token, map[string]interface{}{"option": option}, &resp); err != nil {
			return nil, err
		}
		for _, raw := range firstArray(resp, "dentries", "items", "data", "result") {
			entry := parseDriveEntry(raw)
			if entry.ID == "" {
				continue
			}
			out = append(out, entry)
			if len(out) >= limit {
				return nil, fmt.Errorf("DingTalk directory contains more than %d entries", limit)
			}
		}
		next = firstString(resp, "nextToken")
		if next == "" {
			next = firstString(asMap(resp["result"]), "nextToken")
		}
		if next == "" {
			next = firstString(asMap(resp["data"]), "nextToken")
		}
		if next == "" {
			break
		}
	}
	return out, nil
}

func (c *client) listAllDriveEntries(ctx context.Context, token, spaceID string, limit int) ([]driveEntry, error) {
	var out []driveEntry
	queue := []string{"0"}
	seen := map[string]bool{}
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		if seen[parent] {
			continue
		}
		seen[parent] = true
		next := ""
		for {
			q := url.Values{}
			q.Set("parentId", parent)
			q.Set("maxResults", "100")
			q.Set("unionId", c.unionID)
			if next != "" {
				q.Set("nextToken", next)
			}
			path := "/v1.0/drive/spaces/" + url.PathEscape(spaceID) + "/files?" + q.Encode()
			var resp map[string]interface{}
			if err := c.doJSON(ctx, http.MethodGet, path, token, nil, &resp); err != nil {
				return nil, err
			}
			for _, raw := range firstArray(resp, "files", "items", "data", "result") {
				entry := parseDriveEntry(raw)
				if entry.ID == "" {
					continue
				}
				out = append(out, entry)
				if entry.isFolder() {
					queue = append(queue, entry.ID)
				}
				if len(out) >= limit {
					return nil, fmt.Errorf("DingTalk directory contains more than %d entries", limit)
				}
			}
			next = firstString(resp, "nextToken")
			if next == "" {
				next = firstString(asMap(resp["result"]), "nextToken")
			}
			if next == "" {
				break
			}
		}
	}
	return out, nil
}

func (c *client) downloadInfo(ctx context.Context, token, spaceID, fileID string) (string, map[string]string, error) {
	path := "/v1.0/storage/spaces/" + url.PathEscape(spaceID) + "/dentries/" +
		url.PathEscape(fileID) + "/downloadInfos/query?unionId=" + url.QueryEscape(c.unionID)
	var resp map[string]interface{}
	err := c.doJSON(ctx, http.MethodPost, path, token,
		map[string]interface{}{"option": map[string]interface{}{"preferIntranet": false}}, &resp)
	if err != nil {
		legacyPath := "/v1.0/drive/spaces/" + url.PathEscape(spaceID) + "/files/" +
			url.PathEscape(fileID) + "/downloadInfos?unionId=" + url.QueryEscape(c.unionID)
		if legacyErr := c.doJSON(ctx, http.MethodGet, legacyPath, token, nil, &resp); legacyErr != nil {
			return "", nil, fmt.Errorf("storage downloadInfo failed: %v; drive downloadInfo failed: %w", err, legacyErr)
		}
	}
	node := asMap(resp["downloadInfo"])
	if len(node) == 0 {
		node = asMap(resp["result"])
	}
	if len(node) == 0 {
		node = resp
	}
	downloadURL := firstString(node, "resourceUrl", "downloadUrl", "url", "downloadURL")
	if downloadURL == "" {
		downloadURL = firstArrayURL(node, "resourceUrls", "internalResourceUrls", "urls")
	}
	if downloadURL == "" {
		downloadURL = firstStringRecursive(resp, "resourceUrl", "downloadUrl", "url", "downloadURL")
	}
	if downloadURL == "" {
		return "", nil, fmt.Errorf("DingTalk download URL missing")
	}
	headers := map[string]string{}
	for k, v := range asMap(node["headers"]) {
		if s, ok := v.(string); ok {
			headers[k] = s
		}
	}
	return downloadURL, headers, nil
}

func (c *client) download(ctx context.Context, rawURL string, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFileBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxFileBytes {
		return nil, fmt.Errorf("file exceeds 50MB")
	}
	return body, nil
}

func (c *client) readDocument(ctx context.Context, token, documentID string) ([]byte, error) {
	path := "/v1.0/doc/suites/documents/" + url.PathEscape(documentID) +
		"/blocks?operatorId=" + url.QueryEscape(c.unionID)
	var resp map[string]interface{}
	if err := c.doJSON(ctx, http.MethodGet, path, token, nil, &resp); err != nil {
		return nil, err
	}
	var texts []string
	collectText(resp, &texts)
	content := strings.TrimSpace(strings.Join(texts, "\n"))
	if content == "" {
		return nil, fmt.Errorf("DingTalk document has no readable text blocks")
	}
	return []byte(content), nil
}

func (c *client) doJSON(ctx context.Context, method, path, token string, payload interface{}, out interface{}) error {
	var payloadBytes []byte
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		payloadBytes = b
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		var body io.Reader
		if payloadBytes != nil {
			body = bytes.NewReader(payloadBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", userAgent)
		if token != "" {
			req.Header.Set("x-acs-dingtalk-access-token", token)
		}
		if payloadBytes != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			if sleepErr := sleepCtx(ctx, time.Duration(attempt+1)*800*time.Millisecond); sleepErr != nil {
				return sleepErr
			}
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("DingTalk API temporary failure HTTP %d: %s", resp.StatusCode, truncate(string(data), 300))
			if attempt < 2 {
				if sleepErr := sleepCtx(ctx, time.Duration(attempt+1)*800*time.Millisecond); sleepErr != nil {
					return sleepErr
				}
				continue
			}
		}
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return fmt.Errorf("%w: DingTalk permission denied HTTP %d: %s", datasource.ErrInvalidCredentials, resp.StatusCode, truncate(string(data), 500))
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return fmt.Errorf("DingTalk API HTTP %d: %s", resp.StatusCode, truncate(string(data), 500))
		}
		if out != nil && len(data) > 0 {
			if err := json.Unmarshal(data, out); err != nil {
				return fmt.Errorf("decode DingTalk response: %w", err)
			}
		}
		return nil
	}
	return lastErr
}

func parseDriveEntry(raw interface{}) driveEntry {
	node := unwrap(raw)
	modified := firstTime(node, "modifyTime", "modifiedTime", "updatedAt", "modifiedAt")
	return driveEntry{
		ID:         firstString(node, "dentryId", "fileId", "nodeId", "id"),
		ParentID:   firstString(node, "parentId", "parentDentryId", "parentNodeId"),
		Name:       firstString(node, "name", "fileName", "title"),
		Path:       firstString(node, "filePath", "path", "displayPath"),
		Type:       firstString(node, "fileType", "dentryType", "type", "entryType"),
		MediaType:  firstString(node, "mediaType", "mimeType", "contentType", "fileExtension", "extension", "category"),
		Size:       firstInt64(node, "size", "fileSize", "sizeBytes"),
		ModifiedAt: modified,
		Version:    firstString(node, "version", "revision", "versionId"),
		URL:        firstString(node, "url", "webUrl", "shareUrl"),
		DocKey:     firstString(node, "docKey", "documentId", "nodeId", "uuid"),
	}
}

func unwrap(raw interface{}) map[string]interface{} {
	node := asMap(raw)
	if child := asMap(node["dentry"]); len(child) > 0 {
		return child
	}
	if child := asMap(node["item"]); len(child) > 0 {
		return child
	}
	return node
}

func asMap(raw interface{}) map[string]interface{} {
	if m, ok := raw.(map[string]interface{}); ok {
		return m
	}
	return map[string]interface{}{}
}

func firstString(m map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch t := v.(type) {
			case string:
				if strings.TrimSpace(t) != "" {
					return strings.TrimSpace(t)
				}
			case float64:
				return strconv.FormatInt(int64(t), 10)
			case json.Number:
				return t.String()
			}
		}
	}
	return ""
}

func firstArray(m map[string]interface{}, keys ...string) []interface{} {
	for _, key := range keys {
		if arr, ok := m[key].([]interface{}); ok {
			return arr
		}
		for _, child := range []string{"result", "data"} {
			if arr, ok := asMap(m[child])[key].([]interface{}); ok {
				return arr
			}
		}
	}
	return nil
}

func firstArrayURL(m map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		arr, ok := m[key].([]interface{})
		if !ok || len(arr) == 0 {
			continue
		}
		if s, ok := arr[0].(string); ok {
			return s
		}
		if s := firstString(asMap(arr[0]), "url", "resourceUrl", "downloadUrl"); s != "" {
			return s
		}
	}
	return ""
}

func firstStringRecursive(raw interface{}, keys ...string) string {
	if m, ok := raw.(map[string]interface{}); ok {
		if s := firstString(m, keys...); s != "" {
			return s
		}
		for _, v := range m {
			if s := firstStringRecursive(v, keys...); s != "" {
				return s
			}
		}
	}
	if arr, ok := raw.([]interface{}); ok {
		for _, v := range arr {
			if s := firstStringRecursive(v, keys...); s != "" {
				return s
			}
		}
	}
	return ""
}

func firstInt64(m map[string]interface{}, keys ...string) int64 {
	for _, k := range keys {
		switch v := m[k].(type) {
		case float64:
			return int64(v)
		case int64:
			return v
		case json.Number:
			n, _ := v.Int64()
			return n
		case string:
			n, _ := strconv.ParseInt(v, 10, 64)
			return n
		}
	}
	return 0
}

func firstTime(m map[string]interface{}, keys ...string) time.Time {
	for _, k := range keys {
		switch v := m[k].(type) {
		case float64:
			raw := int64(v)
			if raw > 9999999999 {
				return time.UnixMilli(raw)
			}
			return time.Unix(raw, 0)
		case string:
			if t, err := time.Parse(time.RFC3339, v); err == nil {
				return t
			}
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				if n > 9999999999 {
					return time.UnixMilli(n)
				}
				return time.Unix(n, 0)
			}
		}
	}
	return time.Time{}
}

func collectText(raw interface{}, out *[]string) {
	switch node := raw.(type) {
	case map[string]interface{}:
		for k, v := range node {
			if k == "text" {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					*out = append(*out, strings.TrimSpace(s))
					continue
				}
			}
			collectText(v, out)
		}
	case []interface{}:
		for _, v := range node {
			collectText(v, out)
		}
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
