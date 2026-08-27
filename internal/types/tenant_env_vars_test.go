package types

import (
	"bytes"
	"encoding/json"
	"log"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

// skillEnvAESKey is exactly 32 bytes, the only length GetAESKey accepts.
const skillEnvAESKey = "0123456789abcdef0123456789abcdef"

func TestSkillEnvVarsValueEncryptsOnlyTheValue(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", skillEnvAESKey)

	vars := SkillEnvVars{{
		Name:        "TAVILY_API_KEY",
		Description: "Tavily search key",
		Required:    true,
		Value:       "tvly-super-secret",
	}}

	stored, err := vars.Value()
	require.NoError(t, err)
	raw, ok := stored.([]byte)
	require.True(t, ok, "Value must marshal to JSON bytes for a jsonb column")

	require.NotContains(t, string(raw), "tvly-super-secret",
		"the plaintext secret must never reach the column")

	var decoded []map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Len(t, decoded, 1)
	require.Equal(t, "TAVILY_API_KEY", decoded[0]["name"],
		"the name stays plaintext so the UI can render it without a key")
	require.Equal(t, "Tavily search key", decoded[0]["description"])
	require.Equal(t, true, decoded[0]["required"])
	require.True(t, strings.HasPrefix(decoded[0]["value"].(string), utils.EncPrefix),
		"only the value is encrypted")

	require.Equal(t, "tvly-super-secret", vars[0].Value,
		"the receiver holds the caller's plaintext; encryption must happen on a copy")
}

func TestSkillEnvVarsValueScanRoundTrip(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", skillEnvAESKey)

	vars := SkillEnvVars{
		{Name: "WITH_VALUE", Description: "has an admin value", Required: true, Value: "secret-1"},
		{Name: "UNSET", Description: "user supplies it"},
	}

	stored, err := vars.Value()
	require.NoError(t, err)

	var got SkillEnvVars
	require.NoError(t, got.Scan(stored))
	require.Len(t, got, 2)
	require.Equal(t, "WITH_VALUE", got[0].Name)
	require.True(t, got[0].Required)
	require.Equal(t, "secret-1", got[0].Value, "a stored value reads back as plaintext")
	require.Equal(t, "UNSET", got[1].Name)
	require.Empty(t, got[1].Value, "an empty value stays empty rather than becoming ciphertext")
}

func TestSkillEnvVarsScanKeepsDeclarationWhenValueUndecryptable(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", skillEnvAESKey)
	var logs bytes.Buffer
	previousLogOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previousLogOutput) })

	// Ciphertext that cannot be opened, as after a key rotation.
	stored := []byte(`[{"name":"TAVILY_API_KEY","description":"key","required":true,` +
		`"value":"` + utils.EncPrefix + `not-real-ciphertext"}]`)

	var got SkillEnvVars
	require.NoError(t, got.Scan(stored),
		"an unreadable secret must not fail the whole row load")
	require.Len(t, got, 1)
	require.Equal(t, "TAVILY_API_KEY", got[0].Name, "the declaration survives")
	require.True(t, got[0].Required)
	require.Empty(t, got[0].Value, "an unreadable value reports as unset, never as ciphertext")
	require.Contains(t, logs.String(), "[crypto] skill_env_vars.TAVILY_API_KEY: decrypt failed",
		"the unreadable value must be visible to operators")
}

func TestSkillEnvVarsScanAcceptsNilAndEmpty(t *testing.T) {
	var fromNil SkillEnvVars
	require.NoError(t, fromNil.Scan(nil))
	require.Empty(t, fromNil)

	var fromEmpty SkillEnvVars
	require.NoError(t, fromEmpty.Scan([]byte("")))
	require.Empty(t, fromEmpty)
}

func TestSkillEnvVarsGet(t *testing.T) {
	vars := SkillEnvVars{
		{Name: "A", Value: "a"},
		{Name: "B", Required: true},
	}

	got, ok := vars.Get("B")
	require.True(t, ok)
	require.True(t, got.Required)

	_, ok = vars.Get("MISSING")
	require.False(t, ok)
}

// The declaration type must be unserialisable in its own right: today every
// handler projects through a DTO that reports only is_set, and this is what
// makes a future forgotten DTO drop the credential instead of leaking it.
func TestSkillEnvVarsNeverSerialiseTheirValues(t *testing.T) {
	vars := SkillEnvVars{{Name: "TAVILY_API_KEY", Required: true, Value: "tvly-super-secret"}}

	raw, err := json.Marshal(vars)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "tvly-super-secret",
		"marshalling a declaration must never emit the admin value")
	require.Contains(t, string(raw), "TAVILY_API_KEY")

	entity, err := json.Marshal(&TenantSkillEntity{ID: "skill-1", Name: "web-search", Envs: vars})
	require.NoError(t, err)
	require.NotContains(t, string(entity), "tvly-super-secret",
		"marshalling the skill row must never emit the admin value either")
	require.Contains(t, string(entity), "TAVILY_API_KEY")
}

func TestTenantUserEnvVarNeverSerialisesItsValue(t *testing.T) {
	raw, err := json.Marshal(&TenantUserEnvVar{
		ID: "env-1", TenantID: 7, Name: "TAVILY_API_KEY", Value: "secret",
	})
	require.NoError(t, err)
	require.NotContains(t, string(raw), "secret",
		"the type itself must be unserialisable, so no handler has to remember to strip it")
	require.Contains(t, string(raw), "TAVILY_API_KEY")
}

func TestTenantUserEnvVarAfterFindTreatsUndecryptableValueAsUnset(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", skillEnvAESKey)
	var logs bytes.Buffer
	previousLogOutput := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(previousLogOutput) })
	row := &TenantUserEnvVar{
		Name:  "TAVILY_API_KEY",
		Value: utils.EncPrefix + "not-real-ciphertext",
	}

	require.NoError(t, row.AfterFind(nil),
		"an unreadable secret must not fail the whole row load")
	require.Empty(t, row.Value, "an unreadable value reports as unset, never as ciphertext")
	require.Contains(t, logs.String(), "[crypto] tenant_user_env_vars.TAVILY_API_KEY: decrypt failed",
		"the unreadable value must be visible to operators")
}
