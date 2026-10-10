package headertemplate

import (
	"net/http"
	"strings"
	"testing"
)

func TestResolveArbitraryRequestHeaderAndCandidates(t *testing.T) {
	headers := http.Header{"X-Aaa": {"department-A"}, "X-Unused": {"ignored"}}
	ctx := NewContext(map[string]string{"external.user_id": "employee-7", "principal.type": "api_external_user"}, headers)
	headers.Set("X-AAA", "changed-after-capture")
	for _, tc := range []struct{ value, want string }{
		{"{{request.headers.X-AAA}}", "department-A"},
		{"{{request.headers.x-aaa}}", "department-A"},
		{"{{ user.email ?? external.user_id ?? im.user_id }}", "employee-7"},
		{"prefix-{{request.headers.X-AAA}}-{{external.user_id}}", "prefix-department-A-employee-7"},
		{`\{{literal}}`, "{{literal}}"},
		{"static", "static"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			template, err := Parse(tc.value)
			if err != nil {
				t.Fatal(err)
			}
			got, err := template.Resolve(ctx)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

func TestMissingDuplicateAndUnsafeValuesFailWithoutLeakingValues(t *testing.T) {
	for _, header := range []http.Header{
		{},
		{"X-Aaa": {""}},
		{"X-Aaa": {"secret-one", "secret-two"}},
		{"X-Aaa": {"secret\r\nInjected: true"}},
	} {
		template, err := Parse("{{request.headers.X-AAA ?? user.email}}")
		if err != nil {
			t.Fatal(err)
		}
		_, err = template.Resolve(NewContext(nil, header))
		if err == nil {
			t.Fatal("missing, duplicate or unsafe value was accepted")
		}
		if strings.Contains(err.Error(), "secret") {
			t.Fatalf("error disclosed value: %v", err)
		}
	}
}

func TestTemplateRejectsUnknownMalformedAndProtectedReferences(t *testing.T) {
	for _, value := range []string{
		"{{user.department}}", "{{}}", "{{user.email", "{{user.email ?? }}",
		"{{request.headers.Authorization}}", "{{request.headers.cookie}}",
		"{{request.headers.X-API-Key}}", "{{request.headers.X-External-User-ID}}",
		"{{request.headers.X-External-User-Token}}", "{{request.headers.X-Tenant-ID}}",
		"{{request.headers.Proxy-Credential}}", "{{request.headers.X-Forwarded-For}}",
		"{{request.headers.X-User-ID}}", "{{request.headers.X-Workspace-ID}}", "{{request.headers.X-Auth-Token}}",
		"{{request.headers.X Bad}}", "{{request.headers.}}", "{{user.email()}}",
	} {
		if _, err := Parse(value); err == nil {
			t.Errorf("accepted %q", value)
		}
	}
}

func TestSubstitutionsAreNotEvaluatedAgain(t *testing.T) {
	template, _ := Parse("{{request.headers.X-AAA}}")
	got, err := template.Resolve(NewContext(nil, http.Header{"X-Aaa": {"{{user.email}}"}}))
	if err != nil || got != "{{user.email}}" {
		t.Fatalf("recursive evaluation: %q, %v", got, err)
	}
}

func TestResolveOptionalOmitsWholeHeaderWhenAnyExpressionIsMissing(t *testing.T) {
	for _, value := range []string{
		"{{request.headers.X-AAA}}",
		"prefix-{{request.headers.X-AAA}}-suffix",
		"{{request.headers.X-AAA ?? im.user_id}}",
		"{{tenant.id}}-{{request.headers.X-AAA}}",
	} {
		template, err := Parse(value)
		if err != nil {
			t.Fatal(err)
		}
		got, present, err := template.ResolveOptional(NewContext(map[string]string{"tenant.id": "7"}, nil))
		if err != nil || present || got != "" {
			t.Fatalf("%q: got %q, present %t, err %v", value, got, present, err)
		}
	}
}

func TestResolveOptionalUsesFallbackAndStillRejectsInvalidValues(t *testing.T) {
	template, err := Parse("dept:{{request.headers.X-AAA ?? user.email}}")
	if err != nil {
		t.Fatal(err)
	}
	got, present, err := template.ResolveOptional(NewContext(map[string]string{"user.email": "alice@example.com"}, nil))
	if err != nil || !present || got != "dept:alice@example.com" {
		t.Fatalf("fallback: got %q, present %t, err %v", got, present, err)
	}
	_, _, err = template.ResolveOptional(NewContext(nil, http.Header{"X-Aaa": {"one", "two"}}))
	if err == nil {
		t.Fatal("duplicate request header values were accepted")
	}
	_, _, err = template.ResolveOptional(NewContext(nil, http.Header{"X-Aaa": {"bad\r\nvalue"}}))
	if err == nil {
		t.Fatal("unsafe request header value was accepted")
	}
}

func TestResolveOptionalDoesNotHideUnsafeLaterExpressionBehindMissingValue(t *testing.T) {
	template, err := Parse("{{request.headers.X-Missing}}/{{request.headers.X-Unsafe}}")
	if err != nil {
		t.Fatal(err)
	}
	for _, headers := range []http.Header{
		{"X-Unsafe": {"one", "two"}},
		{"X-Unsafe": {"bad\r\nvalue"}},
	} {
		_, _, err := template.ResolveOptional(NewContext(nil, headers))
		if err == nil {
			t.Fatal("unsafe later expression was hidden by a missing earlier one")
		}
	}
}
