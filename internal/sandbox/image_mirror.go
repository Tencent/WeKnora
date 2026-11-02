package sandbox

import (
	"os"
	"strings"
)

// StandardImageMirrorEnv redirects where the standard sandbox images are
// fetched from. Offline and intranet deployments cannot reach Docker Hub;
// setting this to a private registry rewrites every standard sandbox image
// reference — the base, cube, desktop and desktop-cube flavors of
// wechatopenai/weknora-sandbox — before WeKnora hands it to the sandbox
// host that does the pulling.
//
// The value is a registry host, optionally with a port or a path prefix
// ("registry.internal.example.com:5000"); a scheme is tolerated and
// ignored. WeKnora never pulls these images itself — the Docker Engine, the
// Cube cluster or E2B does — so transport security belongs on the pulling
// side: an HTTP or self-signed private registry needs the Docker daemon's
// insecure-registries entry (or the Cube cluster's equivalent), not a
// WeKnora setting.
const StandardImageMirrorEnv = "WEKNORA_IMAGE_REGISTRY_MIRROR"

// standardImageRepository is the repository every default sandbox image
// shares. Mirror matching happens on this path so all flavors — and any tag
// pinned on them — redirect together.
const standardImageRepository = "wechatopenai/weknora-sandbox"

// StandardDockerImage is the effective base sandbox image for the Docker and
// E2B backends, with the registry mirror applied.
func StandardDockerImage() string {
	return ApplyStandardImageMirror(DefaultDockerImage)
}

// StandardDesktopDockerImage is the effective desktop sandbox image for the
// E2B backend, with the registry mirror applied.
func StandardDesktopDockerImage() string {
	return ApplyStandardImageMirror(DefaultDesktopDockerImage)
}

// StandardCubeTemplateImage is the effective base template image for the
// Cube backend, with the registry mirror applied.
func StandardCubeTemplateImage() string {
	return ApplyStandardImageMirror(DefaultCubeTemplateImage)
}

// StandardCubeDesktopTemplateImage is the effective desktop template image
// for the Cube backend, with the registry mirror applied.
func StandardCubeDesktopTemplateImage() string {
	return ApplyStandardImageMirror(DefaultCubeDesktopTemplateImage)
}

// ApplyStandardImageMirror rewrites a sandbox image reference from Docker
// Hub to the registry in StandardImageMirrorEnv, preserving the tag or
// digest. A reference that already carries a different explicit registry —
// an image the admin pointed at their own mirror — passes through
// untouched, and so does everything when no mirror is configured. The
// rewrite is idempotent.
func ApplyStandardImageMirror(image string) string {
	mirror := mirrorRegistry()
	if mirror == "" {
		return image
	}
	ref := strings.TrimSpace(image)
	if ref == "" {
		return ref
	}

	// Split off an optional digest first so registry ports and tags keep
	// their meaning in the checks below.
	digest := ""
	if at := strings.Index(ref, "@"); at >= 0 {
		ref, digest = ref[:at], ref[at:]
	}
	// A colon after the last slash separates a tag; one before it is a
	// registry port (mirrors normalizeImageRepository's split).
	tag := ""
	if colon := strings.LastIndex(ref, ":"); colon > strings.LastIndex(ref, "/") {
		ref, tag = ref[:colon], ref[colon:]
	}

	// An explicit registry host is a leading path component with a dot, a
	// port or the literal localhost. Only Docker Hub redirects: any other
	// host was chosen by the admin and is reachable where it matters.
	if host, rest, ok := strings.Cut(strings.Trim(ref, "/"), "/"); ok &&
		host != "" && (strings.ContainsAny(host, ".:") || host == "localhost") {
		if !strings.EqualFold(host, "docker.io") {
			return image
		}
		ref = rest
	}
	if !strings.EqualFold(strings.Trim(ref, "/"), standardImageRepository) {
		return image
	}
	return mirror + "/" + standardImageRepository + tag + digest
}

// mirrorRegistry reads the mirror setting without a scheme or trailing
// slashes so plain concatenation produces a valid reference.
func mirrorRegistry() string {
	mirror := strings.TrimSpace(os.Getenv(StandardImageMirrorEnv))
	if mirror == "" {
		return ""
	}
	mirror = strings.TrimPrefix(strings.TrimPrefix(mirror, "https://"), "http://")
	return strings.TrimRight(mirror, "/")
}
