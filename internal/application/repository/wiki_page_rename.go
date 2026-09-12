package repository

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/mattn/go-sqlite3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ErrWikiPageSlugConflict indicates that the destination slug is already in use.
var ErrWikiPageSlugConflict = errors.New("wiki page slug already exists")

var _ interfaces.WikiPageRenamer = (*wikiPageRepository)(nil)

var wikiRenameLinkPattern = regexp.MustCompile(`\[\[([^\]]+)\]\]`)

// RenamePage keeps the UUID and all provenance/history columns intact. The
// graph scan deliberately includes archived pages and does not trust cached
// backlinks: a missing backlink must not leave an old link in a page body.
// This costs O(total KB content) memory/work and locks every existing live row
// until commit, blocking concurrent Wiki writes in large KBs. No external work
// runs under these locks. Later writes that introduce old slugs still need the
// normal link-repair path; rename does not reserve the former slug forever.
func (r *wikiPageRepository) RenamePage(
	ctx context.Context, req interfaces.WikiPageRenameRequest,
) (*interfaces.WikiPageRenameResult, error) {
	if err := validateWikiRename(req); err != nil {
		return nil, err
	}
	result := &interfaces.WikiPageRenameResult{}
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var pages []*types.WikiPage
		// Stable lock ordering serializes overlapping renames. PostgreSQL
		// reads the latest committed row after waiting for an existing writer;
		// SQLite serializes writes and rejects a stale read-to-write upgrade.
		if err := tx.Where("knowledge_base_id = ?", req.KnowledgeBaseID).
			Order(clause.OrderByColumn{Column: clause.Column{Name: "id"}}).
			Clauses(clause.Locking{Strength: "UPDATE"}).Find(&pages).Error; err != nil {
			return err
		}
		for _, page := range pages {
			if page.ID == req.PageID && page.Slug == req.OldSlug {
				result.Page = page
			}
		}
		if result.Page == nil {
			return ErrWikiPageNotFound
		}
		for _, page := range pages {
			if page.Slug == req.NewSlug {
				return ErrWikiPageSlugConflict
			}
		}
		incoming := slices.Clone(result.Page.InLinks)
		for _, page := range pages {
			if slices.Contains(page.OutLinks, req.OldSlug) ||
				renameWikiLinkContent(page.Content, req.OldSlug, req.NewSlug) != page.Content {
				if !slices.Contains(incoming, page.Slug) {
					incoming = append(incoming, page.Slug)
				}
			}
		}

		for _, page := range pages {
			updates := make(map[string]interface{})
			oldSlug, oldUpdatedAt := page.Slug, page.UpdatedAt
			if page.ID == req.PageID {
				page.Slug = req.NewSlug
				updates["slug"] = page.Slug
				page.InLinks = incoming
				updates["in_links"] = incoming
			}
			if content := renameWikiLinkContent(page.Content, req.OldSlug, req.NewSlug); content != page.Content {
				page.Content = content
				updates["content"] = content
				// A body can contain a link absent from the cached out_links.
				if !slices.Contains(page.OutLinks, req.OldSlug) && !slices.Contains(page.OutLinks, req.NewSlug) {
					page.OutLinks = append(page.OutLinks, req.NewSlug)
					updates["out_links"] = page.OutLinks
				}
			}
			if summary := renameWikiLinkContent(page.Summary, req.OldSlug, req.NewSlug); summary != page.Summary {
				page.Summary = summary
				updates["summary"] = summary
			}
			if links, changed := renameWikiSlugRefs(page.InLinks, req.OldSlug, req.NewSlug); changed {
				page.InLinks = links
				updates["in_links"] = links
			}
			if links, changed := renameWikiSlugRefs(page.OutLinks, req.OldSlug, req.NewSlug); changed {
				page.OutLinks = links
				updates["out_links"] = links
			}
			if slices.Contains(result.Page.OutLinks, page.Slug) && !slices.Contains(page.InLinks, req.NewSlug) {
				page.InLinks = append(page.InLinks, req.NewSlug)
				updates["in_links"] = page.InLinks
			}
			if page.ParentSlug == req.OldSlug {
				page.ParentSlug = req.NewSlug
				updates["parent_slug"] = page.ParentSlug
			}
			if len(updates) == 0 {
				continue
			}
			page.UpdatedAt = time.Now()
			if !page.UpdatedAt.After(oldUpdatedAt) {
				page.UpdatedAt = oldUpdatedAt.Add(time.Microsecond)
			}
			updates["updated_at"] = page.UpdatedAt
			// Version alone cannot fence machine-only link/meta writes. Include
			// updated_at and the old slug while updating only renamed fields.
			write := tx.Model(&types.WikiPage{}).
				Where("knowledge_base_id = ? AND id = ? AND slug = ? AND version = ? AND updated_at = ?",
					req.KnowledgeBaseID, page.ID, oldSlug, page.Version, oldUpdatedAt).
				Updates(updates)
			if write.Error != nil {
				return write.Error
			}
			if write.RowsAffected != 1 {
				return ErrWikiPageConflict
			}
			result.AffectedPages = append(result.AffectedPages, page)
		}
		// Keep issue status, evidence, reporter and timestamps of creation.
		// Revisions retain their historical slug and remain reachable by UUID.
		return tx.Model(&types.WikiPageIssue{}).
			Where("knowledge_base_id = ? AND slug = ?", req.KnowledgeBaseID, req.OldSlug).
			Update("slug", req.NewSlug).Error
	})
	if err != nil {
		var pgErr interface{ SQLState() string }
		var sqliteErr sqlite3.Error
		if errors.Is(err, gorm.ErrDuplicatedKey) ||
			(errors.As(err, &pgErr) && pgErr.SQLState() == "23505") ||
			(errors.As(err, &sqliteErr) && sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique) {
			return nil, fmt.Errorf("%w: %s", ErrWikiPageSlugConflict, req.NewSlug)
		}
		return nil, err
	}
	return result, nil
}

