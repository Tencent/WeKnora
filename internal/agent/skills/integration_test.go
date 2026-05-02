package skills

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
)

// TestExampleSkillsIntegration tests with the actual example skills in examples/skills
func TestExampleSkillsIntegration(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("Failed to get current file path")
	}

	// Navigate from internal/agent/skills to examples/skills
	skillsDir := filepath.Join(filepath.Dir(filename), "..", "..", "..", "examples", "skills")

	repo := NewFSRepository([]string{skillsDir})
	all, err := repo.Discover(context.Background())
	if err != nil {
		t.Fatalf("Failed to discover skills: %v", err)
	}

	if len(all) == 0 {
		t.Skip("No example skills found in examples/skills directory")
	}

	t.Logf("Discovered %d example skills:", len(all))
	for _, m := range all {
		t.Logf("  - %s: %s", m.Name, truncate(m.Description, 60))
	}

	pdfSkill, err := repo.GetByName(context.Background(), "pdf-processing")
	if err != nil {
		t.Skipf("pdf-processing skill not present: %v", err)
	}

	if pdfSkill.Name != "pdf-processing" {
		t.Errorf("Expected name 'pdf-processing', got '%s'", pdfSkill.Name)
	}

	if pdfSkill.Instructions == "" {
		t.Error("Expected instructions to be non-empty")
	}

	t.Logf("PDF Processing skill instructions length: %d characters", len(pdfSkill.Instructions))

	formsFile, err := repo.ReadFile(context.Background(), "pdf-processing", "FORMS.md")
	if err != nil {
		t.Fatalf("Failed to load FORMS.md: %v", err)
	}

	if formsFile.Content == "" {
		t.Error("Expected FORMS.md content to be non-empty")
	}

	t.Logf("FORMS.md content length: %d characters", len(formsFile.Content))

	scriptFile, err := repo.ReadFile(context.Background(), "pdf-processing", "scripts/analyze_form.py")
	if err != nil {
		t.Fatalf("Failed to load analyze_form.py: %v", err)
	}

	if !scriptFile.IsScript {
		t.Error("analyze_form.py should be marked as script")
	}

	t.Logf("analyze_form.py content length: %d characters", len(scriptFile.Content))

	files, err := repo.ListFiles(context.Background(), "pdf-processing")
	if err != nil {
		t.Fatalf("Failed to list skill files: %v", err)
	}

	t.Logf("Files in pdf-processing skill:")
	for _, f := range files {
		t.Logf("  - %s (script: %v)", f, IsScript(f))
	}
}

// TestRuntimeWithExampleSkills exercises the full SkillRuntime against real
// preloaded skills (when available).
func TestRuntimeWithExampleSkills(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("Failed to get current file path")
	}

	skillsDir := filepath.Join(filepath.Dir(filename), "..", "..", "..", "examples", "skills")

	rt := NewRuntime(Options{
		SkillDirs:   []string{skillsDir},
		Enabled:     true,
		SandboxMode: "disabled",
	})

	ctx := context.Background()
	if err := rt.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize runtime: %v", err)
	}

	metas := rt.ListMetadata(ctx)
	if len(metas) == 0 {
		t.Skip("No example skills found")
	}

	t.Logf("Runtime discovered %d skills for system prompt injection", len(metas))
	for _, m := range metas {
		t.Logf("Level 1 (metadata): %s - %s", m.Name, truncate(m.Description, 50))
	}

	skill, err := rt.Load(ctx, "pdf-processing")
	if err != nil {
		t.Skipf("pdf-processing skill not present: %v", err)
	}
	t.Logf("Level 2 (instructions): Loaded %d characters of instructions", len(skill.Instructions))

	formsContent, err := rt.ReadFile(ctx, "pdf-processing", "FORMS.md")
	if err != nil {
		t.Fatalf("Failed to read skill file: %v", err)
	}
	t.Logf("Level 3 (resources): Loaded FORMS.md with %d characters", len(formsContent))

	info, err := rt.GetInfo(ctx, "pdf-processing")
	if err != nil {
		t.Fatalf("Failed to get skill info: %v", err)
	}
	t.Logf("Skill info: name=%s, files=%d", info.Name, len(info.Files))
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
