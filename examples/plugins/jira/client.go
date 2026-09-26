package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/pluginsdk/pluginapi"
)

// gatewayURL is Atlassian's API gateway, where OAuth tokens are used. Tests
// point it at a fake.
var gatewayURL = "https://api.atlassian.com"

// httpClient honors HTTPS_PROXY: a host plugin's traffic must go through the
// plugin host's egress proxy.
var httpClient = &http.Client{
	Timeout:   30 * time.Second,
	Transport: &http.Transport{Proxy: http.ProxyFromEnvironment},
}

// Credential fields, as the instance schema names them.
type credentials struct {
	Auth     string `json:"auth"`
	Account  string `json:"account"` // an OAuth access token by the time it reaches us
	Site     string `json:"site"`    // cloud ID
	BaseURL  string `json:"base_url"`
	Email    string `json:"email"`
	APIToken string `json:"api_token"`
}

// client calls one Jira Cloud site's REST API.
type client struct {
	api     string // REST root: the site, or the gateway's /ex/jira/{cloudId}
	siteURL string // for links to issues
	auth    string // Authorization header
}

// site is a Jira site an OAuth token can reach.
type site struct {
	ID   string `json:"id"`
	URL  string `json:"url"`
	Name string `json:"name"`
}

// accessibleSites lists the sites an OAuth token was granted.
func accessibleSites(ctx context.Context, token string) ([]site, error) {
	c := &client{api: gatewayURL, auth: "Bearer " + token}
	var sites []site
	err := c.do(ctx, http.MethodGet, "/oauth/token/accessible-resources", nil, &sites)
	return sites, err
}

// newClient checks the credentials and connects to the site they name.
// Problems with the form come back as invalid_config on its fields.
func newClient(ctx context.Context, cr credentials, locale string) (*client, error) {
	switch cr.Auth {
	case "", "oauth":
		if cr.Account == "" {
			return nil, pluginapi.InvalidConfig(say(locale, msgConnectAccount),
				map[string]string{"credentials.account": "required"})
		}
		if cr.Site == "" {
			return nil, pluginapi.InvalidConfig(say(locale, msgChooseSite),
				map[string]string{"credentials.site": "required"})
		}
		sites, err := accessibleSites(ctx, cr.Account)
		if err != nil {
			return nil, err
		}
		for _, s := range sites {
			if s.ID == cr.Site {
				return &client{
					api:     gatewayURL + "/ex/jira/" + url.PathEscape(s.ID),
					siteURL: strings.TrimSuffix(s.URL, "/"),
					auth:    "Bearer " + cr.Account,
				}, nil
			}
		}
		return nil, pluginapi.InvalidConfig(say(locale, msgSiteGone),
			map[string]string{"credentials.site": "not granted"})
	case "token":
		base := strings.TrimSuffix(strings.TrimSpace(cr.BaseURL), "/")
		fields := map[string]string{}
		if base == "" {
			fields["credentials.base_url"] = "required"
		}
		if cr.Email == "" {
			fields["credentials.email"] = "required"
		}
		if cr.APIToken == "" {
			fields["credentials.api_token"] = "required"
		}
		if len(fields) > 0 {
			return nil, pluginapi.InvalidConfig(say(locale, msgTokenFields), fields)
		}
		basic := cr.Email + ":" + cr.APIToken
		return &client{
			api: base, siteURL: base,
			auth: "Basic " + base64.StdEncoding.EncodeToString([]byte(basic)),
		}, nil
	default:
		return nil, pluginapi.InvalidConfig(say(locale, msgUnknownAuth),
			map[string]string{"credentials.auth": "enum"})
	}
}

// do sends one request and decodes a JSON answer. Jira's failures map to
// protocol errors: WeKnora retries unavailable and rate_limited, and shows
// unauthorized as broken credentials.
func (c *client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.api+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return pluginapi.Errorf(pluginapi.CodeUnavailable, "jira: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return pluginapi.Errorf(pluginapi.CodeUnavailable, "jira: %v", err)
	}
	if resp.StatusCode >= 300 {
		return statusError(resp, data)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("jira %s: decode: %w", path, err)
	}
	return nil
}

func statusError(resp *http.Response, body []byte) error {
	msg := jiraMessage(body)
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return pluginapi.Errorf(
			pluginapi.CodeUnauthorized,
			"jira refused the credentials (%d): %s",
			resp.StatusCode,
			msg,
		)
	case resp.StatusCode == http.StatusTooManyRequests:
		e := pluginapi.Errorf(pluginapi.CodeRateLimited, "jira rate limit: %s", msg)
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
			e.Details = &pluginapi.ErrorDetails{RetryAfter: s}
		}
		return e
	case resp.StatusCode >= 500:
		return pluginapi.Errorf(pluginapi.CodeUnavailable, "jira %d: %s", resp.StatusCode, msg)
	case resp.StatusCode == http.StatusBadRequest:
		// Mostly a JQL filter Jira does not accept.
		return pluginapi.InvalidConfig("jira rejected the query: "+msg,
			map[string]string{"settings.jql": msg})
	default:
		return fmt.Errorf("jira %d: %s", resp.StatusCode, msg)
	}
}

