// Package headertemplate resolves explicitly configured MCP headers from a
// request snapshot. It has no dependency on the HTTP framework or MCP transport.
package headertemplate

import (
	"fmt"
	"net/http"
	"strings"
)

// Context is an immutable snapshot. Neither input maps nor internal maps are
// exposed to callers, including goroutines detached from the inbound request.
type Context struct {
	values  map[string]string
	headers http.Header
}

func NewContext(values map[string]string, headers http.Header) *Context {
	c := &Context{values: make(map[string]string, len(values)), headers: make(http.Header)}
	for key, value := range values {
		c.values[key] = value
	}
	for key, values := range headers {
		if ValidHeaderName(key) && !ProtectedRequestHeader(key) {
			name := http.CanonicalHeaderKey(key)
			c.headers[name] = append(c.headers[name], values...)
		}
	}
	return c
}

var identityVariables = map[string]bool{
	"user.id": true, "user.email": true, "principal.id": true,
	"principal.type": true, "external.user_id": true, "im.user_id": true,
	"im.platform": true, "tenant.id": true,
}

// ProtocolHeader identifies headers owned by HTTP or the MCP transport.
func ProtocolHeader(name string) bool {
	name = strings.ToLower(name)
	switch name {
	case "host", "connection", "keep-alive", "te", "trailer", "transfer-encoding",
		"upgrade", "content-length", "content-type", "accept", "accept-encoding",
		"forwarded", "mcp-session-id", "mcp-protocol-version", "last-event-id":
		return true
	}
	return strings.HasPrefix(name, "proxy-") || strings.HasPrefix(name, "sec-") ||
		strings.HasPrefix(name, "x-forwarded-") || strings.HasPrefix(name, "x-weknora-") ||
		strings.HasPrefix(name, "x-principal-") || strings.HasPrefix(name, "x-auth-") ||
		strings.HasPrefix(name, "x-workspace-") || strings.HasPrefix(name, "x-actor-") ||
		strings.HasPrefix(name, "x-user-") || strings.HasPrefix(name, "x-tenant-") ||
		strings.HasPrefix(name, "x-embed-")
}

// ProtectedRequestHeader prevents forwarding platform credentials or reading
// unverified identity headers instead of their authenticated built-in values.
func ProtectedRequestHeader(name string) bool {
	if ProtocolHeader(name) {
		return true
	}
	switch strings.ToLower(name) {
	case "authorization", "cookie", "set-cookie", "x-api-key",
		"x-external-user-token", "x-external-user-id", "x-tenant-id",
		"x-weknora-desktop-token", "x-embed-session", "x-embed-visitor",
		"x-user-id", "x-user-email", "x-authenticated-user", "x-authenticated-email":
		return true
	}
	return false
}

func ValidHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, b := range []byte(name) {
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(b)) {
			continue
		}
		return false
	}
	return true
}

func ValidHeaderValue(value string) bool {
	for _, b := range []byte(value) {
		if b < 0x20 || b == 0x7f {
			return false
		}
	}
	return true
}

type part struct {
	text       string
	candidates []string
}

type Template struct {
	parts   []part
	dynamic bool
}

func (t *Template) Dynamic() bool { return t.dynamic }

func Parse(value string) (*Template, error) {
	t := &Template{}
	var literal strings.Builder
	flush := func() {
		if literal.Len() > 0 {
			t.parts = append(t.parts, part{text: literal.String()})
			literal.Reset()
		}
	}
	for i := 0; i < len(value); {
		if strings.HasPrefix(value[i:], `\{{`) {
			literal.WriteString("{{")
			i += 3
			continue
		}
		if !strings.HasPrefix(value[i:], "{{") {
			literal.WriteByte(value[i])
			i++
			continue
		}
		flush()
		end := strings.Index(value[i+2:], "}}")
		if end < 0 {
			return nil, fmt.Errorf("unclosed expression at offset %d", i)
		}
		candidates := strings.Split(value[i+2:i+2+end], "??")
		for j, candidate := range candidates {
			candidate = strings.TrimSpace(candidate)
			if !identityVariables[candidate] {
				name, ok := strings.CutPrefix(candidate, "request.headers.")
				if !ok || !ValidHeaderName(name) {
					return nil, fmt.Errorf("unknown variable or invalid expression at offset %d", i)
				}
				if ProtectedRequestHeader(name) {
					return nil, fmt.Errorf("protected request header at offset %d", i)
				}
				candidate = "request.headers." + http.CanonicalHeaderKey(name)
			}
			candidates[j] = candidate
		}
		t.parts = append(t.parts, part{candidates: candidates})
		t.dynamic = true
		i += end + 4
	}
	flush()
	return t, nil
}

func (t *Template) Resolve(ctx *Context) (string, error) {
	value, _, err := t.resolve(ctx, false)
	return value, err
}

// ResolveOptional omits a dynamic header when no candidate can supply every
// expression. Malformed or unsafe values remain errors, never omissions.
func (t *Template) ResolveOptional(ctx *Context) (string, bool, error) {
	return t.resolve(ctx, true)
}

func (t *Template) resolve(ctx *Context, allowMissing bool) (string, bool, error) {
	var out strings.Builder
	missing := false
	for _, p := range t.parts {
		if len(p.candidates) == 0 {
			out.WriteString(p.text)
			continue
		}
		found := false
		for _, variable := range p.candidates {
			value := ""
			if ctx != nil {
				if name, ok := strings.CutPrefix(variable, "request.headers."); ok {
					values := ctx.headers.Values(name)
					if len(values) > 1 {
						return "", false, fmt.Errorf("multiple values for %s", variable)
					}
					if len(values) == 1 {
						value = values[0]
					}
				} else {
					value = ctx.values[variable]
				}
			}
			if !ValidHeaderValue(value) {
				return "", false, fmt.Errorf("invalid value for %s", variable)
			}
			if strings.TrimSpace(value) == "" {
				continue
			}
			out.WriteString(value)
			found = true
			break
		}
		if !found {
			if allowMissing {
				missing = true
				continue
			}
			return "", false, fmt.Errorf("missing value for %s", strings.Join(p.candidates, " ?? "))
		}
	}
	if missing {
		return "", false, nil
	}
	value := out.String()
	if !ValidHeaderValue(value) {
		return "", false, fmt.Errorf("invalid header value")
	}
	return value, true, nil
}
