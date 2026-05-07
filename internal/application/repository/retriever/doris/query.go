package doris

import (
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// 字段名常量。Doris 是 SQL 库，字段名要在 SELECT/WHERE/INSERT 多处复用，
// 用常量统一防止笔误。
const (
	fieldID              = "id"
	fieldContent         = "content"
	fieldSourceID        = "source_id"
	fieldSourceType      = "source_type"
	fieldChunkID         = "chunk_id"
	fieldKnowledgeID     = "knowledge_id"
	fieldKnowledgeBaseID = "knowledge_base_id"
	fieldTagID           = "tag_id"
	fieldIsEnabled       = "is_enabled"
	fieldEmbedding       = "embedding"
)

// columns 是 INSERT / SELECT 时使用的标准列序。
var columns = []string{
	fieldID, fieldContent, fieldSourceID, fieldSourceType,
	fieldChunkID, fieldKnowledgeID, fieldKnowledgeBaseID, fieldTagID,
	fieldIsEnabled, fieldEmbedding,
}

// columnsForRetrieve 是 Retrieve 时 SELECT 的列序，
// 不包含 embedding（向量本身查询结果中无需返回，省带宽）。
var columnsForRetrieve = []string{
	fieldID, fieldContent, fieldSourceID, fieldSourceType,
	fieldChunkID, fieldKnowledgeID, fieldKnowledgeBaseID, fieldTagID,
	fieldIsEnabled,
}

// columnsForCopy 是 CopyIndices 中分页 SELECT 时使用的列序，
// 比 columnsForRetrieve 多 embedding，因为复制目的是搬运向量本身。
var columnsForCopy = []string{
	fieldID, fieldContent, fieldSourceID, fieldSourceType,
	fieldChunkID, fieldKnowledgeID, fieldKnowledgeBaseID, fieldTagID,
	fieldIsEnabled, fieldEmbedding,
}

// whereCond 表示一个 WHERE 子条件：clause 是参数化 SQL 片段（带 ? 占位），
// args 是对应顺序的参数值。所有用户输入字段（IDs）必须通过 args 传入，
// 严禁拼到 clause 字符串里。
type whereCond struct {
	clause string
	args   []any
}

// whereBuilder 用于把 RetrieveParams 中的过滤条件翻译成 SQL WHERE 子句。
//
// 每个 add* 方法对应一种 IN / NOT IN / = 算子；最终 build() 用 AND 拼接。
type whereBuilder struct {
	conds []whereCond
}

// addEqual 追加一个 field = ? 条件。
func (w *whereBuilder) addEqual(field string, value any) {
	w.conds = append(w.conds, whereCond{
		clause: field + " = ?",
		args:   []any{value},
	})
}

// addIn 追加一个 field IN (?, ?, ...) 条件。values 为空时不追加任何东西。
func (w *whereBuilder) addIn(field string, values []string) {
	if len(values) == 0 {
		return
	}
	placeholders := make([]string, len(values))
	args := make([]any, len(values))
	for i, v := range values {
		placeholders[i] = "?"
		args[i] = v
	}
	w.conds = append(w.conds, whereCond{
		clause: field + " IN (" + strings.Join(placeholders, ", ") + ")",
		args:   args,
	})
}

// addNotIn 追加一个 field NOT IN (?, ?, ...) 条件。
func (w *whereBuilder) addNotIn(field string, values []string) {
	if len(values) == 0 {
		return
	}
	placeholders := make([]string, len(values))
	args := make([]any, len(values))
	for i, v := range values {
		placeholders[i] = "?"
		args[i] = v
	}
	w.conds = append(w.conds, whereCond{
		clause: field + " NOT IN (" + strings.Join(placeholders, ", ") + ")",
		args:   args,
	})
}

// build 返回 WHERE 子句（不含 "WHERE " 前缀）和参数数组。
// 没有任何条件时返回 ("1 = 1", nil)，方便调用方无脑拼接。
func (w *whereBuilder) build() (string, []any) {
	if len(w.conds) == 0 {
		return "1 = 1", nil
	}
	parts := make([]string, len(w.conds))
	var args []any
	for i, c := range w.conds {
		parts[i] = c.clause
		args = append(args, c.args...)
	}
	return strings.Join(parts, " AND "), args
}

// buildBaseFilter 将 RetrieveParams 中的过滤条件翻译为 whereBuilder。
// 默认追加 is_enabled = TRUE，与 Qdrant/Milvus/Weaviate 保持一致：
// 关闭的 chunk 不参与检索。
func buildBaseFilter(params types.RetrieveParams) *whereBuilder {
	w := &whereBuilder{}
	w.addEqual(fieldIsEnabled, true)

	if len(params.KnowledgeBaseIDs) > 0 {
		w.addIn(fieldKnowledgeBaseID, params.KnowledgeBaseIDs)
	}
	if len(params.KnowledgeIDs) > 0 {
		w.addIn(fieldKnowledgeID, params.KnowledgeIDs)
	}
	if len(params.TagIDs) > 0 {
		w.addIn(fieldTagID, params.TagIDs)
	}
	if len(params.ExcludeKnowledgeIDs) > 0 {
		w.addNotIn(fieldKnowledgeID, params.ExcludeKnowledgeIDs)
	}
	if len(params.ExcludeChunkIDs) > 0 {
		w.addNotIn(fieldChunkID, params.ExcludeChunkIDs)
	}
	return w
}

// parseEmbeddingLiteral 解析 Doris ARRAY<FLOAT> 通过 MySQL 协议返回的
// 字面量字符串（形如 "[1,2,3]"）为 []float32。
//
// CopyIndices 路径需要从源行读出向量本身再写回目标行；此处的解析容错优先：
// 不带 [] 也接受、空数组返回 nil。
func parseEmbeddingLiteral(raw []byte) ([]float32, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" {
		return nil, nil
	}
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	out := make([]float32, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		f, err := strconv.ParseFloat(p, 32)
		if err != nil {
			return nil, err
		}
		out = append(out, float32(f))
	}
	return out, nil
}

// embeddingLiteral 把 []float32 转为 Doris ARRAY<FLOAT> 字面量字符串：
// "[1.23,4.56,...]"。
//
// 为何不用占位符：go-sql-driver/mysql 不支持 ARRAY 类型的参数绑定，
// Doris 端也只接受字面量形式。这里用 strconv.FormatFloat（'g' + bitSize=32）
// 而不是 fmt.Sprintf("%f", v)，原因有二：
//  1. fmt 在某些 locale 下会用千分位分隔符，破坏 SQL 语法；
//  2. 'g' 比 'f' 短且不会丢精度。
//
// 注入风险：[]float32 元素是嵌入模型输出的有限位数浮点数，序列化后只可能
// 包含 [0-9eE+-.\s] 字符，不会逃逸出字面量上下文。
func embeddingLiteral(vec []float32) string {
	if len(vec) == 0 {
		return "[]"
	}
	var sb strings.Builder
	sb.Grow(len(vec) * 12)
	sb.WriteByte('[')
	for i, v := range vec {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.FormatFloat(float64(v), 'g', -1, 32))
	}
	sb.WriteByte(']')
	return sb.String()
}
