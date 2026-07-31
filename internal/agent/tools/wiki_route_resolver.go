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
func resolveUniqueWikiPage(
	ctx context.Context,
	service interfaces.WikiPageService,
	slug string,
	kbIDs []string,
	routes *WikiRouteResolver,
) (*types.WikiPage, string, error) {
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return nil, "", fmt.Errorf("slug is required")
	}
	scopes := NewWikiScopesFromKBIDs(kbIDs)
	preferred := routes.scopesForSlug(slug, scopes)
	ordered := append(append([]WikiScope(nil), preferred...), scopesOutsideKBs(scopes, preferred)...)
	type hit struct {
		page *types.WikiPage
		kbID string
	}
	var hits []hit
	for _, scope := range ordered {
		page, err := service.GetPageBySlug(ctx, scope.KnowledgeBaseID, slug)
		if err != nil {
			if errors.Is(err, repository.ErrWikiPageNotFound) {
				continue
			}
			return nil, "", fmt.Errorf(
				"failed to resolve wiki page %s in knowledge base %s: %w",
				slug, scope.KnowledgeBaseID, err,
			)
		}
		if page == nil {
			continue
		}
		if page.KnowledgeBaseID != "" && page.KnowledgeBaseID != scope.KnowledgeBaseID {
			return nil, "", fmt.Errorf(
				"wiki page %s returned knowledge base %s while resolving allowed scope %s",
				slug, page.KnowledgeBaseID, scope.KnowledgeBaseID,
			)
		}
		kbID := scope.KnowledgeBaseID
		hits = append(hits, hit{page: page, kbID: kbID})
		routes.rememberPage(page, kbID)
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

func wikiKnowledgeBasesForSourceRefs(
	ctx context.Context,
	refs []string,
	knowledgeService interfaces.KnowledgeService,
	allowedKBIDs []string,
) ([]string, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if knowledgeService == nil {
		return nil, fmt.Errorf("knowledge service is unavailable")
	}
	allowed := make(map[string]struct{}, len(allowedKBIDs))
	for _, kbID := range dedupNonEmptyStrings(allowedKBIDs) {
		allowed[kbID] = struct{}{}
	}
	var kbIDs []string
	for _, ref := range refs {
		knowledgeID := strings.TrimSpace(strings.SplitN(ref, "|", 2)[0])
		if knowledgeID == "" {
			continue
		}
		knowledge, err := knowledgeService.GetKnowledgeByIDOnly(ctx, knowledgeID)
		if err != nil || knowledge == nil {
			if err == nil {
				err = fmt.Errorf("empty result")
			}
			return nil, fmt.Errorf("failed to resolve source document %s: %w", knowledgeID, err)
		}
		if _, ok := allowed[knowledge.KnowledgeBaseID]; !ok {
			return nil, fmt.Errorf(
				"source document %s belongs to non-Wiki or unauthorized knowledge base %s",
				knowledgeID, knowledge.KnowledgeBaseID,
			)
		}
		kbIDs = append(kbIDs, knowledge.KnowledgeBaseID)
	}
	return dedupNonEmptyStrings(kbIDs), nil
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
		issue, err := service.GetIssue(ctx, kbID, issueID)
		if err != nil {
			if errors.Is(err, repository.ErrWikiIssueNotFound) {
				continue
			}
			return nil, fmt.Errorf("failed to read issue in knowledge base %s: %w", kbID, err)
		}
		if match != nil {
			return nil, fmt.Errorf("issue_id %s is ambiguous across current Wiki scopes", issueID)
		}
		match = issue
	}
	if match == nil {
		return nil, fmt.Errorf("issue_id %s is not within the current Wiki scope", issueID)
	}
	return match, nil
}
