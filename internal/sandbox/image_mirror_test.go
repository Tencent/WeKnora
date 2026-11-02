package sandbox

import "testing"

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
