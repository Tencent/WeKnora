package knowledge

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type FirstStagePageInput struct {
	Object        ClassifiedKnowledge `json:"object"`
	TimeRange     string              `json:"time_range"`
	ChunkRefs     []string            `json:"chunk_refs"`
	FieldEvidence map[string][]string `json:"field_evidence"`
}

type FirstStagePageRender struct {
	PageType string `json:"page_type"`
	Title    string `json:"title"`
	Content  string `json:"content"`
}

type FirstStagePageExpectation struct {
	Object          ClassifiedKnowledge
	TimeRange       string
	VideoDurationMs int64
	ChunkRefs       []string
	SourceRefs      []string
}

// RenderFirstStageObjectPage routes one passed object through its P3 template.
// Field evidence is explicit input: this function never infers that an object-
// level citation supports every structure field.
func RenderFirstStageObjectPage(input FirstStagePageInput) (FirstStagePageRender, error) {
	object := input.Object
	object.Title = CanonicalKnowledgeTitle(object.Title)
	if object.Title == "" {
		return FirstStagePageRender{}, fmt.Errorf("knowledge object title cannot be only a type decoration")
	}
	fieldEvidence, err := NormalizeFirstStageFieldEvidence(object, input.FieldEvidence)
	if err != nil {
		return FirstStagePageRender{}, err
	}
	input.FieldEvidence = fieldEvidence
	switch object.PrimaryType {
	case TypeEntity:
		fields := make(map[string]EntityFieldEvidence, len(object.StructureFields))
		for key, value := range object.StructureFields {
			if strings.TrimSpace(value) != "" {
				normalizedKey := strings.ToLower(strings.TrimSpace(key))
				fields[normalizedKey] = EntityFieldEvidence{EvidenceIDs: input.FieldEvidence[normalizedKey]}
			}
		}
		render, err := RenderEntityPage(EntityTemplateInput{Object: object, TimeRange: input.TimeRange, ChunkRefs: input.ChunkRefs, FieldEvidence: fields})
		return FirstStagePageRender{PageType: render.PageType, Title: render.Title, Content: render.Content}, err
	case TypeConcept:
		fields := make(map[string]ConceptFieldEvidence, len(object.StructureFields))
		for key, value := range object.StructureFields {
			if strings.TrimSpace(value) != "" {
				normalizedKey := strings.ToLower(strings.TrimSpace(key))
				fields[normalizedKey] = ConceptFieldEvidence{EvidenceIDs: input.FieldEvidence[normalizedKey]}
			}
		}
		render, err := RenderConceptPage(ConceptTemplateInput{Object: object, TimeRange: input.TimeRange, ChunkRefs: input.ChunkRefs, FieldEvidence: fields})
		return FirstStagePageRender{PageType: render.PageType, Title: render.Title, Content: render.Content}, err
	case TypeMethodology:
		fields := make(map[string]MethodologyFieldEvidence, len(object.StructureFields))
		for key, value := range object.StructureFields {
			if strings.TrimSpace(value) != "" {
				normalizedKey := strings.ToLower(strings.TrimSpace(key))
				fields[normalizedKey] = MethodologyFieldEvidence{EvidenceIDs: input.FieldEvidence[normalizedKey]}
			}
		}
		render, err := RenderMethodologyPage(MethodologyTemplateInput{Object: object, TimeRange: input.TimeRange, ChunkRefs: input.ChunkRefs, FieldEvidence: fields})
		return FirstStagePageRender{PageType: render.PageType, Title: render.Title, Content: render.Content}, err
	case TypeCase:
		fields := make(map[string]CaseFieldEvidence, len(object.StructureFields))
		for key, value := range object.StructureFields {
			if strings.TrimSpace(value) != "" {
				normalizedKey := strings.ToLower(strings.TrimSpace(key))
				fields[normalizedKey] = CaseFieldEvidence{EvidenceIDs: input.FieldEvidence[normalizedKey]}
			}
		}
		render, err := RenderCasePage(CaseTemplateInput{Object: object, TimeRange: input.TimeRange, ChunkRefs: input.ChunkRefs, FieldEvidence: fields})
		return FirstStagePageRender{PageType: render.PageType, Title: render.Title, Content: render.Content}, err
	case TypeInsight:
		fields := make(map[string]InsightFieldEvidence, len(object.StructureFields))
		for key, value := range object.StructureFields {
			if strings.TrimSpace(value) != "" {
				normalizedKey := strings.ToLower(strings.TrimSpace(key))
				fields[normalizedKey] = InsightFieldEvidence{EvidenceIDs: input.FieldEvidence[normalizedKey]}
			}
		}
		render, err := RenderInsightPage(InsightTemplateInput{Object: object, TimeRange: input.TimeRange, ChunkRefs: input.ChunkRefs, FieldEvidence: fields})
		return FirstStagePageRender{PageType: render.PageType, Title: render.Title, Content: render.Content}, err
	default:
		return FirstStagePageRender{}, fmt.Errorf("unsupported primary_type %q", object.PrimaryType)
	}
}

