package types

import (
	"fmt"
	"net/textproto"
	"sort"
	"strings"
)

// reservedPaddleOCRVLVLHeaders are request headers the client owns. A gateway
// in front of PaddleOCR-VL authenticates the caller with its own headers, but
// it must not be able to rewrite the framing or content type of the parse
// request.
var reservedPaddleOCRVLHeaders = map[string]struct{}{
	"connection":        {},
	"content-length":    {},
	"content-type":      {},
	"expect":            {},
	"host":              {},
	"keep-alive":        {},
	"proxy-connection":  {},
	"te":                {},
	"trailer":           {},
	"transfer-encoding": {},
	"upgrade":           {},
}

// NormalizePaddleOCRVLHeaders trims the names and values of the extra headers
// sent to a self-hosted PaddleOCR-VL service, canonicalizes the names and drops
// rows the operator left blank. It returns nil for an empty result so an
// untouched setting stays absent from the stored JSON.
func NormalizePaddleOCRVLHeaders(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	out := make(map[string]string, len(headers))
	for name, value := range headers {
		name = textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		out[name] = strings.TrimSpace(value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ValidatePaddleOCRVLHeaders rejects header names and values that cannot be
// sent on an HTTP request. Reserved headers are refused instead of silently
// ignored so an operator can see why their gateway header has no effect.
func ValidatePaddleOCRVLHeaders(headers map[string]string) error {
	if len(headers) == 0 {
		return nil
	}
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, rawName := range names {
		name := textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(rawName))
		if name == "" {
			return fmt.Errorf("paddleocr_vl_headers contains an empty header name")
		}
		if !validHeaderFieldName(name) {
			return fmt.Errorf("paddleocr_vl_headers: %q is not a valid HTTP header name", rawName)
		}
		if _, reserved := reservedPaddleOCRVLHeaders[strings.ToLower(name)]; reserved {
			return fmt.Errorf("paddleocr_vl_headers: %q is managed by WeKnora and cannot be overridden", name)
		}
		if !validHeaderFieldValue(headers[rawName]) {
			return fmt.Errorf("paddleocr_vl_headers: value of %q contains an invalid character", name)
		}
	}
	return nil
}

// mergeParserHeaderSecrets resolves redacted header values in incoming against
// the stored headers. Headers are matched by their canonical name so a
// case-only edit keeps the stored credential, and keys that are absent from
// incoming are dropped: deleting a row must really delete it.
func mergeParserHeaderSecrets(incoming, existing map[string]string) map[string]string {
	if len(incoming) == 0 {
		return nil
	}
	prev := make(map[string]string, len(existing))
	for name, value := range existing {
		prev[textproto.CanonicalMIMEHeaderKey(name)] = value
	}
	out := make(map[string]string, len(incoming))
	for name, value := range incoming {
		name = textproto.CanonicalMIMEHeaderKey(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		resolved := PreserveIfRedacted(value, prev[name])
		if resolved == "" {
			continue
		}
		out[name] = resolved
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// IsReservedPaddleOCRVLHeader reports whether WeKnora owns the header and will
// not forward an operator-supplied value for it.
func IsReservedPaddleOCRVLHeader(name string) bool {
	_, reserved := reservedPaddleOCRVLHeaders[strings.ToLower(strings.TrimSpace(name))]
	return reserved
}

// validHeaderFieldName implements the RFC 7230 token charset used for HTTP
// field names.
func validHeaderFieldName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' {
			continue
		}
		if strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0 {
			continue
		}
		return false
	}
	return true
}

// validHeaderFieldValue rejects control characters (other than the allowed
// horizontal tab), which would make the HTTP client fail at request time.
func validHeaderFieldValue(value string) bool {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c == '\t' {
			continue
		}
		if c < 0x20 || c == 0x7f {
			return false
		}
	}
	return true
}
