package pluginapi

import "time"

// SearchPath is the web search endpoint for provider id.
func SearchPath(id string) string { return "/v1/websearch/" + id + "/search" }

// SearchInput is the input of a web search.
type SearchInput struct {
	Query       string `json:"query"`
	MaxResults  int    `json:"maxResults"`
	IncludeDate bool   `json:"includeDate,omitempty"`
	// Region and Freshness are optional filters; a provider that cannot
	// honour a requested filter must fail rather than ignore it.
	Region    string `json:"region,omitempty"`
	Freshness string `json:"freshness,omitempty"`
}

// SearchOutput is the output of a web search.
type SearchOutput struct {
	Results []SearchResult `json:"results"`
}

// SearchResult is one hit.
type SearchResult struct {
	Title       string     `json:"title"`
	URL         string     `json:"url"`
	Snippet     string     `json:"snippet,omitempty"`
	Content     string     `json:"content,omitempty"`
	Age         string     `json:"age,omitempty"`
	PublishedAt *time.Time `json:"publishedAt,omitempty"`
}
