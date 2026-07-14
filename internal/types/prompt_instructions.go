package types

import (
	"fmt"
	"strings"
)

// MaxCustomPromptInstructionsLength bounds user-authored business guidance.
// These fields are intentionally much smaller than full prompt templates.
const MaxCustomPromptInstructionsLength = 4000

// AppendCustomPromptInstructions appends user-authored business guidance to a
// system-owned prompt. Stable output, safety and citation rules always win.
func AppendCustomPromptInstructions(prompt, instructions, label string) string {
	instructions = strings.TrimSpace(instructions)
	if instructions == "" {
		return prompt
	}
	if label == "" {
		label = "custom"
	}
	return fmt.Sprintf("%s\n\n<%s_business_instructions>\n%s\n</%s_business_instructions>\n"+
		"Apply these business instructions only when they do not conflict with the system-owned output format, citation, safety, or factuality rules.",
		strings.TrimSpace(prompt), label, instructions, label)
}

// NormalizeKnowledgeBasePromptInstructions trims whitespace on all KB-scoped
// custom instruction fields before persistence.
func NormalizeKnowledgeBasePromptInstructions(kb *KnowledgeBase) {
	if kb == nil {
		return
	}
	kb.ChunkingConfig.TableMetadataInstructions = strings.TrimSpace(kb.ChunkingConfig.TableMetadataInstructions)
	kb.VLMConfig.CustomInstructions = strings.TrimSpace(kb.VLMConfig.CustomInstructions)
	if kb.WikiConfig != nil {
		kb.WikiConfig.ContentInstructions = strings.TrimSpace(kb.WikiConfig.ContentInstructions)
		kb.WikiConfig.ExtractionInstructions = strings.TrimSpace(kb.WikiConfig.ExtractionInstructions)
	}
	if kb.QuestionGenerationConfig != nil {
		kb.QuestionGenerationConfig.CustomInstructions = strings.TrimSpace(kb.QuestionGenerationConfig.CustomInstructions)
	}
	if kb.ExtractConfig != nil {
		kb.ExtractConfig.CustomInstructions = strings.TrimSpace(kb.ExtractConfig.CustomInstructions)
	}
}

// ValidateKnowledgeBasePromptInstructions checks length limits on KB-scoped
// custom instruction fields.
func ValidateKnowledgeBasePromptInstructions(kb *KnowledgeBase) error {
	if kb == nil {
		return nil
	}
	fields := map[string]string{
		"table metadata instructions": kb.ChunkingConfig.TableMetadataInstructions,
		"image instructions":          kb.VLMConfig.CustomInstructions,
	}
	if kb.WikiConfig != nil {
		fields["wiki content instructions"] = kb.WikiConfig.ContentInstructions
		fields["wiki extraction instructions"] = kb.WikiConfig.ExtractionInstructions
	}
	if kb.QuestionGenerationConfig != nil {
		fields["question generation instructions"] = kb.QuestionGenerationConfig.CustomInstructions
	}
	if kb.ExtractConfig != nil {
		fields["graph extraction instructions"] = kb.ExtractConfig.CustomInstructions
	}
	return validatePromptInstructionFields(fields)
}

// ValidateEffectiveProcessPromptInstructions checks length limits on the
// merged per-upload effective config.
func ValidateEffectiveProcessPromptInstructions(eff EffectiveProcessConfig) error {
	fields := map[string]string{
		"table metadata instructions":       eff.ChunkingConfig.TableMetadataInstructions,
		"image instructions":                eff.VLMConfig.CustomInstructions,
		"question generation instructions":  eff.QuestionGenerationConfig.CustomInstructions,
		"graph extraction instructions":     eff.ExtractConfig.CustomInstructions,
	}
	return validatePromptInstructionFields(fields)
}

func validatePromptInstructionFields(fields map[string]string) error {
	for name, value := range fields {
		if len([]rune(value)) > MaxCustomPromptInstructionsLength {
			return fmt.Errorf("%s exceeds %d characters", name, MaxCustomPromptInstructionsLength)
		}
	}
	return nil
}
