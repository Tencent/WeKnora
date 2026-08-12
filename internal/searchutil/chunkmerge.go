package searchutil

import (
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/types"
)

// JoinChunkContent joins two current chunk bodies without relying on parser
// offsets. Exact containment is collapsed, a real suffix/prefix overlap is
// removed, and otherwise both bodies are retained with separator between
// them. The conservative fallback intentionally prefers small duplication
// over silently dropping edited content.
func JoinChunkContent(acc, next, separator string) string {
	if acc == "" {
		return next
	}
	if next == "" {
		return acc
	}
	if ContainsChunkContent(acc, next) {
		return acc
	}
	if ContainsChunkContent(next, acc) {
		return next
	}

	accRunes := []rune(acc)
	nextRunes := []rune(next)
	maxOverlap := minInt(len(accRunes), len(nextRunes))
	// Editable chunks may be much larger than parser-produced chunks. Bound
	// suffix matching so an adversarial 200 KB edit cannot turn retrieval into
	// quadratic work. Parser overlap windows are normally far below this cap;
	// larger unmatched overlap is safely retained as duplication.
	if maxOverlap > defaultSearchSpan {
		maxOverlap = defaultSearchSpan
	}
	for overlap := maxOverlap; overlap >= minOverlapRunes; overlap-- {
		if runeSlicesEqual(accRunes[len(accRunes)-overlap:], nextRunes[:overlap]) {
			return acc + string(nextRunes[overlap:])
		}
	}
	return acc + separator + next
}

// ContainsChunkContent reports whether the complete current body is safely
// represented by another body. Very short substrings are not treated as
// containment because common words and punctuation would create false drops.
func ContainsChunkContent(container, contained string) bool {
	if container == "" || contained == "" {
		return false
	}
	if container == contained {
		return true
	}
	return len([]rune(contained)) >= minOverlapRunes && strings.Contains(container, contained)
}

func runeSlicesEqual(left, right []rune) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

// 这里实现 chunk 内容的「重叠拼接」公共逻辑，供文档重建（reconstructContent）、
// 知识图谱内容合并（graph mergeChunkContents）等路径复用。聊天检索链路允许
// 用户编辑 Chunk，使用上面的 JoinChunkContent，避免依赖原文位置坐标。
//
// 历史上各处都用「按位置」的公式裁剪重叠（offset = len(content) - (EndAt -
// lastEndAt) 之类），它默认 len([]rune(Content)) == EndAt-StartAt。但有两类
// 数据会破坏这个不变式，导致拼接错位、丢字或重复：
//  1. 父子分块器会给被拆开的表格「补写表头」，补进去的表头是零宽度的
//     （start == end），位置坐标无法表达它，content 比 EndAt-StartAt 更长；
//  2. content 里可能保留 HTML 实体（如 &#34; / &gt;），其字符数比原文区间长。
//
// 因此这里改为「按文本」匹配重叠：在下一段开头的窗口里查找已合并文本的后缀首次
// 出现的位置，从该位置之后接上。位置信息（StartAt/EndAt）仅用于估算搜索窗口
// 大小，不再用于裁剪。

const (
	// minOverlapRunes 是参与匹配的最短后缀长度。太短（如表格分隔行 |---|）
	// 容易误匹配，因此忽略。
	minOverlapRunes = 12
	// defaultSearchSpan 是搜索窗口的下限，保证即使位置信息缺失/为 0 也能
	// 检测到一定范围内的真实重叠。
	defaultSearchSpan = 400
)

// AppendWithOverlap 把 next 追加到 acc 之后，并去除二者之间的重叠部分。
//
// positionOverlap 是由 StartAt/EndAt 估算的重叠量（lastEnd - curStart），仅用于
// 界定搜索窗口大小；真正的重叠按文本匹配，能兼容补写表头与 HTML 实体长度偏差。
// 若找不到文本重叠，则原样拼接（不裁剪），宁可保留也不破坏内容。
func AppendWithOverlap(acc, next string, positionOverlap int) string {
	if acc == "" {
		return next
	}
	if next == "" {
		return acc
	}

	accRunes := []rune(acc)
	nextRunes := []rune(next)

	span := positionOverlap
	if span < 0 {
		span = 0
	}

	maxK := minInt(len(accRunes), len(nextRunes))
	if cap := maxInt(span*3, defaultSearchSpan); maxK > cap {
		maxK = cap
	}
	// 重叠内容之前最多允许跳过多少前缀（即补写的表头等合成文本）。
	headSlack := maxInt(span*2, 320)

	for k := maxK; k >= minOverlapRunes; k-- {
		needle := accRunes[len(accRunes)-k:]
		if pos := indexRunes(nextRunes, needle, headSlack); pos >= 0 {
			return acc + string(nextRunes[pos+k:])
		}
	}
	return acc + next
}

