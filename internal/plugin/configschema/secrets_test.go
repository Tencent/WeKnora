package configschema

import (
	"errors"
	"testing"
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
