package contextcmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
)

func TestAdd_HappyPath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out, _ := iostreams.SetForTest(t)

	if err := runAdd(&AddOptions{Host: "https://my.example.com", User: "alice@example.com"}, "staging"); err != nil {
		t.Fatalf("runAdd: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	c, ok := cfg.Contexts["staging"]
	if !ok {
		t.Fatalf("staging not in Contexts; got keys=%v", contextKeys(cfg.Contexts))
	}
	if c.Host != "https://my.example.com" {
		t.Errorf("Host=%q, want https://my.example.com", c.Host)
	}
	if c.User != "alice@example.com" {
		t.Errorf("User=%q, want alice@example.com", c.User)
	}
	// First context auto-becomes current.
	if cfg.CurrentContext != "staging" {
		t.Errorf("first context should auto-become current, got CurrentContext=%q", cfg.CurrentContext)
	}
	if !strings.Contains(out.String(), "staging") {
		t.Errorf("output should mention added name, got %q", out.String())
	}
}

func TestAdd_DuplicateName(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)

	cfg := &config.Config{
		CurrentContext: "staging",
		Contexts:       map[string]config.Context{"staging": {Host: "https://old.example.com"}},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	err := runAdd(&AddOptions{Host: "https://new.example.com"}, "staging")
	if err == nil {
		t.Fatal("expected error on duplicate name")
	}
	cm, ok := err.(*cmdutil.Error)
	if !ok {
		t.Fatalf("expected *cmdutil.Error, got %T", err)
	}
	if cm.Code != cmdutil.CodeResourceAlreadyExists {
		t.Errorf("code=%q, want %q", cm.Code, cmdutil.CodeResourceAlreadyExists)
	}
	// Existing entry must NOT be overwritten.
	got, _ := config.Load()
	if got.Contexts["staging"].Host != "https://old.example.com" {
		t.Errorf("existing context overwritten; Host=%q", got.Contexts["staging"].Host)
	}
}

func TestAdd_BadHost(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)

	bad := []string{
		"",                     // empty
		"my.example.com",       // missing scheme
		"ftp://my.example.com", // wrong scheme
		"http://",              // missing host
	}
	for _, h := range bad {
		err := runAdd(&AddOptions{Host: h}, "staging")
		if err == nil {
			t.Errorf("host=%q: expected error", h)
			continue
		}
		cm, ok := err.(*cmdutil.Error)
		if !ok {
			t.Errorf("host=%q: expected *cmdutil.Error, got %T", h, err)
			continue
		}
		if cm.Code != cmdutil.CodeInputInvalidArgument {
			t.Errorf("host=%q: code=%q, want %q", h, cm.Code, cmdutil.CodeInputInvalidArgument)
		}
	}
}

func TestAdd_SecondContextDoesNotChangeCurrent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)

	cfg := &config.Config{
		CurrentContext: "production",
		Contexts:       map[string]config.Context{"production": {Host: "https://prod.example.com"}},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := runAdd(&AddOptions{Host: "https://stg.example.com"}, "staging"); err != nil {
		t.Fatalf("runAdd: %v", err)
	}
	got, _ := config.Load()
	if got.CurrentContext != "production" {
		t.Errorf("adding a second context must not switch current; got %q", got.CurrentContext)
	}
}

func TestAdd_JSON(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out, _ := iostreams.SetForTest(t)

	if err := runAdd(&AddOptions{Host: "https://my.example.com", JSONOut: true}, "staging"); err != nil {
		t.Fatalf("runAdd: %v", err)
	}
	var env format.Envelope
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("invalid JSON envelope: %v\noutput=%q", err, out.String())
	}
	if !env.OK {
		t.Fatalf("envelope.ok=false, error=%+v", env.Error)
	}
	if env.Risk == nil || env.Risk.Level != format.RiskWrite {
		t.Errorf("envelope.risk should be write-level, got %+v", env.Risk)
	}
}

