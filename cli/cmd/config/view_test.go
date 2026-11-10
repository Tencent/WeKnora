package configcmd

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/config"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

type viewEnvelope struct {
	OK   bool `json:"ok"`
	Data struct {
		ActiveProfile string `json:"active_profile"`
		ProfileSource string `json:"profile_source"`
		AuthSource    string `json:"auth_source"`
		Host          string `json:"host"`
		KBID          string `json:"kb_id"`
		KBSource      string `json:"kb_source"`
		FormatDefault string `json:"format_default"`
		ConfigFile    string `json:"config_file"`
	} `json:"data"`
}

func runViewJSON(t *testing.T, f *cmdutil.Factory) viewEnvelope {
	t.Helper()
	out, _ := iostreams.SetForTest(t)
	// --format is a root-level persistent flag, so drive runView directly with
	// a JSON FormatOptions rather than through standalone leaf flag parsing.
	cmd := NewCmdView(f)
	require.NoError(t, runView(cmd, f, &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}))
	var env viewEnvelope
	require.NoError(t, json.Unmarshal(out.Bytes(), &env), "envelope: %q", out.String())
	return env
}

// TestConfigView_ConfigProfileSource: with a current_profile set in config and
// no flag/env override, config view reports the profile, its source, and the
// host — and resolves the KB to "(unresolved)" (no --kb/env/link) without error.
func TestConfigView_ConfigProfileSource(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := &config.Config{
		CurrentProfile: "prod",
		Profiles:       map[string]config.Profile{"prod": {Host: "https://kb.example.com"}},
	}
	require.NoError(t, config.Save(cfg))

	env := runViewJSON(t, networkFreeFactory(t))
	assert.True(t, env.OK)
	assert.Equal(t, "prod", env.Data.ActiveProfile)
	assert.Equal(t, "config current_profile", env.Data.ProfileSource)
	assert.Equal(t, "https://kb.example.com", env.Data.Host)
	assert.Equal(t, "(unresolved)", env.Data.KBSource, "no --kb/env/link must report unresolved, not error")
	assert.Equal(t, "json", env.Data.FormatDefault)
	assert.NotEmpty(t, env.Data.ConfigFile)
}

// TestConfigView_ProfileOverrideSource: the --profile override (set on the
// Factory by root's PersistentPreRunE) wins over config and is reported as
// such, so an agent can see why a non-default profile is active.
func TestConfigView_ProfileOverrideSource(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := &config.Config{
		CurrentProfile: "prod",
		Profiles:       map[string]config.Profile{"prod": {Host: "https://kb.example.com"}},
	}
	require.NoError(t, config.Save(cfg))

	f := networkFreeFactory(t)
	f.ProfileOverride = "other"
	env := runViewJSON(t, f)
	assert.Equal(t, "other", env.Data.ActiveProfile)
	assert.Equal(t, "--profile flag", env.Data.ProfileSource)
}

// TestConfigView_NoProfile: config view succeeds with an empty config —
// active_profile "" / source "(none)" — never erroring on a fresh install.
func TestConfigView_NoProfile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	env := runViewJSON(t, networkFreeFactory(t))
	assert.True(t, env.OK)
	assert.Equal(t, "", env.Data.ActiveProfile)
	assert.Equal(t, "(none)", env.Data.ProfileSource)
}

// networkFreeFactory builds a Factory whose Client closure fails the test if
// invoked — config view must resolve everything locally (ResolveKBLocal,
// ActiveProfile) and never build the SDK client.
func networkFreeFactory(t *testing.T) *cmdutil.Factory {
	t.Helper()
	f := cmdutil.New()
	f.Client = func() (*sdk.Client, error) {
		t.Fatal("config view must not build the SDK client / hit the network")
		return nil, nil
	}
	return f
}

// TestConfigView_EnvCredentialSurfaced: when WEKNORA_API_KEY + WEKNORA_HOST are
// set, config view reports the env override (auth_source + the env host), not
// the bypassed config profile's host.
func TestConfigView_EnvCredentialSurfaced(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := &config.Config{
		CurrentProfile: "prod",
		Profiles:       map[string]config.Profile{"prod": {Host: "https://configured.example.com"}},
	}
	require.NoError(t, config.Save(cfg))
	t.Setenv("WEKNORA_API_KEY", "sk-test")
	t.Setenv("WEKNORA_HOST", "https://env-override.example.com")

	env := runViewJSON(t, networkFreeFactory(t))
	assert.Contains(t, env.Data.AuthSource, "WEKNORA_API_KEY env")
	assert.Equal(t, "https://env-override.example.com", env.Data.Host, "host must be the env override, not the profile host")
}
