package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// ErrSkillSourceInvalid marks every rejection of a registry / git / URL
// source so the handler can map the class to 400 without matching on text.
var ErrSkillSourceInvalid = errors.New("skill source is invalid")

const (
	defaultSkillRegistryOrigin = "https://clawhub.ai"
	skillSourceUserAgent       = "WeKnora-SkillInstaller (+https://github.com/Tencent/WeKnora)"
	skillSourceFetchTimeout    = 60 * time.Second
	skillSourceMaxHops         = 3
)

var (
	skillSourceHTTPOnce    sync.Once
	skillSourceHTTPDefault *http.Client

	semverLike = regexp.MustCompile(`^v?\d+\.\d+(\.\d+)?([.-][0-9A-Za-z.-]+)?$`)
)

type skillSourceKind string

const (
	skillSourceRegistry skillSourceKind = "registry"
	skillSourceGitHub   skillSourceKind = "github"
	skillSourceGitLab   skillSourceKind = "gitlab"
	skillSourceDirect   skillSourceKind = "direct"
)

// parsedSkillSource is one install input after host-specific URL/slug rules
// have been applied, and before any bytes are fetched.
type parsedSkillSource struct {
	Kind      skillSourceKind
	Registry  string // origin, e.g. https://clawhub.ai or a SkillHub host
	Slug      string
	Version   string
	Owner     string // GitHub/GitLab owner, or ClawHub ownerHandle
	Repo      string
	Ref       string
	Subdir    string
	DirectURL string
}

type skillSourceHandoff struct {
	SourceRef   string `json:"sourceRef"`
	Repo        string `json:"repo"`
	Commit      string `json:"commit"`
	Path        string `json:"path"`
	ArchiveURL  string `json:"archiveUrl"`
	DownloadURL string `json:"downloadUrl"`
}

// InstallSkillFromSource resolves a ClawHub / SkillHub / skills.sh / git /
// direct-zip locator to a skill bundle and runs the same install as an upload.
//
// Every fetch is anonymous. Private registries are deliberately out of scope:
// carrying a credential here would mean deciding, per hop, which of a
// registry's handoff targets may see it, and there is no private registry to
// validate that against yet.
func (s *TenantSkillService) InstallSkillFromSource(
	ctx context.Context, tenantID uint64, configID, source string,
) (string, error) {
	// The config is authorized before the fetch, not by InstallSkill after it.
	// The source is a caller-supplied host, so an unknown config ID must not
	// be able to spend an outbound request and a body-sized download first.
	cfgEntity, err := s.configs.GetByID(ctx, tenantID, configID)
	if err != nil {
		return "", err
	}
	if cfgEntity == nil {
		return "", apperrors.NewNotFoundError("sandbox config not found")
	}

	archive, err := fetchSkillArchive(ctx, source, s.sourceHTTP)
	if err != nil {
		return "", err
	}
	return s.InstallSkill(ctx, tenantID, configID, archive)
}

func skillSourceHTTPClient(override *http.Client) *http.Client {
	if override != nil {
		return override
	}
	skillSourceHTTPOnce.Do(func() {
		cfg := secutils.DefaultSSRFSafeHTTPClientConfig()
		cfg.Timeout = skillSourceFetchTimeout
		skillSourceHTTPDefault = secutils.NewSSRFSafeHTTPClient(cfg)
	})
	return skillSourceHTTPDefault
}

func fetchSkillArchive(ctx context.Context, source string, client *http.Client) ([]byte, error) {
	parsed, err := parseSkillSource(source)
	if err != nil {
		return nil, err
	}
	httpClient := skillSourceHTTPClient(client)
	fetched, err := fetchSkillSourceBytes(ctx, httpClient, parsed, 0)
	if err != nil {
		return nil, err
	}
	return normalizeFetchedSkillArchive(fetched.body, fetched.contentType, fetched.subdir)
}

