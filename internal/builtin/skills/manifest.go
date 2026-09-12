package skills

import (
	"encoding/json"
	"fmt"

	"github.com/Tencent/WeKnora/internal/types"
)

// ManifestLabel is the image label containing the complete built-in skill declaration.
const ManifestLabel = "org.weknora.skills.manifest"

// PublishedManifest describes precisely the resources in this server release.
func PublishedManifest() *types.BuiltinSkillsManifest {
	m, _ := PublishedManifestForProfile("office-core")
	return m
}

// PublishedManifestForProfile declares only the packages installed in this image tier.
func PublishedManifestForProfile(profile string) (*types.BuiltinSkillsManifest, error) {
	if profile != "office-core" {
		return nil, fmt.Errorf("unknown builtin skills profile %q", profile)
	}
	m := &types.BuiltinSkillsManifest{SchemaVersion: 1, Profile: profile, Version: Version}
	for _, e := range List() {
		if e.Distribution != "builtin" || e.Category != "office" {
			continue
		}
		m.Skills = append(m.Skills, types.BuiltinSkillDeclaration{Name: e.Name, Digest: e.Digest, Verified: true})
	}
	return m, nil
}

// ParseManifest decodes a bounded, supported manifest; invalid declarations return nil.
func ParseManifest(raw string) *types.BuiltinSkillsManifest {
	if len(raw) == 0 || len(raw) > 64*1024 {
		return nil
	}
	var m types.BuiltinSkillsManifest
	if json.Unmarshal([]byte(raw), &m) != nil || m.SchemaVersion != 1 || len(m.Skills) > 128 {
		return nil
	}
	return &m
}

// CompatibleEntries never substitutes newer instructions for an older image.
func CompatibleEntries(m *types.BuiltinSkillsManifest) []Entry {
	if m == nil || m.SchemaVersion != 1 {
		return nil
	}
	declared := map[string]types.BuiltinSkillDeclaration{}
	for _, s := range m.Skills {
		if _, duplicate := declared[s.Name]; duplicate {
			return nil
		}
		declared[s.Name] = s
	}
	var entries []Entry
	for _, e := range List() {
		d := declared[e.Name]
		if e.Distribution != "builtin" || !d.Verified || d.Digest != e.Digest {
			continue
		}
		entries = append(entries, e)
	}
	return entries
}
