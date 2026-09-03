package types

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseResourcePathRejectsMalformedHandles(t *testing.T) {
	valid := strings.Repeat("a", ResourceHandleLength)
	if handle, ok := ParseResourcePath(ResourceScheme + valid); !ok || handle != valid {
		t.Fatalf("ParseResourcePath(valid) = (%q, %v), want (%q, true)", handle, ok, valid)
	}

	for _, value := range []string{
		"",
		"local://7/exports/a.png",
		ResourceScheme + strings.Repeat("a", ResourceHandleLength-1),
		ResourceScheme + strings.Repeat("a", ResourceHandleLength+1),
		ResourceScheme + strings.Repeat("a", ResourceHandleLength-1) + "/",
		// Multi-byte input must not slip through a byte-wise character check.
		ResourceScheme + strings.Repeat("中", ResourceHandleLength),
	} {
		if _, ok := ParseResourcePath(value); ok {
			t.Errorf("ParseResourcePath(%q) accepted a malformed reference", value)
		}
	}
}

func TestScanResourceReferences(t *testing.T) {
	first := ResourceScheme + strings.Repeat("a", ResourceHandleLength)
	second := ResourceScheme + strings.Repeat("b", ResourceHandleLength)

	cases := []struct {
		name string
		text string
		want []string
	}{
		{name: "no references", text: "普通正文，没有引用。", want: nil},
		{
			name: "markdown image and link",
			text: "![图](" + first + ")\n\n[表格](" + second + ")",
			want: []string{first, second},
		},
		{
			name: "repeated reference is reported once",
			text: "![a](" + first + ") ![b](" + first + ")",
			want: []string{first},
		},
		{
			// A longer run of handle characters is a different token; binding
			// its 22-char prefix would attach the wrong file.
			name: "over-long handle is not truncated into a match",
			text: ResourceScheme + strings.Repeat("a", ResourceHandleLength+3),
			want: nil,
		},
		{
			name: "trailing punctuation still delimits a handle",
			text: "见 " + first + "。",
			want: []string{first},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ScanResourceReferences(tc.text); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ScanResourceReferences() = %v, want %v", got, tc.want)
			}
		})
	}
}
