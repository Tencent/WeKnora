package embedding

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/limiter"
	"github.com/Tencent/WeKnora/internal/models/provider"
	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/panjf2000/ants/v2"
)

type cloudBatchTestTransport func(*http.Request) (*http.Response, error)

func (f cloudBatchTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// Exercise the factory's pool wiring and the concurrency wrapper with an
// in-memory transport; no requests reach the configured address.
func newCloudBatchTestEmbedder(t *testing.T, transport cloudBatchTestTransport) Embedder {
	t.Helper()
	t.Setenv("SSRF_WHITELIST", "cloud-batch.invalid")
	secutils.ResetSSRFWhitelistForTest()
	t.Cleanup(secutils.ResetSSRFWhitelistForTest)
	pool, err := ants.NewPool(4)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Release)
	raw, err := newEmbedder(Config{
		Source: types.ModelSourceRemote, Provider: string(provider.ProviderWeKnoraCloud),
		BaseURL: "https://cloud-batch.invalid", ModelID: "cloud-batch-test", ModelName: "test",
		AppID: "test-app", AppSecret: "test-secret",
	}, NewBatchEmbedder(pool), nil)
	if err != nil {
		t.Fatal(err)
	}
	raw.(*WeKnoraCloudEmbedder).client = &http.Client{Transport: transport}
	return wrapEmbeddingConcurrency(raw, 1)
}

func TestWeKnoraCloudPoolBatchesAndPreservesOrder(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "2")
	var mu sync.Mutex
	var sizes []int
	embedder := newCloudBatchTestEmbedder(t, func(r *http.Request) (*http.Response, error) {
		var input weKnoraCloudEmbedRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			return nil, err
		}
		mu.Lock()
		sizes = append(sizes, len(input.Input))
		mu.Unlock()
		// Return provider entries in reverse order to exercise both index
		// restoration within a batch and ordered reassembly across batches.
		entries := make([]string, 0, len(input.Input))
		for i := len(input.Input) - 1; i >= 0; i-- {
			entries = append(entries, fmt.Sprintf(`{"index":%d,"embedding":[%d]}`, i, len(input.Input[i])))
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"data":[` + strings.Join(entries, ",") + `]}`)),
		}, nil
	})
	got, err := embedder.BatchEmbedWithPool(context.Background(), embedder, []string{"a", "bb", "ccc", "dddd", "eeeee"})
	if err != nil {
		t.Fatal(err)
	}
	if want := [][]float32{{1}, {2}, {3}, {4}, {5}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("embeddings = %v, want %v", got, want)
	}
	if len(sizes) != 3 {
		t.Fatalf("request sizes = %v, want three batches", sizes)
	}
	for _, size := range sizes {
		if size < 1 || size > 2 {
			t.Fatalf("request size %d violates BATCH_EMBED_SIZE=2", size)
		}
	}
}

func TestWeKnoraCloudPoolHonoursConcurrencyLimit(t *testing.T) {
	t.Setenv("BATCH_EMBED_SIZE", "1")
	limiter.SetGovernor(limiter.NewLocalLimiter(), 10)
	t.Cleanup(func() { limiter.SetGovernor(nil, 0) })
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	embedder := newCloudBatchTestEmbedder(t, func(*http.Request) (*http.Response, error) {
		entered <- struct{}{}
		<-release
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"data":[{"index":0,"embedding":[1]}]}`)),
		}, nil
	})
	ctx := types.WithBackgroundTask(context.Background())
	var wg sync.WaitGroup
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(func() { unblock(); wg.Wait() })
	errs := make(chan error, 2)
	// Separate callers represent simultaneous wiki batches sharing a model.
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := embedder.BatchEmbedWithPool(ctx, embedder, []string{"x"})
			errs <- err
		}()
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first provider request did not enter")
	}
	select {
	case <-entered:
		t.Error("two provider requests entered concurrently with model limit=1")
	case <-time.After(150 * time.Millisecond):
	}
	unblock()
	for range 2 {
		select {
		case err := <-errs:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("pooled embedding did not complete after release")
		}
	}
}
