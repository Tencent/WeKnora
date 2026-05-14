package auth

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/secrets"
)

// tokenTestFactory wires a config + in-memory secrets store the same way
// the production Factory does, so runToken exercises the real LoadSecret
// path.
func tokenTestFactory(t *testing.T, cfg *config.Config, store *secrets.MemStore) *cmdutil.Factory {
	t.Helper()
	f := &cmdutil.Factory{
		Config:  func() (*config.Config, error) { return cfg, nil },
		Secrets: func() (secrets.Store, error) { return store, nil },
	}
	return f
}

func TestAuthToken_BearerMode_PlainOutput(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "prod",
		Contexts: map[string]config.Context{
			"prod": {Host: "https://kb.example.com", TokenRef: "prod:access", RefreshRef: "prod:refresh"},
		},
	}
	store := secrets.NewMemStore()
	_ = store.Set("prod", "access", "jwt-token-xyz")

	out, _ := iostreams.SetForTest(t)
	err := runToken(tokenTestFactory(t, cfg, store), nil)
	if err != nil {
		t.Fatalf("runToken: %v", err)
	}
	got := out.String()
	if got != "jwt-token-xyz" {
		t.Errorf("expected raw token, got %q", got)
	}
	if strings.HasSuffix(got, "\n") {
		t.Errorf("output must NOT end with newline (clean $(...) substitution); got %q", got)
	}
}

func TestAuthToken_APIKeyMode_PlainOutput(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "ci",
		Contexts: map[string]config.Context{
			"ci": {Host: "https://kb.example.com", APIKeyRef: "ci:api_key"},
		},
	}
	store := secrets.NewMemStore()
	_ = store.Set("ci", "api_key", "sk_test_apikey_42")

	out, _ := iostreams.SetForTest(t)
	if err := runToken(tokenTestFactory(t, cfg, store), nil); err != nil {
		t.Fatalf("runToken: %v", err)
	}
	if got := out.String(); got != "sk_test_apikey_42" {
		t.Errorf("expected api-key value, got %q", got)
	}
}

