package client

import "net/url"

// ResourceURLMode selects how file references are returned in API responses.
// Empty leaves the choice to the server (handle by default, or
// RESOURCE_URL_MODE when set).
type ResourceURLMode string

const (
	// ResourceURLModeHandle returns internal resource:// handles; clients fetch
	// bytes through the authenticated /files proxy.
	ResourceURLModeHandle ResourceURLMode = "handle"
	// ResourceURLModePublic returns time-limited HTTP(S) URLs loadable without
	// WeKnora credentials.
	ResourceURLModePublic ResourceURLMode = "public"
)

// ResourceURLOptions carries the optional resource_urls query parameter shared
// by chat, message-history, knowledge-search, and hybrid-search endpoints.
type ResourceURLOptions struct {
	ResourceURLs ResourceURLMode
}

func applyResourceURLQuery(q url.Values, opts *ResourceURLOptions) {
	if opts == nil || opts.ResourceURLs == "" || opts.ResourceURLs == ResourceURLModeHandle {
		return
	}
	q.Set("resource_urls", string(opts.ResourceURLs))
}
