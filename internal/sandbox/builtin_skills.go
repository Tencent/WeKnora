package sandbox

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	builtin "github.com/Tencent/WeKnora/internal/builtin/skills"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/moby/moby/client"
)

const builtinManifestMetadata = "weknora_builtin_skills"

// SessionBuiltinSkillsReader uses only the provider control plane. Empty
// sessionID asks about the configured template; a bound session always asks
// about its own image, including when that sandbox is paused.
type SessionBuiltinSkillsReader interface {
	BuiltinSkills(context.Context, string) (*types.BuiltinSkillsManifest, error)
}

// TemplateSkillsEndpoint normalizes the provider endpoint used to bind template declarations.
func TemplateSkillsEndpoint(cfg *Config) string {
	var endpoint string
	switch cfg.Type {
	case SandboxTypeCube:
		endpoint = cfg.CubeAPIURL
	case SandboxTypeE2B:
		endpoint = cfg.E2BAPIURL
	case SandboxTypeDocker:
		endpoint = cfg.DockerHost
	}
	return strings.TrimRight(strings.TrimSpace(endpoint), "/")
}

// ApplyTemplateSkills binds the publisher's declaration to the revision the
// provider actually reports. A name, tag, or envd version is not proof.
func ApplyTemplateSkills(cfg *Config, templates []RemoteTemplate) {
	defer func() {
		for i := range templates {
			templates[i].BuiltinSkillNames = []string{}
			for _, e := range builtin.CompatibleEntries(templates[i].BuiltinSkills) {
				templates[i].BuiltinSkillNames = append(templates[i].BuiltinSkillNames, e.Name)
			}
		}
	}()
	if cfg.Type == SandboxTypeDocker {
		return
	}
	d := cfg.TemplateSkills
	if d == nil || d.Provider != string(cfg.Type) ||
		strings.TrimRight(strings.TrimSpace(d.Endpoint), "/") != TemplateSkillsEndpoint(cfg) || d.Revision == "" {
		return
	}
	for i := range templates {
		t := &templates[i]
		if t.BuiltinSkills == nil && t.ID == d.TemplateID && t.Version == d.Revision && IsTemplateReady(t.Status) {
			raw, _ := json.Marshal(d.Manifest)
			t.BuiltinSkills = builtin.ParseManifest(string(raw))
		}
	}
}

func (c *DockerRemoteClient) inspectBuiltinImage(
	ctx context.Context,
	ref string,
) (string, *types.BuiltinSkillsManifest, error) {
	i, err := c.api.ImageInspect(ctx, ref)
	if err != nil {
		return ref, nil, err
	}
	if i.Config == nil {
		return i.ID, nil, nil
	}
	return i.ID, builtin.ParseManifest(i.Config.Labels[builtin.ManifestLabel]), nil
}

func (m *SessionBoundManager) templateBuiltinSkills(ctx context.Context) (string, *types.BuiltinSkillsManifest, error) {
	ref := EffectiveTemplateID(m.config)
	if docker, ok := m.metadataClient.(*DockerRemoteClient); ok {
		return docker.inspectBuiltinImage(ctx, ref)
	}
	if m.config.BuiltinSnapshotSkills != nil {
		return ref, m.config.BuiltinSnapshotSkills, nil
	}
	catalog, ok := m.metadataClient.(RemoteTemplateCatalog)
	if !ok || m.config.TemplateSkills == nil {
		return ref, nil, nil
	}
	templates, err := catalog.ListTemplates(ctx)
	if err != nil {
		return ref, nil, err
	}
	ApplyTemplateSkills(m.config, templates)
	for _, t := range templates {
		if t.ID == ref {
			return ref, t.BuiltinSkills, nil
		}
	}
	return ref, nil, nil
}

// BuiltinSkills reads the template or bound session manifest without entering the sandbox.
func (m *SessionBoundManager) BuiltinSkills(
	ctx context.Context,
	sessionID string,
) (*types.BuiltinSkillsManifest, error) {
	return m.builtinSkills(ctx, sessionID, false)
}

// BuiltinSkillsForRun follows the lifecycle's next-turn rollout decision using
// only its binding state. Browser preview continues to inspect the live image.
func (m *SessionBoundManager) BuiltinSkillsForRun(
	ctx context.Context,
	sessionID string,
) (*types.BuiltinSkillsManifest, error) {
	return m.builtinSkills(ctx, sessionID, true)
}

func (m *SessionBoundManager) builtinSkills(
	ctx context.Context,
	sessionID string,
	planned bool,
) (*types.BuiltinSkillsManifest, error) {
	if sessionID != "" {
		key, err := m.sessionKey(ctx, sessionID)
		if err != nil {
			return nil, err
		}
		binding, err := m.bindings.Get(ctx, key)
		if err != nil {
			return nil, err
		}
		if planned && binding != nil && binding.StaleAt != nil && m.lifecycle.shouldRebuildStaleBinding(ctx, key) {
			binding = nil
		}
		if binding != nil {
			if binding.Provider != m.client.Provider() {
				return nil, nil
			}
			if docker, ok := m.metadataClient.(*DockerRemoteClient); ok {
				i, err := docker.api.ContainerInspect(ctx, binding.SandboxID, client.ContainerInspectOptions{})
				if err != nil {
					return nil, err
				}
				_, manifest, err := docker.inspectBuiltinImage(ctx, i.Container.Image)
				return manifest, err
			}
			// These SDK adapters implement Get through Connect, which may
			// resume a paused VM. Only List is a read-only control-plane call.
			summaries, err := m.client.List(ctx, RemoteListFilter{
				Metadata: map[string]string{
					remoteMetadataTenantID:  strconv.FormatUint(key.TenantID, 10),
					remoteMetadataSessionID: key.SessionID,
				},
				States: []RemoteSandboxState{RemoteStateRunning, RemoteStatePaused, RemoteStateTransitioning},
			})
			if err != nil {
				return nil, err
			}
			for _, summary := range summaries {
				if summary.ID == binding.SandboxID {
					return builtin.ParseManifest(summary.Metadata[builtinManifestMetadata]), nil
				}
			}
			return nil, nil
		}
	}
	_, manifest, err := m.templateBuiltinSkills(ctx)
	return manifest, err
}

// Stamp capabilities when the sandbox is first created, so a later template
// update cannot change an existing session's advertised resources.
func (m *SessionBoundManager) prepareBuiltinCreate(
	ctx context.Context,
	request RemoteCreateRequest,
) RemoteCreateRequest {
	ref, manifest, err := m.templateBuiltinSkills(ctx)
	if err != nil {
		return request
	} // Metadata unavailable must not prevent a normal shell session.
	if m.client.Provider() == SandboxTypeDocker && ref != "" {
		request.TemplateID = ref
	}
	if manifest != nil && m.client.Capabilities().SupportsMetadata {
		if request.Metadata == nil {
			request.Metadata = map[string]string{}
		}
		raw, _ := json.Marshal(manifest)
		request.Metadata[builtinManifestMetadata] = string(raw)
	}
	return request
}