// parseSkillSource maps one paste onto exactly one kind. It does not probe
// the network to guess: owner/slug is both a ClawHub locator and a GitHub
// repo, so a slash without a URL or a leading @ is refused rather than
// fetched twice.
//
//	@owner/slug          ClawHub (default registry). The API slug is the
//	                     last segment; owner becomes ownerHandle.
//	my-skill             ClawHub slug (no slash)
//	my-skill@1.2.0       ClawHub slug + version
//	https://…            host decides (GitHub / GitLab / ClawHub / SkillHub /
//	                     skills.sh / zip|SKILL.md / self-hosted registry)
func parseSkillSource(raw string) (parsedSkillSource, error) {
	input := strings.TrimSpace(raw)
	if input == "" {
		return parsedSkillSource{}, fmt.Errorf("%w: source is required", ErrSkillSourceInvalid)
	}
	input = strings.TrimRight(input, "/")

	if strings.Contains(input, "://") {
		return parseSkillSourceURL(input)
	}
	if strings.HasPrefix(input, "@") {
		return parseRegistrySlug(defaultSkillRegistryOrigin, strings.TrimPrefix(input, "@"))
	}
	if strings.Contains(input, "/") {
		return parsedSkillSource{}, fmt.Errorf(
			"%w: %q is ambiguous; use @%s for ClawHub, or paste a github.com / gitlab.com / skills.sh / skillhub URL",
			ErrSkillSourceInvalid, input, input)
	}
	return parseRegistrySlug(defaultSkillRegistryOrigin, input)
}

func parseSkillSourceURL(raw string) (parsedSkillSource, error) {
	parsedURL, err := url.Parse(raw)
	if err != nil {
		return parsedSkillSource{}, fmt.Errorf("%w: not a valid URL", ErrSkillSourceInvalid)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return parsedSkillSource{}, fmt.Errorf("%w: only http(s) sources are allowed", ErrSkillSourceInvalid)
	}
	host := strings.ToLower(parsedURL.Hostname())
	switch {
	case host == "github.com" || host == "www.github.com" || host == "codeload.github.com":
		if host == "codeload.github.com" {
			return parsedSkillSource{Kind: skillSourceDirect, DirectURL: parsedURL.String()}, nil
		}
		return parseGitHubURL(parsedURL)
	case host == "gitlab.com" || host == "www.gitlab.com":
		return parseGitLabURL(parsedURL)
	case host == "skills.sh" || host == "www.skills.sh":
		return parseSkillsShURL(parsedURL)
	case isClawHubHost(host):
		return parseRegistryURL(parsedURL)
	case isSkillHubCNHost(host):
		return parseSkillHubCNURL(parsedURL)
	default:
		if isDirectArchivePath(parsedURL.Path) {
			return parsedSkillSource{Kind: skillSourceDirect, DirectURL: parsedURL.String()}, nil
		}
		return parseRegistryURL(parsedURL)
	}
}

func isClawHubHost(host string) bool {
	switch host {
	case "clawhub.ai", "www.clawhub.ai", "clawhub.com", "www.clawhub.com":
		return true
	default:
		return false
	}
}

const skillHubCNAPIOrigin = "https://api.skillhub.cn"

func isSkillHubCNHost(host string) bool {
	switch strings.ToLower(host) {
	case "skillhub.cn", "www.skillhub.cn", "api.skillhub.cn":
		return true
	default:
		return false
	}
}

// parseSkillHubCNURL maps public SkillHub.cn pages onto the download API.
// Page URLs look like /skills/{slug} or /skills/{publisher}/{slug}; the API
// keys downloads by the skill name only, on api.skillhub.cn (www is an SPA).
func parseSkillHubCNURL(u *url.URL) (parsedSkillSource, error) {
	trimmed := strings.Trim(u.Path, "/")
	if strings.HasPrefix(trimmed, "api/v1/download") {
		direct := skillHubCNAPIOrigin + "/api/v1/download"
		if u.RawQuery != "" {
			direct += "?" + u.RawQuery
		}
		return parsedSkillSource{
			Kind:      skillSourceDirect,
			Registry:  skillHubCNAPIOrigin,
			DirectURL: direct,
		}, nil
	}
	parts := splitPath(trimmed)
	if len(parts) >= 1 && strings.EqualFold(parts[0], "skills") {
		parts = parts[1:]
	}
	if len(parts) == 0 || len(parts) > 2 {
		return parsedSkillSource{}, fmt.Errorf("%w: unrecognized registry path", ErrSkillSourceInvalid)
	}
	slug := parts[len(parts)-1]
	version := strings.TrimSpace(u.Fragment)
	if qVersion := strings.TrimSpace(u.Query().Get("version")); qVersion != "" {
		version = qVersion
	}
	slug, fromSpec := splitTrailingVersion(slug)
	if fromSpec != "" {
		version = fromSpec
	}
	if slug == "" {
		return parsedSkillSource{}, fmt.Errorf("%w: skill slug is required", ErrSkillSourceInvalid)
	}
	return parsedSkillSource{
		Kind:     skillSourceRegistry,
		Registry: skillHubCNAPIOrigin,
		Slug:     slug,
		Version:  version,
	}, nil
}

