// Package knowledge Wiki 页面到 5 类型知识的映射（CP-T007）。
//
// 设计要点（spec §2.1 / §2.2）：
//   - KnowledgeType（前端枚举）: entity / concept / case / methodology / insight
//   - 页面类型既可能由 WeKnora 原生 page_type 表示，也可能由 skill frontmatter.type 表示
//   - 真实类型统一映射在聚合 API 内部做，前端不感知实体 6 类细分
//
// 页面来源：
//   - WeKnora 原生 Wiki：page_type 为 entity / concept
//   - extract-video-knowledge skill：page_type=index，frontmatter.type 表示五类业务知识
package knowledge

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// WikiObjectContractVersion identifies the shared AI, write-tool, API, and UI
// contract for P3 knowledge objects.
const WikiObjectContractVersion = "p3-wiki-object/v1"

// KnowledgeType 前端五类型枚举（与 spec §2.1 对齐）
type KnowledgeType string

const (
	TypeEntity      KnowledgeType = "entity"
	TypeConcept     KnowledgeType = "concept"
	TypeCase        KnowledgeType = "case"
	TypeMethodology KnowledgeType = "methodology"
	TypeInsight     KnowledgeType = "insight"
)

// SkillFrontmatterType skill 内部类型
const (
	SkillTypeMethodology = "methodology"
	SkillTypeCase        = "case"
	SkillTypeInsight     = "insight"
	SkillTypeConcept     = "concept"
	SkillTypeEntity      = "entity"
)

// MapSkillToKnowledgeType 把 skill frontmatter.type 映回前端 5 类型
func MapSkillToKnowledgeType(frontmatterType string) KnowledgeType {
	switch frontmatterType {
	case SkillTypeMethodology:
		return TypeMethodology
	case SkillTypeCase:
		return TypeCase
	case SkillTypeInsight:
		return TypeInsight
	case SkillTypeEntity:
		return TypeEntity
	case SkillTypeConcept:
		return TypeConcept
	default:
		if IsEntitySubType(frontmatterType) {
			return TypeEntity
		}
		return ""
	}
}

// MapPageTypeToKnowledgeType 把 WeKnora 原生 page_type 映回 5 类型
func MapPageTypeToKnowledgeType(pageType string, frontmatterType string) KnowledgeType {
	switch pageType {
	case "entity":
		return TypeEntity
	case "concept":
		// 概念页可能是 skill 挂靠的 concept（真实类型在 frontmatter）
		if frontmatterType == "" {
			return TypeConcept
		}
		return MapSkillToKnowledgeType(frontmatterType)
	case "index":
		return MapSkillToKnowledgeType(frontmatterType)
	default:
		return ""
	}
}

// IsEntitySubType 判断是否为实体 6 类细分之一
func IsEntitySubType(t string) bool {
	digest, err := LoadDefaultFrameworkDigest()
	if err != nil {
		return false
	}
	for _, entry := range digest.Entries {
		if entry.PrimaryType == TypeEntity && entry.EntitySubType == t {
			return true
		}
	}
	return false
}

// AnchorItem 关联知识条目（聚合 API 返回结构）
type AnchorItem struct {
	ID                   string        `json:"id"` // Wiki page id 或 knowledge id
	Slug                 string        `json:"slug"`
	Title                string        `json:"title"`
	Type                 KnowledgeType `json:"type"` // 5 类型之一
	PrimaryType          KnowledgeType `json:"primary_type,omitempty"`
	KnowledgeObjectID    string        `json:"knowledge_object_id,omitempty"`
	TranscriptGeneration string        `json:"transcript_generation,omitempty"`
	AuditStatus          string        `json:"audit_status,omitempty"`
	CoreContent          string        `json:"core_content,omitempty"`
	StructureFields      []DetailField `json:"structure_fields,omitempty"`
	EvidenceIDs          []string      `json:"evidence_ids,omitempty"`
	InformationNature    string        `json:"information_nature,omitempty"`
	TimeRange            string        `json:"time_range,omitempty"`
	RelatedKnowledge     []DetailLink  `json:"related_knowledge,omitempty"`
	RelatedEntities      []DetailLink  `json:"related_entities,omitempty"`
	RelatedContent       []DetailLink  `json:"related_content,omitempty"`
	SourceVideoTitle     string        `json:"source_video_title,omitempty"`
	Timestamp            string        `json:"timestamp,omitempty"`
	Seconds              int           `json:"seconds,omitempty"`
	EntitySubType        string        `json:"entity_sub_type,omitempty"` // person / organization / ...
	PageType             string        `json:"page_type"`                 // WeKnora 原生 page_type
	Source               string        `json:"source"`                    // "native" / "skill"
	Confidence           float64       `json:"confidence,omitempty"`
	RelatedVideoIDs      []string      `json:"related_video_ids,omitempty"`
}

// DetailField carries the type-framework structure fields extracted from a Wiki
// page. Labels are frontend-facing Chinese labels from extract-video-knowledge.
type DetailField struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Value string `json:"value"`
}

// DetailLink is a Wiki double-link extracted from the page contract sections
// such as related_atom_ids / related_entity_ids.
type DetailLink struct {
	Title      string `json:"title"`
	Slug       string `json:"slug,omitempty"`
	TargetType string `json:"target_type,omitempty"`
}

