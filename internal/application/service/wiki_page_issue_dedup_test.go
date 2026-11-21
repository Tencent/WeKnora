package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// dedupWikiRepo is a minimal WikiPageRepository double backed by an in-memory
// slice. It only implements what CreateIssue's duplicate-reuse path reads:
// CreateIssue, ListIssues and UpdateIssueStatus.
type dedupWikiRepo struct {
	interfaces.WikiPageRepository
	issues  []*types.WikiPageIssue
	updated int
}

func (r *dedupWikiRepo) CreateIssue(_ context.Context, issue *types.WikiPageIssue) error {
	r.issues = append(r.issues, issue)
	return nil
}

func (r *dedupWikiRepo) ListIssues(
	_ context.Context, kbID string, slug string, status string,
) ([]*types.WikiPageIssue, error) {
	var out []*types.WikiPageIssue
	for _, issue := range r.issues {
		matchesKB := issue.KnowledgeBaseID == kbID
		matchesSlug := slug == "" || issue.Slug == slug
		matchesStatus := status == "" || issue.Status == status
		if matchesKB && matchesSlug && matchesStatus {
			out = append(out, issue)
		}
	}
	return out, nil
}

func (r *dedupWikiRepo) UpdateIssueStatus(_ context.Context, kbID string, issueID string, status string) error {
	for _, issue := range r.issues {
		if issue.ID == issueID && issue.KnowledgeBaseID == kbID {
			issue.Status = status
			r.updated++
		}
	}
	return nil
}

func (r *dedupWikiRepo) UpdateIssue(_ context.Context, issue *types.WikiPageIssue) error {
	for _, existing := range r.issues {
		if existing.ID == issue.ID && existing.KnowledgeBaseID == issue.KnowledgeBaseID {
			existing.Description = issue.Description
			existing.SuspectedKnowledgeIDs = issue.SuspectedKnowledgeIDs
			existing.ReportedBy = issue.ReportedBy
			existing.Status = issue.Status
			r.updated++
		}
	}
	return nil
}

func TestCreateIssueReusesPendingDuplicate(t *testing.T) {
	repo := &dedupWikiRepo{issues: []*types.WikiPageIssue{{
		ID: "issue-1", TenantID: 1, KnowledgeBaseID: "kb-1",
		Slug: "concept/a", IssueType: "out_of_date",
		Description: "first report", Status: "pending",
		ReportedBy: "wiki-researcher-agent",
	}}}
	svc := &wikiPageService{repo: repo}

	second, err := svc.CreateIssue(context.Background(), &types.WikiPageIssue{
		TenantID: 1, KnowledgeBaseID: "kb-1",
		Slug: "concept/a", IssueType: "out_of_date",
		Description: "second report from another session", Status: "pending",
		ReportedBy: "wiki-researcher-agent",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if second.ID != "issue-1" {
		t.Fatalf("duplicate report must reuse the pending issue, got id=%s", second.ID)
	}
	if len(repo.issues) != 1 {
		t.Fatalf("no new row may be inserted for a duplicate, rows=%d", len(repo.issues))
	}
	if second.Description != "second report from another session" {
		t.Fatalf("reused issue should carry the newest report, got %q", second.Description)
	}
}

func TestCreateIssueKeepsDistinctIssuesSeparate(t *testing.T) {
	repo := &dedupWikiRepo{issues: []*types.WikiPageIssue{{
		ID: "issue-1", TenantID: 1, KnowledgeBaseID: "kb-1",
		Slug: "concept/a", IssueType: "out_of_date",
		Description: "stale number", Status: "pending",
		ReportedBy: "wiki-researcher-agent",
	}}}
	svc := &wikiPageService{repo: repo}

	// Different issue_type for the same page is a distinct report.
	if _, err := svc.CreateIssue(context.Background(), &types.WikiPageIssue{
		TenantID: 1, KnowledgeBaseID: "kb-1",
		Slug: "concept/a", IssueType: "mixed_entities",
		Description: "two products merged", Status: "pending",
		ReportedBy: "wiki-researcher-agent",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Same type but a different page is a distinct report.
	if _, err := svc.CreateIssue(context.Background(), &types.WikiPageIssue{
		TenantID: 1, KnowledgeBaseID: "kb-1",
		Slug: "concept/b", IssueType: "out_of_date",
		Description: "other page", Status: "pending",
		ReportedBy: "wiki-researcher-agent",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Same type but a resolved report must not swallow a new finding.
	if _, err := svc.CreateIssue(context.Background(), &types.WikiPageIssue{
		TenantID: 1, KnowledgeBaseID: "kb-1",
		Slug: "concept/a", IssueType: "resolved-typed",
		Description: "post-fix finding", Status: "pending",
		ReportedBy: "wiki-researcher-agent",
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(repo.issues) != 4 {
		t.Fatalf("distinct reports must each create a row, rows=%d", len(repo.issues))
	}
}