func parseRegistryURL(u *url.URL) (parsedSkillSource, error) {
	origin := u.Scheme + "://" + u.Host
	trimmed := strings.Trim(u.Path, "/")
	if strings.HasPrefix(trimmed, "api/v1/download") {
		return parsedSkillSource{
			Kind:      skillSourceDirect,
			Registry:  origin,
			DirectURL: u.String(),
		}, nil
	}
	slug, version, err := slugAndVersionFromPath(trimmed, u.Fragment)
	if err != nil {
		return parsedSkillSource{}, err
	}
	if qVersion := strings.TrimSpace(u.Query().Get("version")); qVersion != "" {
		version = qVersion
	}
	src := parsedSkillSource{
		Kind:     skillSourceRegistry,
		Registry: origin,
		Slug:     slug,
		Version:  version,
	}
	if isClawHubHost(u.Hostname()) {
		src.Owner, src.Slug = splitClawHubOwnerSlug(slug)
	}
	return src, nil
}

func parseRegistrySlug(origin, spec string) (parsedSkillSource, error) {
	spec = strings.TrimSpace(strings.TrimPrefix(spec, "@"))
	if spec == "" {
		return parsedSkillSource{}, fmt.Errorf("%w: skill slug is required", ErrSkillSourceInvalid)
	}
	slug, version := splitTrailingVersion(spec)
	if slug == "" {
		return parsedSkillSource{}, fmt.Errorf("%w: skill slug is required", ErrSkillSourceInvalid)
	}
	src := parsedSkillSource{
		Kind:     skillSourceRegistry,
		Registry: origin,
		Slug:     slug,
		Version:  version,
	}
	if isClawHubOrigin(origin) {
		src.Owner, src.Slug = splitClawHubOwnerSlug(slug)
	}
	return src, nil
}

func isClawHubOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" {
		return false
	}
	return isClawHubHost(u.Hostname())
}

// splitClawHubOwnerSlug turns a ClawHub locator into the API slug.
// ClawHub pages and CLI refs look like owner/slug, but GET /api/v1/download
// keys by the skill slug alone and takes the publisher as ownerHandle.
func splitClawHubOwnerSlug(spec string) (owner, slug string) {
	parts := splitPath(spec)
	switch len(parts) {
	case 0:
		return "", spec
	case 1:
		return "", parts[0]
	case 2:
		return parts[0], parts[1]
	default:
		return "", spec
	}
}

func slugAndVersionFromPath(trimmed, fragment string) (string, string, error) {
	if trimmed == "" {
		return "", "", fmt.Errorf("%w: skill slug is required", ErrSkillSourceInvalid)
	}
	parts := splitPath(trimmed)
	if len(parts) >= 1 && strings.EqualFold(parts[0], "skills") {
		parts = parts[1:]
	}
	if len(parts) >= 3 && strings.EqualFold(parts[1], "skills") {
		parts = []string{parts[0], parts[2]}
	}
	if len(parts) > 2 {
		return "", "", fmt.Errorf("%w: unrecognized registry path", ErrSkillSourceInvalid)
	}
	slug := strings.Join(parts, "/")
	version := strings.TrimSpace(fragment)
	slug, fromSpec := splitTrailingVersion(slug)
	if fromSpec != "" {
		version = fromSpec
	}
	if slug == "" {
		return "", "", fmt.Errorf("%w: skill slug is required", ErrSkillSourceInvalid)
	}
	return slug, version, nil
}

