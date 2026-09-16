package browserskill

import (
	"encoding/json"
	"net/url"
)

// NavigationIncomplete checks the known lifecycle result, never arbitrary page text.
func NavigationIncomplete(method string, raw json.RawMessage) bool {
	switch method {
	case "navigate", "navigate_back", "navigate_forward", "reload", "wait_for_navigation":
		var result struct {
			Reached string `json:"reached"`
		}
		return json.Unmarshal(raw, &result) == nil && result.Reached == "timeout"
	}
	return false
}

// Progress needs a readable address, never URL credentials or query tokens.
func statusPageURL(raw string) string {
	if len(raw) > 8192 {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	u.User, u.RawQuery, u.Fragment, u.RawFragment, u.ForceQuery = nil, "", "", "", false
	return u.String()
}
