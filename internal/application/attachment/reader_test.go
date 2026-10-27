package attachment

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type testStore struct {
	doc     *types.TemporaryDocument
	allowed bool
	loads   int
}

func (s *testStore) SessionAttachments(context.Context) (types.MessageAttachments, error) {
	if !s.allowed {
		return nil, nil
	}
	return types.MessageAttachments{{ID: s.doc.ID, FileName: "report.pdf"}}, nil
}

func (s *testStore) TemporaryDocument(context.Context, string) (*types.TemporaryDocument, error) {
	s.loads++
	return s.doc, nil
}

type testCache struct {
	mu     sync.Mutex
	values map[string]Description
	fail   bool
}

func (c *testCache) Get(_ context.Context, id, key string) (*Description, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return nil, errors.New("cache offline")
	}
	v, ok := c.values[id+key]
	if !ok {
		return nil, nil
	}
	return &v, nil
}

func (c *testCache) Put(_ context.Context, id, key string, v Description) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fail {
		return errors.New("cache offline")
	}
	c.values[id+key] = v
	return nil
}

func imageFixture(t *testing.T) (*testStore, []byte) {
	t.Helper()
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	return &testStore{allowed: true, doc: &types.TemporaryDocument{
		ID: "document", Status: types.TemporaryDocumentStatusReady,
		ResourceRef: "local://owner/image", MimeType: "image/png",
	}}, b.Bytes()
}

func readOne(
	ctx context.Context, reader *Reader, number int, detail, focus string,
) (*interfaces.AttachmentImage, error) {
	reads, err := reader.ReadImages(ctx, "document", []int{number}, detail, focus)
	if err != nil {
		return nil, err
	}
	return reads[0].Image, reads[0].Err
}

func TestReaderAuthorizesBeforeDocumentImageOrCacheAccess(t *testing.T) {
	store, data := imageFixture(t)
	store.allowed = false
	reader := NewReader(store, 0, func(context.Context, string) ([]byte, error) {
		t.Fatal("unauthorized file access")
		return data, nil
	})
	_, err := readOne(context.Background(), reader, 1, "brief", "")
	require.ErrorIs(t, err, interfaces.ErrAttachmentNotFound)
	require.Zero(t, store.loads)
}

func TestDescriptionsSurviveReadersAndSeparateReadingRequirements(t *testing.T) {
	store, data := imageFixture(t)
	cache := &testCache{values: map[string]Description{}}
	calls := 0
	makeReader := func(model string) *Reader {
		reader := NewReader(store, 0, func(context.Context, string) ([]byte, error) { return data, nil })
		reader.Images = &ImageReader{
			Model: model, Cache: cache,
			Describe: func(ctx context.Context, _ []byte, prompt string) (string, error) {
				calls++
				deadline, ok := ctx.Deadline()
				require.True(t, ok)
				require.LessOrEqual(t, time.Until(deadline), imageDescribeFullTimeout)
				return prompt, nil
			},
		}
		return reader
	}
	ctx := context.Background()
	first, err := readOne(ctx, makeReader("vision-a"), 1, "", " axes ")
	require.NoError(t, err)
	require.NotNil(t, first.Description)
	again, err := readOne(ctx, makeReader("vision-a"), 1, "brief", "axes")
	require.NoError(t, err)
	require.Equal(t, first.Description, again.Description)
	require.Equal(t, 1, calls)
	for _, tc := range []struct{ model, detail, focus string }{
		{"vision-a", "brief", "legend"}, {"vision-a", "full", "axes"}, {"vision-b", "brief", "axes"},
	} {
		_, err = readOne(ctx, makeReader(tc.model), 1, tc.detail, tc.focus)
		require.NoError(t, err)
	}
	require.Equal(t, 4, calls)
	data = append(data, 0) // Different source bytes cannot reuse the observation.
	_, err = readOne(ctx, makeReader("vision-a"), 1, "brief", "axes")
	require.NoError(t, err)
	require.Equal(t, 5, calls)
}

