package types

import (
	"net/url"
	"strings"
	"unicode"
)

// ValidSourceURL accepts durable, human-facing HTTP links. Storage handles,
// credentials and expiring download grants must never become document links.
func ValidSourceURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsFunc(raw, unicode.IsControl) {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil ||
		(u.Scheme != "https" && u.Scheme != "http") ||
		u.Hostname() == "" ||
		u.User != nil ||
		u.Opaque != "" {
		return ""
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return ""
	}
	for key := range query {
		key = strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(key))
		if strings.HasPrefix(key, "xamz") || strings.HasPrefix(key, "xgoog") {
			return ""
		}
		switch key {
		case "token", "accesstoken", "refreshtoken", "idtoken", "authtoken", "downloadtoken", "downloadticket",
			"authorization", "auth", "authkey", "apikey", "clientsecret", "secret", "password", "credential",
			"signature", "sig", "sign", "qsignature", "hmac", "awsaccesskeyid", "ossaccesskeyid", "accesskeyid",
			"securitytoken", "xosssecuritytoken", "xcossecuritytoken",
			"xbcesignature", "xsignature", "policy", "keypairid", "expires", "expiry", "expiration":
			return ""
		}
	}
	path := strings.ToLower(u.Path)
	if strings.HasPrefix(path, "/api/v1/files/") || strings.HasPrefix(path, "/api/v1/resources/") ||
		strings.HasPrefix(path, "/r/") ||
		(strings.HasPrefix(path, "/open-apis/") && strings.Contains(path, "/download")) {
		return ""
	}
	return raw
}
