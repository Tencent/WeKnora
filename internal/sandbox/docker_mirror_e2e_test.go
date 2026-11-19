package sandbox

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestDockerEnsureStandardTemplatePullsThroughMirrorE2E exercises the Docker
// backend against the real daemon when WEKNORA_DOCKER_MIRROR_E2E names a
// local registry that serves wechatopenai/weknora-sandbox:main — e.g. a
// registry:2 container on 127.0.0.1:5000 with the image pushed under the
// mirrored repository path. It skips otherwise: CI has no daemon with a
// pre-seeded mirror.
//
// The config carries the Docker Hub form of the standard image on purpose:
// that is what a workspace config saved before a mirror was introduced (or
// typed by hand) holds, and the daemon must still receive the mirrored
// reference and pull it from the local registry.
func TestDockerEnsureStandardTemplatePullsThroughMirrorE2E(t *testing.T) {
	mirror := os.Getenv("WEKNORA_DOCKER_MIRROR_E2E")
	if mirror == "" {
		t.Skip("set WEKNORA_DOCKER_MIRROR_E2E=<registry host holding wechatopenai/weknora-sandbox:main> to run")
	}
	t.Setenv(StandardImageMirrorEnv, mirror)

	client, err := NewDockerRemoteClientForCheck(&Config{
		Type:           SandboxTypeDocker,
		DockerImage:    DefaultDockerImage,
		DefaultTimeout: time.Minute,
	})
	require.NoError(t, err)

	mirrored := mirror + "/wechatopenai/weknora-sandbox:main"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// EnsureStandardTemplate returns while a cold pull may still be running
	// in the background; poll until the daemon reports the mirrored image.
	var status string
	for deadline := time.Now().Add(4 * time.Minute); time.Now().Before(deadline); {
		template, err := client.EnsureStandardTemplate(ctx)
		require.NoError(t, err)
		require.Equal(t, mirrored, template.Image,
			"the daemon-facing reference must be the mirrored one")
		status = template.Status
		if status == "ready" {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("daemon never reported %q present — pull from the mirror failed (last status %q)", mirrored, status)
}
