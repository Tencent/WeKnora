package contextcmd

import (
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

func TestUse_OK(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out, _ := iostreams.SetForTest(t)

	cfg := &config.Config{
		CurrentContext: "staging",
		Contexts: map[string]config.Context{
			"staging":    {Host: "https://staging.example.com"},
			"production": {Host: "https://prod.example.com"},
		},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("Save initial config: %v", err)
	}

	if err := runUse("production"); err != nil {
		t.Fatalf("runUse: %v", err)
	}

	got, err := config.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.CurrentContext != "production" {
		t.Errorf("CurrentContext = %q, want production", got.CurrentContext)
	}
	if !strings.Contains(out.String(), "production") {
		t.Errorf("output should mention switched-to context, got %q", out.String())
	}
}

func TestUse_NotFound_WithDidYouMean(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)

	cfg := &config.Config{Contexts: map[string]config.Context{
		"production": {Host: "https://prod"},
		"staging":    {Host: "https://staging"},
	}}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	err := runUse("prodution") // typo: missing 'c'
	if err == nil {
		t.Fatal("expected error")
	}
	cm, ok := err.(*cmdutil.Error)
	if !ok {
		t.Fatalf("expected *cmdutil.Error, got %T", err)
	}
	if cm.Code != cmdutil.CodeLocalContextNotFound {
		t.Errorf("code = %q, want %q", cm.Code, cmdutil.CodeLocalContextNotFound)
	}
	if !strings.Contains(cm.Hint, "production") {
		t.Errorf("hint should suggest 'production', got %q", cm.Hint)
	}
}

func TestUse_NotFound_EmptyContexts(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)

	err := runUse("anything")
	if err == nil {
		t.Fatal("expected error")
	}
	cm := err.(*cmdutil.Error)
	if !strings.Contains(cm.Hint, "auth login") {
		t.Errorf("hint should mention `auth login` for empty Contexts, got %q", cm.Hint)
	}
}

func TestUse_CaseSensitive(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)

	cfg := &config.Config{Contexts: map[string]config.Context{
		"Production": {Host: "https://prod"},
	}}
	_ = config.Save(cfg)

	err := runUse("production") // lowercase — must NOT match "Production"
	if err == nil {
		t.Fatal("expected case-sensitive miss")
	}
	cm := err.(*cmdutil.Error)
	// did-you-mean kicks in (distance 1 — "P"→"p")
	if !strings.Contains(cm.Hint, "Production") {
		t.Errorf("hint should suggest 'Production' (case-different), got %q", cm.Hint)
	}
}