func parseGitHubURL(u *url.URL) (parsedSkillSource, error) {
	parts := splitPath(u.Path)
	if len(parts) < 2 {
		return parsedSkillSource{}, fmt.Errorf("%w: github URL must be owner/repo", ErrSkillSourceInvalid)
	}
	owner, repo := parts[0], strings.TrimSuffix(parts[1], ".git")
	src := parsedSkillSource{Kind: skillSourceGitHub, Owner: owner, Repo: repo, Ref: "HEAD"}
	rest := parts[2:]
	if len(rest) == 0 {
		return src, nil
	}
	switch rest[0] {
	case "tree", "blob":
		if len(rest) < 2 {
			return parsedSkillSource{}, fmt.Errorf("%w: github tree URL is missing a ref", ErrSkillSourceInvalid)
		}
		src.Ref = rest[1]
		src.Subdir = strings.Join(rest[2:], "/")
		if rest[0] == "blob" && strings.EqualFold(path.Base(src.Subdir), "SKILL.md") {
			src.Subdir = path.Dir(src.Subdir)
			if src.Subdir == "." {
				src.Subdir = ""
			}
		}
	case "archive":
		src.DirectURL = u.String()
		src.Kind = skillSourceDirect
	case "releases":
		src.DirectURL = u.String()
		src.Kind = skillSourceDirect
	default:
		src.Subdir = strings.Join(rest, "/")
	}
	return src, nil
}

func parseGitLabURL(u *url.URL) (parsedSkillSource, error) {
	trimmed := strings.Trim(u.Path, "/")
	project, extra, found := strings.Cut(trimmed, "/-/")
	if !found {
		project = trimmed
	}
	parts := splitPath(project)
	if len(parts) < 2 {
		return parsedSkillSource{}, fmt.Errorf("%w: gitlab URL must be group/project", ErrSkillSourceInvalid)
	}
	repo := strings.TrimSuffix(parts[len(parts)-1], ".git")
	owner := strings.Join(parts[:len(parts)-1], "/")
	src := parsedSkillSource{Kind: skillSourceGitLab, Owner: owner, Repo: repo, Ref: "HEAD"}
	if extra == "" {
		return src, nil
	}
	extraParts := splitPath(extra)
	switch extraParts[0] {
	case "tree", "blob":
		if len(extraParts) < 2 {
			return parsedSkillSource{}, fmt.Errorf("%w: gitlab tree URL is missing a ref", ErrSkillSourceInvalid)
		}
		src.Ref = extraParts[1]
		src.Subdir = strings.Join(extraParts[2:], "/")
		if extraParts[0] == "blob" && strings.EqualFold(path.Base(src.Subdir), "SKILL.md") {
			src.Subdir = path.Dir(src.Subdir)
			if src.Subdir == "." {
				src.Subdir = ""
			}
		}
	case "archive":
		src.DirectURL = u.String()
		src.Kind = skillSourceDirect
	}
	return src, nil
}

func parseSkillsShURL(u *url.URL) (parsedSkillSource, error) {
	parts := splitPath(u.Path)
	if len(parts) < 2 {
		return parsedSkillSource{}, fmt.Errorf("%w: skills.sh URL must be owner/repo", ErrSkillSourceInvalid)
	}
	src := parsedSkillSource{
		Kind:  skillSourceGitHub,
		Owner: parts[0],
		Repo:  strings.TrimSuffix(parts[1], ".git"),
		Ref:   "HEAD",
	}
	if len(parts) > 2 {
		src.Subdir = strings.Join(parts[2:], "/")
	}
	return src, nil
}

func splitTrailingVersion(spec string) (slug, version string) {
	i := strings.LastIndex(spec, "@")
	if i <= 0 {
		return spec, ""
	}
	candidate := spec[i+1:]
	if strings.EqualFold(candidate, "latest") || semverLike.MatchString(candidate) {
		return spec[:i], candidate
	}
	return spec, ""
}

func splitPath(p string) []string {
	var out []string
	for _, part := range strings.Split(strings.Trim(p, "/"), "/") {
		if part == "" || part == "." {
			continue
		}
		out = append(out, part)
	}
	return out
}