// ValidateFirstStageWikiObjectPage applies the full object contract and the
// P4 rule that relations, related content and Wiki double-links are absent.
func ValidateFirstStageWikiObjectPage(content, pageType string, expected FirstStagePageExpectation) error {
	object := expected.Object
	validation, err := ValidateWikiObjectPage(content, pageType, object.SourceVideoID, object.TranscriptGeneration)
	if err != nil {
		return err
	}
	if validation.KnowledgeObjectID != object.CandidateID || validation.KnowledgeType != object.PrimaryType ||
		validation.EntitySubType != object.EntitySubType {
		return fmt.Errorf("first-stage page object identity or type mismatch")
	}
	frontmatter, _ := parseWikiFrontmatter(content)
	if len(anySliceValue(frontmatter["relations"])) != 0 {
		return fmt.Errorf("first-stage object page relations must be empty")
	}
	if len(anySliceValue(frontmatter["related_content"])) != 0 {
		return fmt.Errorf("first-stage object page related_content must be empty")
	}
	if strings.ToLower(strings.TrimSpace(stringValue(frontmatter["page_type"]))) != "index" {
		return fmt.Errorf("first-stage object page frontmatter page_type must be index")
	}
	if !sameFirstStageValues(stringSliceValue(frontmatter["evidence_ids"]), object.EvidenceIDs) {
		return fmt.Errorf("first-stage object page evidence_ids mismatch")
	}
	if !sameFirstStageValues(stringSliceValue(frontmatter["source_refs"]), expected.SourceRefs) {
		return fmt.Errorf("first-stage object page source_refs mismatch")
	}
	if !sameFirstStageValues(stringSliceValue(frontmatter["chunk_refs"]), expected.ChunkRefs) {
		return fmt.Errorf("first-stage object page chunk_refs mismatch")
	}
	timeRange := strings.TrimSpace(stringValue(frontmatter["time_range"]))
	if timeRange != strings.TrimSpace(expected.TimeRange) || ValidateFirstStageTimeRange(timeRange, expected.VideoDurationMs) != nil {
		return fmt.Errorf("first-stage object page time_range mismatch or invalid")
	}
	if strings.Contains(content, "[[") || strings.Contains(content, "]]") {
		return fmt.Errorf("first-stage object page must not contain Wiki double-links")
	}
	return nil
}

func ValidateFirstStageFieldEvidence(object ClassifiedKnowledge, fieldEvidence map[string][]string) error {
	_, err := NormalizeFirstStageFieldEvidence(object, fieldEvidence)
	return err
}

