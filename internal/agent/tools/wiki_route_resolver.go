package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var (
	errWikiPageNotFoundInScope = errors.New("wiki page not found in current scope")
	errWikiPageAmbiguous       = errors.New("wiki page exists in multiple knowledge bases")
)

// wikiHintUsageError is shared by mutation tools when the model-supplied
// knowledge_base_id is not an in-scope KB ID. It never quotes the rejected
// value: the model may have invented any shorthand, and echoing one wrong
// guess does not help the next one. It points at the handle sources the
// model already sees instead.
const wikiHintUsageError = "knowledge_base_id must be the bN knowledge-base handle of an in-scope knowledge base; " +
	"take it from your runtime context, wiki_read_page metadata (<knowledge_base_id>), wiki_search results, " +
	"or inbox events (user.switched_kb carries kb_id)"

// WikiRouteResolver stores request-local provenance for wiki slugs. It is
// server-side routing state, not a model-handle registry: only KBs already in
// the current WikiScope can ever be returned.
type WikiRouteResolver struct {
	mu     sync.RWMutex
	bySlug map[string]map[string]struct{}
}

func NewWikiRouteResolver() *WikiRouteResolver {
	return &WikiRouteResolver{bySlug: make(map[string]map[string]struct{})}
}

func (r *WikiRouteResolver) remember(slug, kbID string) {
	if r == nil || slug == "" || kbID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	owners := r.bySlug[slug]
	if owners == nil {
		owners = make(map[string]struct{})
		r.bySlug[slug] = owners
	}
	owners[kbID] = struct{}{}
}

func (r *WikiRouteResolver) forget(slug, kbID string) {
	if r == nil || slug == "" || kbID == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	owners := r.bySlug[slug]
	delete(owners, kbID)
	if len(owners) == 0 {
		delete(r.bySlug, slug)
	}
}

func (r *WikiRouteResolver) rememberPage(page *types.WikiPage, kbID string) {
	if r == nil || page == nil || kbID == "" {
		return
	}
	r.remember(page.Slug, kbID)
	for _, slug := range page.OutLinks {
		r.remember(slug, kbID)
	}
	for _, slug := range page.InLinks {
		r.remember(slug, kbID)
	}
}

// scopesForSlug returns cached owners that are still present in the current
// server-owned scope. An empty result means the caller should search all
// scopes. Scope order and per-KB document/tag filters are preserved.
func (r *WikiRouteResolver) scopesForSlug(slug string, scopes []WikiScope) []WikiScope {
	if r == nil || slug == "" || len(scopes) == 0 {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	owners := r.bySlug[slug]
	if len(owners) == 0 {
		return nil
	}
	matched := make([]WikiScope, 0, len(owners))
	for _, scope := range scopes {
		if _, ok := owners[scope.KnowledgeBaseID]; ok {
			matched = append(matched, scope)
		}
	}
	return matched
}

func scopesOutsideKBs(scopes []WikiScope, excluded []WikiScope) []WikiScope {
	if len(excluded) == 0 {
		return scopes
	}
	excludedKBs := make(map[string]struct{}, len(excluded))
	for _, scope := range excluded {
		excludedKBs[scope.KnowledgeBaseID] = struct{}{}
	}
	remaining := make([]WikiScope, 0, len(scopes))
	for _, scope := range scopes {
		if _, ok := excludedKBs[scope.KnowledgeBaseID]; !ok {
			remaining = append(remaining, scope)
		}
	}
	return remaining
}

// resolveUniqueWikiPage is the shared mutation/issue routing boundary. It
// checks every allowed KB (cached provenance only affects order) and refuses
// ambiguous slugs instead of silently mutating the first KB.
//
// A non-empty kbHint (the model-supplied knowledge_base_id) restricts
// resolution to that single KB. The hint must already be inside the allowed
// scope, and a miss inside the hinted KB is reported as-is instead of
// falling back to the other KBs — a fallback would silently mutate a
// different KB than the one the model named. A hint that is not an allowed
// scope ID is rejected with a generic usage error that lists the in-scope
// owners of the slug (if any); the rejected value itself is never quoted,
// because echoing one invented shorthand does not help the model's next
// guess — the handle sources the model already sees do.
func resolveUniqueWikiPage(
	ctx context.Context,
	service interfaces.WikiPageService,
	slug string,
	kbIDs []string,
	routes *WikiRouteResolver,
	kbHint string,
) (*types.WikiPage, string, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return nil, "", fmt.Errorf("slug is required")
	}
	kbHint = strings.TrimSpace(kbHint)
	hintAllowed := false
	if kbHint != "" {
		for _, id := range dedupNonEmptyStrings(kbIDs) {
			if id == kbHint {
				hintAllowed = true
				break
			}
		}
	}
	if kbHint != "" && hintAllowed {
		page, err := service.GetPageBySlug(ctx, kbHint, slug)
		if err != nil && !errors.Is(err, repository.ErrWikiPageNotFound) {
			return nil, "", fmt.Errorf(
				"failed to resolve wiki page %s in knowledge base %s: %w",
				slug, kbHint, err,
			)
		}
		if err != nil || page == nil {
			return nil, "", fmt.Errorf(
				"%w: %s (in knowledge base %s)",
				errWikiPageNotFoundInScope, slug, kbHint,
			)
		}
		if page.KnowledgeBaseID != "" && page.KnowledgeBaseID != kbHint {
			return nil, "", fmt.Errorf(
				"wiki page %s returned knowledge base %s while resolving allowed scope %s",
				slug, page.KnowledgeBaseID, kbHint,
			)
		}
		routes.rememberPage(page, kbHint)
		return page, kbHint, nil
	}
	hits, err := scanWikiPageOwners(ctx, service, slug, kbIDs, routes)
	if err != nil {
		return nil, "", err
	}
	if kbHint != "" {
		owners := make([]string, 0, len(hits))
		for _, item := range hits {
			owners = append(owners, item.kbID)
		}
		if len(owners) == 0 {
			return nil, "", fmt.Errorf(
				"%s; page %s was not found in the current wiki scope either",
				wikiHintUsageError, slug,
			)
		}
		return nil, "", fmt.Errorf(
			"%s; page %s exists in: %s — pass one of these IDs",
			wikiHintUsageError, slug, strings.Join(owners, ", "),
		)
	}
	switch len(hits) {
	case 0:
		return nil, "", fmt.Errorf("%w: %s", errWikiPageNotFoundInScope, slug)
	case 1:
		return hits[0].page, hits[0].kbID, nil
	default:
		owners := make([]string, 0, len(hits))
		for _, item := range hits {
			owners = append(owners, item.kbID)
		}
		return nil, "", fmt.Errorf("%w: slug %s belongs to %s", errWikiPageAmbiguous, slug, strings.Join(owners, ", "))
	}
}

