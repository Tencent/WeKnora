package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/stretchr/testify/require"
)

func TestParseSkillSource(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want parsedSkillSource
	}{
		{
			name: "clawhub at-slug",
			in:   "@lyingbug/weknora",
			want: parsedSkillSource{
				Kind: skillSourceRegistry, Registry: defaultSkillRegistryOrigin,
				Slug: "lyingbug/weknora",
			},
		},
		{
			name: "clawhub page url",
			in:   "https://clawhub.ai/lyingbug/weknora",
			want: parsedSkillSource{
				Kind: skillSourceRegistry, Registry: "https://clawhub.ai",
				Slug: "lyingbug/weknora",
			},
		},
		{
			name: "clawhub canonical skills path",
			in:   "https://clawhub.ai/steipete/skills/github",
			want: parsedSkillSource{
				Kind: skillSourceRegistry, Registry: "https://clawhub.ai",
				Slug: "steipete/github",
			},
		},
		{
			name: "skillhub team slug",
			in:   "my-team--email-sender",
			want: parsedSkillSource{
				Kind: skillSourceRegistry, Registry: defaultSkillRegistryOrigin,
				Slug: "my-team--email-sender",
			},
		},
		{
			name: "skillhub page with version",
			in:   "https://skillhub.example.com/my-skill@1.2.0",
			want: parsedSkillSource{
				Kind: skillSourceRegistry, Registry: "https://skillhub.example.com",
				Slug: "my-skill", Version: "1.2.0",
			},
		},
		{
			name: "skillhub.cn publisher page",
			in:   "https://skillhub.cn/skills/clawhub_pskoett/self-improving-agent",
			want: parsedSkillSource{
				Kind: skillSourceRegistry, Registry: skillHubCNAPIOrigin,
				Slug: "self-improving-agent",
			},
		},
		{
			name: "skillhub.cn slug page",
			in:   "https://skillhub.cn/skills/evez-api-gateway",
			want: parsedSkillSource{
				Kind: skillSourceRegistry, Registry: skillHubCNAPIOrigin,
				Slug: "evez-api-gateway",
			},
		},
		{
			name: "generic registry skills prefix",
			in:   "https://skillhub.example.com/skills/my-skill",
			want: parsedSkillSource{
				Kind: skillSourceRegistry, Registry: "https://skillhub.example.com",
				Slug: "my-skill",
			},
		},
		{
			name: "github repo url",
			in:   "https://github.com/vercel-labs/agent-skills",
			want: parsedSkillSource{
				Kind: skillSourceGitHub, Owner: "vercel-labs", Repo: "agent-skills", Ref: "HEAD",
			},
		},
		{
			name: "github tree path",
			in:   "https://github.com/vercel-labs/agent-skills/tree/main/skills/web-design",
			want: parsedSkillSource{
				Kind: skillSourceGitHub, Owner: "vercel-labs", Repo: "agent-skills",
				Ref: "main", Subdir: "skills/web-design",
			},
		},
		{
			name: "skills.sh maps to github",
			in:   "https://skills.sh/vercel-labs/agent-skills/web-design",
			want: parsedSkillSource{
				Kind: skillSourceGitHub, Owner: "vercel-labs", Repo: "agent-skills",
				Ref: "HEAD", Subdir: "web-design",
			},
		},
		{
			name: "github shorthand",
			in:   "vercel-labs/agent-skills@frontend-design",
			want: parsedSkillSource{
				Kind: skillSourceGitHub, Owner: "vercel-labs", Repo: "agent-skills",
				Ref: "HEAD", Subdir: "frontend-design",
			},
		},
		{
			name: "gitlab project",
			in:   "https://gitlab.com/group/project/-/tree/main/skills/foo",
			want: parsedSkillSource{
				Kind: skillSourceGitLab, Owner: "group", Repo: "project",
				Ref: "main", Subdir: "skills/foo",
			},
		},
		{
			name: "direct zip",
			in:   "https://example.com/skills/demo.zip",
			want: parsedSkillSource{Kind: skillSourceDirect, DirectURL: "https://example.com/skills/demo.zip"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseSkillSource(tt.in)
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestParseSkillSourceRejects(t *testing.T) {
	_, err := parseSkillSource("")
	require.ErrorIs(t, err, ErrSkillSourceInvalid)

	_, err = parseSkillSource("file:///etc/passwd")
	require.ErrorIs(t, err, ErrSkillSourceInvalid)
	require.ErrorContains(t, err, "http(s)")
}

func TestSkillHubCNMapsToDownloadAPI(t *testing.T) {
	got, err := parseSkillSource("https://skillhub.cn/skills/clawhub_pskoett/self-improving-agent")
	require.NoError(t, err)
	u, err := got.fetchURL()
	require.NoError(t, err)
	require.Equal(t, skillHubCNAPIOrigin+"/api/v1/download?slug=self-improving-agent", u)
}

func TestFetchSkillArchiveFromRegistry(t *testing.T) {
	archive := zipBundle(t, map[string]string{"SKILL.md": validSkillMD})
	var sawAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/api/v1/download" || r.URL.Query().Get("slug") != "owner/demo" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)
	allowLoopbackSkillFetch(t)

	got, err := fetchSkillArchive(t.Context(), server.URL+"/owner/demo", "sk_test", server.Client())
	require.NoError(t, err)
	require.Equal(t, "Bearer sk_test", sawAuth)

	bundle, err := ParseSkillBundle(got)
	require.NoError(t, err)
	require.Equal(t, "pdf-tools", bundle.Name)
}

func TestFetchSkillArchiveFollowsGitHubHandoff(t *testing.T) {
	archive := zipBundle(t, map[string]string{
		"repo-main/README.md":               "# repo",
		"repo-main/skills/foo/SKILL.md":     validSkillMD,
		"repo-main/skills/foo/scripts/a.py": "print(1)\n",
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/download":
			_ = json.NewEncoder(w).Encode(skillSourceHandoff{
				SourceRef:  "public-github",
				ArchiveURL: "http://" + r.Host + "/archive.zip",
				Path:       "skills/foo",
			})
		case "/archive.zip":
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	allowLoopbackSkillFetch(t)

	got, err := fetchSkillArchive(t.Context(), server.URL+"/owner/demo", "", server.Client())
	require.NoError(t, err)
	bundle, err := ParseSkillBundle(got)
	require.NoError(t, err)
	require.Equal(t, "pdf-tools", bundle.Name)
	require.Contains(t, bundle.Files, "scripts/a.py")
	require.NotContains(t, bundle.Files, "README.md")
}

func TestFetchSkillArchiveFromSkillMarkdown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte(validSkillMD))
	}))
	t.Cleanup(server.Close)
	allowLoopbackSkillFetch(t)

	got, err := fetchSkillArchive(t.Context(), server.URL+"/SKILL.md", "", server.Client())
	require.NoError(t, err)
	bundle, err := ParseSkillBundle(got)
	require.NoError(t, err)
	require.Equal(t, "pdf-tools", bundle.Name)
}

