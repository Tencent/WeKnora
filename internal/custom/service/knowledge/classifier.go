package knowledge

import (
	"fmt"
	"sort"
	"strings"
)

// Classify assigns exactly one business knowledge type to a validated
// metadata-only candidate. It deliberately uses no document prose: the
// context is passed only to enforce the P3-01 identity and evidence gate.
// Type-specific publish thresholds remain the responsibility of P3-04.
func Classify(candidate Candidate, context DocumentContext) (ClassifiedKnowledge, error) {
	if err := ValidateClassificationInput(candidate, context); err != nil {
		return ClassifiedKnowledge{}, fmt.Errorf("validate classification input: %w", err)
	}
	if strings.TrimSpace(candidate.CoreContent) == "" {
		return ClassifiedKnowledge{}, fmt.Errorf("candidate core_content is required for classification output")
	}

	primaryType, entitySubType, confidence, ok := classifyStructure(candidate.StructureFields)
	if !ok {
		return ClassifiedKnowledge{}, fmt.Errorf("cannot determine primary_type from candidate structure_fields")
	}

	classified := ClassifiedKnowledge{
		CandidateID:              candidate.ID,
		SourceDocumentID:         candidate.SourceDocumentID,
		SourceVideoID:            candidate.SourceVideoID,
		TranscriptGeneration:     candidate.TranscriptGeneration,
		PrimaryType:              primaryType,
		EntitySubType:            entitySubType,
		Title:                    strings.TrimSpace(candidate.Title),
		CoreContent:              strings.TrimSpace(candidate.CoreContent),
		StructureFields:          cloneStructureFields(candidate.StructureFields),
		EvidenceIDs:              sortedEvidenceIDs(candidate.EvidenceIDs),
		ClassificationConfidence: confidence,
		AuditStatus:              "pending",
	}
	if err := ValidateClassifiedKnowledge(candidate, context, classified); err != nil {
		return ClassifiedKnowledge{}, fmt.Errorf("validate classification output: %w", err)
	}
	return classified, nil
}

type classificationMatch struct {
	primaryType    KnowledgeType
	entitySubType  string
	matchingFields int
	priority       int
}

type classificationRule struct {
	primaryType   KnowledgeType
	entitySubType string
	fields        []string
	priority      int
}

func classifyStructure(fields map[string]string) (KnowledgeType, string, float64, bool) {
	normalized := normalizedStructureFields(fields)
	rules := frameworkClassificationRules()
	matches := make([]classificationMatch, 0, len(rules))
	totalMatches := 0
	for _, rule := range rules {
		count := countPopulatedFields(normalized, rule.fields)
		if count == 0 {
			continue
		}
		matches = append(matches, classificationMatch{
			primaryType: rule.primaryType, entitySubType: rule.entitySubType, matchingFields: count, priority: rule.priority,
		})
		totalMatches += count
	}
	if len(matches) == 0 {
		return "", "", 0, false
	}

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].matchingFields != matches[j].matchingFields {
			return matches[i].matchingFields > matches[j].matchingFields
		}
		return matches[i].priority < matches[j].priority
	})
	winner := matches[0]
	return winner.primaryType, winner.entitySubType, float64(winner.matchingFields) / float64(totalMatches), true
}

func normalizedStructureFields(fields map[string]string) map[string]string {
	normalized := make(map[string]string, len(fields))
	for key, value := range fields {
		normalized[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
	}
	return normalized
}

func countPopulatedFields(fields map[string]string, keys []string) int {
	count := 0
	for _, key := range keys {
		if strings.TrimSpace(fields[key]) != "" {
			count++
		}
	}
	return count
}

func cloneStructureFields(fields map[string]string) map[string]string {
	if len(fields) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(fields))
	for key, value := range fields {
		cloned[key] = value
	}
	return cloned
}

func sortedEvidenceIDs(ids []string) []string {
	sorted := append([]string(nil), ids...)
	sort.Strings(sorted)
	return sorted
}
