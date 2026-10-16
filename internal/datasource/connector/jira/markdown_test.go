package jira

import (
	"strings"
	"testing"
)

func TestIssueToMarkdown(t *testing.T) {
	iss := issue{
		ID:  "10001",
		Key: "PROJ-123",
		Fields: issueFields{
			Summary: "Fix gateway timeout on login",
			IssueType: struct {
				Name string `json:"name"`
			}{Name: "Bug"},
			Status: struct {
				Name string `json:"name"`
			}{Name: "Closed"},
			Priority: &struct {
				Name string `json:"name"`
			}{Name: "High"},
			Resolution: &struct {
				Name string `json:"name"`
			}{Name: "Fixed"},
			Assignee: &struct {
				DisplayName string `json:"displayName"`
			}{DisplayName: "Alice Developer"},
			Reporter: &struct {
				DisplayName string `json:"displayName"`
			}{DisplayName: "Bob Reporter"},
			Created: "2024-01-10T08:00:00.000+0000",
			Updated: "2024-01-11T12:00:00.000+0000",
			Components: []struct {
				Name string `json:"name"`
			}{
				{Name: "Gateway"},
				{Name: "Auth"},
			},
			Labels: []string{"incident", "q1-fix"},
			Comment: &commentList{
				Total: 1,
				Comments: []comment{
					{
						ID: "c1",
						Author: &struct {
							DisplayName string `json:"displayName"`
						}{DisplayName: "Alice Developer"},
						Created: "2024-01-11T10:00:00.000+0000",
						Body:    "Plain text fallback",
					},
				},
			},
		},
		RenderedFields: &renderedFields{
			Description: "<p>The <strong>gateway</strong> was failing with 504 errors.</p>",
			Comment: &renderedCommentList{
				Comments: []renderedComment{
					{
						ID:   "c1",
						Body: "<p>Root cause was <em>connection pool</em> exhaustion.</p>",
					},
				},
			},
		},
	}

	baseURL := "https://jira.example.com"
	md, err := issueToMarkdown(iss, baseURL, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(md, "# [PROJ-123] Fix gateway timeout on login") {
		t.Errorf("expected title in markdown, got:\n%s", md)
	}
	if !strings.Contains(md, "| Issue Key | PROJ-123 |") {
		t.Errorf("expected Issue Key in table, got:\n%s", md)
	}
	if !strings.Contains(md, "| Issue Type | Bug |") {
		t.Errorf("expected Bug in table, got:\n%s", md)
	}
	if !strings.Contains(md, "| Components | Gateway, Auth |") {
		t.Errorf("expected Components in table, got:\n%s", md)
	}
	if !strings.Contains(md, "[PROJ-123](https://jira.example.com/browse/PROJ-123)") {
		t.Errorf("expected issue URL in table, got:\n%s", md)
	}
	if !strings.Contains(md, "The **gateway** was failing with 504 errors.") {
		t.Errorf("expected rendered HTML description in markdown, got:\n%s", md)
	}
	if !strings.Contains(md, "Root cause was *connection pool* exhaustion.") {
		t.Errorf("expected rendered HTML comment in markdown, got:\n%s", md)
	}
}

func TestIssueToMarkdownWithoutComments(t *testing.T) {
	iss := issue{
		Key: "PROJ-456",
		Fields: issueFields{
			Summary: "Feature request",
			Comment: &commentList{
				Total: 1,
				Comments: []comment{
					{
						ID:   "c1",
						Body: "Should not appear",
					},
				},
			},
		},
	}

	md, err := issueToMarkdown(iss, "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(md, "Should not appear") {
		t.Errorf("expected comments to be omitted when includeComments=false")
	}
}

func TestIssueToMarkdownWithAttachments(t *testing.T) {
	iss := issue{
		Key: "PROJ-789",
		Fields: issueFields{
			Summary: "Issue with attachment",
			Attachment: []attachment{
				{
					Filename: "error.log",
					Size:     2048,
					Created:  "2024-01-15T10:00:00.000Z",
					Author: &struct {
						DisplayName string `json:"displayName"`
					}{DisplayName: "Alice"},
					TextContent: "2024-01-15 10:00:01 ERROR Connection reset",
				},
				{
					Filename: "diagram.png",
					Size:     10240,
					Created:  "2024-01-15T10:05:00.000Z",
					Author: &struct {
						DisplayName string `json:"displayName"`
					}{DisplayName: "Bob"},
				},
			},
		},
	}

	md, err := issueToMarkdown(iss, "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(md, "## Attachments") {
		t.Errorf("expected ## Attachments section")
	}
	if !strings.Contains(md, "| error.log | 2.0 KB | Alice | 2024-01-15T10:00:00.000Z |") {
		t.Errorf("expected attachment table row in markdown:\n%s", md)
	}
	if !strings.Contains(md, "### 📎 error.log (2.0 KB)") {
		t.Errorf("expected embedded attachment heading in markdown:\n%s", md)
	}
	if !strings.Contains(md, "```log\n2024-01-15 10:00:01 ERROR Connection reset\n```") {
		t.Errorf("expected embedded code block in markdown:\n%s", md)
	}
	if strings.Contains(md, "### 📎 diagram.png") {
		t.Errorf("non-text attachment should not have embedded code block")
	}
}
