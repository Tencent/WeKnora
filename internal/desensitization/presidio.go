package desensitization

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/desensitization/builtin"
	"github.com/Tencent/WeKnora/internal/types"
)

type presidioAnalyzerHit struct {
	Start      int     `json:"start"`
	End        int     `json:"end"`
	Score      float64 `json:"score"`
	EntityType string  `json:"entity_type"`
}

func maskWithPresidio(
	ctx context.Context, text string, cfg types.DesensitizationConfig, deps Deps,
) (string, types.JSONMap, error) {
	analyzer := trimURL(deps.PresidioAnalyzerURL)
	if analyzer == "" {
		return "", nil, fmt.Errorf("%w: presidio analyzer URL is not configured", ErrEngineUnavailable)
	}

	// Structured China identifiers stay on the builtin recognizers (checksums).
	out, report, err := builtin.Mask(ctx, text, cfg)
	if err != nil {
		return "", nil, err
	}
	if report == nil {
		report = types.JSONMap{}
	}

	client := deps.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	hits, err := analyzePresidio(ctx, client, analyzer, text)
	if err != nil {
		return "", nil, err
	}

	enabled := map[string]struct{}{}
	for _, t := range cfg.EntityTypes {
		enabled[t] = struct{}{}
	}
	extra := 0
	for _, hit := range hits {
		orig, ok := presidioSpanText(text, hit.Start, hit.End)
		if !ok {
			continue
		}
		mapped := mapPresidioEntity(hit.EntityType)
		if mapped == "" {
			continue
		}
		if _, ok := enabled[mapped]; !ok {
			continue
		}
		if orig == "" || !strings.Contains(out, orig) {
			continue
		}
		out = strings.Replace(out, orig, placeholder(mapped), 1)
		extra++
	}
	report["engine"] = types.DesensitizationEnginePresidio
	report["presidio_hits"] = extra
	return out, report, nil
}

func mapPresidioEntity(entityType string) string {
	switch strings.ToUpper(strings.TrimSpace(entityType)) {
	case "EMAIL_ADDRESS", "EMAIL":
		return types.DesensitizationEntityEmail
	case "PHONE_NUMBER", "PHONE_NUMBER_CN", "CN_PHONE":
		return types.DesensitizationEntityCNMobile
	case "CREDIT_CARD", "CRYPTO", "IBAN_CODE":
		return types.DesensitizationEntityCNBankCard
	case "CN_ID_CARD", "CN_IDCARD":
		return types.DesensitizationEntityCNIDCard
	default:
		return ""
	}
}

func analyzePresidio(ctx context.Context, client *http.Client, base, text string) ([]presidioAnalyzerHit, error) {
	body, err := json.Marshal(map[string]any{
		"text":            text,
		"language":        "zh",
		"score_threshold": 0.4,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/analyze", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEngineUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%w: presidio analyzer HTTP %d", ErrEngineUnavailable, resp.StatusCode)
	}
	var hits []presidioAnalyzerHit
	if err := json.Unmarshal(raw, &hits); err != nil {
		return nil, fmt.Errorf("%w: decode presidio response: %v", ErrEngineUnavailable, err)
	}
	return hits, nil
}

// presidioSpanText maps Presidio character (rune) offsets to a Go substring.
// Python/Presidio indexes Unicode code points; Go string indexes UTF-8 bytes.
func presidioSpanText(text string, start, end int) (string, bool) {
	runes := []rune(text)
	if start < 0 || end > len(runes) || start >= end {
		return "", false
	}
	return string(runes[start:end]), true
}