func validateWikiRename(req interfaces.WikiPageRenameRequest) error {
	if strings.TrimSpace(req.KnowledgeBaseID) == "" || strings.TrimSpace(req.PageID) == "" ||
		strings.TrimSpace(req.OldSlug) == "" {
		return errors.New("knowledge_base_id, page_id and old_slug are required")
	}
	slug := req.NewSlug
	if slug == "" || slug == req.OldSlug || utf8.RuneCountInString(slug) > 255 ||
		strings.HasPrefix(slug, "/") || strings.HasSuffix(slug, "/") || strings.Contains(slug, "//") {
		return errors.New("new_slug must be a different valid wiki slug (at most 255 characters)")
	}
	for _, c := range slug {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '/', c >= 0x4E00 && c <= 0x9FFF:
		default:
			return errors.New("new_slug contains an invalid wiki slug character")
		}
	}
	return nil
}

func normalizeWikiRenameLink(slug string) string {
	return strings.ReplaceAll(strings.ToLower(strings.TrimSpace(slug)), " ", "-")
}

func renameWikiLinkContent(content, oldSlug, newSlug string) string {
	return wikiRenameLinkPattern.ReplaceAllStringFunc(content, func(link string) string {
		inner := link[2 : len(link)-2]
		slug, display, hasDisplay := strings.Cut(inner, "|")
		if normalizeWikiRenameLink(slug) != oldSlug {
			return link
		}
		if hasDisplay {
			return "[[" + newSlug + "|" + display + "]]"
		}
		return "[[" + newSlug + "]]"
	})
}

func renameWikiSlugRefs(refs types.StringArray, oldSlug, newSlug string) (types.StringArray, bool) {
	if !slices.Contains(refs, oldSlug) {
		return refs, false
	}
	out := make(types.StringArray, 0, len(refs))
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		if ref == oldSlug {
			ref = newSlug
		}
		if !seen[ref] {
			out = append(out, ref)
			seen[ref] = true
		}
	}
	return out, true
}
