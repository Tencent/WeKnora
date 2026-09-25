package service

import (
	"fmt"
	"strings"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

var parserOutboundURLKeys = []string{
	"mineru_endpoint",
	"mineru_vlm_server_url",
	"odl_hybrid_url",
	"paddleocr_vl_endpoint",
	"paddleocr_vl_cloud_base_url",
}

// validateParserEngineOverrideURLs validates every parser override that can
// cause this process or the trusted DocReader service to make an outbound
// request. Per-upload overrides are included because API callers can provide
// the generic parser_engine_overrides map directly.
func validateParserEngineOverrideURLs(overrides map[string]string) error {
	for _, key := range parserOutboundURLKeys {
		rawURL := strings.TrimSpace(overrides[key])
		if rawURL == "" {
			continue
		}
		if err := secutils.ValidateURLForSSRF(rawURL); err != nil {
			return fmt.Errorf("%s failed SSRF validation: %w", key, err)
		}
	}
	return nil
}
