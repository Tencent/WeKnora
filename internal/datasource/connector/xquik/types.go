// Package xquik implements the Xquik data source connector for WeKnora.
package xquik

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

const (
	defaultBaseURL         = "https://xquik.com/api/v1"
	defaultResultsPerQuery = 100
	maxResultsPerQuery     = 1000
	maxQueries             = 20
	maxQueryRunes          = 512
	maxPersistedPostIDs    = maxQueries * maxResultsPerQuery
	syncOverlap            = 5 * time.Minute
)

type config struct {
	APIKey          string
	Queries         []string
	ResultsPerQuery int
}

func parseConfig(ds *types.DataSourceConfig) (*config, error) {
	if ds == nil {
		return nil, fmt.Errorf("%w: config is nil", datasource.ErrInvalidConfig)
	}

	apiKey, _ := ds.Credentials["api_key"].(string)
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" {
		return nil, fmt.Errorf("%w: api_key is required", datasource.ErrInvalidCredentials)
	}

	rawQueries, queriesConfigured, err := stringSetting(ds.Settings, "queries")
	if err != nil {
		return nil, err
	}
	if !queriesConfigured {
		rawQueries, _ = ds.Credentials["queries"].(string)
	}
	queries, err := parseQueries(rawQueries)
	if err != nil {
		return nil, err
	}

	results := defaultResultsPerQuery
	if value, ok, settingErr := numericSetting(ds.Settings, "results_per_query"); settingErr != nil {
		return nil, settingErr
	} else if ok {
		results = value
	} else if value, ok, credentialErr := numericSetting(
		ds.Credentials,
		"results_per_query",
	); credentialErr != nil {
		return nil, credentialErr
	} else if ok {
		results = value
	}
	if results < 1 || results > maxResultsPerQuery {
		return nil, fmt.Errorf(
			"%w: results_per_query must be between 1 and %d",
			datasource.ErrInvalidConfig,
			maxResultsPerQuery,
		)
	}

	return &config{APIKey: apiKey, Queries: queries, ResultsPerQuery: results}, nil
}

func stringSetting(settings map[string]interface{}, key string) (string, bool, error) {
	if len(settings) == 0 {
		return "", false, nil
	}
	raw, exists := settings[key]
	if !exists {
		return "", false, nil
	}
	value, ok := raw.(string)
	if !ok {
		return "", true, fmt.Errorf("%w: %s must be a string", datasource.ErrInvalidConfig, key)
	}
	return strings.TrimSpace(value), true, nil
}

func numericSetting(settings map[string]interface{}, key string) (int, bool, error) {
	if len(settings) == 0 {
		return 0, false, nil
	}
	raw, exists := settings[key]
	if !exists {
		return 0, false, nil
	}
	invalid := func() (int, bool, error) {
		return 0, true, fmt.Errorf("%w: %s must be an integer", datasource.ErrInvalidConfig, key)
	}
	switch value := raw.(type) {
	case int:
		return value, true, nil
	case int32:
		return int(value), true, nil
	case int64:
		return int(value), true, nil
	case float64:
		if value != float64(int(value)) {
			return invalid()
		}
		return int(value), true, nil
	case json.Number:
		parsed, err := strconv.Atoi(value.String())
		if err != nil {
			return invalid()
		}
		return parsed, true, nil
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return invalid()
		}
		return parsed, true, nil
	default:
		return invalid()
	}
}

func parseQueries(raw string) ([]string, error) {
	seen := make(map[string]struct{})
	queries := make([]string, 0)
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		query := strings.TrimSpace(line)
		if query == "" {
			continue
		}
		if utf8.RuneCountInString(query) > maxQueryRunes {
			return nil, fmt.Errorf(
				"%w: each query must be at most %d characters",
				datasource.ErrInvalidConfig,
				maxQueryRunes,
			)
		}
		if _, exists := seen[query]; exists {
			continue
		}
		seen[query] = struct{}{}
		queries = append(queries, query)
		if len(queries) > maxQueries {
			return nil, fmt.Errorf(
				"%w: at most %d queries are supported",
				datasource.ErrInvalidConfig,
				maxQueries,
			)
		}
	}
	if len(queries) == 0 {
		return nil, fmt.Errorf("%w: queries is required", datasource.ErrInvalidConfig)
	}
	return queries, nil
}

