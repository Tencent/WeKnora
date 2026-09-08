package knowledge

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// SemanticIdentityAdapter is the boundary for a WeKnora semantic model. The
// write path only consumes a structured result; it never treats a title score
// as an identity decision.
type SemanticIdentityAdapter interface {
	Compare(context.Context, IdentityCandidate, IdentityCandidate) (SemanticIdentityAssessment, error)
}

type SemanticIdentityAssessment struct {
	Decision       string   `json:"decision"`
	Confidence     float64  `json:"confidence"`
	Reason         string   `json:"reason"`
	ConflictFields []string `json:"conflict_fields,omitempty"`
}

// RuleSemanticIdentityAdapter is the deterministic fallback used when a
// WeKnora model adapter is not configured. It preserves the fail-closed shape
// while keeping local acceptance and offline operation deterministic.
type RuleSemanticIdentityAdapter struct{}

func (RuleSemanticIdentityAdapter) Compare(_ context.Context, left, right IdentityCandidate) (SemanticIdentityAssessment, error) {
	comparison := CompareIdentity(left, right)
	decision := "different_object"
	switch comparison.Decision {
	case IdentityReuse:
		decision = "same_object"
	case IdentityConflict:
		decision = "uncertain"
	}
	return SemanticIdentityAssessment{
		Decision:       decision,
		Confidence:     comparison.Score,
		Reason:         comparison.Reason,
		ConflictFields: conflictFields(left, right, comparison),
	}, nil
}

func conflictFields(left, right IdentityCandidate, comparison IdentityComparison) []string {
	fields := make([]string, 0, 2)
	if left.KnowledgeType != right.KnowledgeType {
		fields = append(fields, "knowledge_type")
	}
	if left.KnowledgeType == TypeEntity && left.EntitySubType != right.EntitySubType {
		fields = append(fields, "entity_sub_type")
	}
	if !comparison.ContextMatch {
		fields = append(fields, "semantic_content")
	}
	return fields
}

// EvidenceContribution is the only source-specific data kept on a canonical
// object page. Evidence text remains in the transcript knowledge base.
type EvidenceContribution struct {
	VideoID              string   `yaml:"video_id" json:"video_id"`
	SourceDocumentID     string   `yaml:"source_document_id" json:"source_document_id"`
	TranscriptGeneration string   `yaml:"transcript_generation" json:"transcript_generation"`
	EvidenceIDs          []string `yaml:"evidence_ids,omitempty" json:"evidence_ids,omitempty"`
	ChunkRefs            []string `yaml:"chunk_refs,omitempty" json:"chunk_refs,omitempty"`
	TimeRange            string   `yaml:"time_range,omitempty" json:"time_range,omitempty"`
	QualityStatus        string   `yaml:"quality_status" json:"quality_status"`
}

// ParseEvidenceContributions reads the canonical contribution list and
// backfills one contribution from the legacy single-source fields.
func ParseEvidenceContributions(content string) ([]EvidenceContribution, error) {
	frontmatter, _ := parseWikiFrontmatter(content)
	var result []EvidenceContribution
	if raw, ok := frontmatter["evidence_contributions"]; ok && raw != nil {
		items, ok := raw.([]any)
		if !ok {
			return nil, fmt.Errorf("evidence_contributions must be a list")
		}
		parsed, err := parseContributionItems(items)
		if err != nil {
			return nil, err
		}
		result = append(result, parsed...)
	} else if raw, ok := frontmatter["evidence_contribution"]; ok && raw != nil {
		values, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("evidence_contribution must be an object")
		}
		parsed, err := parseContributionItems([]any{values})
		if err != nil {
			return nil, err
		}
		result = append(result, parsed...)
	}
	if len(result) > 0 {
		for index, contribution := range result {
			if contribution.VideoID == "" || contribution.SourceDocumentID == "" || contribution.TranscriptGeneration == "" {
				return nil, fmt.Errorf("evidence_contributions[%d] requires video_id, source_document_id and transcript_generation", index)
			}
			if len(contribution.EvidenceIDs) == 0 {
				return nil, fmt.Errorf("evidence_contributions[%d].evidence_ids must not be empty", index)
			}
		}
	}
	if len(result) == 0 {
		legacy := EvidenceContribution{
			VideoID:              strings.TrimSpace(stringValue(frontmatter["source_video_id"])),
			SourceDocumentID:     firstString(stringSliceValue(frontmatter["source_refs"])),
			TranscriptGeneration: strings.TrimSpace(stringValue(frontmatter["transcript_generation"])),
			EvidenceIDs:          cleanStrings(stringSliceValue(frontmatter["evidence_ids"])),
			ChunkRefs:            cleanStrings(stringSliceValue(frontmatter["chunk_refs"])),
			TimeRange:            strings.TrimSpace(stringValue(frontmatter["time_range"])),
			QualityStatus:        strings.TrimSpace(stringValue(frontmatter["audit_status"])),
		}
		if legacy.QualityStatus == "" {
			legacy.QualityStatus = "passed"
		}
		if legacy.VideoID != "" && legacy.SourceDocumentID != "" && legacy.TranscriptGeneration != "" && len(legacy.EvidenceIDs) > 0 {
			result = append(result, legacy)
		}
	}
	return result, nil
}

