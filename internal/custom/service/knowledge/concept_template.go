package knowledge

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ConceptFieldEvidence connects one concept framework field to the evidence
// IDs that support its value.
type ConceptFieldEvidence struct {
	EvidenceIDs []string `json:"evidence_ids"`
}

// ConceptSourceParagraph is an optional readable source passage for a concept
// page. Evidence IDs stay in frontmatter and audit data.
type ConceptSourceParagraph struct {
	Text        string   `json:"text"`
	EvidenceIDs []string `json:"evidence_ids"`
}

// ConceptTemplateInput is the complete metadata needed to render one concept
// page. It intentionally accepts no subtitle or raw transcript field.
type ConceptTemplateInput struct {
	Object           ClassifiedKnowledge             `json:"object"`
	Aliases          []string                        `json:"aliases,omitempty"`
	Description      string                          `json:"description,omitempty"`
	TimeRange        string                          `json:"time_range"`
	FieldEvidence    map[string]ConceptFieldEvidence `json:"field_evidence,omitempty"`
	SourceParagraphs []ConceptSourceParagraph        `json:"source_paragraphs,omitempty"`
}

// ConceptPageRender is a deterministic Wiki write payload. The caller still
// decides when and whether to call the external Wiki writer.
type ConceptPageRender struct {
	PageType string `json:"page_type"`
	Title    string `json:"title"`
	Content  string `json:"content"`
}

type conceptPageFrontmatter struct {
	ID                       string            `yaml:"id"`
	KnowledgeObjectID        string            `yaml:"knowledge_object_id"`
	Type                     string            `yaml:"type"`
	PrimaryType              string            `yaml:"primary_type"`
	SourceVideoID            string            `yaml:"source_video_id"`
	TranscriptGeneration     string            `yaml:"transcript_generation"`
	TimeRange                string            `yaml:"time_range"`
	Title                    string            `yaml:"title"`
	Aliases                  []string          `yaml:"aliases"`
	InformationNature        string            `yaml:"information_nature"`
	AuditStatus              string            `yaml:"audit_status"`
	ClassificationConfidence float64           `yaml:"classification_confidence"`
	EvidenceIDs              []string          `yaml:"evidence_ids"`
	SourceRefs               []string          `yaml:"source_refs"`
	StructureFields          map[string]string `yaml:"structure_fields"`
	RelatedContent           []any             `yaml:"related_content"`
	Relations                []any             `yaml:"relations"`
}

