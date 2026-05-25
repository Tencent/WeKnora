package types

import (
	"testing"
)

func TestExpandEnv(t *testing.T) {
	t.Setenv("FOO", "foo-value")
	t.Setenv("BAR_2", "bar-2-value")
	// 注意：t.Setenv 不能设空字符串等价于未设；用 os.Unsetenv 显式清空
	t.Setenv("EMPTY_KEPT_LITERAL", "")

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"single var", "${FOO}", "foo-value"},
		{"embedded", "prefix-${FOO}-suffix", "prefix-foo-value-suffix"},
		{"multi vars", "${FOO}:${BAR_2}", "foo-value:bar-2-value"},
		{"unset stays literal", "${UNSET_VAR_XYZ}", "${UNSET_VAR_XYZ}"},
		{"empty value stays literal", "${EMPTY_KEPT_LITERAL}", "${EMPTY_KEPT_LITERAL}"},
		{"no placeholder", "plain text", "plain text"},
		{"empty string", "", ""},
		{"adjacent vars", "${FOO}${BAR_2}", "foo-valuebar-2-value"},

		// default-value form ${NAME:-default}
		{"default used when unset", "${UNSET_VAR_XYZ:-fallback}", "fallback"},
		{"default used when empty", "${EMPTY_KEPT_LITERAL:-fallback}", "fallback"},
		{"default ignored when set", "${FOO:-fallback}", "foo-value"},
		{"bool-like default", "${UNSET_BOOL:-true}", "true"},
		{"int-like default", "${UNSET_INT:-1024}", "1024"},
		{"empty default", "${UNSET_VAR_XYZ:-}", ""},
		{"default with embedded text", "prefix-${UNSET_VAR_XYZ:-mid}-suffix", "prefix-mid-suffix"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := expandEnv(tc.in)
			if got != tc.want {
				t.Errorf("expandEnv(%q) = %q; want %q", tc.in, got, tc.want)
			}
		})
	}
}
