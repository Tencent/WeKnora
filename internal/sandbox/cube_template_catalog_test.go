package sandbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newCubeTemplateClient points a CubeRemoteClient at a bare catalog-API stub.
// The full cubeMockServer models sandboxes rather than the template catalog.
func newCubeTemplateClient(t *testing.T, handler http.HandlerFunc) *CubeRemoteClient {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewCubeRemoteClient(&Config{
		Type:              SandboxTypeCube,
		CubeAPIURL:        server.URL,
		CubeProxyURL:      server.URL,
		CubeSandboxDomain: "cube.app",
		CubeHTTPTimeout:   5 * time.Second,
	})
	require.NoError(t, err)
	return client
}

// Cube omits the name of a template that carries no alias, which used to make
// our own template unrecognisable and every catalog refresh build another one.
func TestCubeRemoteClientListTemplatesRecognisesStandardByImage(t *testing.T) {
	client := newCubeTemplateClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/snapshots" {
			writeJSON(w, http.StatusOK, []map[string]any{})
			return
		}
		require.Equal(t, "/templates", r.URL.Path)
		writeJSON(w, http.StatusOK, []map[string]any{
			{
				"templateID": "tpl-nameless",
				"status":     "READY",
				"imageInfo":  DefaultDockerImage,
			},
			{
				"templateID": "tpl-other",
				"status":     "READY",
				"imageInfo":  "python:3.11",
			},
		})
	})

	templates, err := client.ListTemplates(context.Background())
	require.NoError(t, err)
	require.Len(t, templates, 2)

	require.True(t, templates[0].Standard)
	require.Equal(t, StandardTemplateName, templates[0].Name,
		"a recognised template must be labelled, not shown as a bare ID")
	require.False(t, templates[1].Standard)
	require.Equal(t, "tpl-other", templates[1].Name)
}

func TestCubeRemoteClientListTemplatesMapsCatalogMetadata(t *testing.T) {
	allowInternet := true
	client := newCubeTemplateClient(t, cubeCatalogHandler(
		[]map[string]any{{
			"templateID":          "tpl-full",
			"name":                "custom",
			"status":              "READY",
			"version":             "v2",
			"imageInfo":           "python:3.11",
			"createdAt":           "2026-08-01T12:00:00Z",
			"instanceType":        "small",
			"networkType":         "tap",
			"allowInternetAccess": allowInternet,
		}},
		nil,
	))

	templates, err := client.ListTemplates(context.Background())
	require.NoError(t, err)
	require.Len(t, templates, 1)
	require.Equal(t, "tpl-full", templates[0].ID)
	require.Equal(t, "custom", templates[0].Name)
	require.Equal(t, "v2", templates[0].Version)
	require.Equal(t, "python:3.11", templates[0].Image)
	require.Equal(t, "2026-08-01T12:00:00Z", templates[0].CreatedAt)
	require.Equal(t, "small", templates[0].InstanceType)
	require.Equal(t, "tap", templates[0].NetworkType)
	require.NotNil(t, templates[0].AllowInternetAccess)
	require.True(t, *templates[0].AllowInternetAccess)
}

func TestCubeRemoteClientListTemplatesSurfacesLastError(t *testing.T) {
	client := newCubeTemplateClient(t, cubeCatalogHandler(
		[]map[string]any{{
			"templateID": "tpl-broken",
			"status":     "FAILED",
			"imageInfo":  DefaultDockerImage,
			"lastError":  "pull access denied for wechatopenai/weknora-sandbox",
		}},
		nil,
	))

	templates, err := client.ListTemplates(context.Background())
	require.NoError(t, err)
	require.Len(t, templates, 1)
	require.Equal(t, "pull access denied for wechatopenai/weknora-sandbox", templates[0].Error)
}

