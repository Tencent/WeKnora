package journalrank

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"golang.org/x/time/rate"
)

const (
	defaultEasyScholarURL = "https://www.easyscholar.cc/open/getPublicationRank"
	defaultCrossrefURL    = "https://api.crossref.org/works/"
	defaultTimeout        = 15 * time.Second
	cacheTTL              = 30 * 24 * time.Hour
	maxCacheEntries       = 2048
)

var (
	ErrNotConfigured      = errors.New("easyscholar is not configured")
	ErrPublicationMissing = errors.New("publication name could not be identified")
	doiPattern            = regexp.MustCompile(`(?i)\b10\.\d{4,9}/[-._;()/:a-z0-9]+`)
	journalLinePattern    = regexp.MustCompile(`(?im)^\s*(?:journal|publication|source journal|期刊(?:名称)?)\s*[:：]\s*(.{3,180})\s*$`)
	documentTitlePattern  = regexp.MustCompile(`(?m)^\s*#\s+([^#\r\n].{8,300})\s*$`)
)

type Client struct {
	secretKey   string
	httpClient  *http.Client
	rankURL     string
	crossrefURL string
	limiter     *rate.Limiter
	mu          sync.Mutex
	cache       map[string]cacheEntry
}

type cacheEntry struct {
	value     *types.JournalRankMetadata
	expiresAt time.Time
}

func NewClient() *Client {
	return &Client{
		secretKey:  strings.TrimSpace(os.Getenv("EASYSCHOLAR_SECRET_KEY")),
		httpClient: &http.Client{Timeout: defaultTimeout},
		rankURL:    defaultEasyScholarURL, crossrefURL: defaultCrossrefURL,
		limiter: rate.NewLimiter(rate.Limit(2), 1),
		cache:   make(map[string]cacheEntry),
	}
}

// Enrich identifies a publication and looks up its ranking. The reason is
// safe for logs and Trace output and never includes credentials.
func (c *Client) Enrich(ctx context.Context, metadata map[string]string, text string) (*types.JournalRankMetadata, string, error) {
	if c == nil || c.secretKey == "" {
		return nil, "not_configured", ErrNotConfigured
	}
	publication, doi := ExtractPublication(metadata, text)
	if publication == "" && doi != "" {
		var err error
		publication, err = c.resolveDOI(ctx, doi)
		if err != nil {
			return nil, "doi_resolution_failed", err
		}
	}
	if publication == "" {
		title := ExtractDocumentTitle(metadata, text)
		if title != "" {
			var err error
			publication, err = c.resolveTitle(ctx, title)
			if err != nil && !errors.Is(err, ErrPublicationMissing) {
				return nil, "title_resolution_failed", err
			}
		}
	}
	if publication == "" {
		return nil, "publication_missing", ErrPublicationMissing
	}
	key := normalizePublication(publication)
	if cached, ok := c.getCached(key); ok {
		return cached, "cache_hit", nil
	}
	if err := c.limiter.Wait(ctx); err != nil {
		return nil, "rate_limiter_cancelled", err
	}
	result, err := c.lookup(ctx, publication)
	if err != nil {
		return nil, "lookup_failed", err
	}
	c.putCached(key, result)
	return result, "matched", nil
}

// ExtractDocumentTitle returns bibliographic title metadata or the first
// Markdown H1 emitted by the document parser. It is only used as a conservative
// Crossref fallback after explicit journal metadata and DOI lookup fail.
func ExtractDocumentTitle(metadata map[string]string, text string) string {
	for _, key := range []string{"title", "document_title", "article_title"} {
		if value := cleanDocumentTitle(metadata[key]); value != "" {
			return value
		}
	}
	if match := documentTitlePattern.FindStringSubmatch(text); len(match) == 2 {
		return cleanDocumentTitle(match[1])
	}
	return ""
}

// ExtractPublication returns a trusted journal name and optional DOI. Metadata
// takes precedence because it is less ambiguous than extracted text.
func ExtractPublication(metadata map[string]string, text string) (string, string) {
	for _, key := range []string{"journal", "journal_name", "publication", "publication_name", "container_title"} {
		if value := cleanPublication(metadata[key]); value != "" {
			return value, extractDOI(metadata[key])
		}
	}
	if match := journalLinePattern.FindStringSubmatch(text); len(match) == 2 {
		return cleanPublication(match[1]), extractDOI(text)
	}
	return "", extractDOI(text)
}

func extractDOI(value string) string {
	return strings.TrimRight(doiPattern.FindString(value), ".,;:)]}")
}

func cleanPublication(value string) string {
	value = strings.TrimSpace(strings.Trim(value, "`*_\"'"))
	value = strings.TrimRight(value, ".,;:)")
	if len([]rune(value)) < 3 || len([]rune(value)) > 180 {
		return ""
	}
	return value
}

func cleanDocumentTitle(value string) string {
	value = strings.TrimSpace(strings.Trim(value, "`*_\"'"))
	value = strings.TrimSuffix(value, ".pdf")
	if len([]rune(value)) < 10 || len([]rune(value)) > 300 {
		return ""
	}
	return value
}

func normalizePublication(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func (c *Client) lookup(ctx context.Context, publication string) (*types.JournalRankMetadata, error) {
	u, err := url.Parse(c.rankURL)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	q.Set("secretKey", c.secretKey)
	q.Set("publicationName", publication)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("easyscholar request failed: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("easyscholar returned status %d", resp.StatusCode)
	}
	var payload easyScholarResponse
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("invalid easyscholar response: %w", err)
	}
	if payload.Code != 200 {
		return nil, fmt.Errorf("easyscholar returned code %d: %s", payload.Code, payload.Msg)
	}
	return convertRank(publication, payload.Data), nil
}

