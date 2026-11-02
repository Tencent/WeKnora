package sandbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const testMirror = "registry.internal.example.com:5000"

func TestApplyStandardImageMirror(t *testing.T) {
	t.Setenv(StandardImageMirrorEnv, testMirror)

	cases := []struct {
		name string
		in   string
		want string
	}{
		{"base default", DefaultDockerImage, testMirror + "/wechatopenai/weknora-sandbox:main"},
		{"cube default keeps its tag", DefaultCubeTemplateImage, testMirror + "/wechatopenai/weknora-sandbox:main-cube"},
		{"desktop default", DefaultDesktopDockerImage, testMirror + "/wechatopenai/weknora-sandbox:main-desktop"},
		{"cube desktop default", DefaultCubeDesktopTemplateImage, testMirror + "/wechatopenai/weknora-sandbox:main-desktop-cube"},
		{"pinned tag survives", "wechatopenai/weknora-sandbox:v0.9.0", testMirror + "/wechatopenai/weknora-sandbox:v0.9.0"},
		{"explicit docker.io host", "docker.io/wechatopenai/weknora-sandbox:v0.9.0", testMirror + "/wechatopenai/weknora-sandbox:v0.9.0"},
		{"digest survives", "wechatopenai/weknora-sandbox@sha256:abcd", testMirror + "/wechatopenai/weknora-sandbox@sha256:abcd"},
		{"no tag", "wechatopenai/weknora-sandbox", testMirror + "/wechatopenai/weknora-sandbox"},
		{"already mirrored is a foreign host, passthrough", testMirror + "/wechatopenai/weknora-sandbox:main", testMirror + "/wechatopenai/weknora-sandbox:main"},
		{"admin private registry passes through", "registry.corp:5000/wechatopenai/weknora-sandbox:v1", "registry.corp:5000/wechatopenai/weknora-sandbox:v1"},
		{"foreign repository passes through", "ghcr.io/foo/bar:1", "ghcr.io/foo/bar:1"},
		{"bare official image passes through", "nginx:latest", "nginx:latest"},
		{"localhost host passes through", "localhost/weknora-sandbox:main", "localhost/weknora-sandbox:main"},
		{"empty", "  ", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ApplyStandardImageMirror(tc.in); got != tc.want {
				t.Fatalf("ApplyStandardImageMirror(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestApplyStandardImageMirrorWithoutMirror(t *testing.T) {
	t.Setenv(StandardImageMirrorEnv, "")
	for _, in := range []string{
		DefaultDockerImage,
		DefaultCubeTemplateImage,
		"ghcr.io/foo/bar:1",
		"registry.corp:5000/wechatopenai/weknora-sandbox:v1",
	} {
		if got := ApplyStandardImageMirror(in); got != in {
			t.Fatalf("ApplyStandardImageMirror(%q) = %q without a mirror, want passthrough", in, got)
		}
	}
}

func TestApplyStandardImageMirrorNormalizesMirrorValue(t *testing.T) {
	t.Setenv(StandardImageMirrorEnv, "https://registry.internal.example.com:5000/")
	if got := ApplyStandardImageMirror(DefaultDockerImage); got != testMirror+"/wechatopenai/weknora-sandbox:main" {
		t.Fatalf("scheme and trailing slash not stripped: %q", got)
	}
}

func TestStandardImagesRespectMirror(t *testing.T) {
	t.Setenv(StandardImageMirrorEnv, testMirror)
	if got := StandardDockerImage(); got != testMirror+"/wechatopenai/weknora-sandbox:main" {
		t.Fatalf("StandardDockerImage() = %q", got)
	}
	if got := StandardCubeTemplateImage(); got != testMirror+"/wechatopenai/weknora-sandbox:main-cube" {
		t.Fatalf("StandardCubeTemplateImage() = %q", got)
	}
	if got := StandardDesktopDockerImage(); got != testMirror+"/wechatopenai/weknora-sandbox:main-desktop" {
		t.Fatalf("StandardDesktopDockerImage() = %q", got)
	}
	if got := StandardCubeDesktopTemplateImage(); got != testMirror+"/wechatopenai/weknora-sandbox:main-desktop-cube" {
		t.Fatalf("StandardCubeDesktopTemplateImage() = %q", got)
	}
}

// A mirrored reference must still count as the standard template image: the
// catalog recognises templates by repository path with any registry host
// stripped, so a mirror must not make every refresh rebuild the template.
func TestMirroredImageStillCountsAsStandard(t *testing.T) {
	t.Setenv(StandardImageMirrorEnv, testMirror)
	mirrored := StandardDockerImage()
	if mirrored == DefaultDockerImage {
		t.Fatal("mirror not applied")
	}
	if !isStandardTemplateImage(mirrored) {
		t.Fatalf("isStandardTemplateImage(%q) = false, want true", mirrored)
	}
}

// --- Wire propagation: with a mirror configured, the template-build requests
// the backends send must carry the mirrored reference. ---

func TestCubeTemplateBuildsCarryMirroredImage(t *testing.T) {
	t.Setenv(StandardImageMirrorEnv, testMirror)
	mirroredCube := testMirror + "/wechatopenai/weknora-sandbox:main-cube"
	mirroredDesktop := testMirror + "/wechatopenai/weknora-sandbox:main-desktop-cube"

	t.Run("standard build when absent", func(t *testing.T) {
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
		require.Equal(t, mirroredCube, payload["image"])
		require.Equal(t, StandardTemplateName, payload["name"])
	})

	t.Run("desktop build when absent", func(t *testing.T) {
		var payload map[string]any
		client := newCubeTemplateClient(t, func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				// The hub form on an existing template must still be recognised
				// as standard while the mirror is active.
				writeJSON(w, http.StatusOK, []map[string]any{{
					"templateID": "tpl-cli",
					"status":     "READY",
					"imageInfo":  DefaultCubeTemplateImage,
				}})
			case http.MethodPost:
				require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
				writeJSON(w, http.StatusAccepted, map[string]any{
					"templateID": "tpl-desk",
					"status":     "PENDING",
				})
			}
		})

		template, err := client.EnsureDesktopTemplate(context.Background())
		require.NoError(t, err)
		require.Equal(t, "tpl-desk", template.ID)
		require.Equal(t, mirroredDesktop, payload["image"])
		require.Equal(t, DesktopTemplateName, payload["name"])
	})

	t.Run("in-place rebuild", func(t *testing.T) {
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
			case r.URL.Path == "/templates/tpl-old" && r.Method == http.MethodPost:
				require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
				writeJSON(w, http.StatusAccepted, map[string]any{
					"templateID": "tpl-old",
					"status":     "PENDING",
				})
			case r.URL.Path == "/snapshots" && r.Method == http.MethodGet:
				writeJSON(w, http.StatusOK, []map[string]any{})
			default:
				http.NotFound(w, r)
			}
		})

		_, err := client.ReplaceStandardTemplate(context.Background())
		require.NoError(t, err)
		require.Equal(t, mirroredCube, payload["image"])
	})
}

