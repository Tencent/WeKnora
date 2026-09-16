package confluence

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	editionCloud  = "cloud"
	editionServer = "server"
)

type config struct {
	edition, baseURL, username, secret string
}

func (c config) cloud() bool { return c.edition == editionCloud }

func parseConfig(ds *types.DataSourceConfig) (config, error) {
	if ds == nil {
		return config{}, datasource.ErrInvalidConfig
	}
	value := func(name string) string { v, _ := ds.Credentials[name].(string); return strings.TrimSpace(v) }
	cfg := config{edition: strings.ToLower(value("edition")), baseURL: strings.TrimRight(value("base_url"), "/"), username: value("username")}
	if cfg.edition == "" {
		cfg.edition = editionServer
	}
	if cfg.edition != editionServer && cfg.edition != editionCloud {
		return config{}, fmt.Errorf("%w: unsupported edition %q", datasource.ErrInvalidCredentials, cfg.edition)
	}
	if cfg.baseURL == "" || cfg.username == "" {
		return config{}, fmt.Errorf("%w: base_url and username are required", datasource.ErrInvalidCredentials)
	}
	if cfg.cloud() {
		cfg.secret = value("api_token")
	} else {
		cfg.secret = value("password")
	}
	if cfg.secret == "" {
		return config{}, fmt.Errorf("%w: credentials are required", datasource.ErrInvalidCredentials)
	}
	return cfg, nil
}

type space struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Name  string `json:"name"`
	Links struct {
		WebUI string `json:"webui"`
	} `json:"_links"`
}
type spaceList struct {
	Results []space `json:"results"`
	Links   struct {
		Next string `json:"next"`
	} `json:"_links"`
}
type serverSpaceList struct {
	Results []struct {
		ID    json.Number `json:"id"`
		Key   string      `json:"key"`
		Name  string      `json:"name"`
		Links struct {
			WebUI string `json:"webui"`
		} `json:"_links"`
	} `json:"results"`
	Links struct {
		Next string `json:"next"`
	} `json:"_links"`
}

type page struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Space  struct {
		Key  string `json:"key"`
		Name string `json:"name"`
	} `json:"space"`
	Version struct {
		Number    int    `json:"number"`
		When      string `json:"when"`
		CreatedAt string `json:"createdAt"`
		By        struct {
			DisplayName string `json:"displayName"`
		} `json:"by"`
	} `json:"version"`
	Links struct {
		WebUI string `json:"webui"`
	} `json:"_links"`
}
type pageList struct {
	Results []page `json:"results"`
	Links   struct {
		Next string `json:"next"`
	} `json:"_links"`
}
type cloudPage struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Status    string `json:"status"`
	SpaceID   string `json:"spaceId"`
	CreatedAt string `json:"createdAt"`
	Version   struct {
		Number    int    `json:"number"`
		CreatedAt string `json:"createdAt"`
	} `json:"version"`
	Links struct {
		WebUI string `json:"webui"`
	} `json:"_links"`
}
type cloudPageList struct {
	Results []cloudPage `json:"results"`
	Links   struct {
		Next string `json:"next"`
	} `json:"_links"`
}
type pageBody struct {
	page
	Body struct {
		View struct {
			Value string `json:"value"`
		} `json:"view"`
	} `json:"body"`
}

// cursor deliberately records a semantic version token, rather than a formatted
// timestamp. Cloud exposes version.number; Server/DC does too on supported APIs.
type cursor struct {
	SpacePages map[string]map[string]string `json:"space_pages"`
}

func decodeCursor(old *types.SyncCursor) cursor {
	c := cursor{SpacePages: map[string]map[string]string{}}
	if old == nil {
		return c
	}
	raw, _ := json.Marshal(old.ConnectorCursor)
	_ = json.Unmarshal(raw, &c)
	if c.SpacePages == nil {
		c.SpacePages = map[string]map[string]string{}
	}
	return c
}
func (c cursor) clone() cursor {
	out := cursor{SpacePages: make(map[string]map[string]string, len(c.SpacePages))}
	for resource, pages := range c.SpacePages {
		out.SpacePages[resource] = make(map[string]string, len(pages))
		for id, version := range pages {
			out.SpacePages[resource][id] = version
		}
	}
	return out
}
func (c cursor) syncCursor() *types.SyncCursor {
	raw, _ := json.Marshal(c)
	fields := map[string]interface{}{}
	_ = json.Unmarshal(raw, &fields)
	return &types.SyncCursor{LastSyncTime: time.Now().UTC(), ConnectorCursor: fields}
}
func pageVersion(p page) string {
	if p.Version.Number > 0 {
		return fmt.Sprintf("v:%d", p.Version.Number)
	}
	if p.Version.When != "" {
		return "t:" + p.Version.When
	}
	return "t:" + p.Version.CreatedAt
}
func pageUpdatedAt(p page) time.Time {
	for _, value := range []string{p.Version.When, p.Version.CreatedAt} {
		if t, err := time.Parse(time.RFC3339, value); err == nil {
			return t
		}
	}
	return time.Time{}
}
func safeFilename(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 32 || strings.ContainsRune(`<>:"/\\|?*`, r) {
			return '_'
		}
		return r
	}, strings.TrimSpace(name))
	if name == "" {
		return "untitled"
	}
	runes := []rune(name)
	if len(runes) > 200 {
		return string(runes[:200])
	}
	return name
}
