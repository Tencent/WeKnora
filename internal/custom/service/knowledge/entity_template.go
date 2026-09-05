package knowledge

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// EntityFieldEvidence connects one framework field to the evidence IDs that
// support its value. The field value remains in ClassifiedKnowledge so the
// template cannot invent content while rendering.
type EntityFieldEvidence struct {
	EvidenceIDs []string `json:"evidence_ids"`
}

// EntitySourceParagraph is the readable source passage shown on an entity
// page. Evidence IDs remain frontmatter/audit data and are not exposed in the
// reader-facing body.
type EntitySourceParagraph struct {
	Text        string   `json:"text"`
	EvidenceIDs []string `json:"evidence_ids"`
}

// EntityTemplateInput is the complete metadata needed to render one entity
// page. No subtitle or raw transcript field is accepted by this contract.
type EntityTemplateInput struct {
	Object           ClassifiedKnowledge            `json:"object"`
	Aliases          []string                       `json:"aliases,omitempty"`
	Description      string                         `json:"description,omitempty"`
	TimeRange        string                         `json:"time_range"`
	FieldEvidence    map[string]EntityFieldEvidence `json:"field_evidence,omitempty"`
	SourceParagraphs []EntitySourceParagraph        `json:"source_paragraphs,omitempty"`
}

// EntityPageRender is a deterministic Wiki write payload. The caller still
// decides when and whether to call the external Wiki writer.
type EntityPageRender struct {
	PageType string `json:"page_type"`
	Title    string `json:"title"`
	Content  string `json:"content"`
}

type entityPageFrontmatter struct {
	ID                       string            `yaml:"id"`
	KnowledgeObjectID        string            `yaml:"knowledge_object_id"`
	Type                     string            `yaml:"type"`
	PrimaryType              string            `yaml:"primary_type"`
	EntitySubType            string            `yaml:"entity_sub_type"`
	SourceVideoID            string            `yaml:"source_video_id"`
	TranscriptGeneration     string            `yaml:"transcript_generation"`
	TimeRange                string            `yaml:"time_range"`
	Title                    string            `yaml:"title"`
	CanonicalName            string            `yaml:"canonical_name"`
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

// RenderEntityPage renders the six entity subtypes using the framework
// loaded from type-frameworks.md. Fields are shown only when both their value
// and supporting evidence are present.
func RenderEntityPage(input EntityTemplateInput) (EntityPageRender, error) {
	object := cloneClassifiedKnowledge(input.Object)
	if object.PrimaryType != TypeEntity {
		return EntityPageRender{}, fmt.Errorf("entity template requires primary_type entity")
	}
	if err := ValidatePublishGate(object); err != nil {
		return EntityPageRender{}, fmt.Errorf("validate entity for template: %w", err)
	}
	if strings.ToLower(strings.TrimSpace(object.AuditStatus)) != "passed" {
		return EntityPageRender{}, fmt.Errorf("entity template requires audit_status passed")
	}

	subtype := strings.TrimSpace(object.EntitySubType)
	framework, err := FrameworkFor(TypeEntity, subtype)
	if err != nil {
		return EntityPageRender{}, err
	}
	title := strings.TrimSpace(object.Title)
	if title == "" {
		return EntityPageRender{}, fmt.Errorf("entity title is required")
	}
	if strings.ContainsAny(title, "\r\n\t") {
		return EntityPageRender{}, fmt.Errorf("entity title contains unstable whitespace")
	}
	timeRange := strings.TrimSpace(input.TimeRange)
	if timeRange == "" {
		return EntityPageRender{}, fmt.Errorf("entity time_range is required")
	}

	allowedEvidence := make(map[string]struct{}, len(object.EvidenceIDs))
	for _, evidenceID := range object.EvidenceIDs {
		allowedEvidence[strings.TrimSpace(evidenceID)] = struct{}{}
	}
	fieldEvidence, err := normalizedEntityFieldEvidence(input.FieldEvidence, framework, allowedEvidence)
	if err != nil {
		return EntityPageRender{}, err
	}
	renderedFields := make(map[string]string, len(framework.Fields))
	normalizedObjectFields, err := normalizedEntityStructureFields(object.StructureFields)
	if err != nil {
		return EntityPageRender{}, err
	}
	for _, field := range framework.Fields {
		value := strings.TrimSpace(normalizedObjectFields[field.Key])
		if value == "" || len(fieldEvidence[field.Key]) == 0 {
			continue
		}
		renderedFields[field.Key] = value
	}
	if len(renderedFields) < 2 {
		return EntityPageRender{}, fmt.Errorf("entity template requires at least 2 populated fields with evidence (got %d)", len(renderedFields))
	}

	aliases := cleanAliases(input.Aliases, title)
	description := strings.TrimSpace(input.Description)
	if description == "" {
		description = strings.TrimSpace(object.CoreContent)
	}
	if description == "" {
		return EntityPageRender{}, fmt.Errorf("entity description is required")
	}
	paragraphs, err := normalizeEntitySourceParagraphs(input.SourceParagraphs, allowedEvidence)
	if err != nil {
		return EntityPageRender{}, err
	}

	frontmatter := entityPageFrontmatter{
		ID:                       object.CandidateID,
		KnowledgeObjectID:        object.CandidateID,
		Type:                     string(TypeEntity),
		PrimaryType:              string(TypeEntity),
		EntitySubType:            subtype,
		SourceVideoID:            object.SourceVideoID,
		TranscriptGeneration:     object.TranscriptGeneration,
		TimeRange:                timeRange,
		Title:                    title,
		CanonicalName:            title,
		Aliases:                  aliases,
		InformationNature:        entityInformationNatureLabel(subtype),
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
		return EntityPageRender{}, fmt.Errorf("marshal entity frontmatter: %w", err)
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
	body.WriteString("\n\n## 关键信息维度\n\n")
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
	body.WriteString("\n## 信息性质\n\n")
	body.WriteString(entityInformationNatureLabel(subtype))
	body.WriteString("\n")

	return EntityPageRender{PageType: "index", Title: title, Content: body.String()}, nil
}

func entityInformationNatureLabel(entitySubType string) string {
	switch strings.TrimSpace(entitySubType) {
	case "person":
		return "人物"
	case "organization":
		return "机构"
	case "product":
		return "产品"
	case "technology":
		return "技术"
	case "industry":
		return "行业"
	case "place":
		return "地点"
	default:
		return ""
	}
}

func normalizedEntityFieldEvidence(
	raw map[string]EntityFieldEvidence,
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
			return nil, fmt.Errorf("field_evidence.%s is not valid for entity subtype %s", key, framework.EntitySubType)
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
				return nil, fmt.Errorf("field_evidence.%s evidence ID %q is not present on entity", key, id)
			}
		}
		result[key] = sortedEvidenceIDs(ids)
	}
	return result, nil
}