func NormalizeFirstStageFieldEvidence(object ClassifiedKnowledge, fieldEvidence map[string][]string) (map[string][]string, error) {
	allowed := make(map[string]struct{}, len(object.EvidenceIDs))
	for _, evidenceID := range object.EvidenceIDs {
		allowed[strings.TrimSpace(evidenceID)] = struct{}{}
	}
	structureKeys := make(map[string]struct{}, len(object.StructureFields))
	for key, value := range object.StructureFields {
		if strings.TrimSpace(value) == "" {
			continue
		}
		normalized := strings.ToLower(strings.TrimSpace(key))
		if _, duplicate := structureKeys[normalized]; duplicate {
			return nil, fmt.Errorf("duplicate normalized structure field %s", normalized)
		}
		structureKeys[normalized] = struct{}{}
	}
	normalizedEvidence := make(map[string][]string, len(fieldEvidence))
	for key, ids := range fieldEvidence {
		normalized := strings.ToLower(strings.TrimSpace(key))
		if _, ok := structureKeys[normalized]; !ok {
			return nil, fmt.Errorf("field_evidence.%s has no populated structure field", key)
		}
		if _, duplicate := normalizedEvidence[normalized]; duplicate {
			return nil, fmt.Errorf("duplicate normalized field_evidence key %s", normalized)
		}
		normalizedEvidence[normalized] = append([]string(nil), ids...)
	}
	for key := range structureKeys {
		ids := normalizedEvidence[key]
		if len(ids) == 0 {
			return nil, fmt.Errorf("structure field %s requires explicit evidence", key)
		}
		if err := validateEvidenceIDs(ids); err != nil {
			return nil, fmt.Errorf("field_evidence.%s: %w", key, err)
		}
		for _, evidenceID := range ids {
			if _, ok := allowed[strings.TrimSpace(evidenceID)]; !ok {
				return nil, fmt.Errorf("field_evidence.%s contains foreign evidence", key)
			}
		}
		normalizedEvidence[key] = sortedEvidenceIDs(ids)
	}
	return normalizedEvidence, nil
}

func anySliceValue(value any) []any {
	switch typed := value.(type) {
	case []any:
		return typed
	case nil:
		return nil
	default:
		return []any{typed}
	}
}

var firstStageTimeRangePattern = regexp.MustCompile(`^(\d{2,}):([0-5]\d):([0-5]\d)\.(\d{3})-(\d{2,}):([0-5]\d):([0-5]\d)\.(\d{3})$`)

// ValidateFirstStageTimeRange checks ordering and, when durationMs is positive,
// ensures the evidence projection stays inside the frozen video duration.
func ValidateFirstStageTimeRange(value string, durationMs int64) error {
	matches := firstStageTimeRangePattern.FindStringSubmatch(strings.TrimSpace(value))
	if len(matches) != 9 {
		return fmt.Errorf("time_range format is invalid")
	}
	parts := make([]int64, 8)
	for i := range parts {
		parsed, err := strconv.ParseInt(matches[i+1], 10, 64)
		if err != nil {
			return fmt.Errorf("time_range contains an invalid number")
		}
		parts[i] = parsed
	}
	start := (((parts[0]*60)+parts[1])*60+parts[2])*1000 + parts[3]
	end := (((parts[4]*60)+parts[5])*60+parts[6])*1000 + parts[7]
	if start < 0 || end <= start {
		return fmt.Errorf("time_range end must be after start")
	}
	if durationMs > 0 && end > durationMs {
		return fmt.Errorf("time_range exceeds video duration")
	}
	return nil
}

func sameFirstStageValues(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := append([]string(nil), left...)
	rightCopy := append([]string(nil), right...)
	for i := range leftCopy {
		leftCopy[i] = strings.TrimSpace(leftCopy[i])
	}
	for i := range rightCopy {
		rightCopy[i] = strings.TrimSpace(rightCopy[i])
	}
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	for i := range leftCopy {
		if leftCopy[i] == "" || leftCopy[i] != rightCopy[i] {
			return false
		}
	}
	return true
}
