package jira

import (
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestParseConfig(t *testing.T) {
	t.Run("server edition", func(t *testing.T) {
		ds := &types.DataSourceConfig{
			Settings: map[string]interface{}{
				"edition":  "server",
				"base_url": "https://jira.company.com/",
				"username": "admin",
				"jql":      "status = Done",
			},
			Credentials: map[string]interface{}{
				"password": "secret_password",
			},
		}
		cfg, err := parseConfig(ds)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.edition != "server" {
			t.Errorf("expected server edition, got %s", cfg.edition)
		}
		if cfg.baseURL != "https://jira.company.com" {
			t.Errorf("expected trimmed base URL, got %s", cfg.baseURL)
		}
		if cfg.username != "admin" {
			t.Errorf("expected admin, got %s", cfg.username)
		}
		if cfg.secret != "secret_password" {
			t.Errorf("expected secret_password, got %s", cfg.secret)
		}
		if cfg.jql != "status = Done" {
			t.Errorf("expected jql, got %s", cfg.jql)
		}
		if !cfg.includeComments {
			t.Errorf("expected default includeComments to be true")
		}
	})

	t.Run("cloud edition with api_token", func(t *testing.T) {
		ds := &types.DataSourceConfig{
			Settings: map[string]interface{}{
				"edition":          "cloud",
				"base_url":         "mycompany.atlassian.net",
				"username":         "user@company.com",
				"include_comments": false,
			},
			Credentials: map[string]interface{}{
				"api_token": "cloud_token_123",
			},
		}
		cfg, err := parseConfig(ds)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.edition != "cloud" {
			t.Errorf("expected cloud edition, got %s", cfg.edition)
		}
		if cfg.baseURL != "https://mycompany.atlassian.net" {
			t.Errorf("expected prepended https, got %s", cfg.baseURL)
		}
		if cfg.secret != "cloud_token_123" {
			t.Errorf("expected cloud_token_123, got %s", cfg.secret)
		}
		if cfg.includeComments {
			t.Errorf("expected includeComments to be false")
		}
	})

	t.Run("missing credentials error", func(t *testing.T) {
		ds := &types.DataSourceConfig{
			Settings: map[string]interface{}{
				"edition":  "server",
				"base_url": "https://jira.company.com",
				"username": "admin",
			},
		}
		_, err := parseConfig(ds)
		if err == nil {
			t.Errorf("expected error for missing credentials")
		}
	})
}

func TestPrepareSyncCursors(t *testing.T) {
	initialCursor := cursor{
		ProjectIssues: map[string]map[string]string{
			"PROJ": {"PROJ-1": "2024-01-01"},
		},
		LatestUpdated: map[string]string{
			"PROJ": "2024-01-01T12:00:00.000Z",
		},
	}
	syncCur := initialCursor.syncCursor()

	// Incremental sync
	baseline, next := prepareSyncCursors(syncCur, false)
	if next.FullSync {
		t.Errorf("expected FullSync false for incremental")
	}
	if baseline.ProjectIssues["PROJ"]["PROJ-1"] != "2024-01-01" {
		t.Errorf("expected baseline to contain PROJ-1")
	}

	// Full sync
	baselineFull, nextFull := prepareSyncCursors(syncCur, true)
	if !nextFull.FullSync {
		t.Errorf("expected FullSync true for forceFull")
	}
	if baselineFull.ProjectIssues["PROJ"]["PROJ-1"] != "2024-01-01" {
		t.Errorf("expected full sync baseline to contain previous issues")
	}
	if len(nextFull.ProjectIssues["PROJ"]) != 0 {
		t.Errorf("expected next project issues to start empty for full sync")
	}
}

func TestParseJiraTime(t *testing.T) {
	raw := "2024-01-15T10:30:00.000+0000"
	parsed := parseJiraTime(raw)
	if parsed.IsZero() {
		t.Fatalf("failed to parse jira time: %s", raw)
	}
	if parsed.Year() != 2024 || parsed.Month() != time.January || parsed.Day() != 15 {
		t.Errorf("unexpected time parsed: %v", parsed)
	}
}

func TestIssueFileName(t *testing.T) {
	fileName := issueFileName("PROJ-123", "Fix: /login?fail=true & \"timeout\"")
	if fileName != "[PROJ-123] Fix_ _login_fail=true & _timeout_.md" {
		t.Errorf("unexpected fileName: %s", fileName)
	}
}
