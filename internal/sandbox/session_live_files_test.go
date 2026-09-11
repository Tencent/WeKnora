package sandbox

import (
	"errors"
	"strings"
	"testing"
)

func TestCleanSessionLivePath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		path      string
		allowRoot bool
		want      string
		wantErr   error
	}{
		{name: "root", allowRoot: true},
		{name: "file", path: "report.txt", want: "report.txt"},
		{name: "nested", path: "charts/final.png", want: "charts/final.png"},
		{name: "unicode", path: "results/结果.txt", want: "results/结果.txt"},
		{name: "root not allowed", wantErr: ErrLiveFileInvalidPath},
		{name: "absolute", path: "/etc/passwd", wantErr: ErrLiveFileInvalidPath},
		{name: "backslash", path: `charts\final.png`, wantErr: ErrLiveFileInvalidPath},
		{name: "traversal", path: "../secret", wantErr: ErrLiveFileInvalidPath},
		{name: "nested traversal", path: "charts/../../secret", wantErr: ErrLiveFileInvalidPath},
		{name: "dot component", path: "charts/./final.png", wantErr: ErrLiveFileInvalidPath},
		{name: "empty component", path: "charts//final.png", wantErr: ErrLiveFileInvalidPath},
		{name: "trailing slash", path: "charts/", wantErr: ErrLiveFileInvalidPath},
		{name: "blank component", path: "charts/   /final.png", wantErr: ErrLiveFileInvalidPath},
		{name: "control", path: "charts/\nfinal.png", wantErr: ErrLiveFileInvalidPath},
		{name: "nul", path: "charts/\x00final.png", wantErr: ErrLiveFileInvalidPath},
		{name: "invalid utf8", path: string([]byte{0xff}), wantErr: ErrLiveFileInvalidPath},
		{name: "too long", path: strings.Repeat("a", maxSessionLivePathBytes+1), wantErr: ErrLiveFileInvalidPath},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := cleanSessionLivePath(test.path, test.allowRoot)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("cleanSessionLivePath(%q): error=%v want=%v", test.path, err, test.wantErr)
			}
			if got != test.want {
				t.Fatalf("cleanSessionLivePath(%q)=%q want=%q", test.path, got, test.want)
			}
		})
	}
}

func TestDecodeLiveFileResponseUsesFinalJSONLine(t *testing.T) {
	t.Parallel()

	response, err := decodeLiveFileResponse("shell profile noise\n{\"ok\":true,\"data\":\"YQ==\"}\n")
	if err != nil {
		t.Fatalf("decodeLiveFileResponse: %v", err)
	}
	if !response.OK || response.Data != "YQ==" {
		t.Fatalf("unexpected response: %#v", response)
	}
}

func TestLiveFileResponseErrorPreservesSentinel(t *testing.T) {
	t.Parallel()

	for code, want := range map[string]error{
		"not_found": ErrLiveFileNotFound,
		"conflict":  ErrLiveFileConflict,
		"unsafe":    ErrLiveFileUnsafe,
		"too_large": ErrLiveFileTooLarge,
		"invalid":   ErrLiveFileInvalidPath,
	} {
		if err := liveFileResponseError(code, "safe detail"); !errors.Is(err, want) {
			t.Fatalf("code %q: error=%v want sentinel %v", code, err, want)
		}
	}
}