// StructuredRelation is the only relation contract accepted by the product graph.
// Wiki double-links remain a reading aid and are intentionally represented separately.
type StructuredRelation struct {
	RelationID            string                         `yaml:"relation_id,omitempty" json:"relation_id,omitempty"`
	RelationType          string                         `yaml:"relation_type" json:"relation_type"`
	TargetObjectID        string                         `yaml:"target_object_id" json:"target_object_id"`
	TargetWikiPageID      string                         `yaml:"target_wiki_page_id" json:"target_wiki_page_id"`
	TargetTitle           string                         `yaml:"target_title,omitempty" json:"target_title,omitempty"`
	TargetSlug            string                         `yaml:"target_slug,omitempty" json:"target_slug,omitempty"`
	EvidenceIDs           []string                       `yaml:"evidence_ids,omitempty" json:"evidence_ids,omitempty"`
	TimeRange             string                         `yaml:"time_range,omitempty" json:"time_range,omitempty"`
	Confidence            float64                        `yaml:"confidence,omitempty" json:"confidence,omitempty"`
	EvidenceContributions []RelationEvidenceContribution `yaml:"evidence_contributions,omitempty" json:"evidence_contributions,omitempty"`
}

// RelationEvidenceContribution scopes the proof for one canonical relation to
// the video and transcript generation where that relation was observed.
type RelationEvidenceContribution struct {
	VideoID              string   `yaml:"video_id" json:"video_id"`
	TranscriptGeneration string   `yaml:"transcript_generation" json:"transcript_generation"`
	EvidenceIDs          []string `yaml:"evidence_ids" json:"evidence_ids"`
	TimeRange            string   `yaml:"time_range" json:"time_range"`
	Confidence           float64  `yaml:"confidence" json:"confidence"`
	QualityStatus        string   `yaml:"quality_status" json:"quality_status"`
}

type WikiObjectValidation struct {
	KnowledgeObjectID             string
	KnowledgeType                 KnowledgeType
	EntitySubType                 string
	Title                         string
	Aliases                       []string
	CoreContent                   string
	SourceVideoID                 string
	TranscriptGeneration          string
	AuditStatus                   string
	ClassificationConfidence      float64
	EvidenceIDs                   []string
	SourceRefs                    []string
	EvidenceContributions         []EvidenceContribution
	EvidenceContributionsExplicit bool
	StructureFields               map[string]string
	Relations                     []StructuredRelation
}

var wikiObjectTypes = map[string]KnowledgeType{
	"entity":      TypeEntity,
	"concept":     TypeConcept,
	"case":        TypeCase,
	"methodology": TypeMethodology,
	"insight":     TypeInsight,
}

var wikiLabelPattern = regexp.MustCompile(`^\s*(?:[-+*]\s*)?(?:#{1,6}\s*)?([^:：|]+)\s*[:：|]\s*(.*?)\s*$`)