// AppendWithExactOverlap 在调用方已确认位置坐标可信时，按坐标给出的精确重叠量
// 拼接 acc 与 next：校验 acc 的末 overlap 个字符与 next 的前 overlap 个字符逐字
// 符相等，相等则精确裁剪，overlap 为 0 时直接拼接。
//
// 与 AppendWithOverlap 的区别在于「不猜」：后者为兼容补写表头、HTML 实体等长度
// 偏差，会在窗口内搜索最长后缀匹配，重复周期性文本（表格、日志）可能被误判成重
// 叠而裁掉真实内容。坐标可信时重叠量是已知的，不需要搜索。
//
// 校验不通过返回 ok=false，由调用方决定是否回退到 AppendWithOverlap。
func AppendWithExactOverlap(acc, next string, overlap int) (string, bool) {
	if acc == "" {
		return next, true
	}
	if next == "" {
		return acc, true
	}
	if overlap < 0 {
		return "", false
	}
	if overlap == 0 {
		return acc + next, true
	}

	accRunes := []rune(acc)
	nextRunes := []rune(next)
	if overlap > len(accRunes) || overlap > len(nextRunes) {
		return "", false
	}
	if !runeSlicesEqual(accRunes[len(accRunes)-overlap:], nextRunes[:overlap]) {
		return "", false
	}
	return acc + string(nextRunes[overlap:]), true
}

// MergeTextChunks 按 StartAt（并列时按 ChunkIndex）排序后，用 AppendWithOverlap
// 把多个 chunk 的内容重建为完整文本。gapSep 用于位置不相邻（有间隙）的两段之间
// 的分隔符（如 "\n"），传空串则直接拼接。
//
// 调用方负责先做类型过滤（例如只保留文本 chunk）；本函数不感知 ChunkType。
func MergeTextChunks(chunks []*types.Chunk, gapSep string) string {
	if len(chunks) == 0 {
		return ""
	}

	sorted := make([]*types.Chunk, len(chunks))
	copy(sorted, chunks)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].StartAt == sorted[j].StartAt {
			return sorted[i].ChunkIndex < sorted[j].ChunkIndex
		}
		return sorted[i].StartAt < sorted[j].StartAt
	})

	merged := ""
	mergedEnd := -1
	for _, c := range sorted {
		if c == nil || c.Content == "" {
			continue
		}
		if merged == "" {
			merged = c.Content
			if c.EndAt > 0 {
				mergedEnd = c.EndAt
			}
			continue
		}

		// 间隙 / 位置信息缺失（EndAt==0）：作为独立段落拼接，不做重叠裁剪。
		if c.StartAt > mergedEnd || c.EndAt == 0 {
			if gapSep != "" {
				merged += gapSep
			}
			merged += c.Content
			if c.EndAt > 0 {
				mergedEnd = c.EndAt
			}
			continue
		}

		// 部分重叠或首尾相接：按文本匹配去重叠后拼接。
		if c.EndAt > mergedEnd {
			merged = AppendWithOverlap(merged, c.Content, mergedEnd-c.StartAt)
			mergedEnd = c.EndAt
		}
		// 否则被上一段完全覆盖，跳过。
	}

	return merged
}

// indexRunes 在 haystack 中查找 needle 首次出现的 rune 下标，且起始位置不超过
// maxStart。找不到返回 -1。
func indexRunes(haystack, needle []rune, maxStart int) int {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return -1
	}
	limit := len(haystack) - len(needle)
	if maxStart < limit {
		limit = maxStart
	}
	for i := 0; i <= limit; i++ {
		match := true
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
