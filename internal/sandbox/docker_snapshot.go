package sandbox

import (
	"context"
	"strings"
	"unicode"

	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
)

const (
	// dockerSkillSnapshotRepo is the local image namespace every skill
	// snapshot is committed into. It is not a registry path: these tags are
	// never pulled, and ListTemplates hides anything under it so an admin
	// cannot pick a baked skill image as the config's base template.
	dockerSkillSnapshotRepo = "weknora-skill"

	dockerSkillSnapshotLabel       = "com.weknora.sandbox.skill-snapshot"
	dockerSkillSnapshotSourceLabel = "com.weknora.sandbox.skill-snapshot-source"
)

// CreateSnapshot commits the container's filesystem into a tagged local image.
//
// The Engine pauses the container for the duration of the commit unless we
// opt out (we do not): that is the same "provider pauses while snapshotting"
// contract Cube and E2B honour. The resulting tag is a template ID — Create
// accepts it as RemoteCreateRequest.TemplateID — so the skill install path
// needs no Docker-specific branch past this adapter.
func (c *DockerRemoteClient) CreateSnapshot(
	ctx context.Context, sandboxID string, name string,
) (RemoteSnapshotRef, error) {
	id := strings.TrimSpace(sandboxID)
	if id == "" {
		return RemoteSnapshotRef{}, dockerInvalidRequest("CreateSnapshot", "sandbox ID is required")
	}
	reference, err := dockerSkillSnapshotReference(name, id)
	if err != nil {
		return RemoteSnapshotRef{}, err
	}

	committed, err := c.api.ContainerCommit(ctx, id, client.ContainerCommitOptions{
		Reference: reference,
		Comment:   "weknora skill snapshot",
		Changes: []string{
			"LABEL " + dockerSkillSnapshotLabel + "=true",
			"LABEL " + dockerSkillSnapshotSourceLabel + "=" + id,
		},
	})
	if err != nil {
		return RemoteSnapshotRef{}, dockerError("CreateSnapshot", err)
	}
	if strings.TrimSpace(committed.ID) == "" {
		return RemoteSnapshotRef{}, dockerInvalidRequest(
			"CreateSnapshot", "provider returned an empty snapshot ID")
	}
	return RemoteSnapshotRef{
		ID:    dockerCanonicalSnapshotID(reference),
		Names: []string{dockerCanonicalSnapshotID(reference)},
	}, nil
}

// DeleteSnapshot removes a committed skill image. A missing image is success:
// the reaper and the install-compensation path both retry deletes.
func (c *DockerRemoteClient) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	id := strings.TrimSpace(snapshotID)
	if id == "" {
		return dockerInvalidRequest("DeleteSnapshot", "snapshot ID is required")
	}
	_, err := c.api.ImageRemove(ctx, id, client.ImageRemoveOptions{Force: true})
	if err != nil {
		normalized := dockerError("DeleteSnapshot", err)
		if IsRemoteNotFound(normalized) {
			return nil
		}
		return normalized
	}
	return nil
}

// ListSnapshots returns skill-snapshot images on this daemon. An empty
// sandboxID lists every skill snapshot; a non-empty one keeps those committed
// from that container.
func (c *DockerRemoteClient) ListSnapshots(
	ctx context.Context, sandboxID string,
) ([]RemoteSnapshotRef, error) {
	filters := client.Filters{}.Add("label", dockerSkillSnapshotLabel+"=true")
	if src := strings.TrimSpace(sandboxID); src != "" {
		filters = filters.Add("label", dockerSkillSnapshotSourceLabel+"="+src)
	}
	listed, err := c.api.ImageList(ctx, client.ImageListOptions{All: true, Filters: filters})
	if err != nil {
		return nil, dockerError("ListSnapshots", err)
	}

	wantSource := strings.TrimSpace(sandboxID)
	out := make([]RemoteSnapshotRef, 0, len(listed.Items))
	for _, item := range listed.Items {
		if !dockerImageIsSkillSnapshot(item) {
			continue
		}
		if wantSource != "" && item.Labels[dockerSkillSnapshotSourceLabel] != wantSource {
			continue
		}
		out = append(out, dockerSnapshotRef(item))
	}
	return out, nil
}

func dockerImageIsSkillSnapshot(item image.Summary) bool {
	if item.Labels[dockerSkillSnapshotLabel] == "true" {
		return true
	}
	for _, tag := range item.RepoTags {
		if dockerIsSkillSnapshotRef(tag) {
			return true
		}
	}
	return false
}

func dockerIsSkillSnapshotRef(ref string) bool {
	trimmed := strings.TrimSpace(ref)
	prefix := dockerSkillSnapshotRepo + "/"
	if strings.HasPrefix(trimmed, prefix) {
		return true
	}
	// ImageList sometimes returns the docker.io/ prefix. The tag we mint
	// never has a registry, so this is only a listing alias.
	return strings.HasPrefix(trimmed, "docker.io/"+prefix)
}

func dockerSnapshotRef(item image.Summary) RemoteSnapshotRef {
	names := make([]string, 0, len(item.RepoTags))
	id := strings.TrimSpace(item.ID)
	for _, tag := range item.RepoTags {
		if tag == "" || tag == "<none>:<none>" {
			continue
		}
		canonical := dockerCanonicalSnapshotID(tag)
		names = append(names, canonical)
		if dockerIsSkillSnapshotRef(canonical) && (id == "" || !dockerIsSkillSnapshotRef(id)) {
			id = canonical
		}
	}
	if id == "" && len(names) > 0 {
		id = names[0]
	}
	return RemoteSnapshotRef{ID: id, Names: names}
}

func dockerCanonicalSnapshotID(ref string) string {
	trimmed := strings.TrimPrefix(strings.TrimSpace(ref), "docker.io/")
	return strings.TrimSuffix(trimmed, ":latest")
}

func dockerSkillSnapshotReference(name, sandboxID string) (string, error) {
	base := dockerSanitizeImageName(name)
	if base == "" {
		base = dockerSanitizeImageName(sandboxID)
	}
	if base == "" {
		return "", dockerInvalidRequest("CreateSnapshot", "snapshot name is required")
	}
	return dockerSkillSnapshotRepo + "/" + base, nil
}

// dockerSanitizeImageName maps an install-generated snapshot name onto a
// single Docker path component: lowercase [a-z0-9] with interior - separators.
func dockerSanitizeImageName(raw string) string {
	var b strings.Builder
	lastSep := true
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			lastSep = false
		case r == '.' || r == '_' || r == '-':
			if lastSep || b.Len() == 0 {
				continue
			}
			b.WriteByte('-')
			lastSep = true
		}
	}
	out := strings.Trim(b.String(), "-")
	const maxName = 80
	if len(out) > maxName {
		out = strings.Trim(out[:maxName], "-")
	}
	return out
}
