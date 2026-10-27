package web_search

import (
	"context"
	"errors"
	"strings"
	"testing"

	webfetch "github.com/Tencent/WeKnora/internal/infrastructure/web_fetch"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestFetchLocalRejectsEmptyURL(t *testing.T) {
	for _, ctx := range []context.Context{context.Background(), WithPipelineFetch(context.Background())} {
		_, err := FetchLocal(ctx, "")
		var fetchErr *webfetch.FetchError
		if !errors.As(err, &fetchErr) || fetchErr.Code != webfetch.ErrorInvalidURL {
			t.Fatalf("error = %#v, want invalid URL", err)
		}
	}
}

type stubFetcher struct {
	err   error
	calls int
}

func (f *stubFetcher) Fetch(context.Context, string) (*types.WebPage, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return &types.WebPage{Content: "local", Source: LocalFetchSource}, nil
}

func TestFallbackFetcherRetriesOnlyProviderSideFailures(t *testing.T) {
	for _, tc := range []struct {
		err      error
		fallback bool
	}{
		{&webfetch.FetchError{Code: webfetch.ErrorProviderAuth}, true},
		{&webfetch.FetchError{Code: webfetch.ErrorHTTP429, Retryable: true}, true},
		{&webfetch.FetchError{Code: webfetch.ErrorHTTP5xx, Retryable: true}, true},
		{errors.New("connection reset"), true},
		{&webfetch.FetchError{Code: webfetch.ErrorInvalidURL}, false},
		{&webfetch.FetchError{Code: webfetch.ErrorSSRFRejected}, false},
		{context.DeadlineExceeded, false},
	} {
		secondary := &stubFetcher{}
		page, err := fallbackFetcher{primary: &stubFetcher{err: tc.err}, secondary: secondary}.
			Fetch(context.Background(), "https://example.com")
		if tc.fallback {
			if err != nil || page.Source != LocalFetchSource || secondary.calls != 1 {
				t.Fatalf("%v: page=%+v err=%v calls=%d, want local fallback", tc.err, page, err, secondary.calls)
			}
			continue
		}
		if !errors.Is(err, tc.err) || secondary.calls != 0 {
			t.Fatalf("%v: err=%v calls=%d, want primary error without fallback", tc.err, err, secondary.calls)
		}
	}
}

func TestDeepSeekFetchDoesNotUseLocalReader(t *testing.T) {
	provider := &DeepSeekProvider{apiKey: "sk-test"}
	_, err := provider.Fetch(context.Background(), "https://user:secret@example.com")
	var fetchErr *webfetch.FetchError
	if !errors.As(err, &fetchErr) || fetchErr.Code != webfetch.ErrorInvalidURL {
		t.Fatalf("error = %#v", err)
	}
	if !strings.Contains(fetchErr.Error(), "username") {
		t.Fatalf("error = %v, want the DeepSeek URL check", err)
	}
}