func parseContributionItems(items []any) ([]EvidenceContribution, error) {
	result := make([]EvidenceContribution, 0, len(items))
	for index, item := range items {
		values, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("evidence_contributions[%d] must be an object", index)
		}
		videoID := strings.TrimSpace(stringValue(values["video_id"]))
		if videoID == "" {
			videoID = strings.TrimSpace(stringValue(values["source_video_id"]))
		}
		qualityStatus := strings.TrimSpace(stringValue(values["quality_status"]))
		if qualityStatus == "" {
			qualityStatus = "passed"
		}
		contribution := EvidenceContribution{
			VideoID: videoID, SourceDocumentID: strings.TrimSpace(stringValue(values["source_document_id"])),
			TranscriptGeneration: strings.TrimSpace(stringValue(values["transcript_generation"])),
			EvidenceIDs:          cleanStrings(stringSliceValue(values["evidence_ids"])),
			ChunkRefs:            cleanStrings(stringSliceValue(values["chunk_refs"])),
			TimeRange:            strings.TrimSpace(stringValue(values["time_range"])), QualityStatus: qualityStatus,
		}
		result = append(result, contribution)
	}
	return result, nil
}

// EnsureEvidenceContributions upgrades a newly submitted object to the
// canonical multi-source shape without changing its generated body.
func EnsureEvidenceContributions(content string) (string, error) {
	return mergeCanonicalWikiObject("", content)
}

// MergeCanonicalWikiObject merges one candidate into the canonical page. An
// empty canonical content creates a normalized first page. Existing fields are
// intentionally retained: a source contribution can add evidence, but it must
// not replace the canonical identity or erase facts from another video.
func MergeCanonicalWikiObject(canonicalContent, incomingContent string) (string, error) {
	return mergeCanonicalWikiObject(canonicalContent, incomingContent)
}

func mergeCanonicalWikiObject(canonicalContent, incomingContent string) (string, error) {
	incoming, incomingBody := parseWikiFrontmatter(incomingContent)
	if len(incoming) == 0 {
		return "", fmt.Errorf("incoming knowledge object frontmatter is required")
	}
	base := incoming
	body := incomingBody
	if strings.TrimSpace(canonicalContent) != "" {
		base, body = parseWikiFrontmatter(canonicalContent)
		if len(base) == 0 {
			return "", fmt.Errorf("canonical knowledge object frontmatter is required")
		}
	}
	base = cloneMap(base)

	canonicalTitle := CanonicalKnowledgeTitle(firstNonEmptyString(stringValue(base["title"]), stringValue(base["canonical_name"]), firstWikiHeading(body), stringValue(incoming["title"])))
	if canonicalTitle == "" {
		return "", fmt.Errorf("canonical knowledge object title is required")
	}
	base["title"] = canonicalTitle
	if _, exists := base["canonical_name"]; exists {
		base["canonical_name"] = canonicalTitle
	}
	base["knowledge_object_id"] = firstNonEmptyString(stringValue(base["knowledge_object_id"]), stringValue(incoming["knowledge_object_id"]))
	if base["knowledge_object_id"] == "" {
		return "", fmt.Errorf("canonical knowledge_object_id is required")
	}

	contributions, err := ParseEvidenceContributions(canonicalContent)
	if canonicalContent == "" || len(contributions) == 0 {
		contributions, err = ParseEvidenceContributions(incomingContent)
	}
	if err != nil {
		return "", err
	}
	incomingContributions, err := ParseEvidenceContributions(incomingContent)
	if err != nil {
		return "", err
	}
	if canonicalContent != "" && len(incomingContributions) > 0 {
		incomingContribution := incomingContributions[len(incomingContributions)-1]
		key := contributionKey(incomingContribution)
		replaced := false
		for index := range contributions {
			if contributionKey(contributions[index]) == key {
				contributions[index] = incomingContribution
				replaced = true
				break
			}
		}
		if !replaced {
			contributions = append(contributions, incomingContribution)
		}
	}
	if len(contributions) == 0 {
		return "", fmt.Errorf("at least one evidence contribution is required")
	}
	sort.SliceStable(contributions, func(i, j int) bool { return contributionKey(contributions[i]) < contributionKey(contributions[j]) })
	base["evidence_contributions"] = contributions

	base["source_refs"] = unionStrings(sourceRefsFromContributions(contributions))
	base["evidence_ids"] = unionStrings(evidenceIDsFromContributions(contributions))
	// Keep legacy identity fields populated for readers that have not migrated.
	if stringValue(base["source_video_id"]) == "" {
		base["source_video_id"] = contributions[0].VideoID
	}
	if stringValue(base["transcript_generation"]) == "" {
		base["transcript_generation"] = contributions[0].TranscriptGeneration
	}

	encoded, err := yaml.Marshal(base)
	if err != nil {
		return "", fmt.Errorf("marshal canonical knowledge object: %w", err)
	}
	rewrittenBody, err := rewriteFirstHeading(body, canonicalTitle)
	if err != nil {
		return "", err
	}
	return "---\n" + strings.TrimSpace(string(encoded)) + "\n---\n\n" + strings.TrimSpace(rewrittenBody) + "\n", nil
}

func cloneMap(input map[string]any) map[string]any {
	output := make(map[string]any, len(input))
	for key, value := range input {
		output[key] = value
	}
	return output
}

func contributionKey(value EvidenceContribution) string {
	return strings.Join([]string{value.VideoID, value.TranscriptGeneration}, "\x00")
}

func sourceRefsFromContributions(values []EvidenceContribution) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.SourceDocumentID)
	}
	return result
}

func evidenceIDsFromContributions(values []EvidenceContribution) []string {
	result := make([]string, 0)
	for _, value := range values {
		result = append(result, value.EvidenceIDs...)
	}
	return result
}

func unionStrings(values ...[]string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0)
	for _, group := range values {
		for _, value := range group {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func cleanStrings(values []string) []string { return unionStrings(values) }

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return strings.TrimSpace(values[0])
}
