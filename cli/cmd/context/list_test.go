package contextcmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

func TestList_Empty(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out, _ := iostreams.SetForTest(t)

	if err := runList(&ListOptions{}); err != nil {
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

	if err := runList(&ListOptions{}); err != nil {
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

	if err := runList(&ListOptions{JSONOut: true}); err != nil {
		t.Fatalf("runList: %v", err)
	}

	var env format.Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("invalid JSON envelope: %v\noutput=%q", err, out.String())
	}
	if !env.OK {
		t.Fatalf("envelope.ok=false, error=%+v", env.Error)
	}
	if env.Meta == nil || env.Meta.Context != "staging" {
		t.Errorf("envelope._meta.context should be %q, got %+v", "staging", env.Meta)
	}
	rows, ok := env.Data.([]any)
	if !ok {
		t.Fatalf("envelope.data should be []listEntry, got %T", env.Data)
	}
	if len(rows) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(rows))
	}
	// alphabetical: production before staging
	first := rows[0].(map[string]any)
	if first["name"] != "production" {
		t.Errorf("first row should be production, got %v", first)
	}
	second := rows[1].(map[string]any)
	if second["name"] != "staging" || second["current"] != true {
		t.Errorf("second row should be staging with current=true, got %v", second)
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