func (c *config) selectedQueries(resourceIDs []string) ([]string, error) {
	if len(resourceIDs) == 0 {
		return append([]string(nil), c.Queries...), nil
	}
	allowed := make(map[string]struct{}, len(c.Queries))
	for _, query := range c.Queries {
		allowed[query] = struct{}{}
	}
	seen := make(map[string]struct{}, len(resourceIDs))
	selected := make([]string, 0, len(resourceIDs))
	for _, raw := range resourceIDs {
		query := strings.TrimSpace(raw)
		if _, ok := allowed[query]; !ok {
			continue
		}
		if _, duplicate := seen[query]; duplicate {
			continue
		}
		seen[query] = struct{}{}
		selected = append(selected, query)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("%w: no configured queries are selected", datasource.ErrInvalidConfig)
	}
	return selected, nil
}

type flexibleTime struct {
	time.Time
}

func (t *flexibleTime) UnmarshalJSON(data []byte) error {
	if string(data) == "null" || len(data) == 0 {
		return nil
	}
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		if strings.TrimSpace(text) == "" {
			return nil
		}
		parsed, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			return fmt.Errorf("parse tweet timestamp: %w", err)
		}
		t.Time = parsed.UTC()
		return nil
	}
	var seconds float64
	if err := json.Unmarshal(data, &seconds); err != nil {
		return fmt.Errorf("parse tweet timestamp: %w", err)
	}
	whole, fraction := math.Modf(seconds)
	t.Time = time.Unix(int64(whole), int64(fraction*float64(time.Second))).UTC()
	return nil
}

type author struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Username   string `json:"username"`
	UserName   string `json:"userName"`
	ScreenName string `json:"screen_name"`
}

func (a author) handle() string {
	for _, value := range []string{a.Username, a.UserName, a.ScreenName} {
		if value = singleLine(value); value != "" {
			return strings.TrimPrefix(value, "@")
		}
	}
	return ""
}

type tweet struct {
	ID                 string       `json:"id"`
	Text               string       `json:"text"`
	Language           string       `json:"lang"`
	CreatedAt          flexibleTime `json:"createdAt"`
	CreatedAtSnake     flexibleTime `json:"created_at"`
	Author             author       `json:"author"`
	RetweetCount       int64        `json:"retweetCount"`
	RetweetCountSnake  int64        `json:"retweet_count"`
	ReplyCount         int64        `json:"replyCount"`
	ReplyCountSnake    int64        `json:"reply_count"`
	LikeCount          int64        `json:"likeCount"`
	LikeCountSnake     int64        `json:"like_count"`
	QuoteCount         int64        `json:"quoteCount"`
	QuoteCountSnake    int64        `json:"quote_count"`
	ViewCount          int64        `json:"viewCount"`
	ViewCountSnake     int64        `json:"view_count"`
	BookmarkCount      int64        `json:"bookmarkCount"`
	BookmarkCountSnake int64        `json:"bookmark_count"`
}

func (t tweet) createdTime() time.Time {
	if !t.CreatedAt.IsZero() {
		return t.CreatedAt.Time
	}
	return t.CreatedAtSnake.Time
}

func firstNonZero(values ...int64) int64 {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}

type searchPage struct {
	Tweets           []tweet `json:"tweets"`
	HasNextPage      bool    `json:"has_next_page"`
	NextCursor       string  `json:"next_cursor"`
	LegacyHasMore    bool    `json:"hasMore"`
	LegacyNextCursor string  `json:"nextCursor"`
}

