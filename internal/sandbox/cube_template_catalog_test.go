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
	require.Equal(t, "PENDING", template.Status)
	require.Equal(t, int32(1), rebuilt.Load())
	require.Equal(t, int32(0), created.Load(), "a rebuild must not add a template")
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
