// Package cubeclient is a thin REST client for the Cube (cubejs/cube)
// semantic layer. WeKnora signs its own HS256 JWTs with CUBEJS_API_SECRET;
// the JWT payload minus iat/exp is the securityContext Cube exposes to
// contextToGroups / driverFactory / accessPolicy checks.
package cubeclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// SecurityContext is the identity embedded into every Cube request. Groups
// come from the caller's WeKnora data groups; Cube's accessPolicy decides
// which models/members/rows the identity may see.
type SecurityContext struct {
	Sub      string   `json:"sub,omitempty"`
	Groups   []string `json:"groups,omitempty"`
	TenantID uint64   `json:"tenant_id,omitempty"`
}

// Client talks to the Cube REST API (base URL ending in /cubejs-api/v1).
type Client struct {
	endpoint string
	secret   string
	http     *http.Client
}

// New builds a client. endpoint example: http://cube:4000/cubejs-api/v1
func New(endpoint, secret string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	return &Client{
		endpoint: strings.TrimRight(endpoint, "/"),
		secret:   secret,
		http:     &http.Client{Timeout: timeout},
	}
}

// Endpoint returns the configured API base URL (for health checks).
func (c *Client) Endpoint() string { return c.endpoint }

// sign issues a short-lived JWT whose payload is the securityContext.
func (c *Client) sign(secCtx SecurityContext) (string, error) {
	claims := jwt.MapClaims{
		"iat": time.Now().UTC().Unix(),
		"exp": time.Now().UTC().Add(time.Hour).Unix(),
	}
	if secCtx.Sub != "" {
		claims["sub"] = secCtx.Sub
	}
	if len(secCtx.Groups) > 0 {
		claims["groups"] = secCtx.Groups
	}
	if secCtx.TenantID != 0 {
		claims["tenant_id"] = secCtx.TenantID
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(c.secret))
}

// do performs an authenticated request and decodes the JSON response.
func (c *Client) do(ctx context.Context, _, path string, query url.Values, body any, out any) error {
	if c.endpoint == "" {
		return fmt.Errorf("cube not configured (CUBE_API_URL / CUBEJS_API_SECRET)")
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	u := c.endpoint + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	token, err := c.sign(SecurityContextFromContext(ctx))
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, rdr)
	if err != nil {
		return err
	}
	if body != nil {
		req.Method = http.MethodPost
	}
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("cube request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		// Cube returns {"error": "..."} with a human-readable message.
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &errResp) == nil && errResp.Error != "" {
			return fmt.Errorf("cube error: %s", errResp.Error)
		}
		return fmt.Errorf("cube request failed: HTTP %d: %s", resp.StatusCode, truncate(string(data), 512))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("cube response parse failed: %w", err)
		}
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// MetaCube is one entry of /v1/meta.
type MetaCube struct {
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Type        string          `json:"type"` // "cube" or "view"
	Measures    []MetaMember    `json:"measures"`
	Dimensions  []MetaMember    `json:"dimensions"`
	Segments    []MetaMember    `json:"segments"`
	Joins       json.RawMessage `json:"joins,omitempty"`
}

// MetaMember is one measure/dimension/segment in the meta response.
type MetaMember struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Type        string `json:"type"`
	// AggType is the aggregation of a measure (sum/count/avg/...).
	AggType string `json:"aggType,omitempty"`
	// Meta carries model-level custom metadata.
	Meta map[string]interface{} `json:"meta,omitempty"`
}

// MetaResponse is the /v1/meta envelope.
type MetaResponse struct {
	Cubes []MetaCube `json:"cubes"`
}

// Meta fetches the compiled model metadata under the caller's identity.
func (c *Client) Meta(ctx context.Context) (*MetaResponse, error) {
	var out MetaResponse
	if err := c.do(ctx, http.MethodGet, "/meta", nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Query is the Cube query JSON (measures/dimensions/timeDimensions/filters/...).
type Query map[string]interface{}

// LoadResponse is the /v1/load envelope.
type LoadResponse struct {
	Data       []map[string]interface{} `json:"data"`
	Annotation json.RawMessage          `json:"annotation,omitempty"`
	Error      string                   `json:"error,omitempty"`
	// GeneratedSQL is the SQL that Cube generated for this query
	// (populated by a follow-up /v1/sql dry-run; not part of /v1/load response).
	GeneratedSQL []string `json:"generated_sql,omitempty"`
}

// Load executes a query and returns its rows. The securityContext embedded
// in the JWT decides visibility: accessPolicy denials surface as empty
// results for the affected members — see service.WarnDenied for the
// explicit-denial hint used by the studio and agent tools.
func (c *Client) Load(ctx context.Context, q Query) (*LoadResponse, error) {
	var out LoadResponse
	if err := c.do(ctx, http.MethodPost, "/load", nil, map[string]interface{}{"query": q}, &out); err != nil {
		return nil, err
	}
	if out.Error != "" {
		return nil, fmt.Errorf("cube error: %s", out.Error)
	}
	return &out, nil
}

// SQLResponse is the /v1/sql envelope (dry-run: generated SQL, never executed).
type SQLResponse struct {
	SQL   []string          `json:"sql"`
	Order map[string]string `json:"order,omitempty"`
	Error string            `json:"error,omitempty"`
}

// SQL dry-runs a query and returns the generated SQL without executing it.
func (c *Client) SQL(ctx context.Context, q Query) (*SQLResponse, error) {
	var out SQLResponse
	if err := c.do(ctx, http.MethodPost, "/sql", nil, map[string]interface{}{"query": q}, &out); err != nil {
		return nil, err
	}
	if out.Error != "" {
		return nil, fmt.Errorf("cube error: %s", out.Error)
	}
	return &out, nil
}

// Ping verifies endpoint reachability (config self-check).
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.Meta(ctx)
	return err
}

// WaitUntilCompiledVerify polls /v1/meta until the model appears AND the
// verify callback returns true. The callback receives each /v1/meta response,
// allowing the caller to compare fingerprints against the expected NEW
// definition without making duplicate Meta calls.
func (c *Client) WaitUntilCompiledVerify(
	ctx context.Context,
	modelName string,
	verify func(meta *MetaResponse) bool,
	timeout time.Duration,
) error {
	deadline := time.Now().Add(timeout)
	for {
		meta, err := c.Meta(ctx)
		if err == nil && verify(meta) {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("model %s compile poll timeout (%s); "+
				"the model may have failed to update, check Cube logs", modelName, timeout)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// ---- securityContext plumbing through request context ----

type secCtxKeyType struct{}

// WithSecurityContext stores the caller identity for this request.
func WithSecurityContext(ctx context.Context, sec SecurityContext) context.Context {
	return context.WithValue(ctx, secCtxKeyType{}, sec)
}

// SecurityContextFromContext extracts the caller identity (zero value if
// absent — Cube then applies its default-deny accessPolicy).
func SecurityContextFromContext(ctx context.Context) SecurityContext {
	if v, ok := ctx.Value(secCtxKeyType{}).(SecurityContext); ok {
		return v
	}
	return SecurityContext{}
}