func (c *Client) resolveDOI(ctx context.Context, doi string) (string, error) {
	endpoint := strings.TrimRight(c.crossrefURL, "/") + "/" + url.PathEscape(doi)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "WeKnora journal rank lookup")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("crossref request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("crossref returned status %d", resp.StatusCode)
	}
	var payload struct {
		Message struct {
			ContainerTitle []string `json:"container-title"`
		} `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&payload); err != nil {
		return "", err
	}
	if len(payload.Message.ContainerTitle) == 0 {
		return "", ErrPublicationMissing
	}
	return cleanPublication(payload.Message.ContainerTitle[0]), nil
}

func (c *Client) resolveTitle(ctx context.Context, title string) (string, error) {
	endpoint, err := url.Parse(strings.TrimRight(c.crossrefURL, "/"))
	if err != nil {
		return "", err
	}
	q := endpoint.Query()
	q.Set("query.title", title)
	q.Set("rows", "1")
	q.Set("select", "title,container-title")
	endpoint.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "WeKnora journal rank lookup")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("crossref title request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("crossref returned status %d", resp.StatusCode)
	}
	var payload struct {
		Message struct {
			Items []struct {
				Title          []string `json:"title"`
				ContainerTitle []string `json:"container-title"`
			} `json:"items"`
		} `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&payload); err != nil {
		return "", err
	}
	if len(payload.Message.Items) == 0 || len(payload.Message.Items[0].Title) == 0 ||
		len(payload.Message.Items[0].ContainerTitle) == 0 ||
		titleTokenOverlap(title, payload.Message.Items[0].Title[0]) < 0.7 {
		return "", ErrPublicationMissing
	}
	publication := cleanPublication(payload.Message.Items[0].ContainerTitle[0])
	if publication == "" {
		return "", ErrPublicationMissing
	}
	return publication, nil
}

func titleTokenOverlap(expected, candidate string) float64 {
	expectedTokens := titleTokens(expected)
	candidateTokens := titleTokens(candidate)
	if len(expectedTokens) == 0 || len(candidateTokens) == 0 {
		return 0
	}
	matched := 0
	for token := range expectedTokens {
		if _, ok := candidateTokens[token]; ok {
			matched++
		}
	}
	denominator := len(expectedTokens)
	if len(candidateTokens) > denominator {
		denominator = len(candidateTokens)
	}
	return float64(matched) / float64(denominator)
}

func titleTokens(value string) map[string]struct{} {
	value = strings.ToLower(value)
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	})
	tokens := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if len(field) > 1 {
			tokens[field] = struct{}{}
		}
	}
	return tokens
}

type easyScholarResponse struct {
	Code int              `json:"code"`
	Msg  string           `json:"msg"`
	Data *easyScholarData `json:"data"`
}

type easyScholarData struct {
	OfficialRank struct {
		All    map[string]string `json:"all"`
		Select map[string]string `json:"select"`
	} `json:"officialRank"`
	CustomRank struct {
		RankInfo []customRankInfo `json:"rankInfo"`
		Rank     []string         `json:"rank"`
	} `json:"customRank"`
}

type customRankInfo struct {
	UUID          string `json:"uuid"`
	AbbName       string `json:"abbName"`
	OneRankText   string `json:"oneRankText"`
	TwoRankText   string `json:"twoRankText"`
	ThreeRankText string `json:"threeRankText"`
	FourRankText  string `json:"fourRankText"`
	FiveRankText  string `json:"fiveRankText"`
}

func convertRank(publication string, data *easyScholarData) *types.JournalRankMetadata {
	result := &types.JournalRankMetadata{SchemaVersion: 1, Publication: publication, MatchedAt: time.Now().UTC(), Source: "easyscholar"}
	if data == nil {
		return result
	}
	result.Official, result.OfficialAll = data.OfficialRank.Select, data.OfficialRank.All
	byUUID := make(map[string]customRankInfo, len(data.CustomRank.RankInfo))
	for _, info := range data.CustomRank.RankInfo {
		byUUID[info.UUID] = info
	}
	for _, encoded := range data.CustomRank.Rank {
		parts := strings.SplitN(encoded, "&&&", 2)
		if len(parts) != 2 {
			continue
		}
		info, ok := byUUID[parts[0]]
		if !ok {
			continue
		}
		levels := map[string]string{"1": info.OneRankText, "2": info.TwoRankText, "3": info.ThreeRankText, "4": info.FourRankText, "5": info.FiveRankText}
		if level := strings.TrimSpace(levels[parts[1]]); level != "" {
			result.Custom = append(result.Custom, types.JournalRankCustomDataset{Label: info.AbbName, Level: level})
		}
	}
	result.Found = len(result.Official) > 0 || len(result.Custom) > 0
	return result
}

func (c *Client) getCached(key string) (*types.JournalRankMetadata, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.cache[key]
	if !ok || time.Now().After(entry.expiresAt) {
		if ok {
			delete(c.cache, key)
		}
		return nil, false
	}
	return entry.value, true
}

func (c *Client) putCached(key string, value *types.JournalRankMetadata) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.cache) >= maxCacheEntries {
		for oldKey := range c.cache {
			delete(c.cache, oldKey)
			break
		}
	}
	c.cache[key] = cacheEntry{value: value, expiresAt: time.Now().Add(cacheTTL)}
}
