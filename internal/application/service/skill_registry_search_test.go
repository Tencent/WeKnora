package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Shapes copied from live responses (trimmed): ClawHub federates skills.sh
// listings as install.kind "skills-sh"; SkillHub mirrors ClawHub skills under a
// clawhub_<owner> namespace handle.
const clawHubSearchBody = `{"results":[
 {"slug":"powerpoint-pptx","displayName":"Powerpoint / PPTX","summary":"Create and edit\ndecks",
  "downloads":55673,"official":false,"ownerHandle":"ivan","canonicalUrl":"/ivan/skills/powerpoint-pptx",
  "install":{"kind":"clawhub","reference":"ivan/powerpoint-pptx","sourceUrl":null},
  "native":{"skill":{"isSuspicious":false}}},
 {"slug":"evil","displayName":"Evil","summary":"x","downloads":1,"canonicalUrl":"/bad/skills/evil",
  "install":{"kind":"clawhub","reference":"bad/evil"},"native":{"skill":{"isSuspicious":true}}},
 {"displayName":"frontend-design","summary":"UI","canonicalUrl":"/skills-sh/anthropics/skills/frontend-design",
  "install":{"kind":"skills-sh","reference":"skills-sh:anthropics/skills/frontend-design",
   "sourceUrl":"https://www.skills.sh/anthropics/skills/frontend-design"},"native":{"owner":"not-an-object-shape"}},
 {"slug":"broken","install":{"kind":"mystery","reference":""}}
]}`

const skillHubSearchBody = `{"results":[
 {"slug":"ppt","displayName":"ppt","summary":"","description":"","description_zh":"一键生成演示稿",
  "downloads":50260,"installs":6371,"owner_name":"zhj","source":"community","namespace":{"handle":"user_1"}},
 {"slug":"powerpoint-pptx","displayName":"Powerpoint","summary":"mirror","downloads":10,
  "source":"clawhub","namespace":{"handle":"clawhub_ivan"}}
]}`

func registryServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/search", r.URL.Path)
		require.NotEmpty(t, r.URL.Query().Get("q"))
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testRegistrySearch(clawHub, skillHub *httptest.Server) *skillRegistrySearch {
	r := newSkillRegistrySearch(http.DefaultClient)
	r.clawHubOrigin = clawHub.URL
	r.skillHubOrigin = skillHub.URL
	return r
}

func TestSkillRegistrySearchMapsListingsToInstallableLocators(t *testing.T) {
	var gotSuspiciousFilter string
	clawHub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSuspiciousFilter = r.URL.Query().Get("nonSuspiciousOnly")
		_, _ = w.Write([]byte(clawHubSearchBody))
	}))
	t.Cleanup(clawHub.Close)
	search := testRegistrySearch(clawHub, registryServer(t, skillHubSearchBody, http.StatusOK))

	result, err := search.search(context.Background(), "pptx", 10)

	require.NoError(t, err)
	assert.Equal(t, "true", gotSuspiciousFilter, "ClawHub is asked to leave out listings it flagged")
	assert.Empty(t, result.Unavailable)
	sources := make([]string, 0, len(result.Candidates))
	for _, c := range result.Candidates {
		sources = append(sources, c.Source)
	}
	assert.Equal(t, []string{
		"@ivan/powerpoint-pptx",
		"https://skillhub.cn/skills/ppt",
		"skills-sh:anthropics/skills/frontend-design",
	}, sources, "rankings interleave; the flagged, unmappable and mirrored listings are gone")

	pptx := result.Candidates[0]
	assert.Equal(t, "powerpoint-pptx", pptx.Name)
	assert.Equal(t, "clawhub", pptx.Registry)
	assert.Equal(t, int64(55673), pptx.Downloads)
	assert.Equal(t, clawHub.URL+"/ivan/skills/powerpoint-pptx", pptx.URL)

	ppt := result.Candidates[1]
	assert.Equal(t, "skillhub", ppt.Registry)
	assert.Equal(t, "一键生成演示稿", ppt.Description, "the Chinese description stands in for an empty one")
	assert.Equal(t, int64(6371), ppt.Installs)

	frontend := result.Candidates[2]
	assert.Equal(t, "frontend-design", frontend.Name, "a listing without a slug is named from its locator")
	assert.Equal(t, "skills-sh", frontend.Registry)
	assert.Equal(t, "anthropics", frontend.Owner)
}