// ValidateWikiObjectPage validates the minimum contract required before a Wiki
// knowledge object can be projected or used as a trusted QA source.
func ValidateWikiObjectPage(content, pageType, expectedVideoID, expectedGeneration string) (WikiObjectValidation, error) {
	result := WikiObjectValidation{}
	if strings.TrimSpace(content) == "" {
		return result, fmt.Errorf("wiki content is empty")
	}
	frontmatter, body := parseWikiFrontmatter(content)
	if strings.TrimSpace(pageType) != "index" {
		return result, fmt.Errorf("knowledge object page_type must be index")
	}
	if declaredPageType := strings.ToLower(strings.TrimSpace(stringValue(frontmatter["page_type"]))); declaredPageType != "" && declaredPageType != "index" {
		return result, fmt.Errorf("knowledge object frontmatter page_type must be index")
	}
	primaryType := strings.ToLower(strings.TrimSpace(stringValue(frontmatter["primary_type"])))
	rawType := strings.ToLower(strings.TrimSpace(stringValue(frontmatter["type"])))
	if primaryType == "" || rawType == "" {
		return result, fmt.Errorf("frontmatter must contain both type and primary_type")
	}
	if primaryType != "" && rawType != "" && primaryType != rawType {
		return result, fmt.Errorf("primary_type and type must match")
	}
	if primaryType != "" {
		rawType = primaryType
	}
	if rawType == "method" {
		return result, fmt.Errorf("method is not a supported knowledge type; use methodology")
	}
	if err := rejectMultipleTopLevelTypes(frontmatter, rawType); err != nil {
		return result, err
	}
	knowledgeType, ok := wikiObjectTypes[rawType]
	if !ok {
		return result, fmt.Errorf("unsupported knowledge object type: %s", rawType)
	}
	entitySubType := strings.ToLower(strings.TrimSpace(stringValue(frontmatter["entity_sub_type"])))
	if knowledgeType == TypeEntity {
		if !IsEntitySubType(entitySubType) {
			return result, fmt.Errorf("entity_sub_type is required for entity")
		}
	} else if entitySubType != "" {
		return result, fmt.Errorf("entity_sub_type is only valid for entity")
	}

	result.KnowledgeObjectID = firstNonEmptyString(
		stringValue(frontmatter["knowledge_object_id"]),
		stringValue(frontmatter["id"]),
	)
	if result.KnowledgeObjectID == "" {
		return result, fmt.Errorf("knowledge_object_id is required")
	}
	result.KnowledgeType = knowledgeType
	result.EntitySubType = entitySubType
	result.Title = firstNonEmptyString(
		stringValue(frontmatter["title"]),
		stringValue(frontmatter["canonical_name"]),
		firstWikiHeading(body),
	)
	if result.Title == "" {
		return result, fmt.Errorf("title or canonical_name is required")
	}
	result.Aliases = stringSliceValue(frontmatter["aliases"])
	result.SourceVideoID = strings.TrimSpace(stringValue(frontmatter["source_video_id"]))
	result.TranscriptGeneration = strings.TrimSpace(stringValue(frontmatter["transcript_generation"]))
	// Canonical pages keep a legacy primary source for older readers, while
	// evidence_contributions is the authoritative multi-video ownership index.
	// A read scoped to another active contribution must therefore validate that
	// contribution instead of rejecting the otherwise valid canonical page.
	contributions, contributionErr := ParseEvidenceContributions(content)
	if contributionErr != nil {
		return result, contributionErr
	}
	expectedVideoID = strings.TrimSpace(expectedVideoID)
	expectedGeneration = strings.TrimSpace(expectedGeneration)
	if expectedVideoID != "" && expectedGeneration != "" &&
		(result.SourceVideoID != expectedVideoID || result.TranscriptGeneration != expectedGeneration) {
		matched := false
		for _, contribution := range contributions {
			if contribution.VideoID == expectedVideoID && contribution.TranscriptGeneration == expectedGeneration && strings.EqualFold(strings.TrimSpace(contribution.QualityStatus), "passed") {
				matched = true
				break
			}
		}
		if !matched {
			return result, fmt.Errorf("source_video_id or transcript_generation does not match the active contribution")
		}
		result.SourceVideoID = expectedVideoID
		result.TranscriptGeneration = expectedGeneration
	}
	if result.SourceVideoID == "" || result.TranscriptGeneration == "" {
		return result, fmt.Errorf("source_video_id and transcript_generation are required")
	}
	result.AuditStatus = strings.ToLower(strings.TrimSpace(stringValue(frontmatter["audit_status"])))
	if result.AuditStatus != "passed" {
		return result, fmt.Errorf("audit_status must be passed")
	}
	if nature := strings.TrimSpace(stringValue(frontmatter["information_nature"])); nature != knowledgeInformationNature(knowledgeType, entitySubType) {
		return result, fmt.Errorf("information_nature must be %s", knowledgeInformationNature(knowledgeType, entitySubType))
	}
	result.ClassificationConfidence = floatValue(frontmatter["classification_confidence"])
	if result.ClassificationConfidence <= 0 || result.ClassificationConfidence > 1 {
		return result, fmt.Errorf("classification_confidence must be between 0 and 1")
	}
	result.EvidenceIDs = stringSliceValue(frontmatter["evidence_ids"])
	if len(result.EvidenceIDs) == 0 {
		return result, fmt.Errorf("evidence_ids must contain at least one ID")
	}
	result.SourceRefs = stringSliceValue(frontmatter["source_refs"])
	if len(result.SourceRefs) == 0 {
		return result, fmt.Errorf("source_refs must contain at least one source document ID")
	}
	result.EvidenceContributions = contributions
	_, hasContributionList := frontmatter["evidence_contributions"]
	_, hasSingleContribution := frontmatter["evidence_contribution"]
	result.EvidenceContributionsExplicit = hasContributionList || hasSingleContribution
	result.StructureFields = structureFields(frontmatter["structure_fields"], body)
	required := frameworkKeys(knowledgeType, entitySubType)
	if err := rejectForeignStructureFieldsForType(frontmatter["structure_fields"], required, knowledgeType, entitySubType); err != nil {
		return result, err
	}
	filled := 0
	for _, key := range required {
		if strings.TrimSpace(result.StructureFields[key]) != "" {
			filled++
		}
	}
	minimum := 2
	switch knowledgeType {
	case TypeEntity:
		minimum = 1
	case TypeCase:
		minimum = 3
	}
	if filled < minimum {
		return result, fmt.Errorf("%s requires at least %d populated structure fields", knowledgeType, minimum)
	}
	result.CoreContent = firstNonEmptyString(
		stringValue(frontmatter["core_content"]),
		wikiBodyLabeledValue(body, "一句话概述", "核心内容"),
	)
	if result.CoreContent == "" {
		return result, fmt.Errorf("core content is required")
	}
	return result, nil
}

// ValidateWikiObjectWritePage applies the current strict write contract. Read
// paths keep accepting legacy pages whose core content only exists in the body,
// while every new or repaired AI write must persist the structured field.
func ValidateWikiObjectWritePage(content, pageType, expectedVideoID, expectedGeneration string) (WikiObjectValidation, error) {
	frontmatter, _ := parseWikiFrontmatter(content)
	if strings.TrimSpace(stringValue(frontmatter["core_content"])) == "" {
		return WikiObjectValidation{}, fmt.Errorf("frontmatter.core_content is required by %s", WikiObjectContractVersion)
	}
	result, err := ValidateWikiObjectPage(content, pageType, expectedVideoID, expectedGeneration)
	if err != nil {
		return result, err
	}
	result.CoreContent = strings.TrimSpace(stringValue(frontmatter["core_content"]))
	return result, nil
}

// IsWikiObjectCandidate identifies pages owned by the five-type contract. It
// intentionally checks key presence rather than valid YAML values so malformed
// candidate pages are routed into validation instead of bypassing it.
func IsWikiObjectCandidate(content string) bool {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return false
	}
	keys := make(map[string]struct{})
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if line == "---" {
			break
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, _, ok := strings.Cut(line, ":")
		if ok {
			keys[strings.TrimSpace(key)] = struct{}{}
		}
	}
	_, hasObjectID := keys["knowledge_object_id"]
	if !hasObjectID {
		_, hasObjectID = keys["id"]
	}
	_, hasVideoID := keys["source_video_id"]
	_, hasGeneration := keys["transcript_generation"]
	_, hasType := keys["type"]
	if !hasType {
		_, hasType = keys["primary_type"]
	}
	return hasObjectID && hasVideoID && hasGeneration && hasType
}