func (p searchPage) hasMore() bool {
	return p.HasNextPage || p.LegacyHasMore
}

func (p searchPage) nextCursor() string {
	if p.NextCursor != "" {
		return p.NextCursor
	}
	return p.LegacyNextCursor
}

type searchRequest struct {
	Query     string
	Cursor    string
	SinceTime time.Time
	UntilTime time.Time
	Limit     int
}

type cursorState struct {
	InProgress     bool      `json:"in_progress"`
	StartedAt      time.Time `json:"started_at"`
	PreviousSyncAt time.Time `json:"previous_sync_at,omitempty"`
	SinceTime      time.Time `json:"since_time,omitempty"`
	QueryIndex     int       `json:"query_index,omitempty"`
	Query          string    `json:"query,omitempty"`
	PageCursor     string    `json:"page_cursor,omitempty"`
	ResultsFetched int       `json:"results_fetched,omitempty"`
	PagesFetched   int       `json:"pages_fetched,omitempty"`
	SeenPostIDs    []string  `json:"seen_post_ids,omitempty"`
	SeenPostOffset int       `json:"seen_post_offset,omitempty"`
	QueryListHash  string    `json:"query_list_hash"`
}

func (s cursorState) canResume(queries []string, resultsPerQuery int) bool {
	if !s.InProgress || s.StartedAt.IsZero() || s.QueryIndex < 0 || s.QueryIndex > len(queries) {
		return false
	}
	if s.QueryListHash != queryListHash(queries) {
		return false
	}
	if s.ResultsFetched < 0 || s.ResultsFetched > resultsPerQuery ||
		s.PagesFetched < 0 || s.PagesFetched >= maxPagesPerQuery ||
		len(s.SeenPostIDs) > maxPersistedPostIDs || s.SeenPostOffset < 0 ||
		(len(s.SeenPostIDs) < maxPersistedPostIDs && s.SeenPostOffset != 0) ||
		(len(s.SeenPostIDs) == maxPersistedPostIDs && s.SeenPostOffset >= maxPersistedPostIDs) {
		return false
	}
	seen := make(map[string]struct{}, len(s.SeenPostIDs))
	for _, id := range s.SeenPostIDs {
		if !validPostID(id) {
			return false
		}
		if _, duplicate := seen[id]; duplicate {
			return false
		}
		seen[id] = struct{}{}
	}
	if s.QueryIndex == len(queries) {
		return s.Query == "" && s.PageCursor == "" && s.ResultsFetched == 0 && s.PagesFetched == 0
	}
	if s.Query == "" {
		return s.PageCursor == "" && s.ResultsFetched == 0 && s.PagesFetched == 0
	}
	return s.Query == queries[s.QueryIndex] && s.PageCursor != ""
}

func queryListHash(queries []string) string {
	encoded, _ := json.Marshal(queries)
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest)
}

func decodeCursor(cursor *types.SyncCursor) cursorState {
	if cursor == nil || len(cursor.ConnectorCursor) == 0 {
		return cursorState{}
	}
	data, err := json.Marshal(cursor.ConnectorCursor)
	if err != nil {
		return cursorState{}
	}
	var state cursorState
	if err := json.Unmarshal(data, &state); err != nil {
		return cursorState{}
	}
	return state
}

func connectorCursor(state cursorState, complete bool) *types.SyncCursor {
	state.InProgress = !complete
	if complete {
		state.QueryIndex = 0
		state.Query = ""
		state.PageCursor = ""
		state.ResultsFetched = 0
		state.PagesFetched = 0
		state.SeenPostIDs = nil
		state.SeenPostOffset = 0
		state.QueryListHash = ""
	}
	data, _ := json.Marshal(state)
	values := make(map[string]interface{})
	_ = json.Unmarshal(data, &values)
	lastSync := state.PreviousSyncAt
	if complete {
		lastSync = state.StartedAt
	}
	return &types.SyncCursor{LastSyncTime: lastSync, ConnectorCursor: values}
}
