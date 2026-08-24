// Images as templates for the docker backend.
//
// The settings wizard asks every backend the same two questions — which
// templates exist, and please make sure the standard one does. For Docker the
// answer is images on the daemon: an image is a pre-baked filesystem a sandbox
// starts from, which is exactly what a Cube or E2B template is.
//
// Two differences from the MicroVM backends shape this file:
//
//   - A daemon holds every image its host ever pulled, most of which have
//     nothing to do with sandboxes. Listing all of them would bury the one the
//     admin needs, so the catalog only reports images that are recognisably
//     sandbox templates.
//   - Making the standard template exist means pulling, which can take minutes
//     on a cold host. The pull therefore runs in the background and the
//     template reports "building" until it lands, mirroring how the other
//     backends report an in-flight template build.

package sandbox

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/client"
)

// dockerTemplateLabel marks an image as a WeKnora sandbox template. Images
// built by an operator can opt into the catalog by carrying it.
const dockerTemplateLabel = "com.weknora.sandbox.template"

// dockerImagePulls tracks background pulls per daemon endpoint and image, so a
// second refresh reports the in-flight pull instead of starting another one.
var dockerImagePulls = struct {
	mu    sync.Mutex
	state map[string]*dockerPullState
}{state: make(map[string]*dockerPullState)}

type dockerPullState struct {
	started time.Time
	done    bool
	err     error
}

// ListTemplates reports the sandbox images available on this daemon.
func (c *DockerRemoteClient) ListTemplates(ctx context.Context) ([]RemoteTemplate, error) {
	listed, err := c.api.ImageList(ctx, client.ImageListOptions{})
	if err != nil {
		return nil, dockerError("ListTemplates", err)
	}

	templates := make([]RemoteTemplate, 0, len(listed.Items))
	configured := strings.TrimSpace(c.settings.Image)
	configuredPresent := false
	for _, image := range listed.Items {
		for _, tag := range image.RepoTags {
			if tag == "" || tag == "<none>:<none>" {
				continue
			}
			standard := isStandardTemplateImage(tag)
			if !standard && image.Labels[dockerTemplateLabel] != "true" && tag != configured {
				continue
			}
			if tag == configured {
				configuredPresent = true
			}
			templates = append(templates, RemoteTemplate{
				ID:        tag,
				Name:      tag,
				Status:    "ready",
				Image:     tag,
				Version:   image.ID,
				Standard:  standard,
				CreatedAt: time.Unix(image.Created, 0).UTC().Format(time.RFC3339),
			})
		}
	}

	// A configured image the daemon has not pulled yet is still the template
	// this config will use. Hiding it would tell the admin their own choice
	// does not exist, when all that is missing is a pull.
	if configured != "" && !configuredPresent {
		templates = append(templates, c.pendingTemplate(configured))
	}
	return templates, nil
}

// pendingTemplate describes an image that is not on the daemon yet, reporting
// any background pull's outcome.
func (c *DockerRemoteClient) pendingTemplate(image string) RemoteTemplate {
	template := RemoteTemplate{
		ID:       image,
		Name:     image,
		Image:    image,
		Status:   "missing",
		Standard: isStandardTemplateImage(image),
	}
	dockerImagePulls.mu.Lock()
	state, ok := dockerImagePulls.state[c.pullKey(image)]
	dockerImagePulls.mu.Unlock()
	switch {
	case !ok:
		return template
	case !state.done:
		template.Status = "building"
	case state.err != nil:
		template.Status = "failed"
		template.Error = state.err.Error()
	}
	return template
}

// EnsureStandardTemplate makes the configured image available on the daemon,
// pulling it in the background when it is missing.
func (c *DockerRemoteClient) EnsureStandardTemplate(ctx context.Context) (*RemoteTemplate, error) {
	image := strings.TrimSpace(c.settings.Image)
	if image == "" {
		image = DefaultDockerImage
	}
	if _, err := c.api.ImageInspect(ctx, image); err == nil {
		return &RemoteTemplate{
			ID:       image,
			Name:     image,
			Image:    image,
			Status:   "ready",
			Standard: isStandardTemplateImage(image),
		}, nil
	}

	key := c.pullKey(image)
	dockerImagePulls.mu.Lock()
	state, running := dockerImagePulls.state[key]
	if running && !state.done {
		dockerImagePulls.mu.Unlock()
		pending := c.pendingTemplate(image)
		return &pending, nil
	}
	dockerImagePulls.state[key] = &dockerPullState{started: time.Now()}
	dockerImagePulls.mu.Unlock()

	// Detached from the request: a cold pull outlives the HTTP call that
	// asked for it, and cancelling mid-pull would leave a partial download
	// that the next refresh restarts from scratch.
	go c.pullInBackground(context.WithoutCancel(ctx), key, image)

	pending := c.pendingTemplate(image)
	return &pending, nil
}

func (c *DockerRemoteClient) pullInBackground(ctx context.Context, key, image string) {
	pullCtx, cancel := context.WithTimeout(ctx, dockerImagePullBudget)
	defer cancel()

	err := func() error {
		body, err := c.api.ImagePull(pullCtx, image, client.ImagePullOptions{})
		if err != nil {
			return err
		}
		return awaitImagePull(pullCtx, body)
	}()

	dockerImagePulls.mu.Lock()
	defer dockerImagePulls.mu.Unlock()
	if err != nil {
		dockerImagePulls.state[key] = &dockerPullState{done: true, err: err}
		return
	}
	// A finished pull leaves no state behind: the image is now visible to
	// ImageInspect, which is a better source of truth than our bookkeeping.
	delete(dockerImagePulls.state, key)
}

// dockerImagePullBudget bounds a background pull. Sandbox images are large but
// not unbounded; a pull still running after this either hit a stalled registry
// or a network the daemon cannot reach.
const dockerImagePullBudget = 30 * time.Minute

func (c *DockerRemoteClient) pullKey(image string) string {
	return fmt.Sprintf("%s|%s", c.settings.Endpoint.key(), image)
}

var _ RemoteTemplateCatalog = (*DockerRemoteClient)(nil)
