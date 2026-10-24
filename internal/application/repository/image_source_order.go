package repository

import (
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// sourceOrdinal 只接收 JSON 数字中的非负安全整数；分层 CASE 防止 PostgreSQL 对字符串、对象或超大数字强转失败。
func (d imageAssetDialect) sourceOrdinal(value, kind string, minimum int) string {
	var number, integer string
	if d.postgres {
		number = "(CASE WHEN " + kind + " = 'number' THEN (" + value + ")::numeric END)"
		integer = "TRUNC(" + number + ")"
	} else {
		number = "(CASE WHEN " + kind + " IN ('integer', 'real') THEN " + value + " END)"
		integer = "CAST(" + number + " AS INTEGER)"
	}
	return fmt.Sprintf("(CASE WHEN %s >= %d AND %s <= %d AND %s = %s THEN CAST(%s AS BIGINT) END)",
		number, minimum, number, int64(types.ImageSourceMaxOrdinal), number, integer, number)
}

// sourceAttrOrdinal 按顶层原子键读取保留属性，键名中的点不代表嵌套 JSON 路径。
func (d imageAssetDialect) sourceAttrOrdinal(name string, minimum int) string {
	if d.postgres {
		return d.sourceOrdinal("i.attrs->>'"+name+"'", "jsonb_typeof(i.attrs->'"+name+"')", minimum)
	}
	path := `'$."` + name + `"'`
	return d.sourceOrdinal("json_extract(i.attrs, "+path+")", "json_type(i.attrs, "+path+")", minimum)
}

// sourcePage 优先逐图页码，再从同一文档内同图的 OCR/caption 副本回退 PDF 定位。
// 去重可能由无定位的旧向量 chunk 胜出；复用投影的 image_key，不能跨范围借用页码。
// 普通文本 chunk 可能跨页并包含多图，不能据其定位猜测每张图的页码。
func (d imageAssetDialect) sourcePage() string {
	var locators, page, pdf string
	if d.postgres {
		locators = "jsonb_array_elements(CASE WHEN jsonb_typeof(c.source_locators::jsonb) = 'array' " +
			"THEN c.source_locators::jsonb ELSE '[]'::jsonb END) AS loc(value)"
		page = d.sourceOrdinal("loc.value->>'page'", "jsonb_typeof(loc.value->'page')", 1)
		pdf = "loc.value->>'type' = 'pdf'"
	} else {
		locators = "json_each(CASE WHEN json_valid(c.source_locators) THEN " +
			"CASE WHEN json_type(c.source_locators) = 'array' THEN " +
			"c.source_locators ELSE '[]' END ELSE '[]' END) AS loc"
		// 数组元素也可能是字符串；先过滤对象再把内容交给 JSON 函数。
		object := "(CASE WHEN loc.type = 'object' THEN loc.value ELSE '{}' END)"
		page = d.sourceOrdinal("json_extract("+object+", '$.page')", "json_type("+object+", '$.page')", 1)
		pdf = "json_extract(" + object + ", '$.type') = 'pdf'"
	}
	legacy := "(SELECT MIN(" + page + ") FROM chunk_images source_image JOIN chunks c " +
		"ON c.id = source_image.chunk_id AND c.tenant_id = source_image.tenant_id " +
		"AND c.knowledge_base_id = source_image.knowledge_base_id AND c.knowledge_id = source_image.knowledge_id " +
		"AND c.chunk_type = source_image.chunk_type, " + locators +
		" WHERE source_image.tenant_id = i.tenant_id AND source_image.knowledge_base_id = i.knowledge_base_id " +
		"AND source_image.knowledge_id = i.knowledge_id AND source_image.image_key = i.image_key " +
		"AND source_image.chunk_type IN ('image_ocr', 'image_caption') AND c.deleted_at IS NULL AND " + pdf + ")"
	return "COALESCE(" + d.sourceAttrOrdinal(types.ImageSourcePageAttr, 1) + ", " + legacy + ")"
}

// sourcePageOrder 在分页前按文档、页码及原文图序排序；未知值在升降序中均置后。
// 同页始终保持阅读顺序，最后用资产主键消除缺少旧元数据时的顺序不确定性。
func (d imageAssetDialect) sourcePageOrder(direction string) string {
	page := d.sourcePage()
	index := d.sourceAttrOrdinal(types.ImageSourceIndexAttr, 0)
	return "i.knowledge_id ASC, (" + page + " IS NULL) ASC, " + page + direction +
		", (" + index + " IS NULL) ASC, " + index + " ASC, i.chunk_id ASC, i.image_index ASC"
}
