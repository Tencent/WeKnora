package skills

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestParseSkillFile(t *testing.T) {
	content := `---
name: test-skill
description: A test skill for unit testing purposes.
---
# Test Skill

This is the content of the test skill.

## Usage

Use this skill when testing.
`

	skill, err := ParseSkillFile(content)
	if err != nil {
		t.Fatalf("Failed to parse skill file: %v", err)
	}

	if skill.Name != "test-skill" {
		t.Errorf("Expected name 'test-skill', got '%s'", skill.Name)
	}

	if skill.Description != "A test skill for unit testing purposes." {
		t.Errorf("Expected description 'A test skill for unit testing purposes.', got '%s'", skill.Description)
	}

	if skill.Instructions == "" {
		t.Error("Expected instructions to be non-empty")
	}

	if !skill.Loaded {
		t.Error("Expected Loaded to be true after parsing")
	}

	t.Logf("Parsed skill: name=%s, description=%s, instructions_len=%d",
		skill.Name, skill.Description, len(skill.Instructions))
}

func TestSkillValidation(t *testing.T) {
	tests := []struct {
		name        string
		skillName   string
		description string
		wantErr     bool
		errContains string
	}{
		{
			name:        "valid skill",
			skillName:   "my-skill",
			description: "A valid skill",
			wantErr:     false,
		},
		{
			name:        "empty name",
			skillName:   "",
			description: "A skill",
			wantErr:     true,
			errContains: "name is required",
		},
		{
			name:        "invalid characters in name",
			skillName:   "My Skill",
			description: "A skill",
			wantErr:     true,
			errContains: "lowercase letters",
		},
		{
			name:        "reserved word in name",
			skillName:   "my-claude-skill",
			description: "A skill",
			wantErr:     true,
			errContains: "reserved word",
		},
		{
			name:        "empty description",
			skillName:   "my-skill",
			description: "",
			wantErr:     true,
			errContains: "description is required",
		},
		{
			name:        "name too long",
			skillName:   "this-is-a-very-long-skill-name-that-exceeds-the-maximum-allowed-length-of-64-characters",
			description: "A skill",
			wantErr:     true,
			errContains: "exceeds maximum length",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skill := &Skill{
				Name:        tt.skillName,
				Description: tt.description,
			}

			err := skill.Validate()
			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error containing '%s', got nil", tt.errContains)
				} else if tt.errContains != "" && !containsString(err.Error(), tt.errContains) {
					t.Errorf("Expected error containing '%s', got '%s'", tt.errContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestRepositoryDiscover(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "skills-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	skillDir := filepath.Join(tmpDir, "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("Failed to create skill dir: %v", err)
	}

	skillContent := `---
name: test-skill
description: A test skill for repository testing.
---
# Test Skill

This is the test skill content.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644); err != nil {
		t.Fatalf("Failed to write SKILL.md: %v", err)
	}

	repo := NewFSRepository([]string{tmpDir})
	all, err := repo.Discover(context.Background())
	if err != nil {
		t.Fatalf("Failed to discover skills: %v", err)
	}

	if len(all) != 1 {
		t.Fatalf("Expected 1 skill, got %d", len(all))
	}

	if all[0].Name != "test-skill" {
		t.Errorf("Expected skill name 'test-skill', got '%s'", all[0].Name)
	}

	t.Logf("Discovered %d skills: %v", len(all), all[0].Name)
}

func TestRepositoryGetByName(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "skills-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	skillDir := filepath.Join(tmpDir, "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("Failed to create skill dir: %v", err)
	}

	skillContent := `---
name: test-skill
description: A test skill for content loading.
---
# Test Skill

This is the main content.

## Section 1

More content here.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644); err != nil {
		t.Fatalf("Failed to write SKILL.md: %v", err)
	}

	repo := NewFSRepository([]string{tmpDir})
	skill, err := repo.GetByName(context.Background(), "test-skill")
	if err != nil {
		t.Fatalf("Failed to load skill: %v", err)
	}

	if skill.Name != "test-skill" {
		t.Errorf("Expected skill name 'test-skill', got '%s'", skill.Name)
	}

	if skill.Instructions == "" {
		t.Error("Expected instructions to be non-empty")
	}

	if !skill.Loaded {
		t.Error("Expected Loaded to be true")
	}
}

func TestRepositoryReadFile(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "skills-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	skillDir := filepath.Join(tmpDir, "test-skill")
	scriptsDir := filepath.Join(skillDir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0755); err != nil {
		t.Fatalf("Failed to create skill dir: %v", err)
	}

	skillContent := `---
name: test-skill
description: A test skill with additional files.
---
# Test Skill

See [GUIDE.md](GUIDE.md) for more info.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644); err != nil {
		t.Fatalf("Failed to write SKILL.md: %v", err)
	}

	guideContent := "# Guide\n\nThis is the guide content."
	if err := os.WriteFile(filepath.Join(skillDir, "GUIDE.md"), []byte(guideContent), 0644); err != nil {
		t.Fatalf("Failed to write GUIDE.md: %v", err)
	}

	scriptContent := "#!/usr/bin/env python3\nprint('Hello from script')"
	if err := os.WriteFile(filepath.Join(scriptsDir, "hello.py"), []byte(scriptContent), 0644); err != nil {
		t.Fatalf("Failed to write script: %v", err)
	}

	repo := NewFSRepository([]string{tmpDir})
	if _, err := repo.Discover(context.Background()); err != nil {
		t.Fatalf("Failed to discover skills: %v", err)
	}

	file, err := repo.ReadFile(context.Background(), "test-skill", "GUIDE.md")
	if err != nil {
		t.Fatalf("Failed to load skill file: %v", err)
	}

	if file.Content != guideContent {
		t.Errorf("Expected guide content, got '%s'", file.Content)
	}

	if file.IsScript {
		t.Error("GUIDE.md should not be marked as script")
	}

	scriptFile, err := repo.ReadFile(context.Background(), "test-skill", "scripts/hello.py")
	if err != nil {
		t.Fatalf("Failed to load script file: %v", err)
	}

	if !scriptFile.IsScript {
		t.Error("hello.py should be marked as script")
	}

	// Path traversal protection.
	if _, err := repo.ReadFile(context.Background(), "test-skill", "../../etc/passwd"); err == nil {
		t.Error("Expected path-traversal to be rejected")
	}
}

func TestRuntimeIntegration(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "skills-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	skillDir := filepath.Join(tmpDir, "test-skill")
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatalf("Failed to create skill dir: %v", err)
	}

	skillContent := `---
name: test-skill
description: A test skill for runtime integration testing.
---
# Test Skill

Integration test content.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillContent), 0644); err != nil {
		t.Fatalf("Failed to write SKILL.md: %v", err)
	}

	rt, err := NewRuntime(Options{
		SkillDirs:   []string{tmpDir},
		Enabled:     true,
		SandboxMode: "disabled",
	})
	if err != nil {
		t.Fatalf("Failed to construct runtime: %v", err)
	}

	ctx := context.Background()
	if err := rt.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize runtime: %v", err)
	}

	metas := rt.ListMetadata(ctx)
	if len(metas) != 1 {
		t.Fatalf("Expected 1 skill, got %d", len(metas))
	}

	skill, err := rt.Load(ctx, "test-skill")
	if err != nil {
		t.Fatalf("Failed to load skill: %v", err)
	}
	if skill.Name != "test-skill" {
		t.Errorf("Expected skill name 'test-skill', got '%s'", skill.Name)
	}

	// Allow-list rejection.
	rtRestricted, err := NewRuntime(Options{
		SkillDirs:     []string{tmpDir},
		AllowedSkills: []string{"other-skill"},
		Enabled:       true,
	})
	if err != nil {
		t.Fatalf("construct restricted runtime: %v", err)
	}
	_ = rtRestricted.Initialize(ctx)
	if metas := rtRestricted.ListMetadata(ctx); len(metas) != 0 {
		t.Errorf("Expected allow-list to filter out test-skill, got %d", len(metas))
	}
	if _, err := rtRestricted.Load(ctx, "test-skill"); err == nil {
		t.Error("Expected Load to reject disallowed skill")
	}
}