// Cube stores snapshots in the template store. The settings step is for
// picking a base template, so snap- IDs must not appear even if GET
// /snapshots is empty or missing.
func TestCubeRemoteClientListTemplatesHidesSnapPrefixedIDs(t *testing.T) {
	client := newCubeTemplateClient(t, cubeCatalogHandler(
		[]map[string]any{
			{
				"templateID": "tpl-weknora",
				"name":       "weknora",
				"status":     "READY",
			},
			{
				"templateID": "snap-1546901f7e5e40bdb8794c78",
				"aliases":    []string{"weknora-sk-c838ac20-g2"},
				"status":     "READY",
			},
			{
				"templateID": "SNAP-uppercase",
				"status":     "READY",
			},
		},
		nil,
	))

	templates, err := client.ListTemplates(context.Background())
	require.NoError(t, err)
	require.Len(t, templates, 1)
	require.Equal(t, "tpl-weknora", templates[0].ID)
}

// Snapshot IDs that do not use the snap- prefix still belong to GET
// /snapshots, and must not be offered as a base template.
func TestCubeRemoteClientListTemplatesHidesListedSnapshots(t *testing.T) {
	client := newCubeTemplateClient(t, cubeCatalogHandler(
		[]map[string]any{
			{"templateID": "tpl-weknora", "name": "weknora", "status": "READY"},
			{"templateID": "abc123unprefixed", "aliases": []string{"weknora-sk-cfg-g1"}, "status": "READY"},
		},
		[]map[string]any{
			{"snapshotID": "abc123unprefixed", "names": []string{"weknora-sk-cfg-g1"}},
		},
	))

	templates, err := client.ListTemplates(context.Background())
	require.NoError(t, err)
	require.Len(t, templates, 1)
	require.Equal(t, "tpl-weknora", templates[0].ID)
}

func TestCubeRemoteClientListTemplatesKeepsTemplatesWhenSnapshotListFails(t *testing.T) {
	client := newCubeTemplateClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/templates":
			writeJSON(w, http.StatusOK, []map[string]any{
				{"templateID": "tpl-weknora", "status": "READY"},
				{"templateID": "snap-orphan", "status": "READY"},
			})
		case "/snapshots":
			http.Error(w, "unavailable", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	})

	templates, err := client.ListTemplates(context.Background())
	require.NoError(t, err)
	require.Len(t, templates, 1)
	require.Equal(t, "tpl-weknora", templates[0].ID)
}

func TestCubeTemplateIsSnapshot(t *testing.T) {
	listed := map[string]struct{}{"listed-id": {}}
	require.True(t, cubeTemplateIsSnapshot("snap-1", nil))
	require.True(t, cubeTemplateIsSnapshot("SNAP-1", nil))
	require.True(t, cubeTemplateIsSnapshot("listed-id", listed))
	require.False(t, cubeTemplateIsSnapshot("tpl-weknora", listed))
	require.False(t, cubeTemplateIsSnapshot("", listed))
}

func cubeCatalogHandler(templates, snapshots []map[string]any) http.HandlerFunc {
	if templates == nil {
		templates = []map[string]any{}
	}
	if snapshots == nil {
		snapshots = []map[string]any{}
	}
	return func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/templates" && r.Method == http.MethodGet:
			writeJSON(w, http.StatusOK, templates)
		case r.URL.Path == "/snapshots" && r.Method == http.MethodGet:
			writeJSON(w, http.StatusOK, snapshots)
		default:
			http.NotFound(w, r)
		}
	}
}

// The bug this guards: an unnamed WeKnora template was invisible to the
// idempotency check, so every visit to the template step queued another build.
func TestCubeRemoteClientEnsureStandardTemplateSkipsBuildForNamelessTemplate(t *testing.T) {
	var builds atomic.Int32
	client := newCubeTemplateClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			builds.Add(1)
		}
		writeJSON(w, http.StatusOK, []map[string]any{{
			"templateID": "tpl-nameless",
			"status":     "READY",
			"imageInfo":  DefaultDockerImage,
		}})
	})

	for range 3 {
		template, err := client.EnsureStandardTemplate(context.Background())
		require.NoError(t, err)
		require.Equal(t, "tpl-nameless", template.ID)
	}
	require.Equal(t, int32(0), builds.Load())
}

