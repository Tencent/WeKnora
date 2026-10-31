package web_search

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Providers in this package truncate a failed response body to 4096 bytes
// before putting it in the error string. Zhipu, Metaso and Bocha are Chinese
// services, so that body is usually UTF-8 Chinese: cutting it on a byte index
// can split a multi-byte rune and leave invalid UTF-8 in the error message,
// which then reaches logs and the API response.
func TestProviderErrorPreviewKeepsValidUTF8(t *testing.T) {
	// Each rune is 3 bytes, so byte index maxErrorPreviewBytes does not land on
	// a rune boundary.
	body := strings.Repeat("错", maxErrorPreviewBytes)

	for _, tc := range []struct {
		name   string
		render func(int, []byte) error
	}{
		{"zhipu", zhipuHTTPError},
		{"metaso", metasoHTTPError},
		{"bocha", bochaHTTPError},
		{"deepseek", deepseekHTTPError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.render(500, []byte(body))
			if err == nil {
				t.Fatal("expected an error for a failing status")
			}
			if !utf8.ValidString(err.Error()) {
				t.Fatalf("error message is not valid UTF-8: %q", err.Error())
			}
		})
	}
}

func TestProviderErrorPreviewIsBounded(t *testing.T) {
	body := []byte(strings.Repeat("错", maxErrorPreviewBytes))

	for _, tc := range []struct {
		name   string
		render func(int, []byte) error
	}{
		{"zhipu", zhipuHTTPError},
		{"metaso", metasoHTTPError},
		{"bocha", bochaHTTPError},
		{"deepseek", deepseekHTTPError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.render(500, body)
			if err == nil {
				t.Fatal("expected an error for a failing status")
			}
			// Prefix is "<Provider> API returned status 500: ".
			if limit := len(err.Error()) - 0; limit > 0 {
				if len(err.Error())-len(tc.name+" API returned status 500: ") > maxErrorPreviewBytes {
					t.Fatalf("%s error carries %d preview bytes, want at most %d",
						tc.name,
						len(err.Error())-len(tc.name+" API returned status 500: "),
						maxErrorPreviewBytes)
				}
			}
		})
	}
}