// escapePathSegments escapes a value that spans several path segments, such as
// a "refs/heads/main" ref or a GitLab "group/subgroup" owner, without turning
// its separators into %2F.
func escapePathSegments(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func isDirectArchivePath(p string) bool {
	lower := strings.ToLower(p)
	for _, ext := range []string{".zip", ".tgz", ".tar.gz", ".tar", ".md"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func (s parsedSkillSource) fetchURL() (string, error) {
	if s.DirectURL != "" {
		return s.DirectURL, nil
	}
	switch s.Kind {
	case skillSourceRegistry:
		u, err := url.Parse(strings.TrimRight(s.Registry, "/") + "/api/v1/download")
		if err != nil {
			return "", fmt.Errorf("%w: invalid registry origin", ErrSkillSourceInvalid)
		}
		q := u.Query()
		q.Set("slug", s.Slug)
		if s.Owner != "" {
			q.Set("ownerHandle", s.Owner)
		}
		if s.Version != "" && !strings.EqualFold(s.Version, "latest") {
			if semverLike.MatchString(s.Version) {
				q.Set("version", s.Version)
			} else {
				q.Set("tag", s.Version)
			}
		}
		u.RawQuery = q.Encode()
		return u.String(), nil
	case skillSourceGitHub:
		ref := s.Ref
		if ref == "" {
			ref = "HEAD"
		}
		return fmt.Sprintf("https://codeload.github.com/%s/%s/zip/%s",
			escapePathSegments(s.Owner), url.PathEscape(s.Repo), escapePathSegments(ref)), nil
	case skillSourceGitLab:
		ref := s.Ref
		if ref == "" {
			ref = "HEAD"
		}
		return fmt.Sprintf("https://gitlab.com/%s/%s/-/archive/%s/%s.zip",
			escapePathSegments(s.Owner), url.PathEscape(s.Repo),
			escapePathSegments(ref), url.PathEscape(s.Repo+"-"+ref)), nil
	default:
		return "", fmt.Errorf("%w: cannot resolve source", ErrSkillSourceInvalid)
	}
}

// fetchedSkillSource is the archive a source resolved to, plus the skill root
// inside it. The subdir is carried out of the fetch rather than re-derived from
// the original locator: a registry handoff to a monorepo zip names the one
// skill it meant, and only the last hop knows it.
type fetchedSkillSource struct {
	body        []byte
	contentType string
	subdir      string
}

func fetchSkillSourceBytes(
	ctx context.Context, client *http.Client, src parsedSkillSource, hop int,
) (fetchedSkillSource, error) {
	if hop > skillSourceMaxHops {
		return fetchedSkillSource{}, fmt.Errorf("%w: too many source redirects", ErrSkillSourceInvalid)
	}
	target, err := src.fetchURL()
	if err != nil {
		return fetchedSkillSource{}, err
	}
	body, contentType, err := getSkillURL(ctx, client, target)
	if err != nil {
		return fetchedSkillSource{}, err
	}
	fetched := fetchedSkillSource{body: body, contentType: contentType, subdir: src.Subdir}
	if isZipMagic(body) || looksLikeSkillMarkdown(body) {
		return fetched, nil
	}
	if !looksLikeJSON(contentType, body) {
		if isZipPayload(contentType, body) {
			return fetched, nil
		}
		return fetchedSkillSource{}, fmt.Errorf(
			"%w: remote did not return a skill archive", ErrSkillSourceInvalid)
	}
	var handoff skillSourceHandoff
	if err := json.Unmarshal(body, &handoff); err != nil {
		return fetchedSkillSource{}, fmt.Errorf(
			"%w: remote JSON is not a skill archive", ErrSkillSourceInvalid)
	}
	next, err := sourceFromHandoff(src, handoff)
	if err != nil {
		return fetchedSkillSource{}, err
	}
	return fetchSkillSourceBytes(ctx, client, next, hop+1)
}

func sourceFromHandoff(prev parsedSkillSource, handoff skillSourceHandoff) (parsedSkillSource, error) {
	archiveURL := strings.TrimSpace(handoff.ArchiveURL)
	if archiveURL == "" {
		archiveURL = strings.TrimSpace(handoff.DownloadURL)
	}
	if archiveURL == "" {
		return parsedSkillSource{}, fmt.Errorf("%w: registry response has no archive URL", ErrSkillSourceInvalid)
	}
	if strings.HasPrefix(archiveURL, "/") {
		if prev.Registry == "" {
			return parsedSkillSource{}, fmt.Errorf(
				"%w: registry response has a relative archive URL", ErrSkillSourceInvalid)
		}
		archiveURL = strings.TrimRight(prev.Registry, "/") + archiveURL
	}
	// A handoff that is not a parseable http(s) URL is refused rather than
	// passed through as a direct target: SSRF validation normalises a
	// scheme-less string by prepending https://, which would turn a malformed
	// response into a fetch of a host we never agreed to read.
	next, err := parseSkillSourceURL(archiveURL)
	if err != nil {
		return parsedSkillSource{}, fmt.Errorf(
			"%w: registry archive URL is not usable: %v", ErrSkillSourceInvalid, err)
	}
	if next.Subdir == "" && strings.TrimSpace(handoff.Path) != "" {
		next.Subdir = strings.Trim(handoff.Path, "/")
	}
	if next.Kind == skillSourceGitHub && next.Ref == "HEAD" && strings.TrimSpace(handoff.Commit) != "" {
		next.Ref = handoff.Commit
	}
	return next, nil
}

func getSkillURL(
	ctx context.Context, client *http.Client, rawURL string,
) ([]byte, string, error) {
	if err := secutils.ValidateURLForSSRF(rawURL); err != nil {
		return nil, "", fmt.Errorf("%w: %s", ErrSkillSourceInvalid,
			secutils.FormatSSRFError("skill source", rawURL, err))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("%w: invalid source URL", ErrSkillSourceInvalid)
	}
	req.Header.Set("User-Agent", skillSourceUserAgent)
	req.Header.Set("Accept", "application/zip, application/octet-stream, application/json, text/plain;q=0.9, */*;q=0.8")
	resp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, secutils.ErrSSRFRedirectBlocked) {
			return nil, "", fmt.Errorf("%w: %s", ErrSkillSourceInvalid,
				secutils.FormatSSRFError("skill source", rawURL, err))
		}
		return nil, "", fmt.Errorf("%w: download failed: %v", ErrSkillSourceInvalid, err)
	}
	defer func() { _ = resp.Body.Close() }()

	maxBytes := secutils.GetMaxFileSize()
	if resp.ContentLength > maxBytes {
		return nil, "", fmt.Errorf("%w: skill bundle cannot exceed %d MB",
			ErrSkillSourceInvalid, secutils.GetMaxFileSizeMB())
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		preview, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		msg := strings.TrimSpace(string(preview))
		if msg == "" {
			msg = resp.Status
		}
		return nil, "", fmt.Errorf("%w: download returned HTTP %d: %s",
			ErrSkillSourceInvalid, resp.StatusCode, truncateSkillError(msg))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("%w: failed to read skill archive", ErrSkillSourceInvalid)
	}
	if int64(len(body)) > maxBytes {
		return nil, "", fmt.Errorf("%w: skill bundle cannot exceed %d MB",
			ErrSkillSourceInvalid, secutils.GetMaxFileSizeMB())
	}
	if len(body) == 0 {
		return nil, "", fmt.Errorf("%w: remote returned an empty body", ErrSkillSourceInvalid)
	}
	return body, resp.Header.Get("Content-Type"), nil
}

func normalizeFetchedSkillArchive(body []byte, contentType, subdir string) ([]byte, error) {
	if looksLikeSkillMarkdown(body) {
		files := map[string][]byte{"SKILL.md": body}
		if _, err := skillBundleFromFiles(body, files); err != nil {
			return nil, err
		}
		return zipSkillFiles(files)
	}
	if !isZipPayload(contentType, body) {
		return nil, fmt.Errorf("%w: remote did not return a zip skill bundle", ErrSkillSourceInvalid)
	}
	opts := SkillBundleParseOptions{
		Subdir:           subdir,
		AllowExtraFiles:  true,
		AllowNestedSkill: true,
	}
	bundle, err := ParseSkillBundleWithOptions(body, opts)
	if err != nil {
		return nil, err
	}
	return zipSkillFiles(bundle.Files)
}

func isZipMagic(body []byte) bool {
	return len(body) >= 4 && bytes.HasPrefix(body, []byte("PK\x03\x04"))
}

func isZipPayload(contentType string, body []byte) bool {
	if isZipMagic(body) {
		return true
	}
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "zip")
}

func looksLikeJSON(contentType string, body []byte) bool {
	ct := strings.ToLower(contentType)
	if strings.Contains(ct, "json") {
		return true
	}
	trimmed := bytes.TrimSpace(body)
	return len(trimmed) > 0 && trimmed[0] == '{'
}

func looksLikeSkillMarkdown(body []byte) bool {
	trimmed := bytes.TrimSpace(body)
	if !bytes.HasPrefix(trimmed, []byte("---")) {
		return false
	}
	return bytes.Contains(trimmed, []byte("\nname:")) || bytes.Contains(trimmed, []byte("\nname :"))
}

func truncateSkillError(msg string) string {
	msg = strings.Join(strings.Fields(msg), " ")
	if len(msg) > 200 {
		return msg[:200]
	}
	return msg
}