func normalizedEntityStructureFields(raw map[string]string) (map[string]string, error) {
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

func writeEntityBlockquote(builder *strings.Builder, text string) {
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		fmt.Fprintf(builder, "> %s\n", strings.TrimRight(line, "\r"))
	}
}

func normalizeEntitySourceParagraphs(
	raw []EntitySourceParagraph,
	allowed map[string]struct{},
) ([]EntitySourceParagraph, error) {
	result := make([]EntitySourceParagraph, 0, len(raw))
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
				return nil, fmt.Errorf("source_paragraphs[%d] evidence ID %q is not present on entity", index, id)
			}
		}
		paragraph.EvidenceIDs = sortedEvidenceIDs(paragraph.EvidenceIDs)
		result = append(result, paragraph)
	}
	return result, nil
}

func cleanAliases(raw []string, title string) []string {
	seen := map[string]struct{}{strings.ToLower(strings.TrimSpace(title)): {}}
	result := make([]string, 0, len(raw))
	for _, alias := range raw {
		alias = strings.TrimSpace(alias)
		key := strings.ToLower(alias)
		if alias == "" || key == strings.ToLower(strings.TrimSpace(title)) {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, alias)
	}
	sort.Strings(result)
	return result
}

// ValidateEntityPageRender checks the page-level invariants before a caller
// hands the result to a Wiki writer.
func ValidateEntityPageRender(render EntityPageRender, expectedTitle string) error {
	if render.PageType != "index" {
		return fmt.Errorf("entity page_type must be index")
	}
	if strings.TrimSpace(render.Title) == "" || render.Title != strings.TrimSpace(expectedTitle) {
		return fmt.Errorf("entity page title must equal title")
	}
	if strings.TrimSpace(render.Content) == "" {
		return fmt.Errorf("entity page content is empty")
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
		return fmt.Errorf("entity page must contain one H1 matching title")
	}
	frontmatter, _ := parseWikiFrontmatter(render.Content)
	if frontmatterStringValue(frontmatter, "title") != render.Title ||
		frontmatterStringValue(frontmatter, "canonical_name") != render.Title {
		return fmt.Errorf("entity page title and canonical_name must match")
	}
	if frontmatterStringValue(frontmatter, "type") != string(TypeEntity) ||
		frontmatterStringValue(frontmatter, "primary_type") != string(TypeEntity) {
		return fmt.Errorf("entity page type and primary_type must both be entity")
	}
	if frontmatterStringValue(frontmatter, "time_range") == "" {
		return fmt.Errorf("entity page time_range is required")
	}
	if !bytes.Contains([]byte(render.Content), []byte("## 知识来源")) {
		return fmt.Errorf("entity page must contain source section")
	}
	return nil
}

func frontmatterStringValue(values map[string]any, key string) string {
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}