// One registry being down is reported by name; the other's results still come
// back.
func TestSkillRegistrySearchReportsAnUnreachableRegistry(t *testing.T) {
	search := testRegistrySearch(
		registryServer(t, `{}`, http.StatusBadGateway),
		registryServer(t, skillHubSearchBody, http.StatusOK),
	)

	result, err := search.search(context.Background(), "ppt", 5)

	require.NoError(t, err)
	assert.Equal(t, []string{registryNameClawHub}, result.Unavailable)
	require.NotEmpty(t, result.Candidates)
	assert.Equal(t, "skillhub", result.Candidates[0].Registry)
}

func TestMergeSkillCandidatesStopsAtTheLimit(t *testing.T) {
	a := []tools.SkillCandidate{{Source: "a1"}, {Source: "a2"}, {Source: "a3"}}
	b := []tools.SkillCandidate{{Source: "b1"}}
	got := mergeSkillCandidates([][]tools.SkillCandidate{a, b}, 3)
	require.Len(t, got, 3)
	assert.Equal(t, []string{"a1", "b1", "a2"}, []string{got[0].Source, got[1].Source, got[2].Source})
}

type fakeSkillInventory struct {
	installed []*types.TenantSkillEntity
	catalog   []*types.TenantSkillCatalogEntity
	err       error
	gotConfig string
}

func (f *fakeSkillInventory) ListSkillsByConfig(
	_ context.Context, _ uint64, configID string,
) ([]*types.TenantSkillEntity, error) {
	f.gotConfig = configID
	return f.installed, f.err
}

func (f *fakeSkillInventory) ListCatalogsByTenant(
	_ context.Context, _ uint64,
) ([]*types.TenantSkillCatalogEntity, error) {
	return f.catalog, f.err
}

func TestRunSkillFinderMarksNamesTheWorkspaceAlreadyUses(t *testing.T) {
	inventory := &fakeSkillInventory{
		installed: []*types.TenantSkillEntity{{Name: "PowerPoint-PPTX", Status: types.SkillStatusReady}},
		catalog:   []*types.TenantSkillCatalogEntity{{Name: "ppt"}},
	}
	finder := &runSkillFinder{
		registry: testRegistrySearch(
			registryServer(t, clawHubSearchBody, http.StatusOK),
			registryServer(t, skillHubSearchBody, http.StatusOK),
		),
		inventory: inventory,
		tenantID:  7,
		configID:  "cfg-1",
	}

	result, err := finder.SearchSkills(context.Background(), "pptx", 10)

	require.NoError(t, err)
	assert.Equal(t, "cfg-1", inventory.gotConfig, "installed state is read from the run's own config")
	byName := map[string]tools.SkillCandidate{}
	for _, c := range result.Candidates {
		byName[c.Name] = c
	}
	assert.Equal(t, types.SkillStatusReady, byName["powerpoint-pptx"].InstallStatus)
	assert.True(t, byName["ppt"].InCatalog)
	assert.Empty(t, byName["frontend-design"].InstallStatus)
}

func TestRunSkillFinderPreviewReadsTheBundleItself(t *testing.T) {
	var fetched string
	finder := &runSkillFinder{
		preview: func(_ context.Context, source string) (*SkillBundle, error) {
			fetched = source
			return &SkillBundle{
				Name: "pdf", Description: "PDF toolkit", Version: "1.2.0",
				Files: map[string][]byte{"SKILL.md": nil, "scripts/fill.py": nil},
			}, nil
		},
		inventory: &fakeSkillInventory{installed: []*types.TenantSkillEntity{
			{Name: "pdf", Status: types.SkillStatusInstalling},
		}},
		tenantID: 7,
		configID: "cfg-1",
	}

	c, err := finder.PreviewSkill(context.Background(), " skills-sh:anthropics/skills/pdf ")

	require.NoError(t, err)
	assert.Equal(t, " skills-sh:anthropics/skills/pdf ", fetched)
	assert.Equal(t, "skills-sh:anthropics/skills/pdf", c.Source)
	assert.Equal(t, "pdf", c.Name)
	assert.Equal(t, "skills-sh", c.Registry)
	assert.Equal(t, 2, c.FileCount)
	assert.Equal(t, types.SkillStatusInstalling, c.InstallStatus)
}

// A malformed locator is refused before any download is attempted.
func TestRunSkillFinderPreviewRejectsAnInvalidSourceWithoutFetching(t *testing.T) {
	finder := &runSkillFinder{preview: func(context.Context, string) (*SkillBundle, error) {
		t.Fatal("an invalid source must not be fetched")
		return nil, nil
	}}

	_, err := finder.PreviewSkill(context.Background(), "owner/slug")

	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrSkillSourceInvalid))
}
