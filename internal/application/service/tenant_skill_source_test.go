package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
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
				Owner: "lyingbug", Slug: "weknora",
			},
		},
		{
			name: "clawhub page url",
			in:   "https://clawhub.ai/lyingbug/weknora",
			want: parsedSkillSource{
				Kind: skillSourceRegistry, Registry: "https://clawhub.ai",
				Owner: "lyingbug", Slug: "weknora",
			},
		},
		{
			name: "clawhub canonical skills path",
			in:   "https://clawhub.ai/steipete/skills/github",
			want: parsedSkillSource{
				Kind: skillSourceRegistry, Registry: "https://clawhub.ai",
				Owner: "steipete", Slug: "github",
			},
		},
		{
			name: "clawhub owner skills path",
			in:   "https://clawhub.ai/jixinyi546-maker/skills/emar-ppt-skill",
			want: parsedSkillSource{
				Kind: skillSourceRegistry, Registry: "https://clawhub.ai",
				Owner: "jixinyi546-maker", Slug: "emar-ppt-skill",
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
			name: "registry slug with version",
			in:   "my-skill@1.2.0",
			want: parsedSkillSource{
				Kind: skillSourceRegistry, Registry: defaultSkillRegistryOrigin,
				Slug: "my-skill", Version: "1.2.0",
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
			name: "clawhub skills-sh catalog page",
			in:   "https://clawhub.ai/skills-sh/skills-101/superpowers/ai-image-generation",
			want: parsedSkillSource{
				Kind: skillSourceSkillsSh, Registry: "https://clawhub.ai",
				Owner: "skills-101", Repo: "superpowers", Slug: "ai-image-generation",
			},
		},
		{
			name: "clawhub skills-sh repo named skills",
			in:   "https://clawhub.ai/skills-sh/doany-ai/skills/ai-image-generation",
			want: parsedSkillSource{
				Kind: skillSourceSkillsSh, Registry: "https://clawhub.ai",
				Owner: "doany-ai", Repo: "skills", Slug: "ai-image-generation",
			},
		},
		{
			name: "skills-sh colon reference",
			in:   "skills-sh:skills-101/superpowers/ai-image-generation",
			want: parsedSkillSource{
				Kind: skillSourceSkillsSh, Registry: defaultSkillRegistryOrigin,
				Owner: "skills-101", Repo: "superpowers", Slug: "ai-image-generation",
			},
		},
		{
			name: "skills-sh slash reference",
			in:   "skills-sh/skills-101/superpowers/ai-image-generation",
			want: parsedSkillSource{
				Kind: skillSourceSkillsSh, Registry: defaultSkillRegistryOrigin,
				Owner: "skills-101", Repo: "superpowers", Slug: "ai-image-generation",
			},
		},
		{
			name: "skills.sh catalog page uses clawhub resolver",
			in:   "https://skills.sh/vercel-labs/agent-skills/web-design",
			want: parsedSkillSource{
				Kind: skillSourceSkillsSh, Registry: defaultSkillRegistryOrigin,
				Owner: "vercel-labs", Repo: "agent-skills", Slug: "web-design",
			},
		},
		{
			name: "skills.sh owner/repo stays github",
			in:   "https://skills.sh/vercel-labs/agent-skills",
			want: parsedSkillSource{
				Kind: skillSourceGitHub, Owner: "vercel-labs", Repo: "agent-skills",
				Ref: "HEAD",
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

	// owner/slug is a ClawHub id and a GitHub repo. Refuse rather than guess.
	_, err = parseSkillSource("clawhub_pskoett/self-improving-agent")
	require.ErrorIs(t, err, ErrSkillSourceInvalid)
	require.ErrorContains(t, err, "ambiguous")
	require.ErrorContains(t, err, "@clawhub_pskoett/self-improving-agent")

	_, err = parseSkillSource("vercel-labs/agent-skills@frontend-design")
	require.ErrorIs(t, err, ErrSkillSourceInvalid)
	require.ErrorContains(t, err, "ambiguous")

	_, err = parseSkillSource("https://clawhub.ai/skills-sh/skills-101/superpowers")
	require.ErrorIs(t, err, ErrSkillSourceInvalid)
	require.ErrorContains(t, err, "owner/repo/slug")

	_, err = parseSkillSource("https://clawhub.ai/foo/bar/baz/qux")
	require.ErrorIs(t, err, ErrSkillSourceInvalid)
	require.ErrorContains(t, err, "unrecognized registry path")
}

func TestParseSkillSourceAtSlugIsRegistryNotGitHub(t *testing.T) {
	got, err := parseSkillSource("@clawhub_pskoett/self-improving-agent")
	require.NoError(t, err)
	require.Equal(t, skillSourceRegistry, got.Kind)
	require.Equal(t, defaultSkillRegistryOrigin, got.Registry)
	require.Equal(t, "clawhub_pskoett", got.Owner)
	require.Equal(t, "self-improving-agent", got.Slug)
}

func TestFetchSkillArchiveRejectsAmbiguousShorthandWithoutFetching(t *testing.T) {
	_, err := fetchSkillArchive(t.Context(), "owner/demo", http.DefaultClient)
	require.ErrorIs(t, err, ErrSkillSourceInvalid)
	require.ErrorContains(t, err, "ambiguous")
}

func TestClawHubSkillsShMapsToInstallResolver(t *testing.T) {
	cases := []string{
		"https://clawhub.ai/skills-sh/skills-101/superpowers/ai-image-generation",
		"skills-sh:skills-101/superpowers/ai-image-generation",
		"skills-sh/skills-101/superpowers/ai-image-generation",
		"https://www.skills.sh/skills-101/superpowers/ai-image-generation",
	}
	for _, in := range cases {
		got, err := parseSkillSource(in)
		require.NoError(t, err, in)
		u, err := got.fetchURL()
		require.NoError(t, err, in)
		parsed, err := url.Parse(u)
		require.NoError(t, err, in)
		require.Equal(t, "https://clawhub.ai/api/v1/skills/ai-image-generation/install",
			parsed.Scheme+"://"+parsed.Host+parsed.Path, in)
		require.Equal(t, "skills-sh:skills-101/superpowers/ai-image-generation",
			parsed.Query().Get("reference"), in)
	}
}

func TestSourceFromHandoffSkillsShGitHubNestedPath(t *testing.T) {
	ok := true
	next, err := sourceFromHandoff(parsedSkillSource{
		Kind: skillSourceSkillsSh, Registry: defaultSkillRegistryOrigin,
	}, skillSourceHandoff{
		OK:          &ok,
		InstallKind: "github",
		GitHub: &skillSourceGitHubHandoff{
			Repo:   "skills-101/superpowers",
			Path:   "tools/image/ai-image-generation",
			Commit: "becc25649700d5457772a00e5143e28ccf9e5afa",
			SourceURL: "https://github.com/skills-101/superpowers/tree/" +
				"becc25649700d5457772a00e5143e28ccf9e5afa/tools/image/ai-image-generation",
		},
	})
	require.NoError(t, err)
	require.Equal(t, skillSourceGitHub, next.Kind)
	require.Equal(t, "skills-101", next.Owner)
	require.Equal(t, "superpowers", next.Repo)
	require.Equal(t, "becc25649700d5457772a00e5143e28ccf9e5afa", next.Ref)
	require.Equal(t, "tools/image/ai-image-generation", next.Subdir)
}

func TestSourceFromHandoffSkillsShGitHubWithoutSourceURL(t *testing.T) {
	ok := true
	next, err := sourceFromHandoff(parsedSkillSource{Kind: skillSourceSkillsSh}, skillSourceHandoff{
		OK:          &ok,
		InstallKind: "github",
		GitHub: &skillSourceGitHubHandoff{
			Repo:   "openai/skills",
			Path:   "skills/.curated/pdf",
			Commit: "49f948faa9258a0c61caceaf225e179651397431",
		},
	})
	require.NoError(t, err)
	require.Equal(t, skillSourceGitHub, next.Kind)
	require.Equal(t, "openai", next.Owner)
	require.Equal(t, "skills", next.Repo)
	require.Equal(t, "49f948faa9258a0c61caceaf225e179651397431", next.Ref)
	require.Equal(t, "skills/.curated/pdf", next.Subdir)
}

func TestSourceFromHandoffSkillsShRefused(t *testing.T) {
	ok := false
	_, err := sourceFromHandoff(parsedSkillSource{Kind: skillSourceSkillsSh}, skillSourceHandoff{
		OK:      &ok,
		Reason:  "github_upstream_missing",
		Message: "upstream listing is gone",
	})
	require.ErrorIs(t, err, ErrSkillSourceInvalid)
	require.ErrorContains(t, err, "upstream listing is gone")
}

func TestClawHubOwnerSlugMapsToDownloadAPI(t *testing.T) {
	cases := []string{
		"@jixinyi546-maker/emar-ppt-skill",
		"https://clawhub.ai/jixinyi546-maker/emar-ppt-skill",
		"https://clawhub.ai/jixinyi546-maker/skills/emar-ppt-skill",
	}
	for _, in := range cases {
		got, err := parseSkillSource(in)
		require.NoError(t, err, in)
		u, err := got.fetchURL()
		require.NoError(t, err, in)
		parsed, err := url.Parse(u)
		require.NoError(t, err, in)
		require.Equal(t, "https://clawhub.ai/api/v1/download", parsed.Scheme+"://"+parsed.Host+parsed.Path, in)
		require.Equal(t, "emar-ppt-skill", parsed.Query().Get("slug"), in)
		require.Equal(t, "jixinyi546-maker", parsed.Query().Get("ownerHandle"), in)
	}
}

func TestSkillHubCNMapsToDownloadAPI(t *testing.T) {
	got, err := parseSkillSource("https://skillhub.cn/skills/clawhub_pskoett/self-improving-agent")
	require.NoError(t, err)
	u, err := got.fetchURL()
	require.NoError(t, err)
	require.Equal(t, skillHubCNAPIOrigin+"/api/v1/download?slug=self-improving-agent", u)
}

func TestFetchSkillArchiveFromSkillsShInstallResolver(t *testing.T) {
	archive := zipBundle(t, map[string]string{
		"repo-main/README.md":                                "# repo",
		"repo-main/tools/image/ai-image-generation/SKILL.md": validSkillMD,
		"repo-main/tools/image/ai-image-generation/run.py":   "print(1)\n",
		"repo-main/other/SKILL.md": strings.Replace(
			validSkillMD, "name: pdf-tools", "name: other-skill", 1),
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/skills/ai-image-generation/install":
			require.Equal(t, "skills-sh:skills-101/superpowers/ai-image-generation",
				r.URL.Query().Get("reference"))
			_ = json.NewEncoder(w).Encode(skillSourceHandoff{
				InstallKind: "github",
				GitHub: &skillSourceGitHubHandoff{
					Repo:   "skills-101/superpowers",
					Path:   "tools/image/ai-image-generation",
					Commit: "abc123",
				},
				ArchiveURL: "http://" + r.Host + "/archive.zip",
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

	got, err := fetchSkillArchive(t.Context(),
		server.URL+"/skills-sh/skills-101/superpowers/ai-image-generation", server.Client())
	require.NoError(t, err)
	bundle, err := ParseSkillBundle(got)
	require.NoError(t, err)
	require.Equal(t, "pdf-tools", bundle.Name)
	require.Contains(t, bundle.Files, "run.py")
	require.NotContains(t, bundle.Files, "README.md")
}

func TestFetchSkillArchiveFromRegistry(t *testing.T) {
	archive := zipBundle(t, map[string]string{"SKILL.md": validSkillMD})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/download" || r.URL.Query().Get("slug") != "owner/demo" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(archive)
	}))
	t.Cleanup(server.Close)
	allowLoopbackSkillFetch(t)

	got, err := fetchSkillArchive(t.Context(), server.URL+"/owner/demo", server.Client())
	require.NoError(t, err)

	bundle, err := ParseSkillBundle(got)
	require.NoError(t, err)
	require.Equal(t, "pdf-tools", bundle.Name)
}

// Skill sources are read anonymously. Nothing in this flow holds a credential,
// so no hop may present one.
func TestFetchSkillArchiveSendsNoCredentials(t *testing.T) {
	archive := zipBundle(t, map[string]string{"SKILL.md": validSkillMD})

	var archiveAuth, registryAuth string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/download":
			registryAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(skillSourceHandoff{
				ArchiveURL: "http://" + r.Host + "/skill.zip",
			})
		case "/skill.zip":
			archiveAuth = r.Header.Get("Authorization")
			w.Header().Set("Content-Type", "application/zip")
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	allowLoopbackSkillFetch(t)

	_, err := fetchSkillArchive(t.Context(), server.URL+"/owner/demo", server.Client())
	require.NoError(t, err)
	require.Empty(t, registryAuth)
	require.Empty(t, archiveAuth)
}

// The handoff's path is what names one skill inside a monorepo zip; losing it
// makes a multi-skill repo either ambiguous or wrong.
func TestFetchSkillArchiveUsesHandoffPath(t *testing.T) {
	otherSkillMD := strings.Replace(validSkillMD, "name: pdf-tools", "name: csv-tools", 1)
	archive := zipBundle(t, map[string]string{
		"repo-main/README.md":               "# repo",
		"repo-main/skills/pdf/SKILL.md":     validSkillMD,
		"repo-main/skills/pdf/extract.py":   "print('pdf')\n",
		"repo-main/skills/csv/SKILL.md":     otherSkillMD,
		"repo-main/skills/csv/transform.py": "print('csv')\n",
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/download":
			_ = json.NewEncoder(w).Encode(skillSourceHandoff{
				SourceRef:  "public-github",
				ArchiveURL: "http://" + r.Host + "/archive.zip",
				Path:       "skills/csv",
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

	got, err := fetchSkillArchive(t.Context(), server.URL+"/owner/demo", server.Client())
	require.NoError(t, err)
	bundle, err := ParseSkillBundle(got)
	require.NoError(t, err)
	require.Equal(t, "csv-tools", bundle.Name)
	require.Contains(t, bundle.Files, "transform.py")
	require.NotContains(t, bundle.Files, "extract.py")
}

// A malformed handoff is refused: SSRF validation normalises a scheme-less
// string by prepending https://, so passing it through would fetch a host the
// response never legally named.
func TestFetchSkillArchiveRejectsUnusableHandoffURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(skillSourceHandoff{ArchiveURL: "evil.example.com/skill.zip"})
	}))
	t.Cleanup(server.Close)
	allowLoopbackSkillFetch(t)

	_, err := fetchSkillArchive(t.Context(), server.URL+"/owner/demo", server.Client())
	require.ErrorIs(t, err, ErrSkillSourceInvalid)
	require.ErrorContains(t, err, "archive URL is not usable")
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

	got, err := fetchSkillArchive(t.Context(), server.URL+"/owner/demo", server.Client())
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

	got, err := fetchSkillArchive(t.Context(), server.URL+"/SKILL.md", server.Client())
	require.NoError(t, err)
	bundle, err := ParseSkillBundle(got)
	require.NoError(t, err)
	require.Equal(t, "pdf-tools", bundle.Name)
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

	_, err := fetchSkillArchive(t.Context(), server.URL+"/demo.zip", server.Client())
	require.ErrorIs(t, err, ErrSkillSourceInvalid)
	require.True(t, strings.Contains(err.Error(), "skill archive") ||
		strings.Contains(err.Error(), "zip skill bundle"))
}