// jiraMessage extracts Jira's error text ({"errorMessages": [...]}).
func jiraMessage(body []byte) string {
	var e struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
		Message       string            `json:"message"`
	}
	if json.Unmarshal(body, &e) == nil {
		parts := append([]string{}, e.ErrorMessages...)
		for k, v := range e.Errors {
			parts = append(parts, k+": "+v)
		}
		if e.Message != "" {
			parts = append(parts, e.Message)
		}
		if len(parts) > 0 {
			return strings.Join(parts, "; ")
		}
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// myself is the signed-in user. Its time zone is the one JQL dates are in.
type myself struct {
	AccountID   string `json:"accountId"`
	DisplayName string `json:"displayName"`
	TimeZone    string `json:"timeZone"`
}

func (c *client) myself(ctx context.Context) (*myself, error) {
	var me myself
	if err := c.do(ctx, http.MethodGet, "/rest/api/3/myself", nil, &me); err != nil {
		return nil, err
	}
	return &me, nil
}

type project struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Style       string `json:"style"`
}

// projects lists every project the user can browse.
func (c *client) projects(ctx context.Context) ([]project, error) {
	var out []project
	for start := 0; ; {
		var page struct {
			Values []project `json:"values"`
			IsLast bool      `json:"isLast"`
		}
		q := url.Values{"startAt": {strconv.Itoa(start)}, "maxResults": {"50"}, "orderBy": {"name"}}
		if err := c.do(ctx, http.MethodGet, "/rest/api/3/project/search?"+q.Encode(), nil, &page); err != nil {
			return nil, err
		}
		out = append(out, page.Values...)
		if page.IsLast || len(page.Values) == 0 {
			return out, nil
		}
		start += len(page.Values)
	}
}

type issueType struct {
	Name    string `json:"name"`
	Subtask bool   `json:"subtask"`
}

func (c *client) issueTypes(ctx context.Context) ([]issueType, error) {
	var out []issueType
	err := c.do(ctx, http.MethodGet, "/rest/api/3/issuetype", nil, &out)
	return out, err
}

// issue is the part of a Jira issue the connector turns into a document.
type issue struct {
	ID     string `json:"id"`
	Key    string `json:"key"`
	Fields struct {
		Summary     string          `json:"summary"`
		Description json.RawMessage `json:"description"`
		Status      *named          `json:"status"`
		IssueType   *named          `json:"issuetype"`
		Priority    *named          `json:"priority"`
		Resolution  *named          `json:"resolution"`
		Assignee    *user           `json:"assignee"`
		Reporter    *user           `json:"reporter"`
		Labels      []string        `json:"labels"`
		Created     jiraTime        `json:"created"`
		Updated     jiraTime        `json:"updated"`
		Project     *project        `json:"project"`
		Parent      *struct {
			Key string `json:"key"`
		} `json:"parent"`
		Comment *struct {
			Comments []comment `json:"comments"`
			Total    int       `json:"total"`
		} `json:"comment"`
	} `json:"fields"`
}

type named struct {
	Name string `json:"name"`
}

type user struct {
	DisplayName string `json:"displayName"`
}

type comment struct {
	Author  *user           `json:"author"`
	Created jiraTime        `json:"created"`
	Body    json.RawMessage `json:"body"`
}

// jiraTime parses Jira's timestamps ("2026-09-26T10:04:05.123+0800").
type jiraTime struct{ time.Time }

func (t *jiraTime) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil || s == "" {
		return nil
	}
	for _, layout := range []string{"2006-01-02T15:04:05.000-0700", time.RFC3339Nano} {
		if v, err := time.Parse(layout, s); err == nil {
			t.Time = v
			return nil
		}
	}
	return nil
}

var searchFields = []string{
	"summary", "description", "status", "issuetype", "priority", "resolution", "assignee", "reporter",
	"labels", "created", "updated", "project", "parent", "comment",
}

// search runs JQL a page at a time (the enhanced search API, which pages
// with a token). fn sees each page; returning an error stops the search.
func (c *client) search(ctx context.Context, jql string, withComments bool, fn func([]issue) error) error {
	fields := searchFields
	if !withComments {
		fields = fields[:len(fields)-1]
	}
	token := ""
	for {
		body := map[string]any{"jql": jql, "maxResults": 50, "fields": fields}
		if token != "" {
			body["nextPageToken"] = token
		}
		var page struct {
			Issues        []issue `json:"issues"`
			NextPageToken string  `json:"nextPageToken"`
			IsLast        bool    `json:"isLast"`
		}
		if err := c.do(ctx, http.MethodPost, "/rest/api/3/search/jql", body, &page); err != nil {
			return err
		}
		if err := fn(page.Issues); err != nil {
			return err
		}
		if page.IsLast || page.NextPageToken == "" || len(page.Issues) == 0 {
			return nil
		}
		token = page.NextPageToken
	}
}
