package knowledge

import (
	"bytes"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// InsightFieldEvidence connects one insight framework field to its evidence.
type InsightFieldEvidence struct {
	EvidenceIDs []string `json:"evidence_ids"`
}

// InsightSourceParagraph is an optional readable source passage for an
// insight page. Evidence IDs remain in frontmatter and audit data.
type InsightSourceParagraph struct {
	Text        string   `json:"text"`
	EvidenceIDs []string `json:"evidence_ids"`
}

// InsightTemplateInput is the metadata needed to render one insight page.
// It intentionally accepts no subtitle or raw transcript field.
type InsightTemplateInput struct {
	Object           ClassifiedKnowledge             `json:"object"`
	Aliases          []string                        `json:"aliases,omitempty"`
	Description      string                          `json:"description,omitempty"`
	TimeRange        string                          `json:"time_range"`
	FieldEvidence    map[string]InsightFieldEvidence `json:"field_evidence,omitempty"`
	SourceParagraphs []InsightSourceParagraph        `json:"source_paragraphs,omitempty"`
}

// InsightPageRender is a deterministic Wiki write payload.
type InsightPageRender struct {
	PageType string `json:"page_type"`
	Title    string `json:"title"`
	Content  string `json:"content"`
}

type insightPageFrontmatter struct {
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

// RenderInsightPage renders the insight framework from type-frameworks.md.
// Fields are shown only when both their value and supporting evidence exist.
func RenderInsightPage(input InsightTemplateInput) (InsightPageRender, error) {
	object := cloneClassifiedKnowledge(input.Object)
	if object.PrimaryType != TypeInsight {
		return InsightPageRender{}, fmt.Errorf("insight template requires primary_type insight")
	}
	if err := ValidatePublishGate(object); err != nil {
		return InsightPageRender{}, fmt.Errorf("validate insight for template: %w", err)
	}
	if strings.ToLower(strings.TrimSpace(object.AuditStatus)) != "passed" {
		return InsightPageRender{}, fmt.Errorf("insight template requires audit_status passed")
	}

	framework, err := FrameworkFor(TypeInsight, "")
	if err != nil {
		return InsightPageRender{}, err
	}
	title := strings.TrimSpace(object.Title)
	if title == "" {
		return InsightPageRender{}, fmt.Errorf("insight title is required")
	}
	if strings.ContainsAny(title, "\r\n\t") {
		return InsightPageRender{}, fmt.Errorf("insight title contains unstable whitespace")
	}
	timeRange := strings.TrimSpace(input.TimeRange)
	if timeRange == "" {
		return InsightPageRender{}, fmt.Errorf("insight time_range is required")
	}
	if isPlaceholderTimeRange(timeRange) {
		return InsightPageRender{}, fmt.Errorf("insight time_range must not be a placeholder")
	}
	if err := rejectInsightCaseFields(object.StructureFields); err != nil {
		return InsightPageRender{}, err
	}

	allowedEvidence := make(map[string]struct{}, len(object.EvidenceIDs))
	for _, evidenceID := range object.EvidenceIDs {
		allowedEvidence[strings.TrimSpace(evidenceID)] = struct{}{}
	}
	fieldEvidence, err := normalizedInsightFieldEvidence(input.FieldEvidence, framework, allowedEvidence)
	if err != nil {
		return InsightPageRender{}, err
	}
	normalizedObjectFields, err := normalizedInsightStructureFields(object.StructureFields)
	if err != nil {
		return InsightPageRender{}, err
	}
	renderedFields := make(map[string]string, len(framework.Fields))
	for _, field := range framework.Fields {
		value := strings.TrimSpace(normalizedObjectFields[field.Key])
		if value == "" || len(fieldEvidence[field.Key]) == 0 {
			continue
		}
		renderedFields[field.Key] = value
	}
	if strings.TrimSpace(renderedFields["claim"]) == "" {
		return InsightPageRender{}, fmt.Errorf("insight template requires core claim with evidence")
	}
	if strings.TrimSpace(renderedFields["reasoning"]) == "" && strings.TrimSpace(renderedFields["qualifications"]) == "" {
		return InsightPageRender{}, fmt.Errorf("insight template requires reasoning or qualifications with evidence")
	}

	aliases := cleanAliases(input.Aliases, title)
	description := strings.TrimSpace(input.Description)
	if description == "" {
		description = strings.TrimSpace(object.CoreContent)
	}
	if description == "" {
		return InsightPageRender{}, fmt.Errorf("insight description is required")
	}
	paragraphs, err := normalizeInsightSourceParagraphs(input.SourceParagraphs, allowedEvidence)
	if err != nil {
		return InsightPageRender{}, err
	}

	frontmatter := insightPageFrontmatter{
		ID: object.CandidateID, KnowledgeObjectID: object.CandidateID,
		Type: string(TypeInsight), PrimaryType: string(TypeInsight),
		SourceVideoID: object.SourceVideoID, TranscriptGeneration: object.TranscriptGeneration,
		TimeRange: timeRange, Title: title, Aliases: aliases,
		InformationNature: "洞察", AuditStatus: "passed",
		ClassificationConfidence: object.ClassificationConfidence,
		EvidenceIDs:              sortedEvidenceIDs(object.EvidenceIDs), SourceRefs: sourceDocumentRefs(object.SourceDocumentID),
		StructureFields: renderedFields, RelatedContent: []any{}, Relations: []any{},
	}
	frontmatterYAML, err := yaml.Marshal(frontmatter)
	if err != nil {
		return InsightPageRender{}, fmt.Errorf("marshal insight frontmatter: %w", err)
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
	body.WriteString("\n\n## 洞察结构\n\n")
	for _, field := range framework.Fields {
		if value := renderedFields[field.Key]; value != "" {
			fmt.Fprintf(&body, "- %s：%s\n", field.Label, value)
		}
	}
	body.WriteString("\n## 知识来源\n\n时间范围：")
	body.WriteString(timeRange)
	body.WriteString("\n")
	for _, paragraph := range paragraphs {
		writeEntityBlockquote(&body, paragraph.Text)
	}
	body.WriteString("\n## 信息性质\n\n洞察\n")

	return InsightPageRender{PageType: "index", Title: title, Content: body.String()}, nil
}

func rejectInsightCaseFields(raw map[string]string) error {
	for key, value := range raw {
		normalized := strings.ToLower(strings.TrimSpace(key))
		switch normalized {
		case "context", "actors", "choices", "actions", "outcome", "retrospective":
			if strings.TrimSpace(value) != "" {
				return fmt.Errorf("insight template rejects case field %q", normalized)
			}
		}
	}
	return nil
}

func normalizedInsightFieldEvidence(raw map[string]InsightFieldEvidence, framework FrameworkEntry, allowed map[string]struct{}) (map[string][]string, error) {
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
			return nil, fmt.Errorf("field_evidence.%s is not valid for insight", key)
		}
		if prior, ok := seenKeys[key]; ok && prior != rawKey {
			return nil, fmt.Errorf("field_evidence contains duplicate normalized field %q", key)
		}
		seenKeys[key] = rawKey
		if len(value.EvidenceIDs) == 0 {
			continue
		}
		if err := validateEvidenceIDs(value.EvidenceIDs); err != nil {
			return nil, fmt.Errorf("field_evidence.%s: %w", key, err)
		}
		for _, id := range value.EvidenceIDs {
			if _, ok := allowed[strings.TrimSpace(id)]; !ok {
				return nil, fmt.Errorf("field_evidence.%s evidence ID %q is not present on insight", key, id)
			}
		}
		result[key] = sortedEvidenceIDs(value.EvidenceIDs)
	}
	return result, nil
}

func normalizedInsightStructureFields(raw map[string]string) (map[string]string, error) {
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

func normalizeInsightSourceParagraphs(raw []InsightSourceParagraph, allowed map[string]struct{}) ([]InsightSourceParagraph, error) {
	result := make([]InsightSourceParagraph, 0, len(raw))
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
			if _, ok := allowed[strings.TrimSpace(id)]; !ok {
				return nil, fmt.Errorf("source_paragraphs[%d] evidence ID %q is not present on insight", index, id)
			}
		}
		paragraph.EvidenceIDs = sortedEvidenceIDs(paragraph.EvidenceIDs)
		result = append(result, paragraph)
	}
	return result, nil
}

