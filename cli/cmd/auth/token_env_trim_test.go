package auth

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	"github.com/Tencent/WeKnora/cli/internal/secrets"
)

// The env-credential path must emit the same trimmed value the CLI itself
// authenticates with: EnvCredential gates on the trimmed env value and
// buildClientFromEnv trims before building the client, so a stray newline or
// padding (common in CI secret stores) would otherwise only break the captured
// $(weknora auth token) output, not the CLI's own calls.
func TestAuthToken_EnvTokenTrimmed_Text(t *testing.T) {
	t.Setenv("WEKNORA_TOKEN", "  jwt-env-xyz\n")
	// No profiles: proves the value came from the env path, not the store.
	cfg := &config.Config{}
	out, _ := iostreams.SetForTest(t)
	f := tokenTestFactory(t, cfg, secrets.NewMemStore())
	if err := runToken(f, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}); err != nil {
		t.Fatalf("runToken: %v", err)
	}
	if got := out.String(); got != "jwt-env-xyz" {
		t.Errorf("env token must be emitted trimmed for $(...) capture; got %q", got)
	}
}

func TestAuthToken_EnvAPIKeyTrimmed_Text(t *testing.T) {
	t.Setenv("WEKNORA_API_KEY", "\tsk_env_42  ")
	cfg := &config.Config{}
	out, _ := iostreams.SetForTest(t)
	f := tokenTestFactory(t, cfg, secrets.NewMemStore())
	if err := runToken(f, &cmdutil.FormatOptions{Mode: cmdutil.FormatText}); err != nil {
		t.Fatalf("runToken: %v", err)
	}
	if got := out.String(); got != "sk_env_42" {
		t.Errorf("env api key must be emitted trimmed; got %q", got)
	}
}

func TestAuthToken_EnvTokenTrimmed_JSON(t *testing.T) {
	t.Setenv("WEKNORA_TOKEN", " jwt-env-xyz\n")
	cfg := &config.Config{}
	out, _ := iostreams.SetForTest(t)
	f := tokenTestFactory(t, cfg, secrets.NewMemStore())
	if err := runToken(f, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}); err != nil {
		t.Fatalf("runToken: %v", err)
	}
	var env struct {
		Data struct {
			Token   string `json:"token"`
			Mode    string `json:"mode"`
			Profile string `json:"profile"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out.Bytes(), &env); err != nil {
		t.Fatalf("parse: %v\n%s", err, out.String())
	}
	if env.Data.Token != "jwt-env-xyz" || env.Data.Mode != "bearer" || env.Data.Profile != "(env)" {
		t.Errorf("envelope wrong: %+v", env.Data)
	}
}