// A failed template must be rebuilt in place. Building a fresh one would leave
// the failure behind and repeat on the next refresh.
func TestCubeRemoteClientEnsureStandardTemplateRebuildsFailedTemplate(t *testing.T) {
	var created atomic.Int32
	var rebuilt atomic.Int32
	client := newCubeTemplateClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/templates" && r.Method == http.MethodGet:
			writeJSON(w, http.StatusOK, []map[string]any{{
				"templateID": "tpl-failed",
				"status":     "FAILED",
				"imageInfo":  DefaultDockerImage,
				"lastError":  "no space left on device",
			}})
		case r.URL.Path == "/templates" && r.Method == http.MethodPost:
			created.Add(1)
			writeJSON(w, http.StatusAccepted, map[string]any{"templateID": "tpl-new"})
		case r.URL.Path == "/templates/tpl-failed" && r.Method == http.MethodPost:
			rebuilt.Add(1)
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.Equal(t, DefaultCubeTemplateImage, payload["image"])
			require.Equal(t, StandardTemplateName, payload["name"])
			require.Equal(t, true, payload["allowInternetAccess"])
			writeJSON(w, http.StatusAccepted, map[string]any{
				"templateID": "tpl-failed",
				"status":     "PENDING",
			})
		default:
			http.NotFound(w, r)
		}
	})

	template, err := client.EnsureStandardTemplate(context.Background())
	require.NoError(t, err)
	require.Equal(t, "tpl-failed", template.ID)
	require.Equal(t, "building", template.Status)
	require.Equal(t, int32(1), rebuilt.Load())
	require.Equal(t, int32(0), created.Load(), "a rebuild must not add a template")
}

func TestCubeRemoteClientListTemplatesNormalizesRunningStatus(t *testing.T) {
	client := newCubeTemplateClient(t, cubeCatalogHandler(
		[]map[string]any{{
			"templateID": "tpl-pulling",
			"status":     "RUNNING",
			"imageInfo":  DefaultCubeTemplateImage,
		}},
		nil,
	))

	templates, err := client.ListTemplates(context.Background())
	require.NoError(t, err)
	require.Len(t, templates, 1)
	require.Equal(t, "building", templates[0].Status)
	require.True(t, templates[0].Standard)
	require.False(t, IsTemplateBuildFailed(templates[0].Status))
}

// CubeMaster may refuse redo (for example when the source image never
// landed). The failed card stays in the catalog so the operator can delete
// it by hand; Ensure must not 500 or spawn a replacement.
func TestCubeRemoteClientEnsureStandardTemplateKeepsFailedWhenRedoBlocked(t *testing.T) {
	var created atomic.Int32
	var deleted atomic.Int32
	client := newCubeTemplateClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/templates" && r.Method == http.MethodGet:
			writeJSON(w, http.StatusOK, []map[string]any{{
				"templateID": "tpl-failed",
				"status":     "FAILED",
				"imageInfo":  DefaultCubeTemplateImage,
				"lastError":  "TOOMANYREQUESTS: You have reached your unauthenticated pull rate limit",
			}})
		case r.URL.Path == "/snapshots" && r.Method == http.MethodGet:
			writeJSON(w, http.StatusOK, []map[string]any{})
		case r.URL.Path == "/templates/tpl-failed" && r.Method == http.MethodPost:
			writeJSON(w, http.StatusBadRequest, map[string]any{
				"code": 400,
				"message": "CubeMaster returned error code 130400: " +
					"template redo is not allowed before source image has been pulled successfully",
			})
		case r.URL.Path == "/templates/tpl-failed" && r.Method == http.MethodDelete:
			deleted.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/templates" && r.Method == http.MethodPost:
			created.Add(1)
			writeJSON(w, http.StatusAccepted, map[string]any{
				"templateID": "tpl-fresh",
				"status":     "PENDING",
			})
		default:
			http.NotFound(w, r)
		}
	})

	template, err := client.EnsureStandardTemplate(context.Background())
	require.NoError(t, err)
	require.Equal(t, "tpl-failed", template.ID)
	require.Equal(t, "failed", template.Status)
	require.Contains(t, template.Error, "TOOMANYREQUESTS")
	require.Equal(t, int32(0), deleted.Load())
	require.Equal(t, int32(0), created.Load())
}