func TestFetchSkillArchiveDoesNotSendTokenToGitHub(t *testing.T) {
	src := parsedSkillSource{
		Kind: skillSourceGitHub, Owner: "o", Repo: "r", Ref: "HEAD",
	}
	require.False(t, shouldSendSkillToken(src, "sk_secret"))
	src.Kind = skillSourceRegistry
	src.Registry = "https://skillhub.example.com"
	require.True(t, shouldSendSkillToken(src, "sk_secret"))
}

func TestParseSkillBundleNestedRemoteArchive(t *testing.T) {
	data := zipBundle(t, map[string]string{
		"repo-abc/LICENSE":               "MIT",
		"repo-abc/skills/pdf/SKILL.md":   validSkillMD,
		"repo-abc/skills/pdf/extract.py": "print(1)\n",
	})
	bundle, err := ParseSkillBundleWithOptions(data, SkillBundleParseOptions{
		AllowExtraFiles:  true,
		AllowNestedSkill: true,
	})
	require.NoError(t, err)
	require.Equal(t, "pdf-tools", bundle.Name)
	require.Contains(t, bundle.Files, "extract.py")

	_, err = ParseSkillBundle(data)
	require.ErrorIs(t, err, ErrSkillBundleInvalid, "uploads stay strict about nesting")
}

func allowLoopbackSkillFetch(t *testing.T) {
	t.Helper()
	utils.SetSSRFWhitelistFromRaw("127.0.0.1,::1")
	t.Cleanup(func() { utils.SetSSRFWhitelistFromRaw("") })
}

func TestFetchSkillArchiveRejectsNonSkillHTML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>not a skill</html>"))
	}))
	t.Cleanup(server.Close)
	allowLoopbackSkillFetch(t)

	_, err := fetchSkillArchive(t.Context(), server.URL+"/demo.zip", "", server.Client())
	require.ErrorIs(t, err, ErrSkillSourceInvalid)
	require.True(t, strings.Contains(err.Error(), "skill archive") ||
		strings.Contains(err.Error(), "zip skill bundle"))
}
