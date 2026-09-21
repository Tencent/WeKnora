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

	// DockerForkSnapshotRepo is the local image namespace for session-fork
	// snapshots. It is deliberately separate from dockerSkillSnapshotRepo: the
	// skill reaper prunes by the skill namespace and labels, and letting fork
	// snapshots share them would have each reaper collecting the other's
	// images.
	DockerForkSnapshotRepo = "weknora-fork"

	dockerForkSnapshotLabel       = "com.weknora.sandbox.fork-snapshot"
	dockerForkSnapshotSourceLabel = "com.weknora.sandbox.fork-snapshot-source"
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
	return c.commitSnapshot(
		ctx, id, name,
		dockerSkillSnapshotRepo,
		"weknora skill snapshot",
		dockerSkillSnapshotLabel,
		dockerSkillSnapshotSourceLabel,
		"CreateSnapshot",
	)
}

// CreateForkSnapshot commits the container into the fork image namespace.
// Same shape as CreateSnapshot; a different repo and labels keep the skill
// reaper from collecting (or being collected by) session-fork images.
func (c *DockerRemoteClient) CreateForkSnapshot(
	ctx context.Context, sandboxID string, name string,
) (RemoteSnapshotRef, error) {
	id := strings.TrimSpace(sandboxID)
	if id == "" {
		return RemoteSnapshotRef{}, dockerInvalidRequest("CreateForkSnapshot", "sandbox ID is required")
	}
	return c.commitSnapshot(
		ctx, id, name,
		DockerForkSnapshotRepo,
		"weknora fork snapshot",
		dockerForkSnapshotLabel,
		dockerForkSnapshotSourceLabel,
		"CreateForkSnapshot",
	)
}

func (c *DockerRemoteClient) commitSnapshot(
	ctx context.Context,
	sandboxID, name, repo, comment, label, sourceLabel, op string,
) (RemoteSnapshotRef, error) {
	reference, err := dockerSnapshotReference(repo, name, sandboxID, op)
	if err != nil {
		return RemoteSnapshotRef{}, err
	}

	committed, err := c.api.ContainerCommit(ctx, sandboxID, client.ContainerCommitOptions{
		Reference: reference,
		Comment:   comment,
		Changes: []string{
			"LABEL " + label + "=true",
			"LABEL " + sourceLabel + "=" + sandboxID,
		},
	})
	if err != nil {
		return RemoteSnapshotRef{}, dockerError(op, err)
	}
	if strings.TrimSpace(committed.ID) == "" {
		return RemoteSnapshotRef{}, dockerInvalidRequest(op, "provider returned an empty snapshot ID")
	}
	return RemoteSnapshotRef{
		ID:    dockerCanonicalSnapshotID(reference),
		Names: []string{dockerCanonicalSnapshotID(reference)},
	}, nil
}

// DeleteSnapshot removes a committed skill image. A missing image is success:
// the reaper and the install-compensation path both retry deletes.
//
// PruneChildren is what makes the delete reclaim anything. Each generation is
// committed from a container started off the previous one, so generation N+1
// holds generation N's layers as ancestors. Untagging N alone therefore frees
// nothing while N+1 exists — which is correct and unavoidable — but the layers
// of a chain whose every tag has been retired would stay on disk forever
// without this, because the Go client defaults to noprune. Layers a live image
// still references are refcounted by the daemon, so cascading here cannot take
// the current image's storage out from under it.
func (c *DockerRemoteClient) DeleteSnapshot(ctx context.Context, snapshotID string) error {
	id := strings.TrimSpace(snapshotID)
	if id == "" {
		return dockerInvalidRequest("DeleteSnapshot", "snapshot ID is required")
	}
	_, err := c.api.ImageRemove(ctx, id, client.ImageRemoveOptions{
		PruneChildren: true,
	})
	if err != nil {
		normalized := dockerError("DeleteSnapshot", err)
		if IsRemoteNotFound(normalized) {
			return nil
		}
		return normalized
	}
	if dockerIsForkSnapshotRef(id) {
		c.pruneDanglingForkImages(ctx)
	} else {
		c.pruneDanglingSkillImages(ctx)
	}
	return nil
}

