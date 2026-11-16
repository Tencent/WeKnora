package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeHost(t *testing.T) {
	cases := map[string]string{
		"Accounts.Google.com":   "accounts.google.com",
		"github.com:443":        "github.com",
		"example.com.":          "example.com",
		"[::1]:8080":            "::1",
		"10.0.0.1":              "10.0.0.1",
		" login.example.co.uk ": "login.example.co.uk",
	}
	for in, want := range cases {
		got, err := NormalizeHost(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{
		"", "https://github.com", "*.github.com", "git hub.com", "a/b", "-bad.com",
		"127.1", "10.0.0.01", "0177.0.0.1", "2130706433", "0x7f000001",
	} {
		_, err := NormalizeHost(bad)
		assert.Error(t, err, bad)
	}
	_, err := NormalizeHost("https://user:s3cret@github.com/a")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "s3cret")
}

func TestNormalizeDomainsKeepsSiteFirstAndCaps(t *testing.T) {
	got, err := NormalizeDomains("github.com", []string{"GitHub.com", "api.github.com"})
	require.NoError(t, err)
	assert.Equal(t, []string{"github.com", "api.github.com"}, got)
	_, err = NormalizeDomains("a.com", []string{"b.com", "c.com", "d.com", "e.com", "f.com"})
	assert.Error(t, err, "six hosts exceed the cap")
}

func TestCheckSameSite(t *testing.T) {
	for site, domains := range map[string][]string{
		"accounts.google.com": {"accounts.google.com", "google.com", "mail.google.com"},
		"login.example.co.uk": {"example.co.uk", "sso.example.co.uk"},
		"me.github.io":        {"me.github.io", "api.me.github.io"},
		"10.0.0.1":            {"10.0.0.1"},
		"intranet":            {"intranet"},
	} {
		assert.NoError(t, CheckSameSite(site, domains), site)
	}
	for _, c := range [][2]string{
		{"github.com", "evil.com"},
		{"github.com", "github.com.evil.com"},
		{"login.example.co.uk", "evil.co.uk"},
		{"me.github.io", "attacker.github.io"},
		{"10.0.0.1", "10.0.0.2"},
		{"intranet", "other"},
	} {
		assert.Error(t, CheckSameSite(c[0], []string{c[0], c[1]}), "%s + %s", c[0], c[1])
	}
	r := SecureFormPromptRequest{Title: "t", Kind: "login", Site: "github.com", Domains: []string{"evil.com"}}
	assert.Error(t, r.Validate(), "a form for one site cannot name another")
}

func TestIntersectDomains(t *testing.T) {
	assert.Equal(t, []string{"github.com"},
		IntersectDomains([]string{"github.com", "x.com"}, []string{"evil.com", "github.com"}))
	assert.Empty(t, IntersectDomains([]string{"a.com"}, []string{"b.com"}))
}

func TestSecureFormValidateDefaults(t *testing.T) {
	r := SecureFormPromptRequest{Title: " 登录 GitHub ", Kind: "login", Site: "GitHub.com"}
	require.NoError(t, r.Validate())
	assert.Equal(t, "登录 GitHub", r.Title)
	assert.Equal(t, "github.com", r.Site)
	assert.Equal(t, []string{"github.com"}, r.Domains)
	assert.Equal(t, []string{"username", "password"}, r.Parts)
	assert.True(t, r.AllowsSave())
	assert.Equal(t, []string{"username", "password"}, r.FieldNames())
	typ, ok := r.FieldType("password")
	assert.True(t, ok)
	assert.Equal(t, SecretFieldPassword, typ)
}

func TestSecureFormValidateExtraOnly(t *testing.T) {
	r := SecureFormPromptRequest{Title: "验证码", Site: "bank.example.com", Extra: []SecureFormExtraField{
		{Name: "otp", Label: "短信验证码", Type: SecretFieldOneTimeCode, Required: true},
	}}
	require.NoError(t, r.Validate())
	assert.False(t, r.AllowsSave(), "only a login with a password can be saved")
	assert.Equal(t, []string{"otp"}, r.FieldNames())
	assert.True(t, r.FieldRequired("otp"))
}

func TestSecureFormValidateRejects(t *testing.T) {
	no := false
	cases := []SecureFormPromptRequest{
		{Kind: "login", Site: "a.com"},
		{Title: "t", Kind: "card", Site: "a.com"},
		{Title: "t", Kind: "login"},
		{Title: "t", Kind: "login", Site: "a.com", Parts: []string{"pin"}},
		{Title: "t", Kind: "login", Site: "a.com", Parts: []string{"password", "password"}},
		{Title: "t", Site: "a.com"},
		{
			Title: "t", Site: "a.com", Parts: []string{"password"},
			Extra: []SecureFormExtraField{{Name: "otp", Type: "text"}},
		},
		{Title: "t", Kind: "login", Site: "a.com", Extra: []SecureFormExtraField{{Name: "password", Type: "text"}}},
		{Title: "t", Kind: "login", Site: "a.com", Extra: []SecureFormExtraField{{Name: "Bad Name", Type: "text"}}},
		{Title: "t", Kind: "login", Site: "a.com", Extra: []SecureFormExtraField{{Name: "x", Type: "select"}}},
		{
			Title: "t", Kind: "login", Site: "a.com", Parts: []string{"password"},
			Prefill: &SecureFormPrefill{Username: "u"},
		},
	}
	for i, c := range cases {
		c := c
		assert.Error(t, c.Validate(), "case %d", i)
	}
	ok := SecureFormPromptRequest{Title: "t", Kind: "login", Site: "a.com", AllowSave: &no}
	require.NoError(t, ok.Validate())
	assert.False(t, ok.AllowsSave())
}

func TestValidateSecureFormResponse(t *testing.T) {
	req, _ := json.Marshal(SecureFormPromptRequest{
		Title: "t", Kind: "login", Site: "a.com",
		Domains: []string{"a.com"}, Parts: []string{"username", "password"},
	})
	assert.NoError(t, ValidateSecureFormResponse(req, json.RawMessage(
		`{"handle":"cred_3","fields":["username","password"],"username":"alice","saved":true}`)))
	assert.NoError(t, ValidateSecureFormResponse(req, json.RawMessage(`{"declined":true}`)))
	assert.Error(t, ValidateSecureFormResponse(req, json.RawMessage(
		`{"handle":"cred_1","fields":["password"],"values":{"password":"hunter2"}}`)), "raw values are refused")
	assert.Error(t, ValidateSecureFormResponse(req, json.RawMessage(`{"handle":"x","fields":["password"]}`)))
	assert.Error(t, ValidateSecureFormResponse(req, json.RawMessage(`{"handle":"cred_1","fields":["pin"]}`)))
	assert.Error(t, ValidateSecureFormResponse(req, json.RawMessage(`{"declined":true,"handle":"cred_1"}`)))
	err := ValidateSecureFormResponse(req, json.RawMessage(
		`{"handle":"cred_1","fields":["password"],"secret":"hunter2"}`))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "hunter2")

	for _, trailing := range []string{
		`{"handle":"cred_1","fields":["password"]}{"password":"hunter2"}`,
		`{"declined":true}{"password":"hunter2"}`,
	} {
		err := ValidateSecureFormResponse(req, json.RawMessage(trailing))
		require.Error(t, err, trailing)
		assert.NotContains(t, err.Error(), "hunter2", trailing)
	}

	err = ValidateSecureFormResponse(req, json.RawMessage(`{"handle":"cred_1","fields":["hunter2"]}`))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "hunter2")
}

func TestValidSecretHandle(t *testing.T) {
	assert.True(t, ValidSecretHandle("cred_1"))
	assert.True(t, ValidSecretHandle("cred_1234"))
	for _, bad := range []string{"cred_0", "cred_", "cred_1:x", "CRED_1", "cred_01", ""} {
		assert.False(t, ValidSecretHandle(bad), bad)
	}
}
