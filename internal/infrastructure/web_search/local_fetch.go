package web_search

import (
	"context"
	"errors"

	webfetch "github.com/Tencent/WeKnora/internal/infrastructure/web_fetch"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// LocalFetchSource is WebPage.Source for pages read by WeKnora's own reader.
const LocalFetchSource = "local"

// LocalFetcher returns WeKnora's own page reader, used by providers without a fetch API.
func LocalFetcher() interfaces.WebFetcher {
	return localFetcher{}
}

type localFetcher struct{}

func (localFetcher) Fetch(ctx context.Context, rawURL string) (*types.WebPage, error) {
	content, err := FetchLocal(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	return &types.WebPage{Content: content, Source: LocalFetchSource}, nil
}

type localFetchModeKey struct{}

// WithPipelineFetch selects the quick-answer local reader: 15 seconds, HTTP
// only, and a smaller body. It only affects the local reader; provider fetch
// APIs such as DeepSeek ignore it, so the caller must bound ctx itself. The
// flag rides on ctx because WebFetcher.Fetch has no mode parameter.
func WithPipelineFetch(ctx context.Context) context.Context {
	return context.WithValue(ctx, localFetchModeKey{}, true)
}

// FetchLocal reads a page with WeKnora's own client.
func FetchLocal(ctx context.Context, rawURL string) (string, error) {
	if pipeline, _ := ctx.Value(localFetchModeKey{}).(bool); pipeline {
		return webfetch.NewPipelineFetcher().Fetch(ctx, rawURL)
	}
	return webfetch.NewFetcher().Fetch(ctx, rawURL)
}

// WithLocalFallback reads through primary and retries with the local reader when primary
// fails for its own reasons: rejected credentials, rate limits, outages, or transport errors.
// Failures that describe the page or the caller's deadline are returned as is.
func WithLocalFallback(primary interfaces.WebFetcher) interfaces.WebFetcher {
	return fallbackFetcher{primary: primary, secondary: LocalFetcher()}
}

type fallbackFetcher struct {
	primary, secondary interfaces.WebFetcher
}

func (f fallbackFetcher) Fetch(ctx context.Context, rawURL string) (*types.WebPage, error) {
	page, err := f.primary.Fetch(ctx, rawURL)
	if err == nil || !providerSideFailure(err) || ctx.Err() != nil {
		return page, err
	}
	logger.Warnf(ctx, "[WebFetch] provider fetch failed, using local reader: %v", err)
	return f.secondary.Fetch(ctx, rawURL)
}

func providerSideFailure(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return false
	}
	var fetchErr *webfetch.FetchError
	if !errors.As(err, &fetchErr) {
		return true
	}
	switch fetchErr.Code {
	case webfetch.ErrorProviderAuth, webfetch.ErrorHTTP429, webfetch.ErrorHTTP5xx:
		return true
	}
	return false
}