// ParseWikiObjectRelations validates the write-time shape of structured
// relations without making relation failures invalidate the source object at
// projection time. Callers that persist a relation-bearing page must also
// resolve and validate every target page before writing.
func ParseWikiObjectRelations(content string) ([]StructuredRelation, error) {
	frontmatter, _ := parseWikiFrontmatter(content)
	raw := frontmatter["relations"]
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("relations must be a list")
	}
	result := make([]StructuredRelation, 0, len(items))
	for index, item := range items {
		values, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("relations[%d] must be an object", index)
		}
		relation := StructuredRelation{
			RelationID:       strings.TrimSpace(stringValue(values["relation_id"])),
			RelationType:     strings.ToLower(strings.TrimSpace(stringValue(values["relation_type"]))),
			TargetObjectID:   strings.TrimSpace(stringValue(values["target_object_id"])),
			TargetWikiPageID: strings.TrimSpace(stringValue(values["target_wiki_page_id"])),
			EvidenceIDs:      stringSliceValue(values["evidence_ids"]),
			TimeRange:        strings.TrimSpace(stringValue(values["time_range"])),
			Confidence:       floatValue(values["confidence"]),
		}
		if rawContributions, exists := values["evidence_contributions"]; exists && rawContributions != nil {
			items, ok := rawContributions.([]any)
			if !ok {
				return nil, fmt.Errorf("relations[%d].evidence_contributions must be a list", index)
			}
			for contributionIndex, item := range items {
				data, ok := item.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("relations[%d].evidence_contributions[%d] must be an object", index, contributionIndex)
				}
				contribution := RelationEvidenceContribution{
					VideoID:              firstNonEmptyString(stringValue(data["video_id"]), stringValue(data["source_video_id"])),
					TranscriptGeneration: strings.TrimSpace(stringValue(data["transcript_generation"])),
					EvidenceIDs:          cleanStrings(stringSliceValue(data["evidence_ids"])),
					TimeRange:            strings.TrimSpace(stringValue(data["time_range"])),
					Confidence:           floatValue(data["confidence"]),
					QualityStatus:        strings.ToLower(strings.TrimSpace(stringValue(data["quality_status"]))),
				}
				if contribution.QualityStatus == "" {
					contribution.QualityStatus = "passed"
				}
				if contribution.VideoID == "" || contribution.TranscriptGeneration == "" || len(contribution.EvidenceIDs) == 0 || contribution.TimeRange == "" || contribution.Confidence <= 0 || contribution.Confidence > 1 {
					return nil, fmt.Errorf("relations[%d].evidence_contributions[%d] requires video_id, transcript_generation, evidence_ids, time_range and confidence", index, contributionIndex)
				}
				relation.EvidenceContributions = append(relation.EvidenceContributions, contribution)
			}
		}
		switch {
		case relation.RelationID == "":
			return nil, fmt.Errorf("relations[%d].relation_id is required", index)
		case relation.RelationType == "":
			return nil, fmt.Errorf("relations[%d].relation_type is required", index)
		case relation.TargetObjectID == "":
			return nil, fmt.Errorf("relations[%d].target_object_id is required", index)
		case relation.TargetWikiPageID == "":
			return nil, fmt.Errorf("relations[%d].target_wiki_page_id is required", index)
		case len(relation.EvidenceContributions) == 0 && len(relation.EvidenceIDs) == 0:
			return nil, fmt.Errorf("relations[%d].evidence_ids must contain at least one ID", index)
		case len(relation.EvidenceContributions) == 0 && relation.TimeRange == "":
			return nil, fmt.Errorf("relations[%d].time_range is required", index)
		case len(relation.EvidenceContributions) == 0 && (relation.Confidence <= 0 || relation.Confidence > 1):
			return nil, fmt.Errorf("relations[%d].confidence must be between 0 and 1", index)
		}
		result = append(result, relation)
	}
	return result, nil
}

func knowledgeInformationNature(knowledgeType KnowledgeType, entitySubType string) string {
	switch knowledgeType {
	case TypeEntity:
		switch entitySubType {
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
		}
	case TypeConcept:
		return "概念"
	case TypeMethodology:
		return "方法论"
	case TypeCase:
		return "案例"
	case TypeInsight:
		return "洞察"
	default:
		return ""
	}
	// The switch above covers every supported type; keep an explicit fallback
	// for forward-compatible compiler behavior when new types are introduced.
	return ""
}

func rejectMultipleTopLevelTypes(frontmatter map[string]any, rawType string) error {
	for _, key := range []string{"types", "knowledge_types", "primary_types", "secondary_types", "type_candidates"} {
		values := stringSliceValue(frontmatter[key])
		if len(values) == 0 {
			continue
		}
		seen := make(map[string]struct{}, len(values)+1)
		if rawType != "" {
			seen[rawType] = struct{}{}
		}
		for _, value := range values {
			normalized := strings.ToLower(strings.TrimSpace(value))
			if normalized == "method" {
				normalized = "methodology"
			}
			if _, ok := wikiObjectTypes[normalized]; ok {
				seen[normalized] = struct{}{}
			}
		}
		if len(seen) > 1 || key == "secondary_types" || key == "type_candidates" {
			return fmt.Errorf("multiple top-level types must be split into separate Wiki objects")
		}
	}
	return nil
}

func rejectForeignStructureFields(raw any, allowed []string) error {
	values, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	allowedSet := make(map[string]struct{}, len(allowed))
	for _, key := range allowed {
		allowedSet[key] = struct{}{}
	}
	for key := range values {
		if _, ok := allowedSet[strings.TrimSpace(key)]; !ok {
			return fmt.Errorf("structure_fields.%s is not valid for this knowledge type", key)
		}
	}
	return nil
}

func rejectForeignStructureFieldsForType(raw any, allowed []string, knowledgeType KnowledgeType, entitySubType string) error {
	if err := rejectForeignStructureFields(raw, allowed); err != nil {
		values, _ := raw.(map[string]any)
		for key := range values {
			if !containsTrimmedValue(allowed, key) {
				return fmt.Errorf(
					"structure_fields.%s is not valid for this knowledge type (%s); allowed fields: %s; required combination: %s",
					key, knowledgeType, strings.Join(allowed, ", "), knowledgeStructureRequiredCombination(knowledgeType, entitySubType),
				)
			}
		}
		return err
	}
	return nil
}

