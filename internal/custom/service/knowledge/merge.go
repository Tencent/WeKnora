// Package knowledge Wiki 页面到 5 类型知识的映射（CP-T007）。
//
// 设计要点（spec §2.1 / §2.2）：
//   - 原型 KnowledgeType（前端枚举）: entity / concept / case / method / insight
//   - 页面类型既可能由 WeKnora 原生 page_type 表示，也可能由 skill frontmatter.type 表示
//   - 真实类型统一映射在聚合 API 内部做，前端不感知实体 6 类细分
//
// 页面来源：
//   - WeKnora 原生 Wiki：page_type 为 entity / concept
//   - extract-video-knowledge skill：page_type 为 case / methodology / insight，或 page_type=index 且 frontmatter.type 为五类知识
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
	case "case":
		return TypeCase
	case "methodology":
		return TypeMethod
	case "insight":
		return TypeInsight
	case "index":
		return MapSkillToKnowledgeType(frontmatterType)
	default:
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
//   - nativePages: WeKnora 原生 Wiki 页面或已映射的知识页面
//   - skillPages: skill 产出的 Wiki 页（兼容 page_type=index 与直接使用五类 page_type 的历史数据）
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
	case TypeEntity, TypeConcept, TypeCase, TypeMethod, TypeInsight:
		return true
	default:
		return false
	}
}
