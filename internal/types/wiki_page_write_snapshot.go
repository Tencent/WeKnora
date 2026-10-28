package types

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"time"
)

type wikiPageWriteSnapshotKey struct{}

// WithWikiPageWriteSnapshot 记录生成前的页面快照，供保存时检查是否已被其他写入改变。
// 快照不共享切片；传入 nil 可清除限制，避免后续反链维护误用源页面的快照。
func WithWikiPageWriteSnapshot(ctx context.Context, page *WikiPage) context.Context {
	return context.WithValue(ctx, wikiPageWriteSnapshotKey{}, cloneWikiPageWriteSnapshot(page))
}

// WikiPageWriteSnapshotFromContext 返回独立副本，调用方修改它不会改变保存前提。
func WikiPageWriteSnapshotFromContext(ctx context.Context) (*WikiPage, bool) {
	page, _ := ctx.Value(wikiPageWriteSnapshotKey{}).(*WikiPage)
	return cloneWikiPageWriteSnapshot(page), page != nil
}

// WikiPageWriteSnapshotMatches 同时检查正文和贡献元数据，不能只比较内容版本。
// 入链由其他页面维护，带快照的保存不会写入它；时间戳也不作为冲突依据。
func WikiPageWriteSnapshotMatches(expected, current *WikiPage) bool {
	if expected == nil || current == nil {
		return expected == current
	}
	if !wikiPageSnapshotJSONEqual(expected.PageMetadata, current.PageMetadata) {
		return false
	}
	left, right := cloneWikiPageWriteSnapshot(expected), cloneWikiPageWriteSnapshot(current)
	for _, page := range []*WikiPage{left, right} {
		page.InLinks = nil
		page.PageMetadata = nil
		page.CreatedAt = time.Time{}
		page.UpdatedAt = time.Time{}
	}
	return reflect.DeepEqual(left, right)
}

// cloneWikiPageWriteSnapshot 复制页面的全部可变字段，同时统一空切片的表示。
func cloneWikiPageWriteSnapshot(page *WikiPage) *WikiPage {
	if page == nil {
		return nil
	}
	out := *page
	out.Aliases = append(StringArray(nil), page.Aliases...)
	out.CategoryPath = append(StringArray(nil), page.CategoryPath...)
	out.SourceRefs = append(StringArray(nil), page.SourceRefs...)
	out.ChunkRefs = append(StringArray(nil), page.ChunkRefs...)
	out.InLinks = append(StringArray(nil), page.InLinks...)
	out.OutLinks = append(StringArray(nil), page.OutLinks...)
	out.PageMetadata = append(JSON(nil), page.PageMetadata...)
	return &out
}

// wikiPageSnapshotJSONEqual 忽略数据库 JSON 格式化差异，但保留数字的精确文本。
func wikiPageSnapshotJSONEqual(left, right JSON) bool {
	if bytes.Equal(left, right) {
		return true
	}
	if len(left) == 0 {
		left = JSON("null")
	}
	if len(right) == 0 {
		right = JSON("null")
	}
	if !json.Valid(left) || !json.Valid(right) {
		return false
	}
	decode := func(raw JSON) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		err := decoder.Decode(&value)
		return value, err
	}
	leftValue, leftErr := decode(left)
	rightValue, rightErr := decode(right)
	return leftErr == nil && rightErr == nil && reflect.DeepEqual(leftValue, rightValue)
}
