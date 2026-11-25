package clickhouse

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// 字段名常量。ClickHouse 是 SQL 库，字段名要在 SELECT/WHERE/INSERT 多处复用，
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
// 默认追加 is_enabled = 1，与 Qdrant/Milvus/Weaviate/Doris 保持一致：
// 关闭的 chunk 不参与检索。
func buildBaseFilter(params types.RetrieveParams) *whereBuilder {
	w := &whereBuilder{}
	w.addEqual(fieldIsEnabled, 1)

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

// parseEmbeddingLiteral 解析 Array(Float32) 以字面量形式返回的字符串
// （形如 "[1,2,3]"）为 []float32。
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

// parseEmbeddingValue 把 CopyIndices 扫描到的 embedding 列转成 []float32。
// clickhouse-go 的 database/sql 驱动对 Array(Float32) 通常直接返回
// []float32；走文本协议或 mock 时可能以 "[1,2,3]" 字符串形式出现，
// 两种形态都接受。
func parseEmbeddingValue(v any) ([]float32, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case []float32:
		return t, nil
	case []float64:
		out := make([]float32, len(t))
		for i, f := range t {
			out[i] = float32(f)
		}
		return out, nil
	case []byte:
		return parseEmbeddingLiteral(t)
	case string:
		return parseEmbeddingLiteral([]byte(t))
	default:
		return nil, errUnexpectedEmbeddingType{typ: typeNameOf(v)}
	}
}

func typeNameOf(v any) string {
	if v == nil {
		return "<nil>"
	}
	return fmt.Sprintf("%T", v)
}

// validateEmbedding 校验向量元素均为有限值。
//
// strconv.FormatFloat 对 NaN/±Inf 会输出 "NaN"/"+Inf"/"-Inf"，
// 这些字面量会让拼出来的 SQL 报语法错误（或在某些版本下产生未定义结果）。
// 上游（嵌入模型）正常情况下不会输出非有限值，但 GPU OOM、上游 bug、
// 测试桩都可能触发；这里 fail-fast 比悄悄写脏数据安全。
func validateEmbedding(vec []float32) error {
	for i, v := range vec {
		if f := float64(v); math.IsNaN(f) || math.IsInf(f, 0) {
			return errInvalidEmbedding{index: i, value: v}
		}
	}
	return nil
}

// errInvalidEmbedding 描述哪个下标含非有限值；用结构体而非 fmt.Errorf
// 是为了让上层在日志里能拿到下标做问题定位。
type errInvalidEmbedding struct {
	index int
	value float32
}

func (e errInvalidEmbedding) Error() string {
	return "clickhouse: embedding[" + strconv.Itoa(e.index) +
		"] is not finite: " + strconv.FormatFloat(float64(e.value), 'g', -1, 32)
}

// errUnexpectedEmbeddingType 描述 CopyIndices 扫描到的 embedding 列类型
// 既不是 []float32 也不是字面量字符串。
type errUnexpectedEmbeddingType struct {
	typ string
}

func (e errUnexpectedEmbeddingType) Error() string {
	return "clickhouse: unexpected embedding column type: " + e.typ
}

// embeddingLiteral 把 []float32 转为 Array(Float32) 字面量字符串：
// "[1.23,4.56,...]"。
//
// 为何不用占位符：clickhouse-go 的 database/sql 驱动对 Array(Float32)
// 的参数绑定行为随版本而异，字面量在各版本下都可走通；Doris 侧也是
// 同样原因（go-sql-driver/mysql 不支持 ARRAY 占位符）改用字面量。
// 这里用 strconv.FormatFloat（'g' + bitSize=32）而不是 fmt.Sprintf("%f", v)，
// 原因有二：
//  1. fmt 在某些 locale 下会用千分位分隔符，破坏 SQL 语法；
//  2. 'g' 比 'f' 短且不会丢精度。
//
// 注入风险：[]float32 元素是嵌入模型输出的有限位数浮点数，序列化后只可能
// 包含 [0-9eE+-.\s] 字符，不会逃逸出字面量上下文；调用前须先过
// validateEmbedding 拒绝 NaN/±Inf。
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
