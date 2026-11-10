package mcp

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestSkillReferencedMCPToolsExist is part of the K6 drift guard: the MCP tool
// names the bundled `weknora-shared` skill advertises (in its "CLI vs MCP"
// section) must all be registered by registerTools. Guards against a skill
// naming a renamed/removed tool (e.g. the agent_invoke→session_ask rename).
func TestSkillReferencedMCPToolsExist(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	skillPath := filepath.Join(filepath.Dir(file), "..", "..", "skills", "weknora-shared", "SKILL.md")
	raw, err := os.ReadFile(skillPath)
	if err != nil {
		t.Fatalf("read skill: %v", err)
	}
	body := string(raw)

	// Slice the "CLI vs MCP" section (heading → next "## " heading).
	start := strings.Index(body, "CLI vs MCP")
	if start < 0 {
		t.Fatal("weknora-shared SKILL.md has no 'CLI vs MCP' section to validate")
	}
	section := body[start:]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}

	// Tool names are backtick-quoted snake_case identifiers in that section.
	tokenRe := regexp.MustCompile("`([a-z][a-z_]*)`")
	referenced := map[string]bool{}
	for _, m := range tokenRe.FindAllStringSubmatch(section, -1) {
		tok := m[1]
		if strings.Contains(tok, "_") || tok == "chat" { // tool-shaped (skip prose like `cli`)
			referenced[tok] = true
		}
	}
	if len(referenced) == 0 {
		t.Fatal("found no MCP tool names in the skill's CLI vs MCP section")
	}

	// Live registered tool set.
	c, _ := newTestServer(t, &fakeSvc{})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := c.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	registered := map[string]bool{}
	for _, tool := range res.Tools {
		registered[tool.Name] = true
	}

	for name := range referenced {
		if !registered[name] {
			t.Errorf("weknora-shared skill references MCP tool %q which is NOT registered (renamed/removed?)", name)
		}
	}
}
