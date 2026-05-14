package contextcmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

func TestList_Empty(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out, _ := iostreams.SetForTest(t)

	if err := runList(nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
	if !strings.Contains(out.String(), "No contexts") {
		t.Errorf("empty output should mention `No contexts`, got %q", out.String())
	}
}

func TestList_MultipleSorted(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out, _ := iostreams.SetForTest(t)

	cfg := &config.Config{
		CurrentContext: "staging",
		Contexts: map[string]config.Context{
			"production": {Host: "https://prod.example.com", User: "alice@example.com"},
			"staging":    {Host: "https://staging.example.com"},
			"alpha":      {Host: "https://alpha.example.com"},
		},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := runList(nil); err != nil {
		t.Fatalf("runList: %v", err)
	}
	got := out.String()
	// header
	if !strings.Contains(got, "NAME") || !strings.Contains(got, "HOST") {
		t.Errorf("missing header NAME/HOST in %q", got)
	}
	// row ordering: alpha < production < staging
	iAlpha := strings.Index(got, "alpha")
	iProd := strings.Index(got, "production")
	iStg := strings.Index(got, "staging")
	if !(iAlpha < iProd && iProd < iStg) {
		t.Errorf("rows must be alphabetical, got order alpha=%d prod=%d staging=%d in %q", iAlpha, iProd, iStg, got)
	}
	// active marker on staging row
	stgLine := lineContaining(got, "staging")
	if !strings.HasPrefix(stgLine, "*") {
		t.Errorf("active context row must start with `*`, got %q", stgLine)
	}
}

func TestList_JSON(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out, _ := iostreams.SetForTest(t)

	cfg := &config.Config{
		CurrentContext: "staging",
		Contexts: map[string]config.Context{
			"staging":    {Host: "https://staging.example.com", User: "bob@example.com"},
			"production": {Host: "https://prod.example.com"},
		},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := runList(&cmdutil.JSONOptions{}); err != nil {
		t.Fatalf("runList: %v", err)
	}

	var rows []map[string]any
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatalf("invalid JSON: %v\noutput=%q", err, out.String())
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(rows))
	}
	// alphabetical: production before staging
	if rows[0]["name"] != "production" {
		t.Errorf("first row should be production, got %v", rows[0])
	}
	if rows[1]["name"] != "staging" || rows[1]["current"] != true {
		t.Errorf("second row should be staging with current=true, got %v", rows[1])
	}
}

// lineContaining returns the first line of s that contains needle (trimmed of
// the trailing newline) or "" if no line matches.
func lineContaining(s, needle string) string {
	for l := range strings.SplitSeq(s, "\n") {
		if strings.Contains(l, needle) {
			return l
		}
	}
	return ""
}
