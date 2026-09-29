package confluence

import (
	"context"
	"encoding/base64"
	"errors"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/Tencent/WeKnora/internal/logger"
)

const (
	// maxPageImages is the total data URI image budget one page may carry into the
	// downstream docparser, which enforces a single per-page limit (maxRemoteImages)
	// across every data:image URI regardless of origin. The budget is shared: it
	// covers data URI images already present in the page plus the ones the resolver
	// generates, and a repeated image consumes it per occurrence, not per unique URL.
	// Downloads stay deduplicated by URL, and only occurrences within the remaining
	// budget are collected, so the connector never emits more data URIs than the
	// pipeline will keep.
	maxPageImages = 30
	// maxPageInlineBytes caps the total size of the data URIs one page may carry,
	// both in the download cache and in the rewritten HTML. Without it an extreme
	// page (maxPageImages occurrences of maxImageBytes images) would inline hundreds
	// of MB of base64, copied again by every downstream conversion. Images past the
	// budget keep their original src, like any other image the resolver skips.
	maxPageInlineBytes = 50 << 20
	// imageDownloadConcurrency bounds in-flight image downloads per page so an
	// image-heavy page cannot exhaust HTTP connections or spike memory.
	imageDownloadConcurrency = 4
)

// errImagePageBudget marks an image dropped because the page's inline budget
// (maxPageInlineBytes) was already used up.
var errImagePageBudget = errors.New("confluence page image budget exhausted")

// imgTagRe matches a full <img ...> start tag, tolerating '>' inside quoted
// attribute values. The \b keeps it from matching <image>.
var imgTagRe = regexp.MustCompile(`(?i)<img\b(?:[^>"']|"[^"]*"|'[^']*')*>`)

// srcAttrRe captures the src attribute value. The leading \s and the value group
// keep it from matching data-src/srcset, and support double-quoted,
// single-quoted, or bare values. Group 1 is the "src=" prefix, group 2 the value.
var srcAttrRe = regexp.MustCompile(`(?i)(\ssrc\s*=\s*)("[^"]*"|'[^']*'|[^\s"'>]+)`)

type imgAction uint8

const (
	imgKeep     imgAction = iota // empty, data:, external, or non-http scheme: leave untouched
	imgDownload                  // same-origin private candidate
)

// assetResolver rewrites same-origin private Confluence images in a page's
// body.view HTML into inline data URIs. Persistence and resource:// rewriting
// are left entirely to the downstream docparser image pipeline; the resolver
// only performs the one step that pipeline cannot do on its own: an
// authenticated fetch of Confluence-private bytes.
type assetResolver struct {
	client *client
}

func newAssetResolver(c *client) *assetResolver { return &assetResolver{client: c} }

// Resolve returns content with same-origin private <img src> occurrences replaced
// by data URIs, within the per-page budget that Resolve shares with any data URI
// images already in the page. It is best-effort: an image that is skipped, falls
// past the remaining budget, or fails to download keeps its original src, and image
// problems never fail the page. Downloads are deduplicated by URL, but the budget
// counts written data URIs (occurrences) to match the downstream pipeline, which
// caps every data:image URI per page regardless of where it came from.
func (r *assetResolver) Resolve(ctx context.Context, content string) string {
	if r == nil || r.client == nil {
		return content
	}
	// Data URI images already in the page consume the same downstream budget as the
	// ones we generate, so reserve room for them before inlining any private image.
	available := maxPageImages - countDataURIImages(content)
	if available <= 0 {
		return content
	}
	candidates := r.collect(content, available)
	if len(candidates) == 0 {
		return content
	}
	cache := r.downloadAll(ctx, candidates)
	if len(cache) == 0 {
		return content
	}
	inlined, inlinedBytes := 0, 0
	budgetSpent := false
	return imgTagRe.ReplaceAllStringFunc(content, func(tag string) string {
		if inlined >= available || budgetSpent {
			return tag
		}
		src, ok := extractImageSrc(tag)
		if !ok {
			return tag
		}
		action, abs := r.classify(src)
		if action != imgDownload {
			return tag
		}
		dataURI := cache[abs]
		if dataURI == "" {
			return tag
		}
		if inlinedBytes+len(dataURI) > maxPageInlineBytes {
			budgetSpent = true
			return tag
		}
		inlined++
		inlinedBytes += len(dataURI)
		return replaceImageSrc(tag, dataURI)
	})
}

// collect returns the unique same-origin download candidates whose first
// occurrence falls within the remaining inline budget, in document order. Because
// Resolve writes at most budget data URIs, a URL first seen after that many
// occurrences can never be inlined, so it is not downloaded. Duplicates collapse to
// one entry, so a repeated image is fetched once yet may be inlined at every
// occurrence inside the budget.
func (r *assetResolver) collect(content string, budget int) []string {
	tags := imgTagRe.FindAllString(content, -1)
	order := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	occurrences := 0
	for _, tag := range tags {
		src, ok := extractImageSrc(tag)
		if !ok {
			continue
		}
		action, abs := r.classify(src)
		if action != imgDownload {
			continue
		}
		if occurrences >= budget {
			break
		}
		occurrences++
		if _, dup := seen[abs]; dup {
			continue
		}
		seen[abs] = struct{}{}
		order = append(order, abs)
	}
	return order
}

