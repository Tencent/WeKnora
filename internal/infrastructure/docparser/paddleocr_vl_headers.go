package docparser

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// paddleOCRVLHeadersKey is the ParserEngineOverrides key holding the extra
// request headers as a JSON object. ParserEngineOverrides is map[string]string,
// so the header set is carried as encoded JSON.
const paddleOCRVLHeadersKey = "paddleocr_vl_headers"

// paddleOCRVLHeadersFromOverrides decodes the operator-configured headers for a
// self-hosted PaddleOCR-VL service. Entries that could break the request are
// dropped rather than failing the parse; the settings endpoint rejects them at
// save time, so this only guards per-upload overrides supplied through the API.
func paddleOCRVLHeadersFromOverrides(overrides map[string]string) map[string]string {
	raw := strings.TrimSpace(overrides[paddleOCRVLHeadersKey])
	if raw == "" {
		return nil
	}

	var decoded map[string]string
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		logger.Warnf(context.Background(), "[PaddleOCR-VL] ignoring %s: %v", paddleOCRVLHeadersKey, err)
		return nil
	}

	headers := types.NormalizePaddleOCRVLHeaders(decoded)
	if len(headers) == 0 {
		return nil
	}
	for name := range headers {
		if types.IsReservedPaddleOCRVLHeader(name) {
			logger.Warnf(context.Background(), "[PaddleOCR-VL] ignoring reserved header %q", name)
			delete(headers, name)
		}
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

// applyPaddleOCRVLHeaders adds the configured headers to an outgoing request.
// Content-Type is set by the caller beforehand and is never replaced.
func applyPaddleOCRVLHeaders(req *http.Request, headers map[string]string) {
	for name, value := range headers {
		if types.IsReservedPaddleOCRVLHeader(name) {
			continue
		}
		req.Header.Set(name, value)
	}
}
