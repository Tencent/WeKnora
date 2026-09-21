package types

import (
	"strings"

	"github.com/Tencent/WeKnora/internal/errors"
)

// APIPrincipalConfigInput distinguishes a missing secret from a replacement.
// It never accepts a namespace: only copying an owned key may reuse one.
type APIPrincipalConfigInput struct {
	Mode                APIPrincipalMode `json:"mode"`
	RequireDirectHeader bool             `json:"require_direct_header"`
	HMACSecret          *string          `json:"hmac_secret"`
}

// Resolve validates a policy update and retains a stored secret when omitted.
func (in *APIPrincipalConfigInput) Resolve(existing *APIPrincipalConfig) (*APIPrincipalConfig, error) {
	cfg := &APIPrincipalConfig{Mode: APIPrincipalModeTenant}
	if existing != nil {
		*cfg = *existing
	}
	if in == nil {
		return cfg, nil
	}
	cfg.Mode = in.Mode
	if cfg.Mode == "" {
		cfg.Mode = APIPrincipalModeTenant
	}
	switch cfg.Mode {
	case APIPrincipalModeTenant, APIPrincipalModeDirect, APIPrincipalModeSignedToken:
	default:
		return nil, errors.NewValidationError("mode must be tenant, direct_header, or signed_token")
	}
	cfg.RequireDirectHeader = in.RequireDirectHeader
	cfg.DirectHeaderName = "X-External-User-ID"
	cfg.SignedTokenHeaderName = "X-External-User-Token"
	if in.HMACSecret != nil && strings.TrimSpace(*in.HMACSecret) != "***" {
		cfg.HMACSecret = strings.TrimSpace(*in.HMACSecret)
	}
	if cfg.Mode == APIPrincipalModeSignedToken && cfg.HMACSecret == "" {
		return nil, errors.NewValidationError("hmac_secret is required for signed_token mode")
	}
	return cfg, nil
}
