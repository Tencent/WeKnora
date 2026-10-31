package service

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	secutils "github.com/Tencent/WeKnora/internal/utils"
)

// MaxMarketplaceArchiveBytes caps an uploaded or downloaded marketplace archive.
const MaxMarketplaceArchiveBytes = maxSkillBundleTotalBytes

// FetchMarketplaceArchive resolves a git repository URL, GitHub/GitLab shorthand, or direct tar.gz/zip link,
// validates SSRF safety, and downloads the archive bytes.
func FetchMarketplaceArchive(ctx context.Context, source string, client *http.Client) ([]byte, error) {
	rawSource := strings.TrimSpace(source)
	if rawSource == "" {
		return nil, fmt.Errorf("%w: source URL cannot be empty", ErrSkillSourceInvalid)
	}

	downloadURL, err := resolveMarketplaceDownloadURL(rawSource)
	if err != nil {
		return nil, err
	}

	if err := secutils.ValidateURLForSSRF(downloadURL); err != nil {
		return nil, fmt.Errorf("%w: SSRF validation failed for source URL: %v", ErrSkillSourceInvalid, err)
	}

	httpClient := skillSourceHTTPClient(client)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot create HTTP request: %v", ErrSkillSourceInvalid, err)
	}
	req.Header.Set("User-Agent", skillSourceUserAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to fetch archive: %v", ErrSkillSourceInvalid, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: remote returned status %d", ErrSkillSourceInvalid, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxMarketplaceArchiveBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: failed to read response body: %v", ErrSkillSourceInvalid, err)
	}
	if int64(len(body)) > MaxMarketplaceArchiveBytes {
		return nil, fmt.Errorf("%w: archive is too large", ErrSkillBundleInvalid)
	}

	return body, nil
}

func resolveMarketplaceDownloadURL(raw string) (string, error) {
	// Support github shorthand "owner/repo"
	if !strings.Contains(raw, "://") && strings.Count(raw, "/") == 1 && !strings.Contains(raw, "@") {
		parts := strings.Split(raw, "/")
		owner := parts[0]
		repo := strings.TrimSuffix(parts[1], ".git")
		return fmt.Sprintf(
			"https://codeload.github.com/%s/%s/zip/HEAD",
			escapePathSegments(owner),
			url.PathEscape(repo),
		), nil
	}

	parsedURL, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: not a valid URL: %v", ErrSkillSourceInvalid, err)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return "", fmt.Errorf("%w: only http(s) sources are allowed", ErrSkillSourceInvalid)
	}

	host := strings.ToLower(parsedURL.Hostname())
	switch host {
	case "github.com", "www.github.com":
		src, err := parseGitHubURL(parsedURL)
		if err != nil {
			return "", err
		}
		ref := src.Ref
		if ref == "" {
			ref = "HEAD"
		}
		return fmt.Sprintf("https://codeload.github.com/%s/%s/zip/%s",
			escapePathSegments(src.Owner), url.PathEscape(src.Repo), escapePathSegments(ref)), nil

	case "gitlab.com", "www.gitlab.com":
		src, err := parseGitLabURL(parsedURL)
		if err != nil {
			return "", err
		}
		ref := src.Ref
		if ref == "" {
			ref = "HEAD"
		}
		return fmt.Sprintf(
			"https://gitlab.com/%s/%s/-/archive/%s/%s-%s.zip",
			escapePathSegments(
				src.Owner,
			),
			url.PathEscape(src.Repo),
			escapePathSegments(ref),
			url.PathEscape(src.Repo),
			escapePathSegments(ref),
		), nil

	default:
		return parsedURL.String(), nil
	}
}
