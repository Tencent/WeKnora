package skills

import (
	"fmt"
	"path"
	"sort"

	builtin "github.com/Tencent/WeKnora/internal/builtin/skills"
	"github.com/Tencent/WeKnora/internal/types"
)

// BuiltinSkillSource reads the reviewed server resources while execution uses
// their identical, preinstalled counterparts in the sandbox image.
type BuiltinSkillSource struct{ entries map[string]builtin.Entry }

// NewBuiltinSkillSource selects resources compatible with the declared image manifest.
func NewBuiltinSkillSource(manifest *types.BuiltinSkillsManifest) *BuiltinSkillSource {
	s := &BuiltinSkillSource{entries: map[string]builtin.Entry{}}
	for _, e := range builtin.CompatibleEntries(manifest) {
		s.entries[e.Name] = e
	}
	return s
}

// DiscoverSkills returns metadata for the available image resources.
func (s *BuiltinSkillSource) DiscoverSkills() ([]*SkillMetadata, error) {
	var result []*SkillMetadata
	for name := range s.entries {
		skill, err := s.LoadSkillInstructions(name)
		if err != nil {
			return nil, err
		}
		result = append(result, &SkillMetadata{Name: name, Description: skill.Description, BasePath: skill.BasePath})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (s *BuiltinSkillSource) files(name string) (map[string][]byte, error) {
	e, ok := s.entries[name]
	if !ok {
		return nil, fmt.Errorf("skill not found: %s", name)
	}
	return builtin.FilesForEntry(e)
}

// LoadSkillInstructions loads instructions from the selected resource version.
func (s *BuiltinSkillSource) LoadSkillInstructions(name string) (*Skill, error) {
	files, err := s.files(name)
	if err != nil {
		return nil, err
	}
	skill, err := ParseSkillFile(string(files[SkillFileName]))
	if err != nil {
		return nil, err
	}
	skill.BasePath = path.Join(builtin.ImageRoot, name)
	skill.FilePath = path.Join(skill.BasePath, SkillFileName)
	skill.Loaded = true
	return skill, nil
}

// LoadSkillFile reads a resource after validating its relative path.
func (s *BuiltinSkillSource) LoadSkillFile(name, rel string) (*SkillFile, error) {
	clean, err := safeSkillRelPath(rel)
	if err != nil {
		return nil, err
	}
	files, err := s.files(name)
	if err != nil {
		return nil, err
	}
	content, ok := files[clean]
	if !ok {
		return nil, fmt.Errorf("skill file not found: %s", rel)
	}
	return &SkillFile{
		Name:     clean,
		Path:     path.Join(builtin.ImageRoot, name, clean),
		Content:  string(content),
		IsScript: IsScript(clean),
	}, nil
}

// ListSkillFiles lists files in the selected skill package.
func (s *BuiltinSkillSource) ListSkillFiles(name string) ([]string, error) {
	files, err := s.files(name)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// GetSkillBasePath returns the preinstalled directory for a skill.
func (s *BuiltinSkillSource) GetSkillBasePath(name string) (string, error) {
	if _, ok := s.entries[name]; !ok {
		return "", fmt.Errorf("skill not found: %s", name)
	}
	return path.Join(builtin.ImageRoot, name), nil
}

// RemoteScriptPath resolves a script in the sandbox image.
func (s *BuiltinSkillSource) RemoteScriptPath(name, rel string) (string, error) {
	f, err := s.LoadSkillFile(name, rel)
	if err != nil {
		return "", err
	}
	return f.Path, nil
}

// ImageSkillSources routes by name. Earlier sources take precedence, so a
// workspace's installed skill can override a same-named built-in skill.
type ImageSkillSources struct {
	byName   map[string]SkillSource
	metadata []*SkillMetadata
}

// MergeImageSkillSources combines sources with earlier sources taking precedence.
func MergeImageSkillSources(sources ...SkillSource) (*ImageSkillSources, error) {
	s := &ImageSkillSources{byName: map[string]SkillSource{}}
	for _, source := range sources {
		if source == nil {
			continue
		}
		metadata, err := source.DiscoverSkills()
		if err != nil {
			return nil, err
		}
		for _, m := range metadata {
			if s.byName[m.Name] != nil {
				continue
			}
			s.byName[m.Name] = source
			s.metadata = append(s.metadata, m)
		}
	}
	return s, nil
}

func (s *ImageSkillSources) source(name string) (SkillSource, error) {
	if src := s.byName[name]; src != nil {
		return src, nil
	}
	return nil, fmt.Errorf("skill not found: %s", name)
}

// DiscoverSkills returns metadata for the available image resources.
func (s *ImageSkillSources) DiscoverSkills() ([]*SkillMetadata, error) { return s.metadata, nil }

// LoadSkillInstructions loads instructions from the selected resource version.
func (s *ImageSkillSources) LoadSkillInstructions(n string) (*Skill, error) {
	src, e := s.source(n)
	if e != nil {
		return nil, e
	}
	return src.LoadSkillInstructions(n)
}

// LoadSkillFile reads a resource after validating its relative path.
func (s *ImageSkillSources) LoadSkillFile(n, r string) (*SkillFile, error) {
	src, e := s.source(n)
	if e != nil {
		return nil, e
	}
	return src.LoadSkillFile(n, r)
}

// ListSkillFiles lists files in the selected skill package.
func (s *ImageSkillSources) ListSkillFiles(n string) ([]string, error) {
	src, e := s.source(n)
	if e != nil {
		return nil, e
	}
	return src.ListSkillFiles(n)
}

// GetSkillBasePath returns the preinstalled directory for a skill.
func (s *ImageSkillSources) GetSkillBasePath(n string) (string, error) {
	src, e := s.source(n)
	if e != nil {
		return "", e
	}
	return src.GetSkillBasePath(n)
}

// RemoteScriptPath resolves a script in the sandbox image.
func (s *ImageSkillSources) RemoteScriptPath(n, r string) (string, error) {
	src, e := s.source(n)
	if e != nil {
		return "", e
	}
	if image, ok := src.(imageSkillSource); ok {
		return image.RemoteScriptPath(n, r)
	}
	return "", fmt.Errorf("skill is not in the image: %s", n)
}
