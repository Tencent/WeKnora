package gitlab

import (
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

type projectSelection struct {
	ProjectID string   `json:"project_id"`
	Ref       string   `json:"ref"`
	Paths     []string `json:"paths"`
}

type config struct {
	Projects []projectSelection `json:"projects"`
}

func parseConfig(ds *types.DataSourceConfig) (*config, error) {
	if ds == nil {
		return nil, datasource.ErrInvalidConfig
	}
	raw, ok := ds.Settings["projects"]
	if !ok {
		return nil, fmt.Errorf("%w: settings.projects is required", datasource.ErrInvalidConfig)
	}
	items, ok := raw.([]interface{})
	if !ok {
		return nil, fmt.Errorf("%w: settings.projects must be an array", datasource.ErrInvalidConfig)
	}
	out := &config{Projects: make([]projectSelection, 0, len(items))}
	seen := map[string]bool{}
	for _, rawProject := range items {
		m, ok := rawProject.(map[string]interface{})
		if !ok {
			return nil, fmt.Errorf("%w: invalid project selection", datasource.ErrInvalidConfig)
		}
		id, _ := m["project_id"].(string)
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return nil, fmt.Errorf("%w: project_id must be unique and non-empty", datasource.ErrInvalidConfig)
		}
		seen[id] = true
		ref, _ := m["ref"].(string)
		p := projectSelection{ProjectID: id, Ref: strings.TrimSpace(ref)}
		if rawPaths, exists := m["paths"]; exists {
			values, ok := rawPaths.([]interface{})
			if !ok {
				return nil, fmt.Errorf("%w: paths must be an array", datasource.ErrInvalidConfig)
			}
			for _, value := range values {
				s, ok := value.(string)
				if !ok {
					return nil, fmt.Errorf("%w: path must be a string", datasource.ErrInvalidConfig)
				}
				normalized, err := normalizePath(s)
				if err != nil {
					return nil, err
				}
				p.Paths = append(p.Paths, normalized)
			}
		}
		p.Paths = collapsePaths(p.Paths)
		out.Projects = append(out.Projects, p)
	}
	if len(out.Projects) == 0 {
		return nil, fmt.Errorf("%w: at least one project is required", datasource.ErrInvalidConfig)
	}
	return out, nil
}

func normalizePath(value string) (string, error) {
	v := strings.Trim(strings.TrimSpace(value), "/")
	if v == "" {
		return "", nil
	}
	if strings.Contains(v, "\\") {
		return "", fmt.Errorf("%w: path must use forward slashes", datasource.ErrInvalidConfig)
	}
	if path.Clean(v) != v || v == "." || strings.HasPrefix(v, "../") || strings.Contains(v, "/../") {
		return "", fmt.Errorf("%w: invalid repository path", datasource.ErrInvalidConfig)
	}
	return v, nil
}

func collapsePaths(paths []string) []string {
	if len(paths) == 0 {
		return nil
	}
	sort.Strings(paths)
	for _, p := range paths {
		if p == "" {
			return nil
		}
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if len(out) > 0 && (p == out[len(out)-1] || strings.HasPrefix(p, out[len(out)-1]+"/")) {
			continue
		}
		out = append(out, p)
	}
	return out
}