// RenderConceptPage renders the concept framework from type-frameworks.md.
// Fields are shown only when both their value and supporting evidence exist.
func RenderConceptPage(input ConceptTemplateInput) (ConceptPageRender, error) {
	object := cloneClassifiedKnowledge(input.Object)
	if object.PrimaryType != TypeConcept {
		return ConceptPageRender{}, fmt.Errorf("concept template requires primary_type concept")
	}
	if isOrdinaryWordMeaning(object) {
		return ConceptPageRender{}, fmt.Errorf("concept template rejects ordinary word meaning")
	}
	if err := ValidatePublishGate(object); err != nil {
		return ConceptPageRender{}, fmt.Errorf("validate concept for template: %w", err)
	}
	if strings.ToLower(strings.TrimSpace(object.AuditStatus)) != "passed" {
		return ConceptPageRender{}, fmt.Errorf("concept template requires audit_status passed")
	}

	framework, err := FrameworkFor(TypeConcept, "")
	if err != nil {
		return ConceptPageRender{}, err
	}
	title := strings.TrimSpace(object.Title)
	if title == "" {
		return ConceptPageRender{}, fmt.Errorf("concept title is required")
	}
	if strings.ContainsAny(title, "\r\n\t") {
		return ConceptPageRender{}, fmt.Errorf("concept title contains unstable whitespace")
	}
	timeRange := strings.TrimSpace(input.TimeRange)
	if timeRange == "" {
		return ConceptPageRender{}, fmt.Errorf("concept time_range is required")
	}
	if isPlaceholderTimeRange(timeRange) {
		return ConceptPageRender{}, fmt.Errorf("concept time_range must not be a placeholder")
	}

	allowedEvidence := make(map[string]struct{}, len(object.EvidenceIDs))
	for _, evidenceID := range object.EvidenceIDs {
		allowedEvidence[strings.TrimSpace(evidenceID)] = struct{}{}
	}
	fieldEvidence, err := normalizedConceptFieldEvidence(input.FieldEvidence, framework, allowedEvidence)
	if err != nil {
		return ConceptPageRender{}, err
	}
	normalizedObjectFields, err := normalizedConceptStructureFields(object.StructureFields)
	if err != nil {
		return ConceptPageRender{}, err
	}
	renderedFields := make(map[string]string, len(framework.Fields))
	for _, field := range framework.Fields {
		value := strings.TrimSpace(normalizedObjectFields[field.Key])
		if value == "" || len(fieldEvidence[field.Key]) == 0 {
			continue
		}
		renderedFields[field.Key] = value
	}
	if len(renderedFields) < 2 {
		return ConceptPageRender{}, fmt.Errorf("concept template requires at least 2 populated fields with evidence (got %d)", len(renderedFields))
	}

	aliases := cleanAliases(input.Aliases, title)
	description := strings.TrimSpace(input.Description)
	if description == "" {
		description = strings.TrimSpace(object.CoreContent)
	}
	if description == "" {
		return ConceptPageRender{}, fmt.Errorf("concept description is required")
	}
	paragraphs, err := normalizeConceptSourceParagraphs(input.SourceParagraphs, allowedEvidence)
	if err != nil {
		return ConceptPageRender{}, err
	}

	frontmatter := conceptPageFrontmatter{
		ID:                       object.CandidateID,
		KnowledgeObjectID:        object.CandidateID,
		Type:                     string(TypeConcept),
		PrimaryType:              string(TypeConcept),
		SourceVideoID:            object.SourceVideoID,
		TranscriptGeneration:     object.TranscriptGeneration,
		TimeRange:                timeRange,
		Title:                    title,
		Aliases:                  aliases,
		InformationNature:        "概念",
		AuditStatus:              "passed",
		ClassificationConfidence: object.ClassificationConfidence,
		EvidenceIDs:              sortedEvidenceIDs(object.EvidenceIDs),
		SourceRefs:               sourceDocumentRefs(object.SourceDocumentID),
		StructureFields:          renderedFields,
		RelatedContent:           []any{},
		Relations:                []any{},
	}
	frontmatterYAML, err := yaml.Marshal(frontmatter)
	if err != nil {
		return ConceptPageRender{}, fmt.Errorf("marshal concept frontmatter: %w", err)
	}

	var body strings.Builder
	body.WriteString("---\n")
	body.Write(frontmatterYAML)
	body.WriteString("---\n\n# ")
	body.WriteString(title)
	body.WriteString("\n\n")
	if len(aliases) > 0 {
		body.WriteString("## 别名\n\n")
		for _, alias := range aliases {
			fmt.Fprintf(&body, "- %s\n", alias)
		}
		body.WriteString("\n")
	}
	body.WriteString("一句话概述：")
	body.WriteString(description)
	body.WriteString("\n\n## 概念结构\n\n")
	for _, field := range framework.Fields {
		if value := renderedFields[field.Key]; value != "" {
			fmt.Fprintf(&body, "- %s：%s\n", field.Label, value)
		}
	}
	body.WriteString("\n## 知识来源\n\n")
	body.WriteString("时间范围：")
	body.WriteString(timeRange)
	body.WriteString("\n")
	for _, paragraph := range paragraphs {
		writeEntityBlockquote(&body, paragraph.Text)
	}
	body.WriteString("\n## 信息性质\n\n概念\n")

	return ConceptPageRender{PageType: "index", Title: title, Content: body.String()}, nil
}

func normalizedConceptFieldEvidence(
	raw map[string]ConceptFieldEvidence,
	framework FrameworkEntry,
	allowed map[string]struct{},
) (map[string][]string, error) {
	known := make(map[string]struct{}, len(framework.Fields))
	for _, field := range framework.Fields {
		known[field.Key] = struct{}{}
	}
	result := make(map[string][]string, len(raw))
	seenKeys := make(map[string]string, len(raw))
	for key, value := range raw {
		rawKey := key
		key = strings.ToLower(strings.TrimSpace(key))
		if _, ok := known[key]; !ok {
			return nil, fmt.Errorf("field_evidence.%s is not valid for concept", key)
		}
		if prior, ok := seenKeys[key]; ok && prior != rawKey {
			return nil, fmt.Errorf("field_evidence contains duplicate normalized field %q", key)
		}
		seenKeys[key] = rawKey
		ids := append([]string(nil), value.EvidenceIDs...)
		if len(ids) == 0 {
			continue
		}
		if err := validateEvidenceIDs(ids); err != nil {
			return nil, fmt.Errorf("field_evidence.%s: %w", key, err)
		}
		for _, id := range ids {
			if _, ok := allowed[strings.TrimSpace(id)]; !ok {
				return nil, fmt.Errorf("field_evidence.%s evidence ID %q is not present on concept", key, id)
			}
		}
		result[key] = sortedEvidenceIDs(ids)
	}
	return result, nil
}

