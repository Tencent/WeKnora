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
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
)

type client struct {
	baseURL, appKey, appSecret, operatorID string
	httpClient                             *http.Client
	token                                  string
}

func newClient(c *Config) *client {
	return &client{c.baseURL(), c.AppKey, c.AppSecret, c.OperatorID, datasource.NewConnectorHTTPClient(30 * time.Second), ""}
}

func (c *client) request(ctx context.Context, method, path string, body any, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("x-acs-dingtalk-access-token", c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return fmt.Errorf("%w: dingtalk status=%d", datasource.ErrInvalidCredentials, resp.StatusCode)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("dingtalk api status=%d: %s", resp.StatusCode, string(b))
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func (c *client) authenticate(ctx context.Context) error {
	var out struct {
		AccessToken string `json:"accessToken"`
	}
	if err := c.request(ctx, http.MethodPost, "/v1.0/oauth2/accessToken", map[string]string{"appKey": c.appKey, "appSecret": c.appSecret}, &out); err != nil {
		return err
	}
	if out.AccessToken == "" {
		return fmt.Errorf("%w: empty access token", datasource.ErrInvalidCredentials)
	}
	c.token = out.AccessToken
	return nil
}

func (c *client) listWorkspaces(ctx context.Context) ([]workspace, error) {
	var all []workspace
	for page := 1; ; page++ {
		q := url.Values{"operatorId": {c.operatorID}, "pageNumber": {strconv.Itoa(page)}, "pageSize": {"50"}, "status": {"0"}}
		var out struct {
			Workspaces []struct {
				WorkspaceID    string `json:"workspaceId"`
				WorkspaceName  string `json:"workspaceName"`
				RootDentryUUID string `json:"rootDentryUuid"`
				URL            string `json:"url"`
				CreateTime     string `json:"createTime"`
			} `json:"workspaces"`
		}
		if err := c.request(ctx, http.MethodGet, "/v2.0/wiki/org/workspaces?"+q.Encode(), nil, &out); err != nil {
			return nil, err
		}
		for _, w := range out.Workspaces {
			all = append(all, workspace{w.WorkspaceID, w.WorkspaceName, w.RootDentryUUID, w.URL, w.CreateTime})
		}
		if len(out.Workspaces) < 50 {
			return all, nil
		}
	}
}

func (c *client) listNodes(ctx context.Context, parent string) ([]node, error) {
	var all []node
	next := ""
	for {
		q := url.Values{"operatorId": {c.operatorID}, "parentNodeId": {parent}, "maxResults": {"50"}}
		if next != "" {
			q.Set("nextToken", next)
		}
		var out struct {
			NextToken string `json:"nextToken"`
			Nodes     []struct {
				NodeID       string `json:"nodeId"`
				Name         string `json:"name"`
				Type         string `json:"type"`
				Category     string `json:"category"`
				Extension    string `json:"extension"`
				URL          string `json:"url"`
				ModifiedTime string `json:"modifiedTime"`
				WorkspaceID  string `json:"workspaceId"`
				HasChildren  bool   `json:"hasChildren"`
			} `json:"nodes"`
		}
		if err := c.request(ctx, http.MethodGet, "/v2.0/wiki/nodes?"+q.Encode(), nil, &out); err != nil {
			return nil, err
		}
		for _, n := range out.Nodes {
			all = append(all, node{n.NodeID, n.Name, n.Type, n.Category, n.Extension, n.URL, n.ModifiedTime, n.WorkspaceID, n.HasChildren})
		}
		if out.NextToken == "" {
			return all, nil
		}
		next = out.NextToken
	}
}

func (c *client) documentContent(ctx context.Context, id string) ([]byte, error) {
	q := url.Values{"operatorId": {c.operatorID}, "targetFormat": {"markdown"}}
	var start struct {
		TaskID int64 `json:"taskId"`
	}
	if err := c.request(ctx, http.MethodGet, "/v2.0/doc/query/"+url.PathEscape(id)+"/contents?"+q.Encode(), nil, &start); err != nil {
		return nil, err
	}
	if start.TaskID == 0 {
		return nil, fmt.Errorf("dingtalk returned an empty content task id")
	}
	for i := 0; i < 20; i++ {
		var job struct {
			Status     int    `json:"status"`
			ContentKey string `json:"contentKey"`
			Content    string `json:"content"`
		}
		jq := url.Values{"operatorId": {c.operatorID}, "taskId": {strconv.FormatInt(start.TaskID, 10)}}
		if err := c.request(ctx, http.MethodGet, "/v2.0/doc/contents/"+url.PathEscape(id)+"/jobStatuses?"+jq.Encode(), nil, &job); err != nil {
			return nil, err
		}
		if job.Content != "" {
			return []byte(job.Content), nil
		}
		// DingTalk only guarantees that contentKey is present once the async
		// conversion is ready; status values have changed between API revisions.
		// Prefer the completion artifact over hard-coding a success enum.
		if job.ContentKey != "" {
			if u, err := url.Parse(job.ContentKey); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
				return c.download(ctx, job.ContentKey)
			}
			return []byte(job.ContentKey), nil
		}
		if job.Status < 0 {
			return nil, fmt.Errorf("dingtalk content export failed (status %d)", job.Status)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("dingtalk content export timed out")
}
func (c *client) download(ctx context.Context, u string) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("download content status=%d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 16<<20))
}
