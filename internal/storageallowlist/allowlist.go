package storageallowlist

import (
	"os"
	"strings"
)

const AllowListEnv = "STORAGE_ALLOW_LIST"

var supported = []string{"local", "minio", "cos", "tos", "s3", "oss", "ks3", "obs"}

// Supported returns the canonical storage provider names in display order.
func Supported() []string {
	providers := make([]string, len(supported))
	copy(providers, supported)
	return providers
}

// AllowedMap returns which providers are permitted by STORAGE_ALLOW_LIST.
func AllowedMap() map[string]bool {
	raw := strings.TrimSpace(os.Getenv(AllowListEnv))
	allowed := make(map[string]bool, len(supported))

	if raw == "" {
		for _, provider := range supported {
			allowed[provider] = true
		}
		return allowed
	}

	for _, item := range strings.FieldsFunc(raw, func(r rune) bool {
		switch r {
		case ',', ';', '|', '\n', '\t', ' ':
			return true
		default:
			return false
		}
	}) {
		provider := strings.ToLower(strings.TrimSpace(item))
		if provider == "" {
			continue
		}
		for _, name := range supported {
			if provider == name {
				allowed[provider] = true
				break
			}
		}
	}

	return allowed
}

// IsAllowed reports whether provider is permitted. Empty provider is treated as allowed.
func IsAllowed(provider string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return true
	}
	return AllowedMap()[provider]
}

// FirstAllowed returns the first supported provider allowed by STORAGE_ALLOW_LIST.
func FirstAllowed() string {
	allowed := AllowedMap()
	for _, provider := range supported {
		if allowed[provider] {
			return provider
		}
	}
	return ""
}

// AllowedList returns allowed providers in canonical order.
func AllowedList() []string {
	allowed := AllowedMap()
	out := make([]string, 0, len(supported))
	for _, provider := range supported {
		if allowed[provider] {
			out = append(out, provider)
		}
	}
	return out
}