func TestFailedDescriptionsAreRetriedAndCacheFailureDoesNotLoseResult(t *testing.T) {
	store, data := imageFixture(t)
	cache := &testCache{values: map[string]Description{}}
	reader := NewReader(store, 0, func(context.Context, string) ([]byte, error) { return data, nil })
	calls := 0
	reader.Images = &ImageReader{Cache: cache, Describe: func(context.Context, []byte, string) (string, error) {
		calls++
		if calls == 1 {
			return "", errors.New("model unavailable")
		}
		return "chart", nil
	}}
	first, err := readOne(context.Background(), reader, 1, "brief", "")
	require.NoError(t, err)
	require.NotEmpty(t, first.Warning)
	require.Empty(t, *first.Description)
	second, err := readOne(context.Background(), reader, 1, "brief", "")
	require.NoError(t, err)
	require.Equal(t, "chart", *second.Description)
	require.Equal(t, 2, calls)
	cache.fail = true
	third, err := readOne(context.Background(), reader, 1, "brief", "new focus")
	require.NoError(t, err)
	require.Equal(t, "chart", *third.Description)
	require.Contains(t, third.Warning, "save description")
}

type failedProjection struct{ images int }

func (*failedProjection) Document(context.Context, *types.TemporaryDocument) (string, error) {
	return "", errors.New("sandbox offline")
}

func (p *failedProjection) Image(
	context.Context, *types.TemporaryDocument, int, []byte, string, string, *Description,
) (map[string]string, error) {
	p.images++
	return nil, errors.New("sandbox offline")
}

func TestNativeVisionAndUnavailableSandboxDoNotRequireDescription(t *testing.T) {
	store, data := imageFixture(t)
	reader := NewReader(store, 0, func(context.Context, string) ([]byte, error) { return data, nil })
	projection := &failedProjection{}
	reader.Projection = projection
	document, err := reader.Resolve(context.Background(), "document", true)
	require.NoError(t, err)
	require.NotNil(t, document.Document)
	require.Contains(t, document.Warning, "sandbox offline")
	view, err := readOne(context.Background(), reader, 1, "brief", "")
	require.NoError(t, err)
	require.NotEmpty(t, view.DataURI)
	require.Nil(t, view.Description)
	require.Equal(t, 1, projection.images)
	require.Contains(t, view.Warning, "sandbox offline")
}

func TestConcurrentDescriptionRequestsShareOneCall(t *testing.T) {
	cache := &testCache{values: map[string]Description{}}
	var calls atomic.Int32
	reader := &ImageReader{Cache: cache, Describe: func(context.Context, []byte, string) (string, error) {
		calls.Add(1)
		return "chart", nil
	}}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, _, err := reader.describe(context.Background(), "document", []byte("image"), "brief", "axes")
			require.NoError(t, err)
			require.Equal(t, "chart", value.Text)
		}()
	}
	wg.Wait()
	require.Equal(t, int32(1), calls.Load())
}

func TestReadImagesAuthorizesOnceAndDescribesConcurrently(t *testing.T) {
	store, data := imageFixture(t)
	store.doc.MimeType = "application/pdf"
	store.doc.ImageRefs = types.JSON(`[{"url":"a"},{"url":"b"},{"url":"c"}]`)
	reader := NewReader(store, 0, func(_ context.Context, url string) ([]byte, error) {
		return append(append([]byte(nil), data...), url...), nil
	})
	var inside sync.WaitGroup
	inside.Add(3)
	reader.Images = &ImageReader{Describe: func(_ context.Context, _ []byte, _ string) (string, error) {
		inside.Done()
		// Every description has to be in flight at once to get past here.
		inside.Wait()
		return "chart", nil
	}}
	done := make(chan []interfaces.AttachmentImageRead)
	go func() {
		reads, err := reader.ReadImages(context.Background(), "document", []int{1, 2, 3, 9}, "brief", "")
		require.NoError(t, err)
		done <- reads
	}()
	select {
	case reads := <-done:
		require.Len(t, reads, 4)
		for i, read := range reads[:3] {
			require.Equal(t, i+1, read.Number)
			require.NoError(t, read.Err)
			require.Equal(t, "chart", *read.Image.Description)
		}
		require.Equal(t, 9, reads[3].Number)
		require.Error(t, reads[3].Err)
	case <-time.After(5 * time.Second):
		t.Fatal("images were described one at a time")
	}
	require.Equal(t, 1, store.loads)
}

