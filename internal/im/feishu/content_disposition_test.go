package feishu

import "testing"

func TestContentDispositionFilename(t *testing.T) {
	for _, tc := range []struct {
		name string
		cd   string
		want string
	}{
		{
			name: "quoted with extra parameter",
			cd:   `attachment; filename="Q4 Final.pdf"; size=123`,
			want: "Q4 Final.pdf",
		},
		{
			name: "RFC 5987",
			cd:   `attachment; filename*=UTF-8''%E5%AD%A3%E5%BA%A6%E6%8A%A5%E5%91%8A.pdf`,
			want: "季度报告.pdf",
		},
		{name: "unquoted", cd: `attachment; filename=report.md`, want: "report.md"},
		{name: "malformed trailing params", cd: `attachment; filename=report.md; size=123`, want: "report.md"},
		{name: "missing", cd: `attachment`, want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := contentDispositionFilename(tc.cd); got != tc.want {
				t.Fatalf("contentDispositionFilename(%q) = %q, want %q", tc.cd, got, tc.want)
			}
		})
	}
}