func containsTrimmedValue(values []string, target string) bool {
	target = strings.TrimSpace(target)
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}

func knowledgeStructureRequiredCombination(knowledgeType KnowledgeType, entitySubType string) string {
	switch knowledgeType {
	case TypeEntity:
		return "at least one identity, role, or use attribute allowed for entity_sub_type " + strings.TrimSpace(entitySubType)
	case TypeConcept:
		return "definition plus at least one of components, mechanism, distinction"
	case TypeMethodology:
		return "steps plus at least one of input, criteria, output, applicability"
	case TypeCase:
		return "context and actions plus at least one of outcome, retrospective"
	case TypeInsight:
		return "claim and reasoning"
	default:
		return "use the canonical fields for the selected knowledge type"
	}
}

// WikiObjectWriteRepairHint gives an Agent one complete, source-derived repair
// checklist after a rejected raw Markdown write. Without it, validation stops
// at the first error and the model can exhaust its iteration budget fixing one
// field at a time.
func WikiObjectWriteRepairHint(content string) string {
	frontmatter, _ := parseWikiFrontmatter(content)
	primary := strings.ToLower(strings.TrimSpace(stringValue(frontmatter["primary_type"])))
	rawType := strings.ToLower(strings.TrimSpace(stringValue(frontmatter["type"])))
	selected := primary
	if _, ok := wikiObjectTypes[selected]; !ok {
		selected = rawType
	}
	knowledgeType, ok := wikiObjectTypes[selected]

	parts := []string{
		"complete repair checklist: type and primary_type must be equal and one of entity, concept, methodology, case, insight",
		"page_type must be index",
		"audit_status must be passed",
		"classification_confidence must be a number greater than 0 and at most 1",
		"evidence_ids must contain 1-3 IDs copied from the active source document (do not use evidence_sentence_ids)",
		"source_document_id is required and source_refs must contain that exact ID",
		"structure_fields must use only the selected type's canonical keys",
	}
	if !ok {
		return strings.Join(parts, "; ")
	}

	entitySubType := strings.ToLower(strings.TrimSpace(stringValue(frontmatter["entity_sub_type"])))
	if knowledgeType == TypeEntity {
		parts = append(parts, "entity_sub_type must be one of person, organization, product, technology, industry, place")
		if !IsEntitySubType(entitySubType) {
			return strings.Join(parts, "; ")
		}
	} else {
		parts = append(parts, "entity_sub_type must be omitted")
		entitySubType = ""
	}
	allowed := frameworkKeys(knowledgeType, entitySubType)
	parts = append(parts,
		"information_nature must be "+knowledgeInformationNature(knowledgeType, entitySubType),
		"allowed structure_fields: "+strings.Join(allowed, ", "),
		"required structure_fields: "+knowledgeStructureRequiredCombination(knowledgeType, entitySubType),
	)
	return strings.Join(parts, "; ")
}

type IdentityCandidate struct {
	KnowledgeObjectID    string
	KnowledgeType        KnowledgeType
	EntitySubType        string
	Title                string
	Aliases              []string
	CoreContent          string
	SourceVideoID        string
	TranscriptGeneration string
	StructureFields      map[string]string
	EvidenceIDs          []string
}

type IdentityDecision string

const (
	IdentityReuse    IdentityDecision = "reuse"
	IdentitySeparate IdentityDecision = "separate"
	IdentityConflict IdentityDecision = "conflict"
)

type IdentityComparison struct {
	Decision           IdentityDecision
	Score              float64
	TitleMatch         bool
	TypeMatch          bool
	ContextMatch       bool
	EvidenceOverlap    bool
	NameSimilarity     float64
	SemanticSimilarity float64
	Reason             string
}

// IdentityRecall finds plausible identity pairs. Surface names only recall a
// candidate; CompareIdentity still requires compatible type and meaning before
// it can reuse an identity.
func IdentityRecall(left, right IdentityCandidate) bool {
	if strings.TrimSpace(left.KnowledgeObjectID) != "" && left.KnowledgeObjectID == right.KnowledgeObjectID {
		return true
	}
	nameSimilarity, containment := identityNameSimilarity(left, right)
	semanticSimilarity := identitySemanticSimilarity(left, right)
	if nameSimilarity == 1 {
		return true
	}
	if containment && semanticSimilarity >= 0.18 {
		return true
	}
	return nameSimilarity >= 0.45 && semanticSimilarity >= 0.28 || semanticSimilarity >= 0.58
}

