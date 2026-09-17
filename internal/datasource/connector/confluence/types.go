package confluence

import (
	"encoding/json"
	"fmt"
	"net/url"
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

func configValue(ds *types.DataSourceConfig, name string) string {
	if ds == nil {
		return ""
	}
	if v, ok := ds.Credentials[name].(string); ok {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	if v, ok := ds.Settings[name].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func parseConfig(ds *types.DataSourceConfig) (config, error) {
	if ds == nil {
		return config{}, datasource.ErrInvalidConfig
	}
	cfg := config{
		edition:  strings.ToLower(configValue(ds, "edition")),
		baseURL:  strings.TrimRight(configValue(ds, "base_url"), "/"),
		username: configValue(ds, "username"),
	}
	if cfg.edition == "" {
		cfg.edition = editionServer
	}
	if cfg.edition != editionServer && cfg.edition != editionCloud {
		return config{}, fmt.Errorf("%w: unsupported edition %q", datasource.ErrInvalidCredentials, cfg.edition)
	}
	if cfg.username == "" {
		return config{}, fmt.Errorf("%w: base_url and username are required", datasource.ErrInvalidCredentials)
	}
	normalized, err := normalizeBaseURL(cfg.baseURL, cfg.cloud())
	if err != nil {
		return config{}, err
	}
	cfg.baseURL = normalized
	if cfg.cloud() {
		cfg.secret = configValue(ds, "api_token")
	} else {
		cfg.secret = configValue(ds, "password")
	}
	if cfg.secret == "" {
		return config{}, fmt.Errorf("%w: credentials are required", datasource.ErrInvalidCredentials)
	}
	return cfg, nil
}

func normalizeBaseURL(raw string, cloud bool) (string, error) {
	raw = strings.TrimSpace(strings.TrimRight(raw, "/"))
	if raw == "" {
		return "", fmt.Errorf("%w: base_url and username are required", datasource.ErrInvalidCredentials)
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Scheme == "" {
		return "", fmt.Errorf("%w: invalid base_url", datasource.ErrInvalidCredentials)
	}
	if cloud && isAtlassianCloudHost(parsed.Host) && strings.Trim(parsed.Path, "/") == "" {
		parsed.Path = "/wiki"
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func isAtlassianCloudHost(host string) bool {
	host = strings.ToLower(host)
	return host == "atlassian.net" || strings.HasSuffix(host, ".atlassian.net")
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

// serverSpacePageList accepts both Server envelopes:
//   - GET /rest/api/space/{key}/content/page → flat RestList {results,_links.next}
//   - GET /rest/api/space/{key}/content      → nested {page:{results,_links}}
type serverSpacePageList struct {
	pageList
	Page pageList `json:"page"`
}

func (l serverSpacePageList) pages() []page {
	if len(l.Page.Results) > 0 {
		return l.Page.Results
	}
	return l.Results
}

func (l serverSpacePageList) nextLink() string {
	if l.Page.Links.Next != "" {
		return l.Page.Links.Next
	}
	return l.Links.Next
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

// cursor records a semantic version token per page. Cloud and Server/DC both
// expose version.number on supported APIs.
//
// FullSync / FullSyncBaseline exist only while a force-full run is in progress:
// the service overwrites LastSyncCursor on every checkpoint, so the original
// deletion baseline has to travel inside the checkpoint or retries would either
// re-fetch everything or treat unprocessed pages as deletions.
type cursor struct {
	SpacePages       map[string]map[string]string `json:"space_pages"`
	FullSyncBaseline map[string]map[string]string `json:"full_sync_baseline,omitempty"`
	FullSync         bool                         `json:"full_sync,omitempty"`
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

func clonePageMap(in map[string]map[string]string) map[string]map[string]string {
	out := make(map[string]map[string]string, len(in))
	for resource, pages := range in {
		out[resource] = make(map[string]string, len(pages))
		for id, version := range pages {
			out[resource][id] = version
		}
	}
	return out
}

func (c cursor) clone() cursor {
	return cursor{
		SpacePages:       clonePageMap(c.SpacePages),
		FullSyncBaseline: clonePageMap(c.FullSyncBaseline),
		FullSync:         c.FullSync,
	}
}

func (c cursor) syncCursor() *types.SyncCursor {
	raw, _ := json.Marshal(c)
	fields := map[string]interface{}{}
	_ = json.Unmarshal(raw, &fields)
	return &types.SyncCursor{LastSyncTime: time.Now().UTC(), ConnectorCursor: fields}
}

func prepareSyncCursors(old *types.SyncCursor, forceFull bool) (baseline, next cursor) {
	previous := decodeCursor(old)
	if !forceFull {
		next = previous.clone()
		next.FullSync = false
		next.FullSyncBaseline = nil
		return previous, next
	}
	next = previous.clone()
	if !next.FullSync {
		next.FullSyncBaseline = clonePageMap(previous.SpacePages)
		next.SpacePages = map[string]map[string]string{}
		next.FullSync = true
	}
	baseline.SpacePages = next.FullSyncBaseline
	if baseline.SpacePages == nil {
		baseline.SpacePages = map[string]map[string]string{}
	}
	return baseline, next
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

func pageFileName(title, id string) string {
	base := safeFilename(title)
	id = strings.TrimSpace(id)
	if id == "" {
		return base + ".md"
	}
	return base + "-" + safeFilename(id) + ".md"
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
