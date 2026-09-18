package desensitization

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Tencent/WeKnora/internal/desensitization/builtin"
	"github.com/Tencent/WeKnora/internal/types"
)

const llmMaxRunes = 12000

type llmMatchPayload struct {
	Matches []llmMatch `json:"matches"`
}

type llmMatch struct {
	Text string `json:"text"`
	Type string `json:"type"`
}

func maskWithLLM(ctx context.Context, text string, cfg types.DesensitizationConfig, deps Deps) (string, types.JSONMap, error) {
	if deps.Complete == nil {
		return "", nil, fmt.Errorf("%w: llm completer is not configured", ErrEngineUnavailable)
	}

	out, report, err := builtin.Mask(ctx, text, cfg)
	if err != nil {
		return "", nil, err
	}
	if report == nil {
		report = types.JSONMap{}
	}

	sample := truncateRunes(text, llmMaxRunes)

	system := "You identify mainland China PII in text. Reply with JSON only: {\"matches\":[{\"text\":\"exact substring\",\"type\":\"cn_id_card|cn_mobile|cn_landline|cn_bank_card|cn_uscc|cn_plate|email\"}]}. Do not invent text that is not present."
	user := "Entity types: " + strings.Join(cfg.EntityTypes, ", ") + "\n\n---\n" + sample
	raw, err := deps.Complete(ctx, system, user)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrEngineUnavailable, err)
	}

	payload, err := parseLLMMatches(raw)
	if err != nil {
		return "", nil, fmt.Errorf("%w: %v", ErrEngineUnavailable, err)
	}

	enabled := map[string]struct{}{}
	for _, t := range cfg.EntityTypes {
		enabled[t] = struct{}{}
	}
	extra := 0
	for _, m := range payload.Matches {
		orig := strings.TrimSpace(m.Text)
		mapped := strings.TrimSpace(m.Type)
		if orig == "" || mapped == "" {
			continue
		}
		if _, ok := enabled[mapped]; !ok {
			continue
		}
		if !strings.Contains(out, orig) {
			continue
		}
		out = strings.Replace(out, orig, placeholder(mapped), 1)
		extra++
	}
	report["engine"] = types.DesensitizationEngineLLM
	report["llm_hits"] = extra
	return out, report, nil
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

func parseLLMMatches(raw string) (llmMatchPayload, error) {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimPrefix(trimmed, "```json")
	trimmed = strings.TrimPrefix(trimmed, "```")
	trimmed = strings.TrimSuffix(trimmed, "```")
	trimmed = strings.TrimSpace(trimmed)
	var payload llmMatchPayload
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return llmMatchPayload{}, err
	}
	return payload, nil
}