func TestAuthToken_JSON(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "prod",
		Contexts: map[string]config.Context{
			"prod": {Host: "https://kb.example.com", TokenRef: "prod:access"},
		},
	}
	store := secrets.NewMemStore()
	_ = store.Set("prod", "access", "jwt-xyz")

	out, _ := iostreams.SetForTest(t)
	if err := runToken(tokenTestFactory(t, cfg, store), &cmdutil.JSONOptions{}); err != nil {
		t.Fatalf("runToken: %v", err)
	}
	var env struct {
		OK   bool `json:"ok"`
		Data struct {
			Token   string `json:"token"`
			Mode    string `json:"mode"`
			Context string `json:"context"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("parse: %v\n%s", err, out.String())
	}
	if !env.OK {
		t.Errorf("ok=false")
	}
	if env.Data.Token != "jwt-xyz" || env.Data.Mode != "bearer" || env.Data.Context != "prod" {
		t.Errorf("envelope payload wrong: %+v", env.Data)
	}
}

func TestAuthToken_JSON_FieldFilter(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "ci",
		Contexts: map[string]config.Context{
			"ci": {Host: "https://kb.example.com", APIKeyRef: "ci:api_key"},
		},
	}
	store := secrets.NewMemStore()
	_ = store.Set("ci", "api_key", "sk_42")

	out, _ := iostreams.SetForTest(t)
	jopts := &cmdutil.JSONOptions{Fields: []string{"token"}}
	if err := runToken(tokenTestFactory(t, cfg, store), jopts); err != nil {
		t.Fatalf("runToken: %v", err)
	}
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, has := env.Data["mode"]; has {
		t.Errorf("mode should be filtered out: %+v", env.Data)
	}
	if env.Data["token"] != "sk_42" {
		t.Errorf("token wrong: %+v", env.Data)
	}
}

func TestAuthToken_NoCurrentContext(t *testing.T) {
	cfg := &config.Config{}
	store := secrets.NewMemStore()
	iostreams.SetForTest(t)
	err := runToken(tokenTestFactory(t, cfg, store), nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !cmdutil.IsAuthError(err) {
		t.Errorf("want auth.* code, got %v", err)
	}
}

func TestAuthToken_ContextOverride(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "prod",
		Contexts: map[string]config.Context{
			"prod":    {Host: "https://prod.example.com", TokenRef: "prod:access"},
			"staging": {Host: "https://staging.example.com", APIKeyRef: "staging:api_key"},
		},
	}
	store := secrets.NewMemStore()
	_ = store.Set("prod", "access", "prod-jwt")
	_ = store.Set("staging", "api_key", "staging-key")

	f := tokenTestFactory(t, cfg, store)
	f.ContextOverride = "staging"

	out, _ := iostreams.SetForTest(t)
	if err := runToken(f, nil); err != nil {
		t.Fatalf("runToken: %v", err)
	}
	if got := out.String(); got != "staging-key" {
		t.Errorf("expected staging-key (override), got %q", got)
	}
}

func TestAuthToken_NoStoredCredential(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "prod",
		Contexts: map[string]config.Context{
			"prod": {Host: "https://kb.example.com", TokenRef: "prod:access"},
		},
	}
	store := secrets.NewMemStore()
	// no Set — keyring is empty
	iostreams.SetForTest(t)
	err := runToken(tokenTestFactory(t, cfg, store), nil)
	if err == nil {
		t.Fatal("expected auth.unauthenticated, got nil")
	}
	if !cmdutil.IsAuthError(err) {
		t.Errorf("want auth.*, got %v", err)
	}
}

func TestAuthToken_ContextWithNoCredentialRefs(t *testing.T) {
	cfg := &config.Config{
		CurrentContext: "empty",
		Contexts: map[string]config.Context{
			"empty": {Host: "https://kb.example.com"}, // no TokenRef or APIKeyRef
		},
	}
	store := secrets.NewMemStore()
	iostreams.SetForTest(t)
	err := runToken(tokenTestFactory(t, cfg, store), nil)
	if err == nil {
		t.Fatal("expected auth.unauthenticated, got nil")
	}
	if !cmdutil.IsAuthError(err) {
		t.Errorf("want auth.*, got %v", err)
	}
}

// --- stderr advisory tests --------------------------------------------------
//
// auth token prints the token to stdout unconditionally. When stdout is an
// interactive terminal, it ALSO writes a stderr advisory ("you just put the
// secret in your scrollback") + a mode-specific rotation note for api-key
// credentials. The stdout half must stay clean under all modes so $(...)
// substitution is unaffected — tests assert both axes.

func makeBearerCfg() (*config.Config, *secrets.MemStore) {
	cfg := &config.Config{
		CurrentContext: "prod",
		Contexts: map[string]config.Context{
			"prod": {Host: "https://kb.example.com", TokenRef: "prod:access"},
		},
	}
	store := secrets.NewMemStore()
	_ = store.Set("prod", "access", "jwt-xyz")
	return cfg, store
}

func makeAPIKeyCfg() (*config.Config, *secrets.MemStore) {
	cfg := &config.Config{
		CurrentContext: "ci",
		Contexts: map[string]config.Context{
			"ci": {Host: "https://kb.example.com", APIKeyRef: "ci:api_key"},
		},
	}
	store := secrets.NewMemStore()
	_ = store.Set("ci", "api_key", "sk_42")
	return cfg, store
}

func TestAuthToken_NonTTY_NoStderrHint(t *testing.T) {
	cfg, store := makeBearerCfg()
	out, errBuf := iostreams.SetForTest(t)
	if err := runToken(tokenTestFactory(t, cfg, store), nil); err != nil {
		t.Fatalf("runToken: %v", err)
	}
	if out.String() != "jwt-xyz" {
		t.Errorf("stdout = %q, want %q", out.String(), "jwt-xyz")
	}
	if errBuf.Len() != 0 {
		t.Errorf("non-TTY stderr should be empty (scripts depend on this), got %q", errBuf.String())
	}
}

func TestAuthToken_TTY_BearerMode_StderrHintNoRotationNote(t *testing.T) {
	cfg, store := makeBearerCfg()
	out, errBuf := iostreams.SetForTestWithTTY(t)
	if err := runToken(tokenTestFactory(t, cfg, store), nil); err != nil {
		t.Fatalf("runToken: %v", err)
	}
	if out.String() != "jwt-xyz" {
		t.Errorf("stdout = %q, want raw token only", out.String())
	}
	if !strings.Contains(errBuf.String(), "scrollback") {
		t.Errorf("expected stderr scrollback hint on TTY, got %q", errBuf.String())
	}
	if strings.Contains(errBuf.String(), "api-key") {
		t.Errorf("bearer mode should not surface the api-key rotation note: %q", errBuf.String())
	}
}

func TestAuthToken_TTY_APIKeyMode_IncludesRotationNote(t *testing.T) {
	cfg, store := makeAPIKeyCfg()
	out, errBuf := iostreams.SetForTestWithTTY(t)
	if err := runToken(tokenTestFactory(t, cfg, store), nil); err != nil {
		t.Fatalf("runToken: %v", err)
	}
	if out.String() != "sk_42" {
		t.Errorf("stdout = %q, want raw token only", out.String())
	}
	stderr := errBuf.String()
	if !strings.Contains(stderr, "scrollback") {
		t.Errorf("api-key TTY stderr should still include the scrollback hint, got %q", stderr)
	}
	if !strings.Contains(stderr, "long-lived") || !strings.Contains(stderr, "rotate") {
		t.Errorf("api-key mode should include rotation note, got %q", stderr)
	}
}

func TestAuthToken_TTY_JSONMode_NoStderrHint(t *testing.T) {
	// --json output mode targets script/agent consumers even when stdout
	// happens to be a TTY (e.g. an IDE running the CLI on the user's
	// behalf). Hint would pollute their parsing — suppress.
	cfg, store := makeBearerCfg()
	_, errBuf := iostreams.SetForTestWithTTY(t)
	if err := runToken(tokenTestFactory(t, cfg, store), &cmdutil.JSONOptions{}); err != nil {
		t.Fatalf("runToken: %v", err)
	}
	if errBuf.Len() != 0 {
		t.Errorf("JSON mode should not emit stderr hint, got %q", errBuf.String())
	}
}
