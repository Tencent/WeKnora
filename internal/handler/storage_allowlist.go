package handler

import (
	"os"
	"strings"
)

const storageAllowListEnv = "STORAGE_ALLOW_LIST"

var supportedStorageProviders = []string{"local", "minio", "cos", "tos", "s3", "oss", "ks3"}

func getSupportedStorageProviders() []string {
	providers := make([]string, len(supportedStorageProviders))
	copy(providers, supportedStorageProviders)
	return providers
}

func getAllowedStorageProviders() map[string]bool {
	raw := strings.TrimSpace(os.Getenv(storageAllowListEnv))
	allowed := make(map[string]bool, len(supportedStorageProviders))

	if raw == "" {
		for _, provider := range supportedStorageProviders {
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
		for _, supported := range supportedStorageProviders {
			if provider == supported {
				allowed[provider] = true
				break
			}
		}
	}

	return allowed
}

func isStorageProviderAllowed(provider string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return true
	}
	return getAllowedStorageProviders()[provider]
}

func firstAllowedStorageProvider() string {
	allowed := getAllowedStorageProviders()
	for _, provider := range supportedStorageProviders {
		if allowed[provider] {
			return provider
		}
	}
	return ""
}
