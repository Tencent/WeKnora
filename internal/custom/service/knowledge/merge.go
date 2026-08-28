// Package knowledge 双源合并 + 5 类型映射（CP-T007）。
//
// 设计要点（spec §2.1 / §2.2）：
//   - 原型 KnowledgeType（前端枚举）: entity / concept / case / method / insight
//   - WeKnora page_type 白名单:      entity / concept / index / summary / synthesis / comparison
//   - 真实类型存 frontmatter `type`，映射在聚合 API 内部做，前端不感知 6 类细分
//
// 双源合并：
//   - 第一源（WeKnora 原生 Wiki）：page_type ∈ {entity, concept} → 真实类型 entity / concept
//   - 第二源（skill 产物）：        page_type = index，frontmatter.type ∈ {methodology, case, insight, concept}
package knowledge

// KnowledgeType 前端五类型枚举（与 spec §2.1 对齐）
type KnowledgeType string

const (
	TypeEntity  KnowledgeType = "entity"
	TypeConcept KnowledgeType = "concept"
	TypeCase    KnowledgeType = "case"
	TypeMethod  KnowledgeType = "method"
	TypeInsight KnowledgeType = "insight"
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
		return TypeMethod
	case SkillTypeCase:
		return TypeCase
	case SkillTypeInsight:
		return TypeInsight
	case SkillTypeEntity:
		return TypeEntity
	case SkillTypeConcept, "":
		return TypeConcept
	default:
		// 未知类型降级为 concept（不丢数据）
		return TypeConcept
	}
}

// MapPageTypeToKnowledgeType 把 WeKnora 原生 page_type 映回 5 类型
func MapPageTypeToKnowledgeType(pageType string, frontmatterType string) KnowledgeType {
	switch pageType {
	case "entity":
		return TypeEntity
	case "concept":
		// 概念页可能是 skill 挂靠的 concept（真实类型在 frontmatter）
		return MapSkillToKnowledgeType(frontmatterType)
	default:
		// index / summary / synthesis / comparison 等不由本聚合处理
		return ""
	}
}

// EntitySubTypes 实体 6 类细分（仅内部用，前端聚合时合并为 entity）
var EntitySubTypes = []string{
	"person", "organization", "product", "technology", "industry", "place",
}

// IsEntitySubType 判断是否为实体 6 类细分之一
func IsEntitySubType(t string) bool {
	for _, s := range EntitySubTypes {
		if s == t {
			return true
		}
	}
	return false
}

// AnchorItem 关联知识条目（聚合 API 返回结构）
type AnchorItem struct {
	ID              string        `json:"id"` // Wiki page id 或 knowledge id
	Slug            string        `json:"slug"`
	Title           string        `json:"title"`
	Type            KnowledgeType `json:"type"` // 5 类型之一
	Timestamp       string        `json:"timestamp,omitempty"`
	Seconds         int           `json:"seconds,omitempty"`
	EntitySubType   string        `json:"entity_sub_type,omitempty"` // person / organization / ...
	PageType        string        `json:"page_type"`                 // WeKnora 原生 page_type
	Source          string        `json:"source"`                    // "native" / "skill"
	Confidence      float64       `json:"confidence,omitempty"`
	RelatedVideoIDs []string      `json:"related_video_ids,omitempty"`
}

// MergeAnchors 双源合并：按 ID 去重，5 类型映射，实体 6 类聚合为 entity
//
//   - nativePages: WeKnora 原生 Wiki 页面（page_type ∈ {entity, concept}）
//   - skillPages: skill 产出的 Wiki 页（page_type=index，frontmatter.type ∈ {concept, methodology, insight, case, entity}）
//
// 返回：按类型分组的 AnchorItem 列表
func MergeAnchors(nativePages []AnchorItem, skillPages []AnchorItem) map[KnowledgeType][]AnchorItem {
	out := map[KnowledgeType][]AnchorItem{
		TypeEntity:  {},
		TypeConcept: {},
		TypeCase:    {},
		TypeMethod:  {},
		TypeInsight: {},
	}

	seen := make(map[string]bool)
	add := func(items []AnchorItem) {
		for _, it := range items {
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