// CompareIdentity is deliberately conservative. Names recall candidates, but
// only compatible types plus sufficiently similar definitions and structure
// can reuse a semantic identity.
func CompareIdentity(left, right IdentityCandidate) IdentityComparison {
	nameSimilarity, containment := identityNameSimilarity(left, right)
	semanticSimilarity := identitySemanticSimilarity(left, right)
	result := IdentityComparison{
		TypeMatch:          left.KnowledgeType == right.KnowledgeType && left.EntitySubType == right.EntitySubType,
		TitleMatch:         identityNameMatch(left, right),
		ContextMatch:       semanticSimilarity >= 0.28,
		EvidenceOverlap:    overlapRatio(left.EvidenceIDs, right.EvidenceIDs) > 0,
		NameSimilarity:     nameSimilarity,
		SemanticSimilarity: semanticSimilarity,
	}
	if !IdentityRecall(left, right) {
		result.Decision = IdentitySeparate
		result.Reason = "surface and semantic signals do not recall the same identity"
		return result
	}
	if left.KnowledgeType != right.KnowledgeType {
		result.Decision = IdentityConflict
		result.Reason = "same candidate name has different top-level types"
		return result
	}
	if left.KnowledgeType == TypeEntity && left.EntitySubType != right.EntitySubType {
		result.Decision = IdentityConflict
		result.Reason = "same candidate name has different entity subtypes"
		return result
	}
	score := nameSimilarity*0.45 + semanticSimilarity*0.45
	if result.EvidenceOverlap {
		score += 0.05
	}
	if left.SourceVideoID != "" && left.SourceVideoID == right.SourceVideoID {
		score += 0.05
	}
	if score > 1 {
		score = 1
	}
	result.Score = score
	sameCanonicalName := nameSimilarity == 1
	if sameCanonicalName && semanticSimilarity >= 0.12 {
		result.Decision = IdentityReuse
		result.Reason = "same canonical name and compatible semantic content"
	} else if containment && semanticSimilarity >= 0.22 {
		result.Decision = IdentityReuse
		result.Reason = "one concept title specializes the other while their semantic content agrees"
	} else if nameSimilarity >= 0.55 && semanticSimilarity >= 0.35 {
		result.Decision = IdentityReuse
		result.Reason = "related surface names describe the same structured meaning"
	} else if semanticSimilarity >= 0.62 && nameSimilarity >= 0.30 {
		result.Decision = IdentityReuse
		result.Reason = "different surface names have strongly equivalent semantic content"
	} else {
		result.Decision = IdentitySeparate
		result.Reason = "candidate names are related but semantic content is insufficiently equivalent"
	}
	return result
}

// GroupSemanticIdentities returns deterministic anchor groups. Every member
// must independently match the same canonical anchor; pairwise similarity is
// not transitive and must never merge candidates through an intermediate page.
func GroupSemanticIdentities(candidates []IdentityCandidate) [][]int {
	return groupSemanticIdentities(candidates, semanticIdentityEquivalent)
}

func groupSemanticIdentities(candidates []IdentityCandidate, equivalent func(IdentityCandidate, IdentityCandidate) bool) [][]int {
	remaining := make(map[int]struct{}, len(candidates))
	for index := range candidates {
		remaining[index] = struct{}{}
	}
	groups := make([][]int, 0, len(candidates))
	for len(remaining) > 0 {
		anchor := -1
		for index := range remaining {
			if anchor == -1 || CanonicalIdentityLess(candidates[index], candidates[anchor]) ||
				(!CanonicalIdentityLess(candidates[anchor], candidates[index]) && index < anchor) {
				anchor = index
			}
		}
		group := []int{anchor}
		delete(remaining, anchor)
		for index := range remaining {
			if equivalent(candidates[anchor], candidates[index]) {
				group = append(group, index)
				delete(remaining, index)
			}
		}
		sort.Ints(group)
		groups = append(groups, group)
	}
	return groups
}

// CanonicalIdentityLess defines a stable winner without relying on write time.
// Clean, concise surface names win over type-decorated or compound labels; the
// object ID is the final deterministic tie-breaker.
func CanonicalIdentityLess(left, right IdentityCandidate) bool {
	leftTitle := strings.TrimSpace(left.Title)
	rightTitle := strings.TrimSpace(right.Title)
	leftClean := stripIdentityTypeDecoration(leftTitle) == leftTitle
	rightClean := stripIdentityTypeDecoration(rightTitle) == rightTitle
	if leftClean != rightClean {
		return leftClean
	}
	leftLength, rightLength := len([]rune(leftTitle)), len([]rune(rightTitle))
	if leftLength != rightLength {
		return leftLength < rightLength
	}
	if leftNormalized, rightNormalized := NormalizeIdentity(leftTitle), NormalizeIdentity(rightTitle); leftNormalized != rightNormalized {
		return leftNormalized < rightNormalized
	}
	return strings.TrimSpace(left.KnowledgeObjectID) < strings.TrimSpace(right.KnowledgeObjectID)
}

func semanticIdentityEquivalent(left, right IdentityCandidate) bool {
	if left.KnowledgeType != right.KnowledgeType ||
		(left.KnowledgeType == TypeEntity && left.EntitySubType != right.EntitySubType) {
		return false
	}
	if leftID, rightID := strings.TrimSpace(left.KnowledgeObjectID), strings.TrimSpace(right.KnowledgeObjectID); leftID != "" && leftID == rightID {
		return true
	}
	return CompareIdentity(left, right).Decision == IdentityReuse
}

func NormalizeIdentity(value string) string {
	value = stripIdentityTypeDecoration(value)
	var builder strings.Builder
	for _, char := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			builder.WriteRune(char)
		}
	}
	return builder.String()
}

var identityTypeDecorationPattern = regexp.MustCompile(`(?i)[\s_-]*(?:[（(]\s*(?:实体|概念|案例|方法论|方法|洞察|entity|concept|case|methodology|method|insight)\s*[）)]|【\s*(?:实体|概念|案例|方法论|方法|洞察|entity|concept|case|methodology|method|insight)\s*】|\[\s*(?:实体|概念|案例|方法论|方法|洞察|entity|concept|case|methodology|method|insight)\s*\])\s*$`)
var leadingIdentityTypeDecorationPattern = regexp.MustCompile(`(?i)^\s*(?:[（(]\s*(?:实体|概念|案例|方法论|方法|洞察|entity|concept|case|methodology|method|insight)\s*[）)]|【\s*(?:实体|概念|案例|方法论|方法|洞察|entity|concept|case|methodology|method|insight)\s*】|\[\s*(?:实体|概念|案例|方法论|方法|洞察|entity|concept|case|methodology|method|insight)\s*\])[\s_-]*`)