// pruneDanglingSkillImages drops skill-snapshot images that no longer carry a
// tag. Nothing can boot one: the ledger addresses snapshots by the tag minted
// in CreateSnapshot and never by digest, so an untagged one is unreachable by
// construction. They are left behind by a commit whose ledger write or pointer
// switch failed, and by every delete that ran before PruneChildren was set.
//
// Best effort by nature — reclaiming storage must never turn a successful
// delete into a failed one. An image a container still holds comes back as a
// conflict and is skipped; the next pass retries once that container is gone.
func (c *DockerRemoteClient) pruneDanglingSkillImages(ctx context.Context) {
	listed, err := c.api.ImageList(ctx, client.ImageListOptions{
		All:     true,
		Filters: client.Filters{}.Add("label", dockerSkillSnapshotLabel+"=true"),
	})
	if err != nil {
		return
	}
	for _, item := range listed.Items {
		if !dockerImageIsSkillSnapshot(item) || dockerImageHasTag(item) {
			continue
		}
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		_, _ = c.api.ImageRemove(ctx, id, client.ImageRemoveOptions{PruneChildren: true})
	}
}

// pruneDanglingForkImages drops untagged fork-snapshot images. Same rationale
// as pruneDanglingSkillImages; the filter is the fork label so a skill delete
// cannot sweep a live fork image and vice versa.
func (c *DockerRemoteClient) pruneDanglingForkImages(ctx context.Context) {
	listed, err := c.api.ImageList(ctx, client.ImageListOptions{
		All:     true,
		Filters: client.Filters{}.Add("label", dockerForkSnapshotLabel+"=true"),
	})
	if err != nil {
		return
	}
	for _, item := range listed.Items {
		if !dockerImageIsForkSnapshot(item) || dockerImageHasTag(item) {
			continue
		}
		id := strings.TrimSpace(item.ID)
		if id == "" {
			continue
		}
		_, _ = c.api.ImageRemove(ctx, id, client.ImageRemoveOptions{PruneChildren: true})
	}
}

func dockerImageHasTag(item image.Summary) bool {
	for _, tag := range item.RepoTags {
		if strings.TrimSpace(tag) != "" && tag != "<none>:<none>" {
			return true
		}
	}
	return false
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

func dockerImageIsForkSnapshot(item image.Summary) bool {
	if item.Labels[dockerForkSnapshotLabel] == "true" {
		return true
	}
	for _, tag := range item.RepoTags {
		if dockerIsForkSnapshotRef(tag) {
			return true
		}
	}
	return false
}

func dockerIsSkillSnapshotRef(ref string) bool {
	return dockerIsRepoSnapshotRef(ref, dockerSkillSnapshotRepo)
}

func dockerIsForkSnapshotRef(ref string) bool {
	return dockerIsRepoSnapshotRef(ref, DockerForkSnapshotRepo)
}

func dockerIsLocalSnapshotRef(ref string) bool {
	return dockerIsSkillSnapshotRef(ref) || dockerIsForkSnapshotRef(ref)
}

func dockerIsRepoSnapshotRef(ref, repo string) bool {
	trimmed := strings.TrimSpace(ref)
	prefix := repo + "/"
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

func dockerSnapshotReference(repo, name, sandboxID, op string) (string, error) {
	base := dockerSanitizeImageName(name)
	if base == "" {
		base = dockerSanitizeImageName(sandboxID)
	}
	if base == "" {
		return "", dockerInvalidRequest(op, "snapshot name is required")
	}
	return repo + "/" + base, nil
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

var _ forkSnapshotCreator = (*DockerRemoteClient)(nil)