func TestE2BTemplateBuildsCarryMirroredImage(t *testing.T) {
	t.Setenv(StandardImageMirrorEnv, testMirror)
	mirroredBase := testMirror + "/wechatopenai/weknora-sandbox:main"
	mirroredDesktop := testMirror + "/wechatopenai/weknora-sandbox:main-desktop"

	t.Run("standard build", func(t *testing.T) {
		var startBody struct {
			FromImage string `json:"fromImage"`
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/templates":
				writeJSON(w, http.StatusOK, []map[string]any{})
			case r.Method == http.MethodPost && r.URL.Path == "/v3/templates":
				writeJSON(w, http.StatusAccepted, map[string]any{
					"templateID": "tpl-cli",
					"buildID":    "build-cli",
					"names":      []string{StandardTemplateName},
				})
			case r.Method == http.MethodPost && r.URL.Path == "/v2/templates/tpl-cli/builds/build-cli":
				require.NoError(t, json.NewDecoder(r.Body).Decode(&startBody))
				w.WriteHeader(http.StatusAccepted)
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(server.Close)
		client, err := NewE2BRemoteClient(&Config{
			E2BAPIKey: "key-test", E2BAPIURL: server.URL, E2BSandboxTTL: time.Minute,
		})
		require.NoError(t, err)

		got, err := client.EnsureStandardTemplate(context.Background())
		require.NoError(t, err)
		require.Equal(t, "tpl-cli", got.ID)
		require.Equal(t, mirroredBase, startBody.FromImage)
	})

	t.Run("desktop build", func(t *testing.T) {
		var startBody struct {
			FromImage string `json:"fromImage"`
		}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.Method == http.MethodGet && r.URL.Path == "/templates":
				writeJSON(w, http.StatusOK, []map[string]any{{
					"templateID":  "tpl-cli",
					"names":       []string{"weknora"},
					"buildStatus": "ready",
				}})
			case r.Method == http.MethodPost && r.URL.Path == "/v3/templates":
				writeJSON(w, http.StatusAccepted, map[string]any{
					"templateID": "tpl-desk",
					"buildID":    "build-desk",
					"names":      []string{DesktopTemplateName},
				})
			case r.Method == http.MethodPost && r.URL.Path == "/v2/templates/tpl-desk/builds/build-desk":
				require.NoError(t, json.NewDecoder(r.Body).Decode(&startBody))
				w.WriteHeader(http.StatusAccepted)
			default:
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(server.Close)
		client, err := NewE2BRemoteClient(&Config{
			E2BAPIKey: "key-test", E2BAPIURL: server.URL, E2BSandboxTTL: time.Minute,
		})
		require.NoError(t, err)

		got, err := client.EnsureDesktopTemplate(context.Background())
		require.NoError(t, err)
		require.Equal(t, "tpl-desk", got.ID)
		require.Equal(t, mirroredDesktop, startBody.FromImage)
	})
}
