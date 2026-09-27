package configschema

import (
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/utils"
)

func TestUpdateKeepsRedactedSecretsAndValidates(t *testing.T) {
	s := Object().
		Set("api_key", &Schema{Type: TypeString, Secret: true, MinLength: intPtr(4)}, true).
		Set("region", &Schema{Type: TypeString}, false)
	stored, err := Update(s, nil, map[string]any{"api_key": "secret-1", "region": "eu"})
	if err != nil {
		t.Fatal(err)
	}
	next, err := Update(s, stored, map[string]any{"api_key": RedactedPlaceholder, "region": "us"})
	if err != nil {
		t.Fatalf("a redacted secret must count as set: %v", err)
	}
	opened, err := Open(s, next)
	if err != nil || opened["api_key"] != "secret-1" || opened["region"] != "us" {
		t.Fatalf("opened = %v, %v", opened, err)
	}
	var fe FieldErrors
	if _, err := Update(s, nil, map[string]any{"region": "eu"}); !errors.As(err, &fe) {
		t.Fatalf("a missing required secret must fail validation, got %v", err)
	}
}

// A ciphertext copied from elsewhere is refused, in any field: stored
// configuration would open it for the plugin.
func TestUpdateRefusesSealedValues(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	s := Object().
		Set("api_key", &Schema{Type: TypeString, Secret: true}, false).
		Set("note", &Schema{Type: TypeString}, false)
	other, err := utils.EncryptAESGCM("someone else's password", utils.GetAESKey())
	if err != nil {
		t.Fatal(err)
	}
	for _, values := range []map[string]any{
		{"api_key": other},
		{"note": other},
		{"extra": map[string]any{"deep": []any{other}}},
	} {
		var fe FieldErrors
		if _, err := Update(s, nil, values); !errors.As(err, &fe) || fe[0].Code != CodeSealed {
			t.Errorf("%v: %v", values, err)
		}
	}
}

// A field that was a secret when stored and is a plain field now reaches
// the plugin decrypted and the UI redacted.
func TestFormerSecretsOpenAndRedact(t *testing.T) {
	t.Setenv("SYSTEM_AES_KEY", strings.Repeat("k", 32))
	before := Object().Set("token", &Schema{Type: TypeString, Secret: true}, false)
	stored, err := Update(before, nil, map[string]any{"token": "t-1"})
	if err != nil {
		t.Fatal(err)
	}
	after := Object().Set("token", &Schema{Type: TypeString}, false)
	opened, err := Open(after, stored)
	if err != nil || opened["token"] != "t-1" {
		t.Fatalf("opened = %v, %v", opened, err)
	}
	if got := Redact(after, stored)["token"]; got != RedactedPlaceholder {
		t.Fatalf("redacted = %v", got)
	}
}