func TestCubeRemoteClientEnsureStandardTemplateBuildsWhenAbsent(t *testing.T) {
	var payload map[string]any
	client := newCubeTemplateClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, []map[string]any{})
		case http.MethodPost:
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			writeJSON(w, http.StatusAccepted, map[string]any{
				"templateID": "tpl-fresh",
				"status":     "PENDING",
			})
		}
	})

	template, err := client.EnsureStandardTemplate(context.Background())
	require.NoError(t, err)
	require.Equal(t, "tpl-fresh", template.ID)
	require.True(t, template.Standard)
	// Cube probes envd to decide whether the build succeeded, so the template
	// must be built from the variant that ships it.
	require.Equal(t, DefaultCubeTemplateImage, payload["image"])
	require.Equal(t, StandardTemplateName, payload["name"])
	require.Equal(t, "1G", payload["writableLayerSize"])
	require.EqualValues(t, CubeEnvdPort, payload["probePort"])
	require.Equal(t, CubeEnvdHealthPath, payload["probePath"])
	require.Equal(t, true, payload["allowInternetAccess"])
	_, hasDNS := payload["dns"]
	require.False(t, hasDNS, "empty DNS config must omit the field so Cubelet keeps its default")
}

func TestCubeRemoteClientEnsureStandardTemplateIncludesConfiguredDNS(t *testing.T) {
	var payload map[string]any
	client := newCubeTemplateClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, []map[string]any{})
		case http.MethodPost:
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			writeJSON(w, http.StatusAccepted, map[string]any{
				"templateID": "tpl-dns",
				"status":     "PENDING",
			})
		}
	})
	client.config.CubeDNSServers = []string{"8.8.8.8", "1.1.1.1"}

	template, err := client.EnsureStandardTemplate(context.Background())
	require.NoError(t, err)
	require.Equal(t, "tpl-dns", template.ID)
	require.Equal(t, []any{"8.8.8.8", "1.1.1.1"}, payload["dns"])
}

func TestCubeRemoteClientReplaceStandardTemplateDeletesThenBuilds(t *testing.T) {
	var deleted atomic.Int32
	var payload map[string]any
	client := newCubeTemplateClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/templates" && r.Method == http.MethodGet:
			writeJSON(w, http.StatusOK, []map[string]any{{
				"templateID": "tpl-old",
				"status":     "READY",
				"imageInfo":  DefaultDockerImage,
				"aliases":    []string{StandardTemplateName},
			}})
		case r.URL.Path == "/templates/tpl-old" && r.Method == http.MethodDelete:
			deleted.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/templates" && r.Method == http.MethodPost:
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			writeJSON(w, http.StatusAccepted, map[string]any{
				"templateID": "tpl-new",
				"status":     "PENDING",
			})
		case r.URL.Path == "/snapshots" && r.Method == http.MethodGet:
			writeJSON(w, http.StatusOK, []map[string]any{})
		default:
			http.NotFound(w, r)
		}
	})
	client.config.CubeDNSServers = []string{"192.0.2.53"}

	template, err := client.ReplaceStandardTemplate(context.Background())
	require.NoError(t, err)
	require.Equal(t, "tpl-new", template.ID)
	require.Equal(t, "building", template.Status)
	require.Equal(t, int32(1), deleted.Load())
	require.Equal(t, []any{"192.0.2.53"}, payload["dns"])
	require.Equal(t, true, payload["allowInternetAccess"])
}

// The plain and Cube images share a repository, so a template built from either
// one is still recognised as ours — which is what lets an existing template
// built from the envd-less image be rebuilt in place rather than duplicated.
func TestCubeTemplateImageIsRecognisedAsStandard(t *testing.T) {
	require.True(t, isStandardTemplateImage(DefaultCubeTemplateImage))
	require.True(t, isStandardTemplateImage(DefaultDockerImage))
}

func TestIsStandardTemplateImage(t *testing.T) {
	for _, image := range []string{
		DefaultDockerImage,
		"wechatopenai/weknora-sandbox",
		"docker.io/wechatopenai/weknora-sandbox:latest",
		"docker.io/wechatopenai/weknora-sandbox@sha256:abc",
		"registry.internal:5000/wechatopenai/weknora-sandbox:v1",
	} {
		require.True(t, isStandardTemplateImage(image), image)
	}
	for _, image := range []string{
		"",
		"python:3.11",
		"wechatopenai/weknora-docreader:latest",
		"someone-else/weknora-sandbox:latest",
	} {
		require.False(t, isStandardTemplateImage(image), image)
	}
}