func stripIdentityTypeDecoration(value string) string {
	previous := ""
	for value != previous {
		previous = value
		value = identityTypeDecorationPattern.ReplaceAllString(strings.TrimSpace(value), "")
		value = leadingIdentityTypeDecorationPattern.ReplaceAllString(strings.TrimSpace(value), "")
	}
	return strings.TrimSpace(value)
}

func identityNameMatch(left, right IdentityCandidate) bool {
	names := append([]string{left.Title}, left.Aliases...)
	rightNames := append([]string{right.Title}, right.Aliases...)
	for _, leftName := range names {
		for _, rightName := range rightNames {
			if normalized := NormalizeIdentity(leftName); normalized != "" && normalized == NormalizeIdentity(rightName) {
				return true
			}
		}
	}
	return false
}

func identityNameSimilarity(left, right IdentityCandidate) (float64, bool) {
	leftNames := append([]string{left.Title}, left.Aliases...)
	rightNames := append([]string{right.Title}, right.Aliases...)
	best := 0.0
	contained := false
	for _, leftName := range leftNames {
		leftNormalized := NormalizeIdentity(leftName)
		if leftNormalized == "" {
			continue
		}
		for _, rightName := range rightNames {
			rightNormalized := NormalizeIdentity(rightName)
			if rightNormalized == "" {
				continue
			}
			if leftNormalized == rightNormalized {
				return 1, true
			}
			shorter, longer := leftNormalized, rightNormalized
			if len([]rune(shorter)) > len([]rune(longer)) {
				shorter, longer = longer, shorter
			}
			if len([]rune(shorter)) >= 4 && strings.Contains(longer, shorter) {
				contained = true
				ratio := float64(len([]rune(shorter))) / float64(len([]rune(longer)))
				if ratio > best {
					best = ratio
				}
			}
			if similarity := runeGramSimilarity(leftNormalized, rightNormalized); similarity > best {
				best = similarity
			}
		}
	}
	return best, contained
}

func identitySemanticSimilarity(left, right IdentityCandidate) float64 {
	return runeGramSimilarity(identitySemanticText(left), identitySemanticText(right))
}

func identitySemanticText(candidate IdentityCandidate) string {
	parts := make([]string, 0, len(candidate.StructureFields)+1)
	if value := strings.TrimSpace(candidate.CoreContent); value != "" {
		parts = append(parts, value)
	}
	keys := make([]string, 0, len(candidate.StructureFields))
	for key := range candidate.StructureFields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if value := strings.TrimSpace(candidate.StructureFields[key]); value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, " ")
}

func runeGramSimilarity(left, right string) float64 {
	leftGrams := identityRuneGrams(left)
	rightGrams := identityRuneGrams(right)
	if len(leftGrams) == 0 || len(rightGrams) == 0 {
		return 0
	}
	intersection := 0
	for gram := range leftGrams {
		if _, ok := rightGrams[gram]; ok {
			intersection++
		}
	}
	return float64(2*intersection) / float64(len(leftGrams)+len(rightGrams))
}

func identityRuneGrams(value string) map[string]struct{} {
	var builder strings.Builder
	for _, char := range strings.ToLower(value) {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			builder.WriteRune(char)
		}
	}
	runes := []rune(builder.String())
	if len(runes) == 0 {
		return nil
	}
	grams := make(map[string]struct{}, len(runes))
	if len(runes) == 1 {
		grams[string(runes)] = struct{}{}
		return grams
	}
	for index := 0; index < len(runes)-1; index++ {
		grams[string(runes[index:index+2])] = struct{}{}
	}
	return grams
}

func fieldSimilarity(left, right map[string]string) float64 {
	leftTokens := fieldTokens(left)
	rightTokens := fieldTokens(right)
	if len(leftTokens) == 0 || len(rightTokens) == 0 {
		return 0
	}
	intersection := 0
	for token := range leftTokens {
		if _, ok := rightTokens[token]; ok {
			intersection++
		}
	}
	union := len(leftTokens) + len(rightTokens) - intersection
	if union == 0 {
		return 0
	}
	return float64(intersection) / float64(union)
}

func fieldTokens(fields map[string]string) map[string]struct{} {
	tokens := make(map[string]struct{})
	for _, value := range fields {
		for _, token := range strings.Fields(strings.ToLower(value)) {
			token = NormalizeIdentity(token)
			if len([]rune(token)) >= 2 {
				tokens[token] = struct{}{}
			}
		}
	}
	return tokens
}

func overlapRatio(left, right []string) float64 {
	if len(left) == 0 || len(right) == 0 {
		return 0
	}
	lookup := make(map[string]struct{}, len(left))
	for _, item := range left {
		lookup[strings.TrimSpace(item)] = struct{}{}
	}
	overlap := 0
	for _, item := range right {
		if _, ok := lookup[strings.TrimSpace(item)]; ok {
			overlap++
		}
	}
	return float64(overlap) / float64(len(left)+len(right)-overlap)
}

func parseWikiFrontmatter(content string) (map[string]any, string) {
	lines := strings.Split(strings.TrimSpace(content), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return map[string]any{}, content
	}
	end := -1
	for index := 1; index < len(lines); index++ {
		if strings.TrimSpace(lines[index]) == "---" {
			end = index
			break
		}
	}
	if end < 0 {
		return map[string]any{}, content
	}
	values := map[string]any{}
	if err := yaml.Unmarshal([]byte(strings.Join(lines[1:end], "\n")), &values); err != nil {
		return map[string]any{}, strings.Join(lines[end+1:], "\n")
	}
	return values, strings.Join(lines[end+1:], "\n")
}