func TestDescriptionDeadlineStartsAfterQueueing(t *testing.T) {
	reader := &ImageReader{}
	reader.slotsOnce.Do(func() { reader.slots = make(chan struct{}, 1) })
	reader.slots <- struct{}{}
	const queued = 200 * time.Millisecond
	time.AfterFunc(queued, func() { <-reader.slots })
	var remaining time.Duration
	reader.Describe = func(ctx context.Context, _ []byte, _ string) (string, error) {
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		remaining = time.Until(deadline)
		return "chart", nil
	}
	_, _, err := reader.describe(context.Background(), "document", []byte("image"), "brief", "")
	require.NoError(t, err)
	require.Greater(t, remaining, imageDescribeTimeout-queued/2)
}

func TestCancelledCallerDoesNotFailSharedDescription(t *testing.T) {
	var calls atomic.Int32
	started, release := make(chan struct{}), make(chan struct{})
	reader := &ImageReader{Describe: func(ctx context.Context, _ []byte, _ string) (string, error) {
		calls.Add(1)
		close(started)
		<-release
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return "chart", nil
	}}
	firstCtx, cancel := context.WithCancel(context.Background())
	firstErr := make(chan error, 1)
	go func() {
		_, _, err := reader.describe(firstCtx, "document", []byte("image"), "brief", "")
		firstErr <- err
	}()
	<-started
	second := make(chan *Description, 1)
	go func() {
		value, _, err := reader.describe(context.Background(), "document", []byte("image"), "brief", "")
		require.NoError(t, err)
		second <- value
	}()
	time.Sleep(50 * time.Millisecond) // let the second caller join the in-flight call
	cancel()
	require.ErrorIs(t, <-firstErr, context.Canceled)
	close(release)
	require.Equal(t, "chart", (<-second).Text)
	require.Equal(t, int32(1), calls.Load())
}

func TestChartPromptDoesNotReuseLegacyUnstructuredObservation(t *testing.T) {
	store, data := imageFixture(t)
	const oldPrompt = "Describe this image in at most 120 words. " +
		"Include its type, key labels, numbers and relationships. Do not transcribe long passages. " +
		"Say when content is illegible; do not guess. Plain text only."
	identity, err := json.Marshal([]string{"vision", oldPrompt, fmt.Sprintf("%x", sha256.Sum256(data))})
	require.NoError(t, err)
	key := fmt.Sprintf("%x", sha256.Sum256(identity))
	cache := &testCache{values: map[string]Description{
		store.doc.ID + key: {Text: "160 148 120 116 96", Model: "vision"},
	}}
	reader := NewReader(store, 0, func(context.Context, string) ([]byte, error) { return data, nil })
	calls := 0
	reader.Images = &ImageReader{
		Model: "vision", Cache: cache,
		Describe: func(_ context.Context, image []byte, prompt string) (string, error) {
			calls++
			require.NotEmpty(t, image)
			require.Contains(t, prompt, "one labelled row per category")
			return "East: Q1=102, Q2=148 (万元)", nil
		},
	}
	first, err := readOne(t.Context(), reader, 1, "brief", "")
	require.NoError(t, err)
	require.Equal(t, "East: Q1=102, Q2=148 (万元)", *first.Description)
	_, err = readOne(t.Context(), reader, 1, "brief", "")
	require.NoError(t, err)
	require.Equal(t, 1, calls, "reuse the new observation, not the legacy OCR list")
}
