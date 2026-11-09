package types

import "context"

// WikiPagePublication 限定一次发布可消费的页面回执；版本必须来自已提交的回执，不能从待发布页面补取。
type WikiPagePublication struct {
	TenantID        uint64
	KnowledgeBaseID string
	PageID          string
	Slug            string
	PageVersion     int
}

type wikiPagePublicationKey struct{}

// WithWikiPagePublication 按值保存发布边界，避免调用方后续修改目标影响已经创建的上下文。
// 此边界只约束发布操作，不授权重新生成正文或覆盖页面元数据。
func WithWikiPagePublication(ctx context.Context, publication WikiPagePublication) context.Context {
	return context.WithValue(ctx, wikiPagePublicationKey{}, publication)
}

// WikiPagePublicationFromContext 返回明确提供的发布边界；未提供时保留旧发布入口的行为。
func WikiPagePublicationFromContext(ctx context.Context) (WikiPagePublication, bool) {
	publication, ok := ctx.Value(wikiPagePublicationKey{}).(WikiPagePublication)
	return publication, ok
}
