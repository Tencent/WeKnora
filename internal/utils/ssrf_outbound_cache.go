package utils

import (
	"fmt"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
)

// outboundSSRFValidationTTL bounds how long a successful RoundTrip/redirect
// validation for the same origin may be reused. DNS rebinding at the TCP sink
// is still blocked by SSRFSafeDialContext on every new connection.
const outboundSSRFValidationTTL = 60 * time.Second

var (
	ssrfOutboundCacheGen      atomic.Uint64
	ssrfOutboundCache         sync.Map // string -> *ssrfOutboundCacheEntry
	ssrfOutboundValidateGroup singleflight.Group
	ssrfOutboundValidateMisses atomic.Uint64 // test-only counter
)

type ssrfOutboundCacheEntry struct {
	err       error
	expiresAt time.Time
}

// validateURLForSSRFForOutbound validates URLs on the hot outbound path
// (RoundTripper, redirect checks). Results are cached per origin for a short
// TTL so high-frequency clients do not repeat DNS lookups on every request.
// Handler/input boundaries should keep calling ValidateURLForSSRF directly.
func validateURLForSSRFForOutbound(rawURL string) error {
	if rawURL == "" {
		return nil
	}

	cacheKey, ok := outboundSSRFCacheKey(rawURL)
	if !ok {
		return ValidateURLForSSRF(rawURL)
	}

	now := time.Now()
	if cached, ok := ssrfOutboundCache.Load(cacheKey); ok {
		entry := cached.(*ssrfOutboundCacheEntry)
		if now.Before(entry.expiresAt) {
			return entry.err
		}
		ssrfOutboundCache.Delete(cacheKey)
	}

	result, err, _ := ssrfOutboundValidateGroup.Do(cacheKey, func() (any, error) {
		if cached, ok := ssrfOutboundCache.Load(cacheKey); ok {
			entry := cached.(*ssrfOutboundCacheEntry)
			if time.Now().Before(entry.expiresAt) {
				return entry.err, entry.err
			}
		}

		ssrfOutboundValidateMisses.Add(1)
		validationErr := ValidateURLForSSRF(rawURL)
		ssrfOutboundCache.Store(cacheKey, &ssrfOutboundCacheEntry{
			err:       validationErr,
			expiresAt: time.Now().Add(outboundSSRFValidationTTL),
		})
		return validationErr, validationErr
	})
	if err != nil {
		return err
	}
	if validationErr, ok := result.(error); ok {
		return validationErr
	}
	return nil
}

func outboundSSRFCacheKey(rawURL string) (string, bool) {
	normalized := rawURL
	if !strings.Contains(normalized, "://") {
		normalized = "https://" + normalized
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return "", false
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", false
	}

	host := strings.ToLower(parsed.Hostname())
	if host == "" {
		return "", false
	}

	port := parsed.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}

	var origin string
	if strings.Contains(host, ":") {
		origin = fmt.Sprintf("%s://[%s]:%s", scheme, host, port)
	} else {
		origin = fmt.Sprintf("%s://%s:%s", scheme, host, port)
	}

	return fmt.Sprintf("%d|%s", ssrfOutboundCacheGen.Load(), origin), true
}

func invalidateSSRFOutboundValidationCache() {
	ssrfOutboundCacheGen.Add(1)
	ssrfOutboundCache = sync.Map{}
	ssrfOutboundValidateGroup = singleflight.Group{}
}

// ResetSSRFOutboundValidationCacheForTest clears the outbound validation cache.
// NOT for production use.
func ResetSSRFOutboundValidationCacheForTest() {
	invalidateSSRFOutboundValidationCache()
	ssrfOutboundValidateMisses.Store(0)
}

// SSRFOutboundValidationMissesForTest returns how many uncached outbound
// validations have run since the last ResetSSRFOutboundValidationCacheForTest.
func SSRFOutboundValidationMissesForTest() uint64 {
	return ssrfOutboundValidateMisses.Load()
}
