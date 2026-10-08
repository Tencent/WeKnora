package seafile

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	testRepoID  = "01234567-89ab-cdef-0123-456789abcdef"
	otherRepoID = "fedcba98-7654-3210-fedc-ba9876543210"
	testToken   = "seafile-secret-token-do-not-echo"
)

func TestParseResourceID(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"/", "/"},
		{"////", "/"},
		{"//a///b/", "/a/b"},
		{"/a:b/c:d", "/a:b/c:d"},
		{"/产品/说明.pdf", "/产品/说明.pdf"},
		{"/%2e%2e/a%2Fb", "/%2e%2e/a%2Fb"},
		{"/ spaced /file ", "/ spaced /file "},
	} {
		t.Run(tc.input, func(t *testing.T) {
			repo, p, err := parseResourceID(testRepoID + ":" + tc.input)
			if err != nil || repo != testRepoID || p != tc.want {
				t.Fatalf("parse = (%q, %q, %v), want path %q", repo, p, err, tc.want)
			}
			againRepo, againPath, err := parseResourceID(encodeResourceID(repo, p))
			if err != nil || againRepo != repo || againPath != p {
				t.Fatalf("round trip = (%q, %q, %v)", againRepo, againPath, err)
			}
		})
	}
	for _, id := range []string{
		"", testRepoID, testRepoID + ":", testRepoID + ":relative",
		testRepoID + ":/a/../b", testRepoID + ":/./a", testRepoID + ":/a/..",
		testRepoID + ":/a\\b", testRepoID + ":/a\nb", testRepoID + ":/a\x00b",
		strings.ToUpper(testRepoID) + ":/", "123:/", testRepoID + "0:/",
	} {
		t.Run("invalid "+id, func(t *testing.T) {
			if _, _, err := parseResourceID(id); !errors.Is(err, datasource.ErrInvalidConfig) {
				t.Fatalf("expected ErrInvalidConfig, got %v", err)
			}
		})
	}
}

func TestCollapseRoots(t *testing.T) {
	input := []string{
		testRepoID + ":/ab", testRepoID + ":/a/c", testRepoID + ":/a-b",
		testRepoID + ":/a/", testRepoID + ":/a", testRepoID + ":/z/deep",
	}
	want := []string{"/a", "/a-b", "/ab", "/z/deep"}
	for i := 0; i < len(input); i++ {
		repo, roots, err := collapseRoots(input)
		if err != nil || repo != testRepoID || !slices.Equal(roots, want) {
			t.Fatalf("collapse(%v) = (%q, %v, %v)", input, repo, roots, err)
		}
		input = append(input[1:], input[0])
	}
	_, roots, err := collapseRoots(append(input, testRepoID+":/"))
	if err != nil || !slices.Equal(roots, []string{"/"}) {
		t.Fatalf("root collapse = (%v, %v)", roots, err)
	}
	for _, ids := range [][]string{
		nil,
		{},
		{testRepoID + ":/", otherRepoID + ":/a"},
		{testRepoID + ":/", "invalid"},
		{testRepoID + ":/a", testRepoID + ":/../b"},
	} {
		if _, _, err := collapseRoots(ids); !errors.Is(err, datasource.ErrInvalidConfig) {
			t.Fatalf("collapse(%v): expected ErrInvalidConfig, got %v", ids, err)
		}
	}
}

func TestParseConfig(t *testing.T) {
	for _, settings := range []map[string]any{nil, {}, {"edition": "server"}} {
		cfg, err := parseConfig(&types.DataSourceConfig{
			Credentials: map[string]any{"base_url": "  localhost/seafile///  ", "api_token": "  " + testToken + "  "},
			Settings:    settings,
		})
		if err != nil || cfg.baseURL != "https://localhost/seafile" || cfg.token != testToken {
			t.Fatalf("parseConfig = (%+v, %v)", cfg, err)
		}
	}
	for _, token := range []any{"", " ", "a b", "a\tb", "a\nb", "a\x00b", 123, nil} {
		_, err := parseConfig(&types.DataSourceConfig{Credentials: map[string]any{
			"base_url": "http://localhost", "api_token": token,
		}})
		if !errors.Is(err, datasource.ErrInvalidConfig) {
			t.Fatalf("token %q accepted: %v", token, err)
		}
	}
	for _, base := range []any{
		"", 123, "ftp://localhost", "https://", "http://169.254.169.254",
		"http://user:" + testToken + "@localhost", "http://localhost/?token=" + testToken,
		"http://localhost/#fragment",
	} {
		_, err := parseConfig(&types.DataSourceConfig{Credentials: map[string]any{
			"base_url": base, "api_token": testToken,
		}})
		if !errors.Is(err, datasource.ErrInvalidConfig) {
			t.Fatalf("base_url %q accepted: %v", base, err)
		}
		if strings.Contains(err.Error(), testToken) {
			t.Fatalf("config error leaked the token: %v", err)
		}
	}
	for _, ds := range []*types.DataSourceConfig{nil, {}} {
		if _, err := parseConfig(ds); !errors.Is(err, datasource.ErrInvalidConfig) {
			t.Fatalf("empty config accepted: %v", err)
		}
	}
}

func TestAncestorIDs(t *testing.T) {
	for _, tc := range []struct {
		id   string
		want []string
	}{
		{testRepoID + ":/a/b/c", []string{testRepoID + ":/", testRepoID + ":/a", testRepoID + ":/a/b"}},
		{testRepoID + ":/a:b/c", []string{testRepoID + ":/", testRepoID + ":/a:b"}},
		{testRepoID + ":/", nil},
		{testRepoID + ":/../a", nil},
		{"invalid", nil},
	} {
		if got := ancestorIDs(tc.id); !slices.Equal(got, tc.want) {
			t.Fatalf("ancestorIDs(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}

func TestErrorReason(t *testing.T) {
	for _, code := range []string{
		reasonPermissionDenied, reasonNotFound, reasonFileTooLarge, reasonEmptyFile,
		reasonSourceChanged, reasonInvalidResponse, reasonSSRFBlocked, reasonFetchFailed,
	} {
		if errorReason(code) == "" {
			t.Fatalf("no fallback text for %s", code)
		}
	}
	if errorReason("unknown") != errorReason(reasonFetchFailed) {
		t.Fatal("unknown code should fall back to the generic text")
	}
}

func FuzzResourceID(f *testing.F) {
	for _, id := range []string{testRepoID + ":/", testRepoID + ":/a:b//产品/", testRepoID + ":/../a", ""} {
		f.Add(id)
	}
	f.Fuzz(func(t *testing.T, id string) {
		repo, p, err := parseResourceID(id)
		if err != nil {
			return
		}
		againRepo, againPath, err := parseResourceID(encodeResourceID(repo, p))
		if err != nil || againRepo != repo || againPath != p {
			t.Fatalf("unstable resource id %q", id)
		}
	})
}
