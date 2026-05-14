package contextcmd

import (
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/secrets"
	"github.com/Tencent/WeKnora/cli/internal/testutil"
)

// seedStore returns a MemStore pre-loaded with sentinel values for every
// secret slot a context might reference. Tests assert deletion by checking
// `secrets.ErrNotFound` post-runRemove.
func seedStore(t *testing.T, name string, slots ...string) *secrets.MemStore {
	t.Helper()
	s := secrets.NewMemStore()
	for _, slot := range slots {
		if err := s.Set(name, slot, "sentinel-"+name+"-"+slot); err != nil {
			t.Fatalf("seed %s/%s: %v", name, slot, err)
		}
	}
	return s
}

func assertDeleted(t *testing.T, s *secrets.MemStore, name, slot string) {
	t.Helper()
	if _, err := s.Get(name, slot); !errors.Is(err, secrets.ErrNotFound) {
		t.Errorf("expected %s/%s removed, got err=%v", name, slot, err)
	}
}

func TestRemove_NonCurrent_NoPromptNeeded(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out, _ := iostreams.SetForTest(t)

	cfg := &config.Config{
		CurrentContext: "production",
		Contexts: map[string]config.Context{
			"production": {Host: "https://prod.example.com", TokenRef: "mem://production/access"},
			"staging":    {Host: "https://staging.example.com", APIKeyRef: "mem://staging/api_key"},
		},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	store := seedStore(t, "staging", "api_key")
	p := &testutil.ConfirmPrompter{}
	if err := runRemove(&RemoveOptions{}, nil, "staging", store, p); err != nil {
		t.Fatalf("runRemove: %v", err)
	}
	if p.Asked {
		t.Errorf("non-current remove must not prompt")
	}

	got, _ := config.Load()
	if _, exists := got.Contexts["staging"]; exists {
		t.Errorf("staging should have been removed; Contexts=%v", got.Contexts)
	}
	if got.CurrentContext != "production" {
		t.Errorf("CurrentContext must be unchanged, got %q", got.CurrentContext)
	}
	assertDeleted(t, store, "staging", "api_key")
	if !strings.Contains(out.String(), "staging") {
		t.Errorf("output should mention removed context, got %q", out.String())
	}
}

func TestRemove_NotFound_WithDidYouMean(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)

	cfg := &config.Config{Contexts: map[string]config.Context{
		"production": {Host: "https://prod"},
		"staging":    {Host: "https://staging"},
	}}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	err := runRemove(&RemoveOptions{}, nil, "prodution", secrets.NewMemStore(), &testutil.ConfirmPrompter{})
	if err == nil {
		t.Fatal("expected not-found error")
	}
	cm, ok := err.(*cmdutil.Error)
	if !ok {
		t.Fatalf("expected *cmdutil.Error, got %T", err)
	}
	if cm.Code != cmdutil.CodeLocalContextNotFound {
		t.Errorf("code=%q, want %q", cm.Code, cmdutil.CodeLocalContextNotFound)
	}
	if !strings.Contains(cm.Hint, "production") {
		t.Errorf("hint should suggest 'production', got %q", cm.Hint)
	}
}

func TestRemove_Current_NonTTY_NoYes_RequiresConfirmation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, _ = iostreams.SetForTest(t)

	cfg := &config.Config{
		CurrentContext: "production",
		Contexts: map[string]config.Context{
			"production": {Host: "https://prod", TokenRef: "mem://production/access"},
			"staging":    {Host: "https://staging"},
		},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	store := seedStore(t, "production", "access")
	err := runRemove(&RemoveOptions{}, nil, "production", store, &testutil.ConfirmPrompter{})
	if err == nil {
		t.Fatal("expected confirmation-required error")
	}
	cm, ok := err.(*cmdutil.Error)
	if !ok {
		t.Fatalf("expected *cmdutil.Error, got %T", err)
	}
	if cm.Code != cmdutil.CodeInputConfirmationRequired {
		t.Errorf("code=%q, want %q", cm.Code, cmdutil.CodeInputConfirmationRequired)
	}
	if cmdutil.ExitCode(err) != 10 {
		t.Errorf("expected exit-10, got %d", cmdutil.ExitCode(err))
	}
	// Must not have mutated config or keyring.
	if got, _ := config.Load(); got.CurrentContext != "production" {
		t.Errorf("config mutated despite confirmation gate: CurrentContext=%q", got.CurrentContext)
	}
	if v, err := store.Get("production", "access"); err != nil || v == "" {
		t.Errorf("keyring touched before confirmation, get=%q err=%v", v, err)
	}
}

func TestRemove_Current_WithYes_ClearsCurrent(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out, _ := iostreams.SetForTest(t)

	cfg := &config.Config{
		CurrentContext: "production",
		Contexts: map[string]config.Context{
			"production": {Host: "https://prod", TokenRef: "mem://production/access"},
			"staging":    {Host: "https://staging"},
		},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	store := seedStore(t, "production", "access")
	if err := runRemove(&RemoveOptions{Yes: true}, nil, "production", store, &testutil.ConfirmPrompter{}); err != nil {
		t.Fatalf("runRemove: %v", err)
	}
	got, _ := config.Load()
	if _, exists := got.Contexts["production"]; exists {
		t.Errorf("production should be removed")
	}
	if got.CurrentContext != "" {
		t.Errorf("removing current must clear CurrentContext, got %q", got.CurrentContext)
	}
	assertDeleted(t, store, "production", "access")
	if !strings.Contains(out.String(), "current context cleared") {
		t.Errorf("output should warn about cleared current, got %q", out.String())
	}
}

func TestRemove_Current_TTY_PromptNo(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, errBuf := iostreams.SetForTestWithTTY(t)

	cfg := &config.Config{
		CurrentContext: "production",
		Contexts:       map[string]config.Context{"production": {Host: "https://prod"}},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	p := &testutil.ConfirmPrompter{Answer: false}
	err := runRemove(&RemoveOptions{}, nil, "production", secrets.NewMemStore(), p)
	if err == nil {
		t.Fatal("expected user-aborted error")
	}
	cm, ok := err.(*cmdutil.Error)
	if !ok {
		t.Fatalf("expected *cmdutil.Error, got %T", err)
	}
	if cm.Code != cmdutil.CodeUserAborted {
		t.Errorf("code=%q, want %q", cm.Code, cmdutil.CodeUserAborted)
	}
	if !p.Asked {
		t.Errorf("prompt should have been asked on TTY")
	}
	if !strings.Contains(errBuf.String(), "Aborted") {
		t.Errorf("stderr should contain Aborted, got %q", errBuf.String())
	}
	if got, _ := config.Load(); got.CurrentContext != "production" {
		t.Errorf("aborted remove must not mutate config")
	}
}