func TestRuntimeDisabled(t *testing.T) {
	rt, err := NewRuntime(Options{Enabled: false})
	if err != nil {
		t.Fatalf("construct: %v", err)
	}
	ctx := context.Background()
	if rt.IsEnabled() {
		t.Error("expected runtime to be disabled")
	}
	if metas := rt.ListMetadata(ctx); metas != nil {
		t.Errorf("expected nil metadata when disabled, got %d", len(metas))
	}
	if _, err := rt.Load(ctx, "anything"); err == nil {
		t.Error("expected Load to fail when disabled")
	}
}

func TestFilterMetadata(t *testing.T) {
	all := []*SkillMetadata{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	}
	if got := FilterMetadata(all, nil); len(got) != 3 {
		t.Errorf("nil allow-list should pass through, got %d", len(got))
	}
	if got := FilterMetadata(all, []string{"a", "c"}); len(got) != 2 {
		t.Errorf("expected 2 filtered skills, got %d", len(got))
	}
}

func TestIsScript(t *testing.T) {
	tests := []struct {
		path     string
		expected bool
	}{
		{"script.py", true},
		{"script.sh", true},
		{"script.bash", true},
		{"script.js", true},
		{"script.ts", true},
		{"script.rb", true},
		{"README.md", false},
		{"data.json", false},
		{"config.yaml", false},
	}

	for _, tt := range tests {
		result := IsScript(tt.path)
		if result != tt.expected {
			t.Errorf("IsScript(%s) = %v, expected %v", tt.path, result, tt.expected)
		}
	}
}