// wikiPageHit pairs a found page with the scope KB it was served from.
type wikiPageHit struct {
	page *types.WikiPage
	kbID string
}

// scanWikiPageOwners searches every allowed scope for the slug, preferring
// KBs that the request-scoped route cache already associates with it.
func scanWikiPageOwners(
	ctx context.Context,
	service interfaces.WikiPageService,
	slug string,
	kbIDs []string,
	routes *WikiRouteResolver,
) ([]wikiPageHit, error) {
	scopes := NewWikiScopesFromKBIDs(kbIDs)
	preferred := routes.scopesForSlug(slug, scopes)
	ordered := append(append([]WikiScope(nil), preferred...), scopesOutsideKBs(scopes, preferred)...)
	hits := make([]wikiPageHit, 0, len(scopes))
	for _, scope := range ordered {
		page, err := service.GetPageBySlug(ctx, scope.KnowledgeBaseID, slug)
		if err != nil {
			if errors.Is(err, repository.ErrWikiPageNotFound) {
				continue
			}
			return nil, fmt.Errorf(
				"failed to resolve wiki page %s in knowledge base %s: %w",
				slug, scope.KnowledgeBaseID, err,
			)
		}
		if page == nil {
			continue
		}
		if page.KnowledgeBaseID != "" && page.KnowledgeBaseID != scope.KnowledgeBaseID {
			return nil, fmt.Errorf(
				"wiki page %s returned knowledge base %s while resolving allowed scope %s",
				slug, page.KnowledgeBaseID, scope.KnowledgeBaseID,
			)
		}
		hits = append(hits, wikiPageHit{page: page, kbID: scope.KnowledgeBaseID})
		routes.rememberPage(page, scope.KnowledgeBaseID)
	}
	return hits, nil
}

// resolveWikiCreateKB selects a creation target only when server-side context
// is unambiguous: one cached provenance owner or one Wiki KB in scope.
func resolveWikiCreateKB(
	slug string,
	kbIDs []string,
	routes *WikiRouteResolver,
	serverHints ...string,
) (string, error) {
	scopes := NewWikiScopesFromKBIDs(kbIDs)
	preferred := routes.scopesForSlug(strings.TrimSpace(slug), scopes)
	allowed := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		allowed[scope.KnowledgeBaseID] = struct{}{}
	}
	candidates := make([]string, 0, len(preferred)+len(serverHints))
	for _, scope := range preferred {
		candidates = append(candidates, scope.KnowledgeBaseID)
	}
	for _, kbID := range serverHints {
		if _, ok := allowed[kbID]; ok {
			candidates = append(candidates, kbID)
		}
	}
	candidates = dedupNonEmptyStrings(candidates)
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if len(candidates) > 1 {
		return "", fmt.Errorf(
			"cannot choose a knowledge base for new wiki page %s: server provenance conflicts across %s",
			slug, strings.Join(candidates, ", "),
		)
	}
	if len(scopes) == 1 {
		return scopes[0].KnowledgeBaseID, nil
	}
	return "", fmt.Errorf("cannot choose a knowledge base for new wiki page %s from %d allowed scopes", slug, len(scopes))
}

func firstWikiRoute(routes []*WikiRouteResolver) *WikiRouteResolver {
	if len(routes) > 0 && routes[0] != nil {
		return routes[0]
	}
	return NewWikiRouteResolver()
}

func resolveWikiIssue(
	ctx context.Context,
	service interfaces.WikiPageService,
	issueID string,
	kbIDs []string,
) (*types.WikiPageIssue, error) {
	issueID = strings.TrimSpace(issueID)
	if issueID == "" {
		return nil, fmt.Errorf("issue_id is required")
	}
	var match *types.WikiPageIssue
	for _, kbID := range dedupNonEmptyStrings(kbIDs) {
		issues, err := service.ListIssues(ctx, kbID, "", "")
		if err != nil {
			return nil, fmt.Errorf("failed to list issues in knowledge base %s: %w", kbID, err)
		}
		for _, issue := range issues {
			if issue == nil || issue.ID != issueID {
				continue
			}
			if issue.KnowledgeBaseID != "" && issue.KnowledgeBaseID != kbID {
				return nil, fmt.Errorf(
					"issue_id %s returned knowledge base %s while resolving allowed scope %s",
					issueID, issue.KnowledgeBaseID, kbID,
				)
			}
			if match != nil {
				return nil, fmt.Errorf("issue_id %s is ambiguous across current Wiki scopes", issueID)
			}
			match = issue
		}
	}
	if match == nil {
		return nil, fmt.Errorf("issue_id %s is not within the current Wiki scope", issueID)
	}
	return match, nil
}
