package configschema

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/utils"
)

const testAESKey = "0123456789abcdef0123456789abcdef"

func intPtr(v int) *int           { return &v }
func floatPtr(v float64) *float64 { return &v }

// jiraSchema is a connector-shaped schema: a URL, an auth mode, a token that
// only applies to one mode, and a nested object holding a secret.
func jiraSchema() *Schema {
	auth := Object().
		Set("user", &Schema{Type: TypeString}, false).
		Set("password", &Schema{Type: TypeString, Secret: true}, false)
	return Object().
		Set("base_url", &Schema{Type: TypeString, Format: "uri", Order: 1}, true).
		Set("mode", &Schema{Type: TypeString, OneOf: []*Schema{
			{Const: "token", Title: "API token"}, {Const: "basic", Title: "Basic"},
		}, Order: 2}, true).
		Set("token", &Schema{
			Type: TypeString, Secret: true, MinLength: intPtr(8),
			VisibleIf: map[string]any{"mode": "token"}, Order: 3,
		}, true).
		Set("page_size", &Schema{Type: TypeInteger, Minimum: floatPtr(1), Maximum: floatPtr(100)}, false).
		Set("auth", auth, false)
}

func TestCheckRejectsUnsupportedSchemas(t *testing.T) {
	bad := []struct {
		name   string
		schema *Schema
		want   string
	}{
		{"root not object", &Schema{Type: TypeString}, "root must be an object"},
		{"unknown type", Object().Set("x", &Schema{Type: "date"}, false), `unsupported type "date"`},
		{"secret number", Object().Set("x", &Schema{Type: TypeNumber, Secret: true}, false), "only allowed on strings"},
		{"secret in array", Object().Set("x", &Schema{
			Type: TypeArray, Items: &Schema{Type: TypeString, Secret: true},
		}, false), "secrets inside arrays"},
		{"required ghost", &Schema{Type: TypeObject, Required: []string{"ghost"}}, `"ghost" has no property`},
		{"bad pattern", Object().Set("x", &Schema{Type: TypeString, Pattern: "("}, false), "invalid pattern"},
		{"oneOf without const", Object().Set("x", &Schema{
			Type: TypeString, OneOf: []*Schema{{Title: "A"}},
		}, false), "must carry a const"},
	}
	for _, c := range bad {
		err := c.schema.Check()
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want error containing %q", c.name, err, c.want)
		}
	}
	if err := jiraSchema().Check(); err != nil {
		t.Fatalf("valid schema rejected: %v", err)
	}
}

