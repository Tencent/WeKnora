package contextcmd

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/format"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/prompt"
	"github.com/Tencent/WeKnora/cli/internal/secrets"
)

// confirmPrompter scripts a Confirm answer; Input/Password unused.
type confirmPrompter struct {
	answer bool
	err    error
	asked  bool
}

func (c *confirmPrompter) Input(string, string) (string, error) {
	return "", prompt.ErrAgentNoPrompt
}
func (c *confirmPrompter) Password(string) (string, error) { return "", prompt.ErrAgentNoPrompt }
func (c *confirmPrompter) Confirm(string, bool) (bool, error) {
	c.asked = true
	return c.answer, c.err
}

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
	p := &confirmPrompter{}
	if err := runRemove(&RemoveOptions{}, "staging", store, p); err != nil {
		t.Fatalf("runRemove: %v", err)
	}
	if p.asked {
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

	err := runRemove(&RemoveOptions{}, "prodution", secrets.NewMemStore(), &confirmPrompter{})
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
	err := runRemove(&RemoveOptions{}, "production", store, &confirmPrompter{})
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
	if err := runRemove(&RemoveOptions{Yes: true}, "production", store, &confirmPrompter{}); err != nil {
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

	p := &confirmPrompter{answer: false}
	err := runRemove(&RemoveOptions{}, "production", secrets.NewMemStore(), p)
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
	if !p.asked {
		t.Errorf("prompt should have been asked on TTY")
	}
	if !strings.Contains(errBuf.String(), "Aborted") {
		t.Errorf("stderr should contain Aborted, got %q", errBuf.String())
	}
	if got, _ := config.Load(); got.CurrentContext != "production" {
		t.Errorf("aborted remove must not mutate config")
	}
}

func TestRemove_DryRun(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	out, _ := iostreams.SetForTest(t)

	cfg := &config.Config{
		CurrentContext: "production",
		Contexts:       map[string]config.Context{"production": {Host: "https://prod", TokenRef: "mem://production/access"}},
	}
	if err := config.Save(cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}

	store := seedStore(t, "production", "access")
	if err := runRemove(&RemoveOptions{DryRun: true, JSONOut: true}, "production", store, &confirmPrompter{}); err != nil {
		t.Fatalf("runRemove dry-run: %v", err)
	}
	var env format.Envelope
	if jerr := json.Unmarshal(out.Bytes(), &env); jerr != nil {
		t.Fatalf("invalid envelope: %v\noutput=%q", jerr, out.String())
	}
	if !env.OK || !env.DryRun {
		t.Errorf("envelope should be ok=true, dry_run=true, got %+v", env)
	}
	if env.Risk == nil || env.Risk.Level != format.RiskHighRiskWrite {
		t.Errorf("dry-run on current context should report high-risk-write, got %+v", env.Risk)
	}
	// Nothing actually mutated.
	if got, _ := config.Load(); got.CurrentContext != "production" {
		t.Errorf("dry-run must not mutate config")
	}
	if v, err := store.Get("production", "access"); err != nil || v == "" {
		t.Errorf("dry-run must not touch keyring; get=%q err=%v", v, err)
	}
}
