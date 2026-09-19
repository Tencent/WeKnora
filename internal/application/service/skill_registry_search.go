package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

const (
	// A chat turn waits on this, so it is short: a registry that has not
	// answered in ten seconds is reported unreachable rather than awaited.
	skillRegistrySearchTimeout = 10 * time.Second
	// A preview downloads the whole bundle to read its SKILL.md. The fetch
	// client allows minutes for an admin-initiated install; a card preview
	// gets far less before the model is told the source did not resolve.
	skillSourcePreviewTimeout = 60 * time.Second
	skillRegistryMaxBody      = 2 << 20
	skillHubCNPageOrigin      = "https://skillhub.cn"
	registryNameClawHub       = "ClawHub"
	registryNameSkillHub      = "SkillHub"
)

var (
	skillSearchHTTPOnce    sync.Once
	skillSearchHTTPDefault *http.Client
)

func skillSearchHTTPClient() *http.Client {
	skillSearchHTTPOnce.Do(func() {
		cfg := secutils.DefaultSSRFSafeHTTPClientConfig()
		cfg.Timeout = skillRegistrySearchTimeout
		skillSearchHTTPDefault = secutils.NewSSRFSafeHTTPClient(cfg)
	})
	return skillSearchHTTPDefault
}

// skillRegistrySearch asks public registries for skills by keyword.
//
// ClawHub is asked for vector-ranked results and already federates the
// skills.sh directory, so a skills.sh listing arrives as a skills-sh: locator
// without a second query. SkillHub is asked as well because it is the
// registry reachable from mainland deployments, and it mirrors much of
// ClawHub, which the merge folds together.
type skillRegistrySearch struct {
	client         *http.Client
	clawHubOrigin  string
	skillHubOrigin string
	// fetchClient downloads a previewed bundle; nil is the default
	// SSRF-safe source client an admin install uses.
	fetchClient *http.Client
}

func newSkillRegistrySearch(client *http.Client) *skillRegistrySearch {
	if client == nil {
		client = skillSearchHTTPClient()
	}
	return &skillRegistrySearch{
		client:         client,
		clawHubOrigin:  defaultSkillRegistryOrigin,
		skillHubOrigin: skillHubCNAPIOrigin,
	}
}

type registryHits struct {
	registry   string
	candidates []tools.SkillCandidate
	err        error
}

func (r *skillRegistrySearch) search(
	ctx context.Context, query string, limit int,
) (*tools.SkillSearchResult, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("query is required")
	}
	if limit <= 0 {
		limit = 6
	}
	searches := []struct {
		name string
		run  func(context.Context, string, int) ([]tools.SkillCandidate, error)
	}{
		{registryNameClawHub, r.searchClawHub},
		{registryNameSkillHub, r.searchSkillHub},
	}
	hits := make([]registryHits, len(searches))
	var wg sync.WaitGroup
	for i, s := range searches {
		wg.Add(1)
		go func(i int, name string, run func(context.Context, string, int) ([]tools.SkillCandidate, error)) {
			defer wg.Done()
			found, err := run(ctx, query, limit)
			hits[i] = registryHits{registry: name, candidates: found, err: err}
		}(i, s.name, s.run)
	}
	wg.Wait()

	result := &tools.SkillSearchResult{}
	lists := make([][]tools.SkillCandidate, 0, len(hits))
	for _, h := range hits {
		if h.err != nil {
			logger.Warnf(ctx, "[skill] %s search for %q failed: %v", h.registry, query, h.err)
			result.Unavailable = append(result.Unavailable, h.registry)
			continue
		}
		lists = append(lists, h.candidates)
	}
	result.Candidates = mergeSkillCandidates(lists, limit)
	return result, nil
}

