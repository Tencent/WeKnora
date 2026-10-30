package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// wikiLinkArraySQL 将旧数据中的 SQL NULL、JSON null 视为空数组；column 只能由本文件传入固定列名。
func (r *wikiPageRepository) wikiLinkArraySQL(column string) string {
	if r.wikiDialect() == "sqlite" {
		return fmt.Sprintf("CASE WHEN %s IS NULL OR json_type(%s) = 'null' THEN '[]' ELSE %s END",
			column, column, column)
	}
	return fmt.Sprintf("COALESCE(NULLIF(%s::jsonb, 'null'::jsonb), '[]'::jsonb)", column)
}

// wikiInLinkTarget 限定反链所属页面，缺失或已删除的目标维持原有的跳过语义。
func (r *wikiPageRepository) wikiInLinkTarget(ctx context.Context, tenantID uint64, kbID, targetSlug string) *gorm.DB {
	return r.db.WithContext(ctx).Table("wiki_pages").
		Where("tenant_id = ? AND knowledge_base_id = ? AND slug = ? AND deleted_at IS NULL", tenantID, kbID, targetSlug)
}

// AddInLink 在数据库当前数组上原子追加反链；已有反链不更新行，也不改更新时间。
func (r *wikiPageRepository) AddInLink(
	ctx context.Context, tenantID uint64, kbID, targetSlug, sourceSlug string,
) error {
	links := r.wikiLinkArraySQL("in_links")
	query := r.wikiInLinkTarget(ctx, tenantID, kbID, targetSlug)
	var value any
	if r.wikiDialect() == "sqlite" {
		query = query.Where("NOT EXISTS (SELECT 1 FROM json_each("+links+") WHERE value = ?)", sourceSlug)
		// StringArray.Scan 的 SQLite 存储约定是 []byte，JSON 函数结果须保持 BLOB 编码。
		value = gorm.Expr("CAST(json_insert("+links+", '$[#]', ?) AS BLOB)", sourceSlug)
	} else {
		query = query.Where("NOT jsonb_exists("+links+", ?)", sourceSlug)
		value = gorm.Expr(links+" || jsonb_build_array(CAST(? AS text))", sourceSlug)
	}
	return query.Updates(map[string]any{"in_links": value, "updated_at": time.Now()}).Error
}

// RemoveInLink 原子移除该来源的全部重复反链，保留其他并发写入的来源和原数组顺序。
func (r *wikiPageRepository) RemoveInLink(
	ctx context.Context, tenantID uint64, kbID, targetSlug, sourceSlug string,
) error {
	links := r.wikiLinkArraySQL("in_links")
	query := r.wikiInLinkTarget(ctx, tenantID, kbID, targetSlug)
	var value any
	if r.wikiDialect() == "sqlite" {
		query = query.Where("EXISTS (SELECT 1 FROM json_each("+links+") WHERE value = ?)", sourceSlug)
		value = gorm.Expr("CAST((SELECT json_group_array(value) FROM (SELECT value FROM json_each("+
			links+") WHERE value <> ? ORDER BY CAST(key AS INTEGER))) AS BLOB)", sourceSlug)
	} else {
		query = query.Where("jsonb_exists("+links+", ?)", sourceSlug)
		value = gorm.Expr(links+" - CAST(? AS text)", sourceSlug)
	}
	return query.Updates(map[string]any{"in_links": value, "updated_at": time.Now()}).Error
}

// UpdateLinks 只保存重建得到的链接；旧链接、正文或版本已变化时返回冲突，不覆盖并发写入。
func (r *wikiPageRepository) UpdateLinks(ctx context.Context, previous, page *types.WikiPage) error {
	if previous == nil || page == nil || previous.ID != page.ID || previous.TenantID != page.TenantID ||
		previous.KnowledgeBaseID != page.KnowledgeBaseID || previous.Slug != page.Slug {
		return fmt.Errorf("%w: wiki link update changed page identity", ErrWikiPageConflict)
	}
	query := r.wikiInLinkTarget(ctx, previous.TenantID, previous.KnowledgeBaseID, previous.Slug).
		Where("id = ? AND content = ? AND version = ?", previous.ID, previous.Content, previous.Version)
	for _, snapshot := range []struct {
		column string
		links  types.StringArray
	}{
		{"in_links", previous.InLinks},
		{"out_links", previous.OutLinks},
	} {
		links := snapshot.links
		if links == nil {
			links = types.StringArray{}
		}
		encoded, err := json.Marshal(links)
		if err != nil {
			return fmt.Errorf("encode wiki link snapshot: %w", err)
		}
		column := r.wikiLinkArraySQL(snapshot.column)
		if r.wikiDialect() == "sqlite" {
			query = query.Where("json("+column+") = json(?)", string(encoded))
		} else {
			query = query.Where(column+" = CAST(? AS jsonb)", string(encoded))
		}
	}
	result := query.Updates(map[string]any{
		"in_links":   page.InLinks,
		"out_links":  page.OutLinks,
		"updated_at": time.Now(),
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrWikiPageConflict
	}
	return nil
}