// countDataURIImages returns how many <img> tags in content already carry a
// data:image URI. The downstream docparser counts every data:image occurrence
// against the same per-page limit whether it came from the page or from us, so
// these must be reserved before the resolver inlines any private Confluence image.
func countDataURIImages(content string) int {
	count := 0
	for _, tag := range imgTagRe.FindAllString(content, -1) {
		src, ok := extractImageSrc(tag)
		if !ok {
			continue
		}
		if strings.HasPrefix(strings.ToLower(src), "data:image/") {
			count++
		}
	}
	return count
}

// downloadAll fetches each candidate with bounded concurrency and returns a
// URL-keyed cache of the successful ones as data URIs, holding at most
// maxPageInlineBytes of them. Failures and images past the budget are logged
// (redacted) and omitted, so the rewrite pass leaves their original src in place.
func (r *assetResolver) downloadAll(ctx context.Context, urls []string) map[string]string {
	cache := make(map[string]string, len(urls))
	cachedBytes := 0
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, imageDownloadConcurrency)
	for _, u := range urls {
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(abs string) {
			defer wg.Done()
			defer func() { <-sem }()
			data, mime, err := r.client.downloadImage(ctx, abs)
			if err != nil {
				logImageFailure(ctx, abs, err)
				return
			}
			uri := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
			mu.Lock()
			fits := cachedBytes+len(uri) <= maxPageInlineBytes
			if fits {
				cache[abs] = uri
				cachedBytes += len(uri)
			}
			mu.Unlock()
			if !fits {
				logImageFailure(ctx, abs, errImagePageBudget)
			}
		}(u)
	}
	wg.Wait()
	return cache
}

// classify decides whether src is a same-origin private candidate to download or
// should be left untouched, returning the normalized absolute URL for downloads.
func (r *assetResolver) classify(src string) (imgAction, string) {
	s := strings.TrimSpace(src)
	if s == "" || strings.HasPrefix(strings.ToLower(s), "data:") {
		return imgKeep, ""
	}
	u, err := url.Parse(s)
	if err != nil {
		return imgKeep, ""
	}
	if u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https" {
		// file:, javascript:, and any other scheme are never fetched.
		return imgKeep, ""
	}
	if u.Host != "" {
		if u.Scheme == "" {
			// Protocol-relative //host/path: only same-origin is eligible.
			base, berr := url.Parse(r.client.cfg.baseURL)
			if berr != nil || !strings.EqualFold(base.Host, u.Host) {
				return imgKeep, ""
			}
		} else if !r.client.sameOrigin(u) {
			// Absolute external http(s): keep, never send Confluence credentials.
			return imgKeep, ""
		}
	}
	abs, err := r.client.resolveEndpoint(s)
	if err != nil {
		return imgKeep, ""
	}
	return imgDownload, abs
}

// logImageFailure records a redacted warning: host and path only, never the
// query (which may carry signed tokens), the Authorization header, or the body.
func logImageFailure(ctx context.Context, abs string, err error) {
	host, path := abs, ""
	if u, perr := url.Parse(abs); perr == nil {
		host, path = u.Host, u.Path
	}
	reason := "download_failed"
	switch {
	case errors.Is(err, errImageTooLarge):
		reason = "oversize"
	case errors.Is(err, errImageNonImageContent):
		reason = "non_image_content_type"
	case errors.Is(err, errImageRedirectOffOrigin):
		reason = "cross_origin_redirect"
	case errors.Is(err, errImagePageBudget):
		reason = "page_budget"
	}
	var api *apiError
	if errors.As(err, &api) {
		reason = "status_" + strconv.Itoa(api.status)
	}
	logger.Warnf(ctx, "[Confluence] failed to resolve image host=%s path=%s reason=%s", host, path, reason)
}

// extractImageSrc returns the HTML-unescaped src value of an <img> tag.
func extractImageSrc(tag string) (string, bool) {
	m := srcAttrRe.FindStringSubmatch(tag)
	if m == nil {
		return "", false
	}
	value := strings.TrimSpace(m[2])
	if len(value) >= 2 {
		if q := value[0]; (q == '"' || q == '\'') && value[len(value)-1] == q {
			value = value[1 : len(value)-1]
		}
	}
	value = strings.TrimSpace(html.UnescapeString(value))
	if value == "" {
		return "", false
	}
	return value, true
}

// replaceImageSrc swaps only the src attribute value for a double-quoted data
// URI, preserving the rest of the tag byte-for-byte.
func replaceImageSrc(tag, dataURI string) string {
	loc := srcAttrRe.FindStringSubmatchIndex(tag)
	if loc == nil {
		return tag
	}
	start, end := loc[4], loc[5]
	return tag[:start] + `"` + dataURI + `"` + tag[end:]
}