// mergeSkillCandidates interleaves the registries' own rankings, which are
// not on a comparable scale, and drops a listing another registry already
// contributed.
func mergeSkillCandidates(lists [][]tools.SkillCandidate, limit int) []tools.SkillCandidate {
	out := make([]tools.SkillCandidate, 0, limit)
	seen := make(map[string]bool)
	for i := 0; len(out) < limit; i++ {
		progressed := false
		for _, list := range lists {
			if i >= len(list) {
				continue
			}
			progressed = true
			c := list[i]
			key := candidateKey(c)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, c)
			if len(out) == limit {
				break
			}
		}
		if !progressed {
			break
		}
	}
	return out
}

// candidateKey identifies a listing across registries by what it installs.
// searchSkillHub gives a mirrored ClawHub skill the ClawHub locator, so the
// two listings share a key here.
func candidateKey(c tools.SkillCandidate) string {
	return strings.ToLower(c.Source)
}

func (r *skillRegistrySearch) getJSON(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", skillSourceUserAgent)
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, skillRegistryMaxBody+1))
	if err != nil {
		return err
	}
	if len(body) > skillRegistryMaxBody {
		return errors.New("response too large")
	}
	return json.Unmarshal(body, out)
}

type clawHubSearchResponse struct {
	Results []clawHubSearchHit `json:"results"`
}

type clawHubSearchHit struct {
	Slug         string          `json:"slug"`
	DisplayName  string          `json:"displayName"`
	Summary      string          `json:"summary"`
	Downloads    float64         `json:"downloads"`
	Official     bool            `json:"official"`
	OwnerHandle  string          `json:"ownerHandle"`
	CanonicalURL string          `json:"canonicalUrl"`
	Install      clawHubInstall  `json:"install"`
	Native       json.RawMessage `json:"native"`
}

type clawHubInstall struct {
	Kind      string `json:"kind"`
	Reference string `json:"reference"`
	SourceURL string `json:"sourceUrl"`
}

