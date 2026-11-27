package sandbox

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/client"
)

func (c *DockerRemoteClient) compactSkillSnapshot(
	ctx context.Context, sandboxID, reference string,
	source container.InspectResponse, parent image.InspectResponse,
) error {
	if source.Config == nil || source.State == nil || parent.Os == "" || parent.Architecture == "" {
		return errors.New("cannot compact snapshot without container configuration, state and image platform")
	}

	// Image load needs the layer's size and digest before sending the layer.
	// Spool the export to disk so a large skill image does not fill memory.
	layer, err := os.CreateTemp("", "weknora-skill-layer-*.tar")
	if err != nil {
		return err
	}
	defer func() {
		_ = layer.Close()
		_ = os.Remove(layer.Name())
	}()

	digest := sha256.New()
	size, err := c.exportSnapshotLayer(ctx, sandboxID, source.State, io.MultiWriter(layer, digest))
	if err != nil {
		return err
	}
	if _, err := layer.Seek(0, io.SeekStart); err != nil {
		return err
	}

	config := *source.Config
	config.Labels = map[string]string{}
	maps.Copy(config.Labels, source.Config.Labels)
	config.Labels[dockerSkillSnapshotLabel] = "true"
	config.Labels[dockerSkillSnapshotSourceLabel] = sandboxID
	diffID := fmt.Sprintf("sha256:%x", digest.Sum(nil))
	archive, err := dockerSingleLayerArchive(layer, size, diffID, reference, config, parent)
	if err != nil {
		return err
	}
	response, err := c.api.ImageLoad(ctx, archive, client.ImageLoadWithQuiet(true))
	if err != nil {
		return fmt.Errorf("load compacted image: %w", err)
	}
	defer func() { _ = response.Close() }()
	// The daemon can send an error in the response stream after HTTP 200.
	decoder := json.NewDecoder(response)
	for {
		var message jsonstream.Message
		if err := decoder.Decode(&message); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("read image load response: %w", err)
		}
		if message.Error != nil {
			return fmt.Errorf("load compacted image: %w", message.Error)
		}
	}
}

func (c *DockerRemoteClient) exportSnapshotLayer(
	ctx context.Context, sandboxID string, state *container.State, dst io.Writer,
) (size int64, err error) {
	if state.Running && !state.Paused {
		if _, err := c.api.ContainerPause(ctx, sandboxID, client.ContainerPauseOptions{}); err != nil {
			return 0, fmt.Errorf("pause for snapshot export: %w", err)
		}
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), DefaultDockerHTTPTimeout)
			defer cancel()
			_, resumeErr := c.api.ContainerUnpause(cleanupCtx, sandboxID, client.ContainerUnpauseOptions{})
			if resumeErr != nil {
				err = errors.Join(err, fmt.Errorf("resume after snapshot export: %w", resumeErr))
			}
		}()
	}
	stream, err := c.api.ContainerExport(ctx, sandboxID, client.ContainerExportOptions{})
	if err != nil {
		return 0, fmt.Errorf("export snapshot filesystem: %w", err)
	}
	defer func() { _ = stream.Close() }()
	size, err = io.Copy(dst, stream)
	if err != nil {
		return 0, fmt.Errorf("read snapshot filesystem: %w", err)
	}
	return size, nil
}

// dockerSingleLayerArchive wraps the merged root filesystem in the format
// accepted by Docker image load. The config keeps the container's effective
// image settings, which a filesystem-only export/import would discard.
func dockerSingleLayerArchive(
	layer io.Reader, size int64, diffID, reference string,
	config container.Config, parent image.InspectResponse,
) (io.Reader, error) {
	const layerName = "layer.tar"
	const configName = "config.json"
	const tarBlockSize = 512

	type historyEntry struct {
		Created time.Time `json:"created"`
		Comment string    `json:"comment"`
	}
	created := time.Now().UTC()
	imageConfig := struct {
		Architecture string           `json:"architecture"`
		OS           string           `json:"os"`
		OSVersion    string           `json:"os.version,omitempty"`
		Variant      string           `json:"variant,omitempty"`
		Created      time.Time        `json:"created"`
		Config       container.Config `json:"config"`
		History      []historyEntry   `json:"history"`
		RootFS       struct {
			Type    string   `json:"type"`
			DiffIDs []string `json:"diff_ids"`
		} `json:"rootfs"`
	}{
		Architecture: parent.Architecture,
		OS:           parent.Os,
		OSVersion:    parent.OsVersion,
		Variant:      parent.Variant,
		Created:      created,
		Config:       config,
		History:      []historyEntry{{Created: created, Comment: dockerSkillSnapshotComment}},
	}
	imageConfig.RootFS.Type = "layers"
	imageConfig.RootFS.DiffIDs = []string{diffID}
	manifest := []struct {
		Config   string
		RepoTags []string
		Layers   []string
	}{{Config: configName, RepoTags: []string{reference + ":latest"}, Layers: []string{layerName}}}

	var prefix bytes.Buffer
	writer := tar.NewWriter(&prefix)
	for _, entry := range []struct {
		name  string
		value any
	}{{"manifest.json", manifest}, {configName, imageConfig}} {
		data, err := json.Marshal(entry.value)
		if err != nil {
			return nil, err
		}
		if err := writer.WriteHeader(&tar.Header{Name: entry.name, Mode: 0o600, Size: int64(len(data))}); err != nil {
			return nil, err
		}
		if _, err := writer.Write(data); err != nil {
			return nil, err
		}
	}
	if err := writer.WriteHeader(&tar.Header{Name: layerName, Mode: 0o600, Size: size}); err != nil {
		return nil, err
	}
	// Stream the layer after its header, then pad it and end the outer tar.
	padding := (tarBlockSize - size%tarBlockSize) % tarBlockSize
	trailer := make([]byte, padding+2*tarBlockSize)
	return io.MultiReader(&prefix, layer, bytes.NewReader(trailer)), nil
}
