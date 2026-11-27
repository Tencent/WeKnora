package cmdutil

import (
	"strings"
	"testing"
)

// A scheme-less value is the common typo ("kb.example.com"). The rejection has
// to name the value that was rejected: the parsed scheme is empty for exactly
// this input, so reporting it produced `got ""` and read as "you passed
// nothing".
func TestValidateHTTPURLNamesTheRejectedValue(t *testing.T) {
	for _, tc := range []struct {
		name      string
		value     string
		wantInMsg string
	}{
		{"missing scheme", "kb.example.com", `"kb.example.com"`},
		{"wrong scheme", "ftp://kb.example.com", `"ftp://kb.example.com"`},
		{"missing host", "https://", `"https://"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateHTTPURL("--host", tc.value)
			if err == nil {
				t.Fatalf("ValidateHTTPURL(%q) = nil, want input.invalid_argument", tc.value)
			}
			typed := AsError(err)
			if typed == nil {
				t.Fatalf("ValidateHTTPURL(%q) = %v, want a *Error", tc.value, err)
			}
			if typed.Code != CodeInputInvalidArgument {
				t.Errorf("code = %s, want %s", typed.Code, CodeInputInvalidArgument)
			}
			if !strings.Contains(typed.Message, tc.wantInMsg) {
				t.Errorf("message = %q, want it to contain %s", typed.Message, tc.wantInMsg)
			}
			if strings.Contains(typed.Message, `got ""`) {
				t.Errorf("message = %q, must not report the parsed scheme instead of the input", typed.Message)
			}
		})
	}
}

func TestValidateHTTPURLAcceptsHTTPAndHTTPS(t *testing.T) {
	for _, value := range []string{"http://kb.example.com", "https://kb.example.com:8080/base"} {
		if err := ValidateHTTPURL("--host", value); err != nil {
			t.Errorf("ValidateHTTPURL(%q) = %v, want nil", value, err)
		}
	}
}

func TestNormalizeHostTrimsTrailingSlashes(t *testing.T) {
	got, err := NormalizeHost("  https://kb.example.com///  ")
	if err != nil {
		t.Fatalf("NormalizeHost() error = %v", err)
	}
	if want := "https://kb.example.com"; got != want {
		t.Errorf("NormalizeHost() = %q, want %q", got, want)
	}
}

func TestNormalizeHostRejectsEmpty(t *testing.T) {
	_, err := NormalizeHost("   ")
	if err == nil {
		t.Fatal("NormalizeHost(\"   \") = nil, want an error")
	}
	if typed := AsError(err); typed == nil || typed.Code != CodeInputInvalidArgument {
		t.Errorf("NormalizeHost(\"   \") = %v, want code %s", err, CodeInputInvalidArgument)
	}
}

// Trimming trailing slashes before validation rewrote "https://" into "https:",
// so the rejection quoted a string the user never typed.
func TestNormalizeHostQuotesTheValueAsTyped(t *testing.T) {
	_, err := NormalizeHost("https://")
	if err == nil {
		t.Fatal(`NormalizeHost("https://") = nil, want an error`)
	}
	typed := AsError(err)
	if typed == nil {
		t.Fatalf(`NormalizeHost("https://") = %v, want a *Error`, err)
	}
	if !strings.Contains(typed.Message, `"https://"`) {
		t.Errorf("message = %q, want it to quote the value as typed", typed.Message)
	}
}