func normalizedConceptStructureFields(raw map[string]string) (map[string]string, error) {
	result := make(map[string]string, len(raw))
	seenKeys := make(map[string]string, len(raw))
	for key, value := range raw {
		rawKey := key
		key = strings.ToLower(strings.TrimSpace(key))
		if prior, ok := seenKeys[key]; ok && prior != rawKey {
			return nil, fmt.Errorf("structure_fields contains duplicate normalized field %q", key)
		}
		seenKeys[key] = rawKey
		result[key] = strings.TrimSpace(value)
	}
	return result, nil
}

func normalizeConceptSourceParagraphs(
	raw []ConceptSourceParagraph,
	allowed map[string]struct{},
) ([]ConceptSourceParagraph, error) {
	result := make([]ConceptSourceParagraph, 0, len(raw))
	for index, paragraph := range raw {
		paragraph.Text = strings.TrimSpace(paragraph.Text)
		if paragraph.Text == "" {
			return nil, fmt.Errorf("source_paragraphs[%d].text is required", index)
		}
		if len(paragraph.EvidenceIDs) == 0 {
			return nil, fmt.Errorf("source_paragraphs[%d] requires evidence_ids", index)
		}
		if err := validateEvidenceIDs(paragraph.EvidenceIDs); err != nil {
			return nil, fmt.Errorf("source_paragraphs[%d]: %w", index, err)
		}
		for _, id := range paragraph.EvidenceIDs {
			id = strings.TrimSpace(id)
			if _, ok := allowed[id]; !ok {
				return nil, fmt.Errorf("source_paragraphs[%d] evidence ID %q is not present on concept", index, id)
			}
		}
		paragraph.EvidenceIDs = sortedEvidenceIDs(paragraph.EvidenceIDs)
		result = append(result, paragraph)
	}
	return result, nil
}

// ValidateConceptPageRender checks page-level invariants before a caller hands
// the result to a Wiki writer.
func ValidateConceptPageRender(render ConceptPageRender, expectedTitle string) error {
	if render.PageType != "index" {
		return fmt.Errorf("concept page_type must be index")
	}
	if strings.TrimSpace(render.Title) == "" || render.Title != strings.TrimSpace(expectedTitle) {
		return fmt.Errorf("concept page title must equal title")
	}
	if strings.TrimSpace(render.Content) == "" {
		return fmt.Errorf("concept page content is empty")
	}
	lines := strings.Split(render.Content, "\n")
	h1Count := 0
	h1Title := ""
	for _, line := range lines {
		if strings.HasPrefix(line, "# ") {
			h1Count++
			h1Title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	if h1Count != 1 || h1Title != render.Title {
		return fmt.Errorf("concept page must contain one H1 matching title")
	}
	frontmatter, _ := parseWikiFrontmatter(render.Content)
	if frontmatterStringValue(frontmatter, "title") != render.Title {
		return fmt.Errorf("concept page title and frontmatter title must match")
	}
	if frontmatterStringValue(frontmatter, "type") != string(TypeConcept) ||
		frontmatterStringValue(frontmatter, "primary_type") != string(TypeConcept) {
		return fmt.Errorf("concept page type and primary_type must both be concept")
	}
	if frontmatterStringValue(frontmatter, "information_nature") != "概念" {
		return fmt.Errorf("concept page information_nature must be 概念")
	}
	timeRange := frontmatterStringValue(frontmatter, "time_range")
	if timeRange == "" {
		return fmt.Errorf("concept page time_range is required")
	}
	if isPlaceholderTimeRange(timeRange) {
		return fmt.Errorf("concept page time_range must not be a placeholder")
	}
	if !bytes.Contains([]byte(render.Content), []byte("## 概念结构")) {
		return fmt.Errorf("concept page must contain concept structure section")
	}
	if !bytes.Contains([]byte(render.Content), []byte("## 知识来源")) {
		return fmt.Errorf("concept page must contain source section")
	}
	return nil
}

func isPlaceholderTimeRange(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	for _, phrase := range []string{
		"待定", "未知", "占位", "占位符",
		"placeholder", "unknown", "todo", "tbd", "n/a",
	} {
		if lower == phrase || strings.HasPrefix(lower, phrase) {
			return true
		}
	}
	return false
}