// ValidateInsightPageRender checks page-level invariants before Wiki writing.
func ValidateInsightPageRender(render InsightPageRender, expectedTitle string) error {
	if render.PageType != "index" {
		return fmt.Errorf("insight page_type must be index")
	}
	if strings.TrimSpace(render.Title) == "" || render.Title != strings.TrimSpace(expectedTitle) {
		return fmt.Errorf("insight page title must equal title")
	}
	if strings.TrimSpace(render.Content) == "" {
		return fmt.Errorf("insight page content is empty")
	}
	h1Count, h1Title := 0, ""
	for _, line := range strings.Split(render.Content, "\n") {
		if strings.HasPrefix(line, "# ") {
			h1Count++
			h1Title = strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	if h1Count != 1 || h1Title != render.Title {
		return fmt.Errorf("insight page must contain one H1 matching title")
	}
	frontmatter, _ := parseWikiFrontmatter(render.Content)
	if frontmatterStringValue(frontmatter, "title") != render.Title {
		return fmt.Errorf("insight page title and frontmatter title must match")
	}
	if frontmatterStringValue(frontmatter, "type") != string(TypeInsight) || frontmatterStringValue(frontmatter, "primary_type") != string(TypeInsight) {
		return fmt.Errorf("insight page type and primary_type must both be insight")
	}
	if frontmatterStringValue(frontmatter, "information_nature") != "洞察" {
		return fmt.Errorf("insight page information_nature must be 洞察")
	}
	timeRange := frontmatterStringValue(frontmatter, "time_range")
	if timeRange == "" {
		return fmt.Errorf("insight page time_range is required")
	}
	if isPlaceholderTimeRange(timeRange) {
		return fmt.Errorf("insight page time_range must not be a placeholder")
	}
	if !bytes.Contains([]byte(render.Content), []byte("## 洞察结构")) {
		return fmt.Errorf("insight page must contain insight structure section")
	}
	if !bytes.Contains([]byte(render.Content), []byte("## 知识来源")) {
		return fmt.Errorf("insight page must contain source section")
	}
	return nil
}
