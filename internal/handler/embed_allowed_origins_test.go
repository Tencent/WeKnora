package handler

import "testing"

func TestValidateEmbedAllowedOrigins(t *testing.T) {
	t.Setenv("GIN_MODE", "release")
	for _, tc := range []struct {
		origins []string
		valid   bool
	}{
		{nil, false},
		{[]string{" "}, false},
		{[]string{"*"}, false},
		{[]string{"https://a.example"}, true},
		{[]string{"https://a.example/"}, true},
		{[]string{"*.example.com:8443"}, true},
		{[]string{"https://a.example/path"}, false},
		{[]string{"https://a.example?"}, false},
		{[]string{"https://user@a.example"}, false},
		{[]string{"https://a.example; frame-ancestors *"}, false},
	} {
		if err := validateAllowedOrigins(tc.origins); (err == nil) != tc.valid {
			t.Errorf("%v: %v", tc.origins, err)
		}
	}
	t.Setenv("GIN_MODE", "debug")
	if err := validateAllowedOrigins([]string{"*"}); err != nil {
		t.Fatal(err)
	}
}
