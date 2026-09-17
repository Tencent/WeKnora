package config

import (
	"os"
	"strings"
)

// ConfiguredExternalURL returns the public origin used by URLs that must be
// fetched by a third-party client, such as an IM platform. APP_EXTERNAL_URL
// is the explicit setting. FRONTEND_BASE_URL is a compatible fallback for
// single-origin deployments where the frontend proxy also exposes /r/.
//
// APP_EXTERNAL_URL wins when both variables are set so existing deployments
// keep their current behaviour.
func ConfiguredExternalURL() string {
	for _, name := range []string{"APP_EXTERNAL_URL", "FRONTEND_BASE_URL"} {
		if value := strings.TrimRight(strings.TrimSpace(os.Getenv(name)), "/"); value != "" {
			return value
		}
	}
	return ""
}
