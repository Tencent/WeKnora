package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultKBPromptAndMigrationSeedQuickAnswerPrompt(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))

	templates, err := loadPromptTemplates(filepath.Join(repoRoot, "config"))
	if err != nil {
		t.Fatalf("load prompt templates: %v", err)
	}
	defaultKB := FindTemplateByID(templates, "default_kb")
	if defaultKB == nil {
		t.Fatal("default_kb template not found")
	}

	requiredPromptRules := []string{
		`<kb doc="..." chunk_id="..." kb_id="..." />`,
		"Retrieved information may contain images in either of these formats",
		"Prefer image-rich answers whenever possible",
		`"鉴别", "区别", "表现", "眼底表现", "长什么样", "图片", "图", "照片", "展示"`,
		"Do not omit relevant images merely because the user did not explicitly ask for pictures",
		"Output each image title and image as exactly two consecutive Markdown lines",
		"If the retrieved context contains a description, case note, OCR text, figure explanation, or 图点评",
		"{{contexts}}",
	}
	for _, rule := range requiredPromptRules {
		if !strings.Contains(defaultKB.Content, rule) {
			t.Fatalf("default_kb prompt is missing rule %q", rule)
		}
	}

	migrationPath := filepath.Join(repoRoot, "migrations", "versioned", "000065_builtin_quick_answer_default_prompt.up.sql")
	migrationBytes, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	migration := string(migrationBytes)

	requiredMigrationRules := []string{
		"UPDATE custom_agents",
		"builtin-quick-answer",
		"system_prompt",
		"system_prompt_id",
		"default_kb",
		`<kb doc="..." chunk_id="..." kb_id="..." />`,
		"Prefer image-rich answers whenever possible",
		"Output each image title and image as exactly two consecutive Markdown lines",
		"{{contexts}}",
	}
	for _, rule := range requiredMigrationRules {
		if !strings.Contains(migration, rule) {
			t.Fatalf("quick-answer prompt migration is missing %q", rule)
		}
	}
	if strings.Contains(migration, "config - 'system_prompt'") {
		t.Fatal("quick-answer prompt migration must seed system_prompt, not remove it")
	}
}