func structureFields(raw any, body string) map[string]string {
	result := make(map[string]string)
	if values, ok := raw.(map[string]any); ok {
		for key, value := range values {
			if text := strings.TrimSpace(stringValue(value)); text != "" {
				result[key] = text
			}
		}
	}
	known := map[string]string{}
	for _, line := range strings.Split(body, "\n") {
		match := wikiLabelPattern.FindStringSubmatch(line)
		if len(match) != 3 {
			continue
		}
		key := strings.TrimSpace(match[1])
		value := strings.TrimSpace(match[2])
		if value == "" {
			continue
		}
		known[key] = value
	}
	for label, key := range structureFieldAliases {
		if value := known[label]; value != "" && result[key] == "" {
			result[key] = value
		}
	}
	for key, value := range known {
		if result[key] == "" {
			result[key] = value
		}
	}
	return result
}

var structureFieldAliases = map[string]string{
	"职业身份": "identity", "身份": "identity", "教育背景": "background", "教育背景与经历": "background",
	"擅长领域": "expertise", "关注方向": "expertise", "代表性观点": "standpoint", "判断倾向": "standpoint",
	"机构类型": "org_type", "所在行业": "industry", "行业": "industry", "发展阶段": "stage", "规模": "stage",
	"核心业务": "core_business", "代表性项目": "core_business", "关键人物": "key_people",
	"产品类别": "product_type", "产品类型": "product_type", "目标用户": "target_users", "核心功能": "core_function",
	"技术基础": "tech_basis", "差异化特点": "differentiation", "竞争定位": "differentiation",
	"技术分类": "tech_category", "应用领域": "application_area", "成熟度": "maturity",
	"行业范围": "scope", "范围": "scope", "关键趋势": "key_trends", "地点类型": "place_type",
	"关联活动": "associated_activity", "关联活动或事件": "associated_activity",
	"输入": "input", "前提": "input", "步骤": "steps", "操作步骤": "steps", "行动序列": "steps",
	"判断标准": "criteria", "标准": "criteria", "取舍依据": "criteria", "输出": "output", "产出": "output",
	"适用条件": "applicability", "限制": "applicability", "背景": "context", "具体情境": "context", "情境": "context",
	"参与对象": "actors", "参与者": "actors", "选择": "choices", "选项": "choices", "面临选项": "choices",
	"行动": "actions", "实际执行": "actions", "关键动作": "actions", "结果": "outcome", "后续影响": "outcome",
	"复盘判断": "retrospective", "事后复盘": "retrospective", "定义": "definition", "核心界定": "definition",
	"构成要素": "components", "内部结构": "components", "运行机制": "mechanism", "原理": "mechanism",
	"相邻区别": "distinction", "关键区别": "distinction", "核心判断": "claim", "主张": "claim",
	"推导依据": "reasoning", "推导过程": "reasoning", "依据": "reasoning", "限定条件": "qualifications",
	"适用范围": "qualifications", "影响建议": "implications", "推论": "implications", "影响": "implications",
	"行动建议": "implications",
}

func firstWikiHeading(body string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(strings.TrimPrefix(line, "# "))
		}
	}
	return ""
}

func wikiBodyLabeledValue(body string, labels ...string) string {
	allowed := make(map[string]struct{}, len(labels))
	for _, label := range labels {
		allowed[strings.TrimSpace(label)] = struct{}{}
	}
	for _, line := range strings.Split(body, "\n") {
		match := wikiLabelPattern.FindStringSubmatch(line)
		if len(match) != 3 {
			continue
		}
		if _, ok := allowed[strings.TrimSpace(match[1])]; !ok {
			continue
		}
		if value := strings.TrimSpace(match[2]); value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func stringSliceValue(value any) []string {
	switch values := value.(type) {
	case []any:
		result := make([]string, 0, len(values))
		for _, item := range values {
			if text := strings.TrimSpace(stringValue(item)); text != "" {
				result = append(result, text)
			}
		}
		return result
	case []string:
		result := append([]string(nil), values...)
		sort.Strings(result)
		return result
	case string:
		return []string{strings.TrimSpace(values)}
	default:
		return nil
	}
}

func floatValue(value any) float64 {
	switch number := value.(type) {
	case float64:
		return number
	case float32:
		return float64(number)
	case int:
		return float64(number)
	case int64:
		return float64(number)
	case uint64:
		return float64(number)
	case string:
		var parsed float64
		_, _ = fmt.Sscanf(strings.TrimSpace(number), "%f", &parsed)
		return parsed
	default:
		return 0
	}
}

func sourceDocumentRefs(sourceDocumentID string) []string {
	sourceDocumentID = strings.TrimSpace(sourceDocumentID)
	if sourceDocumentID == "" {
		return nil
	}
	return []string{sourceDocumentID}
}

// MergeAnchors 双源合并：按 ID 去重，5 类型映射，实体 6 类聚合为 entity
//
//   - nativePages: WeKnora 原生 Wiki 页面或已映射的知识页面
//   - skillPages: skill 产出的 Wiki 页（page_type=index，业务类型来自 frontmatter.type）
//
// 返回：按类型分组的 AnchorItem 列表
func MergeAnchors(nativePages []AnchorItem, skillPages []AnchorItem) map[KnowledgeType][]AnchorItem {
	out := map[KnowledgeType][]AnchorItem{
		TypeEntity:      {},
		TypeConcept:     {},
		TypeCase:        {},
		TypeMethodology: {},
		TypeInsight:     {},
	}

	seen := make(map[string]bool)
	add := func(items []AnchorItem) {
		for _, it := range items {
			if !IsKnowledgeType(it.Type) {
				continue
			}
			if seen[it.ID] {
				continue
			}
			seen[it.ID] = true
			out[it.Type] = append(out[it.Type], it)
		}
	}
	add(nativePages)
	add(skillPages)
	return out
}

func IsKnowledgeType(t KnowledgeType) bool {
	switch t {
	case TypeEntity, TypeConcept, TypeCase, TypeMethodology, TypeInsight:
		return true
	default:
		return false
	}
}
