package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
)

// ListingPageStore is scoped to one sync log and its exact source configuration.
type ListingPageStore interface {
	Load(context.Context, string) ([]byte, error)
	Save(context.Context, string, []byte) error
}

type (
	listingStoreKey   struct{}
	listingAttemptKey struct{}
)

// WithListingPageStore enables persisted directory pagination for a sync job.
func WithListingPageStore(ctx context.Context, store ListingPageStore) context.Context {
	return context.WithValue(ctx, listingStoreKey{}, store)
}

// Only ops.List receives this context. Document reads and permission probes
// elsewhere in the connector always reach the provider.
type listingAttempt struct {
	store    ListingPageStore
	resource string
	mu       sync.Mutex
	err      error
	restored bool
}

func startListing(ctx context.Context, resource string) (context.Context, *listingAttempt) {
	store, _ := ctx.Value(listingStoreKey{}).(ListingPageStore)
	a := &listingAttempt{store: store, resource: resource}
	if store == nil {
		return ctx, a
	}
	return context.WithValue(ctx, listingAttemptKey{}, a), a
}

func (a *listingAttempt) interrupt(err error) error {
	if a != nil {
		a.mu.Lock()
		defer a.mu.Unlock()
		if a.err == nil {
			a.err = err
		}
	}
	return err
}

type listingPage[T any] struct {
	Items []T    `json:"items"`
	Next  string `json:"next"`
	More  bool   `json:"more"`
}

// Keep only validated nonterminal pages. Every directory, including a restored
// one, needs a successful live final page before any of its entries are used.
func listPages[T any](
	ctx context.Context, directory []string, label string, fetch func(string) (listingPage[T], error),
) ([]T, error) {
	a, _ := ctx.Value(listingAttemptKey{}).(*listingAttempt)
	var items []T
	seen := map[string]bool{}
	token := ""
	replay := a != nil
	usedCache := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var page listingPage[T]
		var key string
		cached := false
		if a != nil {
			identity, _ := json.Marshal([]interface{}{a.resource, directory, token})
			key = fmt.Sprintf("%x", sha256.Sum256(identity))
			if replay {
				data, err := a.store.Load(ctx, key)
				if err != nil {
					return nil, a.interrupt(fmt.Errorf("load listing checkpoint: %w", err))
				}
				if data != nil {
					if err := json.Unmarshal(data, &page); err != nil || !page.More || page.Next == "" {
						return nil, a.interrupt(fmt.Errorf("invalid %s listing checkpoint", label))
					}
					cached = true
					usedCache = true
					a.mu.Lock()
					a.restored = true
					a.mu.Unlock()
				}
			}
		}
		if !cached {
			var err error
			page, err = fetch(token)
			if err != nil {
				var apiErr *feishuAPIError
				if usedCache && errors.As(err, &apiErr) &&
					(apiErr.statusCode == http.StatusUnauthorized || apiErr.statusCode == http.StatusForbidden) {
					return nil, a.interrupt(err)
				}
				// A saved continuation can expire. Start this directory afresh once.
				if usedCache && ctx.Err() == nil {
					replay = false
					usedCache = false
					items = nil
					token = ""
					seen = map[string]bool{}
					continue
				}
				if token != "" && a != nil {
					return nil, a.interrupt(fmt.Errorf("interrupted %s pagination: %w", label, err))
				}
				return nil, err
			}
		}
		items = append(items, page.Items...)
		if !page.More {
			return items, nil
		}
		if page.Next == "" {
			return nil, a.interrupt(fmt.Errorf("%s pagination: has_more=true with a missing page token", label))
		}
		if seen[page.Next] {
			return nil, a.interrupt(fmt.Errorf("%s pagination repeated page token %q", label, page.Next))
		}
		if !cached && a != nil {
			data, err := json.Marshal(page)
			if err == nil {
				err = a.store.Save(ctx, key, data)
			}
			if err != nil {
				return nil, a.interrupt(fmt.Errorf("save listing checkpoint: %w", err))
			}
		}
		seen[page.Next] = true
		token = page.Next
	}
}
