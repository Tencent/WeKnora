package manifest

import (
	"strings"
	"testing"
)

func TestExpandTemplate(t *testing.T) {
	values := map[string]string{"config.api_key": "k-1", "system.region": "eu"}
	lookup := func(scope, key string) (string, bool) { v, ok := values[scope+"."+key]; return v, ok }

	got, err := ExpandTemplate("Bearer ${config.api_key} @${system.region}", lookup)
	if err != nil || got != "Bearer k-1 @eu" {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := ExpandTemplate("Bearer ${config.token}", lookup); err == nil ||
		!strings.Contains(err.Error(), "config.token") {
		t.Fatalf("a missing value must fail, got %v", err)
	}
}

func TestValidateTemplate(t *testing.T) {
	var errs []string
	add := func(f string, _ ...any) { errs = append(errs, f) }
	validateTemplate("Bearer ${config.api_key}", "h", add)
	if len(errs) != 0 {
		t.Fatalf("valid template rejected: %v", errs)
	}
	validateTemplate("${env.HOME}", "h", add)
	validateTemplate("${config.api-key}", "h", add)
	if len(errs) != 2 {
		t.Fatalf("want 2 errors, got %v", errs)
	}
}