func TestParseRoundTrip(t *testing.T) {
	data, err := json.Marshal(jiraSchema())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"x-secret":true`, `"x-visible-if":{"mode":"token"}`, `"oneOf"`} {
		if !strings.Contains(string(data), key) {
			t.Fatalf("marshalled schema lacks %s: %s", key, data)
		}
	}
	back, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := back.SecretPaths(); strings.Join(got, ",") != "auth.password,token" {
		t.Fatalf("SecretPaths = %v", got)
	}
	if got := back.OrderedKeys(); strings.Join(got, ",") != "auth,page_size,base_url,mode,token" {
		t.Fatalf("OrderedKeys = %v", got)
	}
}

func TestValidate(t *testing.T) {
	s := jiraSchema()
	ok := map[string]any{
		"base_url": "https://acme.atlassian.net", "mode": "token", "token": "abcdefgh", "page_size": 50.0,
	}
	if errs := Validate(s, ok); errs != nil {
		t.Fatalf("valid config rejected: %v", errs)
	}

	// token is required only while mode is token.
	basic := map[string]any{"base_url": "https://acme.atlassian.net", "mode": "basic"}
	if errs := Validate(s, basic); errs != nil {
		t.Fatalf("hidden required field must not be required: %v", errs)
	}

	bad := map[string]any{
		"base_url":  "not a url",
		"mode":      "oauth",
		"token":     "short",
		"page_size": 1.5,
		"auth":      map[string]any{"user": 42},
	}
	errs := Validate(s, bad)
	got := map[string]string{}
	for _, e := range errs {
		got[e.Path] = e.Code
	}
	want := map[string]string{
		"base_url": CodeFormat, "mode": CodeEnum, "page_size": CodeType, "auth.user": CodeType,
	}
	for path, code := range want {
		if got[path] != code {
			t.Errorf("%s: code %q, want %q (all: %v)", path, got[path], code, errs)
		}
	}
	// mode is invalid, so token is hidden and not checked.
	if _, checked := got["token"]; checked {
		t.Errorf("token should be skipped while mode is not token: %v", errs)
	}

	missing := Validate(s, map[string]any{"mode": "token", "token": ""})
	if len(missing) != 2 || missing[0].Code != CodeRequired || missing[1].Code != CodeRequired {
		t.Fatalf("want base_url and token required, got %v", missing)
	}
	if !strings.Contains(missing.Error(), "base_url: is required") {
		t.Fatalf("error text = %q", missing.Error())
	}

	// A redacted secret counts as present and skips its own checks.
	if errs := Validate(s, map[string]any{
		"base_url": "https://acme.atlassian.net", "mode": "token", "token": RedactedPlaceholder,
	}); errs != nil {
		t.Fatalf("redacted secret rejected: %v", errs)
	}

	if errs := Validate(s, map[string]any{
		"base_url": "https://x.io", "mode": "basic", "page_size": 500.0,
	}); len(errs) != 1 || errs[0].Code != CodeMaximum {
		t.Fatalf("want maximum error, got %v", errs)
	}
}

func TestSealOpenRedactMerge(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", testAESKey)
	s := jiraSchema()
	plain := map[string]any{
		"base_url": "https://acme.atlassian.net",
		"mode":     "token",
		"token":    "secret-token",
		"auth":     map[string]any{"user": "bob", "password": "hunter2"},
	}

	sealed, err := Seal(s, plain)
	if err != nil {
		t.Fatal(err)
	}
	if plain["token"] != "secret-token" {
		t.Fatal("Seal must not mutate its input")
	}
	token, _ := sealed["token"].(string)
	password, _ := sealed["auth"].(map[string]any)["password"].(string)
	if !strings.HasPrefix(token, utils.EncPrefix) || !strings.HasPrefix(password, utils.EncPrefix) {
		t.Fatalf("secrets not encrypted: %v", sealed)
	}
	if sealed["base_url"] != plain["base_url"] || sealed["auth"].(map[string]any)["user"] != "bob" {
		t.Fatalf("non-secrets must pass through: %v", sealed)
	}
	// Sealing twice keeps the ciphertext.
	again, err := Seal(s, sealed)
	if err != nil || again["token"] != token {
		t.Fatalf("Seal is not idempotent: %v, %v", again["token"], err)
	}
	// Values sealed here read back through the helper every other table uses.
	if got, err := utils.DecryptStoredSecret(token); err != nil || got != "secret-token" {
		t.Fatalf("DecryptStoredSecret = %q, %v", got, err)
	}

	opened, err := Open(s, sealed)
	if err != nil {
		t.Fatal(err)
	}
	if opened["token"] != "secret-token" || opened["auth"].(map[string]any)["password"] != "hunter2" {
		t.Fatalf("Open = %v", opened)
	}

	redacted := Redact(s, sealed)
	if redacted["token"] != RedactedPlaceholder ||
		redacted["auth"].(map[string]any)["password"] != RedactedPlaceholder {
		t.Fatalf("Redact = %v", redacted)
	}
	if Redact(s, map[string]any{"token": ""})["token"] != "" {
		t.Fatal("an unset secret must stay empty so the UI can tell it apart")
	}

	// The client sends the redacted form back with one field changed.
	incoming := Redact(s, sealed)
	incoming["base_url"] = "https://new.atlassian.net"
	delete(incoming["auth"].(map[string]any), "password")
	merged := Merge(s, sealed, incoming)
	if merged["token"] != token || merged["auth"].(map[string]any)["password"] != password {
		t.Fatalf("Merge must keep stored secrets: %v", merged)
	}
	if merged["base_url"] != "https://new.atlassian.net" {
		t.Fatalf("Merge must apply changed fields: %v", merged)
	}

	replaced := Merge(s, sealed, map[string]any{"token": "new-token"})
	if replaced["token"] != "new-token" {
		t.Fatalf("a new secret must replace the stored one: %v", replaced)
	}
	if replaced["auth"].(map[string]any)["password"] != password {
		t.Fatalf("a nested secret the update leaves out must survive: %v", replaced)
	}
}

func TestOpenWithoutKey(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", testAESKey)
	s := jiraSchema()
	sealed, err := Seal(s, map[string]any{"token": "secret-token", "mode": "token"})
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("SYSTEM_AES_KEY", "")
	if _, err := Open(s, sealed); err == nil || !strings.Contains(err.Error(), "decrypt token") {
		t.Fatalf("Open without key must fail loudly, got %v", err)
	}
	lenient, failed := OpenLenient(s, sealed)
	if lenient["token"] != "" || len(failed) != 1 || failed[0] != "token" {
		t.Fatalf("OpenLenient = %v, failed %v", lenient, failed)
	}
	if lenient["mode"] != "token" {
		t.Fatal("OpenLenient must keep the rest of the row")
	}

	// Legacy plaintext rows still open.
	legacy, err := Open(s, map[string]any{"token": "plain-legacy"})
	if err != nil || legacy["token"] != "plain-legacy" {
		t.Fatalf("legacy plaintext = %v, %v", legacy, err)
	}
}

func TestValidateInContextReadsDollarKeysFromContext(t *testing.T) {
	s := Object().
		Set("bot_token", &Schema{Type: TypeString}, true).
		Set("signing_secret", &Schema{Type: TypeString, VisibleIf: map[string]any{"$mode": "webhook"}}, true)
	value := map[string]any{"bot_token": "x"}
	if errs := ValidateInContext(s, value, map[string]any{"mode": "websocket"}); errs != nil {
		t.Fatalf("websocket mode hides the webhook secret: %v", errs)
	}
	errs := ValidateInContext(s, value, map[string]any{"mode": "webhook"})
	if len(errs) != 1 || errs[0].Path != "signing_secret" {
		t.Fatalf("webhook mode requires the signing secret, got %v", errs)
	}
	// Without a context the field is hidden, never wrongly required.
	if errs := Validate(s, value); errs != nil {
		t.Fatalf("Validate without context: %v", errs)
	}
}