func (r *skillRegistrySearch) searchClawHub(
	ctx context.Context, query string, limit int,
) ([]tools.SkillCandidate, error) {
	u, err := url.Parse(strings.TrimRight(r.clawHubOrigin, "/") + "/api/v1/search")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("limit", fmt.Sprint(limit))
	// ClawHub scans what it hosts; listings it flagged are not offered at all.
	q.Set("nonSuspiciousOnly", "true")
	u.RawQuery = q.Encode()

	var resp clawHubSearchResponse
	if err := r.getJSON(ctx, u.String(), &resp); err != nil {
		return nil, err
	}
	out := make([]tools.SkillCandidate, 0, len(resp.Results))
	for _, hit := range resp.Results {
		if clawHubHitSuspicious(hit.Native) {
			continue
		}
		c, ok := r.clawHubCandidate(hit)
		if !ok {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

// clawHubHitSuspicious reads the per-skill flag defensively: the native block
// differs by listing kind, and a shape it does not expect must not fail the
// whole search.
func clawHubHitSuspicious(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	var native struct {
		Skill struct {
			IsSuspicious bool `json:"isSuspicious"`
		} `json:"skill"`
	}
	if err := json.Unmarshal(raw, &native); err != nil {
		return false
	}
	return native.Skill.IsSuspicious
}

func (r *skillRegistrySearch) clawHubCandidate(hit clawHubSearchHit) (tools.SkillCandidate, bool) {
	ref := strings.TrimSpace(hit.Install.Reference)
	c := tools.SkillCandidate{
		Name:        strings.TrimSpace(hit.Slug),
		DisplayName: hit.DisplayName,
		Description: hit.Summary,
		Owner:       hit.OwnerHandle,
		Downloads:   int64(hit.Downloads),
		Official:    hit.Official,
	}
	if canonical := strings.TrimSpace(hit.CanonicalURL); strings.HasPrefix(canonical, "/") {
		c.URL = strings.TrimRight(r.clawHubOrigin, "/") + canonical
	}
	switch kind := strings.ToLower(hit.Install.Kind); {
	case kind == "clawhub" && ref != "":
		c.Registry = "clawhub"
		c.Source = "@" + strings.TrimPrefix(ref, "@")
	case kind == "skills-sh" && strings.HasPrefix(strings.ToLower(ref), skillsShRefPrefix):
		c.Registry = "skills-sh"
		c.Source = ref
		if c.Owner == "" {
			c.Owner, _, _ = strings.Cut(strings.TrimPrefix(ref, skillsShRefPrefix), "/")
		}
	case hit.Install.SourceURL != "":
		c.Registry = kind
		c.Source = strings.TrimSpace(hit.Install.SourceURL)
	default:
		return tools.SkillCandidate{}, false
	}
	if c.Name == "" {
		c.Name = lastLocatorSegment(c.Source)
	}
	// A card must only carry a locator the register API will take.
	if _, err := parseSkillSource(c.Source); err != nil || c.Name == "" {
		return tools.SkillCandidate{}, false
	}
	return c, true
}

type skillHubSearchResponse struct {
	Results []skillHubSearchHit `json:"results"`
}

type skillHubSearchHit struct {
	Slug          string  `json:"slug"`
	Name          string  `json:"name"`
	DisplayName   string  `json:"displayName"`
	Summary       string  `json:"summary"`
	Description   string  `json:"description"`
	DescriptionZh string  `json:"description_zh"`
	Downloads     float64 `json:"downloads"`
	Installs      float64 `json:"installs"`
	OwnerName     string  `json:"owner_name"`
	Source        string  `json:"source"`
	Namespace     struct {
		Handle string `json:"handle"`
	} `json:"namespace"`
}

func (r *skillRegistrySearch) searchSkillHub(
	ctx context.Context, query string, limit int,
) ([]tools.SkillCandidate, error) {
	u, err := url.Parse(strings.TrimRight(r.skillHubOrigin, "/") + "/api/v1/search")
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("q", query)
	q.Set("limit", fmt.Sprint(limit))
	u.RawQuery = q.Encode()

	var resp skillHubSearchResponse
	if err := r.getJSON(ctx, u.String(), &resp); err != nil {
		return nil, err
	}
	out := make([]tools.SkillCandidate, 0, len(resp.Results))
	for _, hit := range resp.Results {
		slug := strings.TrimSpace(hit.Slug)
		if slug == "" || strings.ContainsAny(slug, "/?#") {
			continue
		}
		c := tools.SkillCandidate{
			Name:        slug,
			DisplayName: firstNonEmpty(hit.DisplayName, hit.Name),
			Description: firstNonEmpty(hit.Summary, hit.Description, hit.DescriptionZh),
			Registry:    "skillhub",
			Owner:       hit.OwnerName,
			Downloads:   int64(hit.Downloads),
			Installs:    int64(hit.Installs),
			URL:         skillHubCNPageOrigin + "/skills/" + url.PathEscape(slug),
		}
		// A mirrored ClawHub listing installs through ClawHub, under the same
		// locator the ClawHub result carries, so the merge sees one skill.
		if owner, ok := strings.CutPrefix(hit.Namespace.Handle, "clawhub_"); ok &&
			strings.EqualFold(hit.Source, "clawhub") && owner != "" {
			c.Source = "@" + owner + "/" + slug
		} else {
			c.Source = c.URL
		}
		if _, err := parseSkillSource(c.Source); err != nil {
			continue
		}
		out = append(out, c)
	}
	return out, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

func lastLocatorSegment(source string) string {
	trimmed := strings.TrimRight(source, "/")
	if i := strings.LastIndex(trimmed, "/"); i >= 0 {
		trimmed = trimmed[i+1:]
	}
	trimmed = strings.TrimPrefix(trimmed, "@")
	name, _ := splitTrailingVersion(trimmed)
	return name
}

// skillInventory is the narrow read of what a workspace already carries.
type skillInventory interface {
	ListSkillsByConfig(ctx context.Context, tenantID uint64, configID string) ([]*types.TenantSkillEntity, error)
	ListCatalogsByTenant(ctx context.Context, tenantID uint64) ([]*types.TenantSkillCatalogEntity, error)
}

// runSkillFinder is search_skills' view of one run: the registries, plus the
// workspace inventory each candidate is annotated against. It is bound to the
// caller's workspace and the sandbox config the run boots, so the model cannot
// point it anywhere else.
type runSkillFinder struct {
	registry  *skillRegistrySearch
	preview   func(ctx context.Context, source string) (*SkillBundle, error)
	inventory skillInventory
	tenantID  uint64
	configID  string
}

var _ tools.SkillFinder = (*runSkillFinder)(nil)

func (f *runSkillFinder) SearchSkills(
	ctx context.Context, query string, limit int,
) (*tools.SkillSearchResult, error) {
	result, err := f.registry.search(ctx, query, limit)
	if err != nil {
		return nil, err
	}
	f.annotate(ctx, result.Candidates)
	return result, nil
}

// PreviewSkill downloads the bundle a source names, so the card shows the
// skill's own SKILL.md name and description rather than the model's account
// of it, and so a source that will not install is caught before the button is.
func (f *runSkillFinder) PreviewSkill(ctx context.Context, source string) (*tools.SkillCandidate, error) {
	parsed, err := parseSkillSource(source)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, skillSourcePreviewTimeout)
	defer cancel()
	bundle, err := f.preview(ctx, source)
	if err != nil {
		return nil, err
	}
	c := tools.SkillCandidate{
		Name:        bundle.Name,
		Description: bundle.Description,
		Version:     bundle.Version,
		Source:      strings.TrimSpace(source),
		Registry:    previewRegistry(parsed),
		FileCount:   len(bundle.Files),
	}
	if u, err := url.Parse(c.Source); err == nil && (u.Scheme == "https" || u.Scheme == "http") {
		c.URL = c.Source
	}
	candidates := []tools.SkillCandidate{c}
	f.annotate(ctx, candidates)
	return &candidates[0], nil
}

func previewRegistry(parsed parsedSkillSource) string {
	switch parsed.Kind {
	case skillSourceSkillsSh:
		return "skills-sh"
	case skillSourceGitHub:
		return "github"
	case skillSourceGitLab:
		return "gitlab"
	case skillSourceRegistry:
		if strings.EqualFold(strings.TrimRight(parsed.Registry, "/"), skillHubCNAPIOrigin) {
			return "skillhub"
		}
		if isClawHubOrigin(parsed.Registry) {
			return "clawhub"
		}
		return "registry"
	default:
		return "url"
	}
}

// annotate marks candidates whose name the workspace already uses. The image
// directory is the skill name, so a same-name install replaces what is there
// whether or not it is the same skill; that is what the card has to warn
// about. Inventory failures leave the candidates unmarked rather than failing
// the search.
func (f *runSkillFinder) annotate(ctx context.Context, candidates []tools.SkillCandidate) {
	if f.inventory == nil || f.tenantID == 0 || len(candidates) == 0 {
		return
	}
	installed := make(map[string]string)
	if f.configID != "" {
		rows, err := f.inventory.ListSkillsByConfig(ctx, f.tenantID, f.configID)
		if err != nil {
			logger.Warnf(ctx, "[skill] list skills of config %s for search annotations failed: %v", f.configID, err)
		}
		for _, row := range rows {
			if row != nil {
				installed[strings.ToLower(row.Name)] = row.Status
			}
		}
	}
	catalog := make(map[string]bool)
	cats, err := f.inventory.ListCatalogsByTenant(ctx, f.tenantID)
	if err != nil {
		logger.Warnf(ctx, "[skill] list catalog for search annotations failed: %v", err)
	}
	for _, cat := range cats {
		if cat != nil {
			catalog[strings.ToLower(cat.Name)] = true
		}
	}
	for i := range candidates {
		key := strings.ToLower(candidates[i].Name)
		candidates[i].InstallStatus = installed[key]
		candidates[i].InCatalog = catalog[key]
	}
}
