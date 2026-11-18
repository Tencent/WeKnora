package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	e2b "github.com/matiasinsaurralde/go-e2b"
)

// maxE2BTemplatePages bounds the catalog walk so a provider that keeps
// minting fresh tokens cannot pin the goroutine. The repeated-token guard in
// listTemplatesV2 catches the common cycle; this catches the rest.
const maxE2BTemplatePages = 1000

// e2bTemplatesEndpoint lists templates through the canonical v2 surface.
// The go-e2b SDK ListTemplates targets the legacy bare-array GET /templates,
// which E2B-compatible control planes (Tencent Cloud AGS among them) do not
// implement: their catalog lives at GET /v2/templates as a paginated object.
// The SDK keeps its client fields private, so this small client carries the
// pieces the v2 call needs.
type e2bTemplatesEndpoint struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

// e2bTemplatesPage mirrors one page of GET /v2/templates. Providers use
// different member casings (AGS: "Templates"/"NextToken", E2B v2:
// "templates"/"nextToken"), so both spellings are accepted.
type e2bTemplatesPage struct {
	Templates []e2b.TemplateDetail
	NextToken string
}

// UnmarshalJSON accepts both the AGS-style capitalized envelope and the
// lowercase E2B v2 one, including per-item field casings.
func (p *e2bTemplatesPage) UnmarshalJSON(data []byte) error {
	// AGS capitalizes the envelope members; E2B v2 lowercases them. Decode
	// through a case-insensitive probe so both spellings resolve.
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	fetch := func(keys ...string) json.RawMessage {
		for _, key := range keys {
			for probed := range probe {
				if strings.EqualFold(probed, key) {
					return probe[probed]
				}
			}
		}
		return nil
	}
	raw := fetch("Templates")
	token := fetch("NextToken", "nextToken")
	if len(token) > 0 {
		_ = json.Unmarshal(token, &p.NextToken)
	}
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, &p.Templates); err == nil && len(p.Templates) > 0 {
		return nil
	}
	// Capitalized member items ("TemplateID"): decode through a shim that
	// lowercases the first rune of every key to match the SDK JSON tags.
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return err
	}
	p.Templates = make([]e2b.TemplateDetail, 0, len(items))
	for _, item := range items {
		remapped := make(map[string]json.RawMessage, len(item))
		for key, value := range item {
			remapped[lowerFirst(key)] = value
		}
		data, err := json.Marshal(remapped)
		if err != nil {
			return err
		}
		var detail e2b.TemplateDetail
		if err := json.Unmarshal(data, &detail); err != nil {
			return err
		}
		p.Templates = append(p.Templates, detail)
	}
	return nil
}

// lowerFirst lowercases only the first rune so "TemplateID" matches the SDK
// JSON tag "templateID". It decodes a rune rather than folding the first byte:
// an ASCII-only bit trick corrupts any key that starts with a non-ASCII rune,
// and also rewrites keys whose first character is not a letter ("_id").
func lowerFirst(key string) string {
	if key == "" {
		return key
	}
	first, size := utf8.DecodeRuneInString(key)
	if first == utf8.RuneError && size <= 1 {
		return key
	}
	return string(unicode.ToLower(first)) + key[size:]
}

// listTemplatesV2 walks every page of GET /v2/templates. A provider without
// the surface answers 404; the caller decides whether to fall back to the
// legacy listing.
func (e *e2bTemplatesEndpoint) listTemplatesV2(ctx context.Context) ([]e2b.TemplateDetail, error) {
	if e == nil || e.baseURL == "" || e.apiKey == "" {
		return nil, &e2b.Error{StatusCode: http.StatusNotFound, Message: "v2 templates endpoint is not configured"}
	}
	base := strings.TrimRight(e.baseURL, "/")
	var all []e2b.TemplateDetail
	// nextToken is opaque: real tokens are frequently base64, so it has to
	// travel through the query encoder ("+" must stay "+", not become a
	// space and truncate the page walk).
	nextToken := ""
	seen := map[string]struct{}{"": {}}
	for page := 0; ; page++ {
		if page >= maxE2BTemplatePages {
			return nil, fmt.Errorf("e2b: list templates v2 exceeded %d pages", maxE2BTemplatePages)
		}
		endpoint := base + "/v2/templates"
		if nextToken != "" {
			endpoint += "?" + url.Values{"nextToken": {nextToken}}.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("e2b: build list templates v2 request: %w", err)
		}
		req.Header.Set("X-API-Key", e.apiKey)
		resp, err := e.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("e2b: send list templates v2 request: %w", err)
		}
		body, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			if readErr == nil && len(body) > 0 {
				return nil, &e2b.Error{StatusCode: resp.StatusCode, Message: string(body)}
			}
			return nil, &e2b.Error{StatusCode: resp.StatusCode, Message: resp.Status}
		}
		if readErr != nil {
			return nil, fmt.Errorf("e2b: read list templates v2 response: %w", readErr)
		}
		var pageData e2bTemplatesPage
		if err := json.Unmarshal(body, &pageData); err != nil {
			return nil, fmt.Errorf("e2b: decode list templates v2 response: %w", err)
		}
		all = append(all, pageData.Templates...)
		if pageData.NextToken == "" {
			return all, nil
		}
		// Same guard as ListSnapshots: a provider that repeats a token (the
		// same one, or a cycle A→B→A) would otherwise spin until the page
		// cap. Fail fast instead of hanging until cancel.
		if _, dup := seen[pageData.NextToken]; dup {
			return nil, fmt.Errorf(
				"e2b: list templates v2 returned a repeated pagination token %q",
				pageData.NextToken,
			)
		}
		seen[pageData.NextToken] = struct{}{}
		nextToken = pageData.NextToken
	}
}

// isE2BNotFoundStatus reports whether err carries an HTTP 404 from the
// control plane, in the SDK typed *e2b.Error shape or wrapped chains.
func isE2BNotFoundStatus(err error) bool {
	var apiErr *e2b.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusNotFound
	}
	return false
}
