package xquik

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/datasource"
	"github.com/Tencent/WeKnora/internal/types"
)

type fakeAPI struct {
	validateErr error
	searchFn    func(searchRequest) (searchPage, error)
	requests    []searchRequest
}

func (f *fakeAPI) validate(context.Context) error { return f.validateErr }

func (f *fakeAPI) search(_ context.Context, request searchRequest) (searchPage, error) {
	f.requests = append(f.requests, request)
	return f.searchFn(request)
}

type recordingHandler struct {
	items         []types.FetchedItem
	checkpoints   []*types.SyncCursor
	emitErr       error
	checkpointErr error
}

func (h *recordingHandler) Emit(_ context.Context, item types.FetchedItem) error {
	if h.emitErr != nil {
		return h.emitErr
	}
	h.items = append(h.items, item)
	return nil
}

func (h *recordingHandler) Checkpoint(_ context.Context, cursor *types.SyncCursor) error {
	if h.checkpointErr != nil {
		return h.checkpointErr
	}
	h.checkpoints = append(h.checkpoints, cursor)
	return nil
}

func connectorWith(apiClient api, now time.Time) *Connector {
	return &Connector{
		apiFactory: func(string) api { return apiClient },
		now:        func() time.Time { return now },
	}
}

func TestConnectorValidateAndListResources(t *testing.T) {
	fake := &fakeAPI{searchFn: func(searchRequest) (searchPage, error) { return searchPage{}, nil }}
	connector := connectorWith(fake, time.Time{})
	config := testConfig("xquik api\nfrom:weknora")

	if connector.Type() != types.ConnectorTypeXquik {
		t.Fatalf("Type() = %q", connector.Type())
	}
	if err := connector.Validate(context.Background(), config); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	resources, err := connector.ListResources(context.Background(), config, "")
	if err != nil {
		t.Fatalf("ListResources() error = %v", err)
	}
	if len(resources) != 2 || resources[0].ExternalID != "xquik api" || resources[1].Type != "search_query" {
		t.Fatalf("resources = %#v", resources)
	}
	children, err := connector.ListResources(context.Background(), config, "xquik api")
	if err != nil || len(children) != 0 {
		t.Fatalf("child resources = %#v, error = %v", children, err)
	}
}

func TestConnectorMetadata(t *testing.T) {
	metadata, ok := datasource.ConnectorMetadataRegistry[types.ConnectorTypeXquik]
	if !ok {
		t.Fatal("Xquik connector metadata is not registered")
	}
	if metadata.AuthType != "api_key" || len(metadata.Capabilities) != 1 || metadata.Capabilities[0] != "incremental" {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestConnectorResumesOnlyUnfinishedScheduledFullSync(t *testing.T) {
	connector := NewConnector()
	unfinished := connectorCursor(cursorState{
		InProgress: true, StartedAt: time.Now().UTC(), QueryListHash: queryListHash([]string{"query"}),
	}, false)
	if !connector.ShouldResumeScheduledFullSync(unfinished) {
		t.Fatal("unfinished cursor was not resumed")
	}
	if connector.ShouldResumeScheduledFullSync(connectorCursor(cursorState{}, true)) {
		t.Fatal("completed cursor was resumed")
	}
}

func TestConnectorFetchStreamPaginatesAndDeduplicates(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	created := now.Add(-time.Hour)
	fake := &fakeAPI{}
	fake.searchFn = func(request searchRequest) (searchPage, error) {
		switch request.Query + ":" + request.Cursor {
		case "first:":
			return searchPage{
				Tweets: []tweet{
					{ID: "1", Text: "First post", CreatedAt: flexibleTime{created}, Author: author{Name: "Ada", Username: "ada"}},
					{ID: "2", Text: "Shared post"},
				},
				HasNextPage: true, NextCursor: "next",
			}, nil
		case "first:next":
			return searchPage{Tweets: []tweet{{ID: "3", Text: "Third post"}}}, nil
		case "second:":
			return searchPage{Tweets: []tweet{{ID: "2", Text: "Shared post"}, {ID: "4", Text: "Fourth post"}}}, nil
		default:
			return searchPage{}, errors.New("unexpected search")
		}
	}
	connector := connectorWith(fake, now)
	config := testConfig("first\nsecond")
	config.Settings["results_per_query"] = 3
	handler := &recordingHandler{}

	final, err := connector.FetchStream(context.Background(), config, nil, handler)
	if err != nil {
		t.Fatalf("FetchStream() error = %v", err)
	}
	if len(handler.items) != 4 {
		t.Fatalf("items = %d, want 4", len(handler.items))
	}
	if len(fake.requests) != 3 || fake.requests[1].Cursor != "next" || fake.requests[1].Limit != 1 {
		t.Fatalf("requests = %#v", fake.requests)
	}
	first := handler.items[0]
	if first.ExternalID != "xquik:post:1" || first.URL != "https://x.com/ada/status/1" {
		t.Fatalf("first item = %#v", first)
	}
	if first.Metadata["channel"] != types.ChannelXquik || first.SourceResourceID != "first" {
		t.Fatalf("first item metadata = %#v", first.Metadata)
	}
	content := string(first.Content)
	if !strings.Contains(content, "> First post") || !strings.Contains(content, "View post on X") {
		t.Fatalf("first content = %q", first.Content)
	}
	if len(handler.checkpoints) != 3 {
		t.Fatalf("checkpoints = %d, want 3", len(handler.checkpoints))
	}
	if !final.LastSyncTime.Equal(now) {
		t.Fatalf("LastSyncTime = %s", final.LastSyncTime)
	}
}

func TestConnectorCountsDuplicatesTowardEachQueryLimit(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	fake := &fakeAPI{}
	fake.searchFn = func(request searchRequest) (searchPage, error) {
		switch request.Query {
		case "first":
			return searchPage{Tweets: []tweet{{ID: "1"}, {ID: "2"}}}, nil
		case "second":
			return searchPage{
				Tweets: []tweet{{ID: "1"}, {ID: "2"}}, HasNextPage: true, NextCursor: "extra",
			}, nil
		default:
			return searchPage{}, errors.New("unexpected search")
		}
	}
	config := testConfig("first\nsecond")
	config.Settings["results_per_query"] = 2
	handler := &recordingHandler{}

	_, err := connectorWith(fake, now).FetchStream(context.Background(), config, nil, handler)
	if err != nil {
		t.Fatalf("FetchStream() error = %v", err)
	}
	if len(fake.requests) != 2 || fake.requests[1].Cursor != "" {
		t.Fatalf("requests = %#v", fake.requests)
	}
	if len(handler.items) != 2 {
		t.Fatalf("items = %d, want 2", len(handler.items))
	}
}

func TestConnectorFetchIncrementalUsesBoundedOverlap(t *testing.T) {
	lastSync := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	now := lastSync.Add(time.Hour)
	fake := &fakeAPI{searchFn: func(searchRequest) (searchPage, error) { return searchPage{}, nil }}
	connector := connectorWith(fake, now)
	config := testConfig("xquik")
	config.ResourceIDs = []string{"xquik"}

	items, final, err := connector.FetchIncremental(
		context.Background(), config, &types.SyncCursor{LastSyncTime: lastSync},
	)
	if err != nil {
		t.Fatalf("FetchIncremental() error = %v", err)
	}
	if len(items) != 0 || len(fake.requests) != 1 {
		t.Fatalf("items = %d, requests = %#v", len(items), fake.requests)
	}
	request := fake.requests[0]
	if !request.SinceTime.Equal(lastSync.Add(-syncOverlap)) || !request.UntilTime.Equal(now) {
		t.Fatalf("time bounds = %s to %s", request.SinceTime, request.UntilTime)
	}
	if !final.LastSyncTime.Equal(now) {
		t.Fatalf("LastSyncTime = %s", final.LastSyncTime)
	}
}

func TestConnectorFetchStreamResumesFromCheckpoint(t *testing.T) {
	started := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	resume := connectorCursor(cursorState{
		InProgress: true, StartedAt: started, Query: "query", PageCursor: "next",
		ResultsFetched: 2, PagesFetched: 1, QueryListHash: queryListHash([]string{"query"}),
	}, false)
	fake := &fakeAPI{searchFn: func(request searchRequest) (searchPage, error) {
		return searchPage{Tweets: []tweet{{ID: "3", Text: "Resumed"}}}, nil
	}}
	connector := connectorWith(fake, started.Add(time.Hour))
	config := testConfig("query")
	config.Settings["results_per_query"] = 3
	handler := &recordingHandler{}

	final, err := connector.FetchStream(context.Background(), config, resume, handler)
	if err != nil {
		t.Fatalf("FetchStream() error = %v", err)
	}
	if len(fake.requests) != 1 || fake.requests[0].Cursor != "next" || fake.requests[0].Limit != 1 {
		t.Fatalf("requests = %#v", fake.requests)
	}
	if len(handler.items) != 1 || !final.LastSyncTime.Equal(started) {
		t.Fatalf("items = %d, final = %#v", len(handler.items), final)
	}
}

func TestConnectorFetchStreamPreservesDeduplicationAcrossResume(t *testing.T) {
	started := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	resume := connectorCursor(cursorState{
		InProgress: true, StartedAt: started, QueryIndex: 1, SeenPostIDs: []string{"1"},
		QueryListHash: queryListHash([]string{"first", "second"}),
	}, false)
	fake := &fakeAPI{searchFn: func(request searchRequest) (searchPage, error) {
		return searchPage{Tweets: []tweet{{ID: "1"}, {ID: "2"}}}, nil
	}}
	config := testConfig("first\nsecond")
	config.Settings["results_per_query"] = 2
	handler := &recordingHandler{}

	_, err := connectorWith(fake, started.Add(time.Hour)).FetchStream(
		context.Background(), config, resume, handler,
	)
	if err != nil {
		t.Fatalf("FetchStream() error = %v", err)
	}
	if len(fake.requests) != 1 || fake.requests[0].Query != "second" {
		t.Fatalf("requests = %#v", fake.requests)
	}
	if len(handler.items) != 1 || handler.items[0].ExternalID != "xquik:post:2" {
		t.Fatalf("items = %#v", handler.items)
	}
}

func TestConnectorContinuesOverflowWithoutAdvancingSyncTime(t *testing.T) {
	previous := time.Date(2026, 8, 26, 10, 0, 0, 0, time.UTC)
	started := previous.Add(2 * time.Hour)
	fake := &fakeAPI{searchFn: func(request searchRequest) (searchPage, error) {
		if request.Cursor == "" {
			return searchPage{
				Tweets: []tweet{{ID: "1"}, {ID: "2"}}, HasNextPage: true, NextCursor: "older",
			}, nil
		}
		if request.Cursor == "older" {
			return searchPage{Tweets: []tweet{{ID: "3"}}}, nil
		}
		return searchPage{}, errors.New("unexpected cursor")
	}}
	connector := connectorWith(fake, started)
	config := testConfig("query")
	config.ResourceIDs = []string{"query"}
	config.Settings["results_per_query"] = 2

	firstItems, continuation, err := connector.FetchIncremental(
		context.Background(), config, &types.SyncCursor{LastSyncTime: previous},
	)
	if err != nil {
		t.Fatalf("first FetchIncremental() error = %v", err)
	}
	state := decodeCursor(continuation)
	if len(firstItems) != 2 || !continuation.LastSyncTime.Equal(previous) ||
		!state.InProgress || state.PageCursor != "older" || state.ResultsFetched != 0 ||
		state.PagesFetched != 0 || state.QueryListHash != queryListHash([]string{"query"}) {
		t.Fatalf("first items=%d cursor=%#v state=%#v", len(firstItems), continuation, state)
	}

	secondItems, final, err := connector.FetchIncremental(context.Background(), config, continuation)
	if err != nil {
		t.Fatalf("second FetchIncremental() error = %v", err)
	}
	if len(secondItems) != 1 || secondItems[0].ExternalID != "xquik:post:3" ||
		!final.LastSyncTime.Equal(started) {
		t.Fatalf("second items=%#v final=%#v", secondItems, final)
	}
	if len(fake.requests) != 2 || fake.requests[1].Cursor != "older" ||
		!fake.requests[1].SinceTime.Equal(previous.Add(-syncOverlap)) ||
		!fake.requests[1].UntilTime.Equal(started) {
		t.Fatalf("requests = %#v", fake.requests)
	}
}

func TestConnectorResetsPageGuardAtOverflowCheckpoint(t *testing.T) {
	started := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	resume := connectorCursor(cursorState{
		InProgress: true, StartedAt: started, Query: "query", PageCursor: "current",
		PagesFetched: maxPagesPerQuery - 1, QueryListHash: queryListHash([]string{"query"}),
	}, false)
	fake := &fakeAPI{searchFn: func(searchRequest) (searchPage, error) {
		return searchPage{
			Tweets: []tweet{{ID: "1"}, {ID: "2"}}, HasNextPage: true, NextCursor: "next-run",
		}, nil
	}}
	config := testConfig("query")
	config.ResourceIDs = []string{"query"}
	config.Settings["results_per_query"] = 2

	_, continuation, err := connectorWith(fake, started.Add(time.Hour)).FetchIncremental(
		context.Background(), config, resume,
	)
	if err != nil {
		t.Fatalf("FetchIncremental() error = %v", err)
	}
	state := decodeCursor(continuation)
	if state.PageCursor != "next-run" || state.PagesFetched != 0 || state.ResultsFetched != 0 {
		t.Fatalf("state = %#v", state)
	}
}

func TestRememberPostIDKeepsBoundedExactWindow(t *testing.T) {
	state := cursorState{}
	seen := make(map[string]struct{})
	rememberPostID(&state, seen, "1", 2)
	rememberPostID(&state, seen, "2", 2)
	rememberPostID(&state, seen, "3", 2)

	if len(state.SeenPostIDs) != 2 || state.SeenPostOffset != 1 {
		t.Fatalf("state = %#v", state)
	}
	if _, exists := seen["1"]; exists {
		t.Fatal("evicted post remained in the deduplication set")
	}
	if _, exists := seen["2"]; !exists {
		t.Fatal("post 2 was evicted early")
	}
	if _, exists := seen["3"]; !exists {
		t.Fatal("post 3 was not remembered")
	}
	resumed := decodeCursor(connectorCursor(state, false))
	if len(resumed.SeenPostIDs) != 2 || resumed.SeenPostOffset != 1 {
		t.Fatalf("resumed state = %#v", resumed)
	}
}

func TestConnectorRejectsResumeAfterSelectedQueriesChange(t *testing.T) {
	started := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	resume := connectorCursor(cursorState{
		InProgress: true, StartedAt: started, QueryIndex: 1,
		QueryListHash: queryListHash([]string{"first", "second"}),
	}, false)
	fake := &fakeAPI{searchFn: func(searchRequest) (searchPage, error) {
		return searchPage{}, nil
	}}
	config := testConfig("replacement\nsecond")
	config.ResourceIDs = []string{"replacement", "second"}

	_, _, err := connectorWith(fake, started.Add(time.Hour)).FetchIncremental(
		context.Background(), config, resume,
	)
	if err != nil {
		t.Fatalf("FetchIncremental() error = %v", err)
	}
	if len(fake.requests) != 2 || fake.requests[0].Query != "replacement" || fake.requests[0].Cursor != "" {
		t.Fatalf("requests = %#v", fake.requests)
	}
}

func TestConnectorFetchStreamRejectsRepeatedCursor(t *testing.T) {
	fake := &fakeAPI{searchFn: func(searchRequest) (searchPage, error) {
		return searchPage{HasNextPage: true, NextCursor: "same"}, nil
	}}
	connector := connectorWith(fake, time.Now().UTC())
	config := testConfig("query")
	handler := &recordingHandler{}

	_, err := connector.FetchStream(context.Background(), config, nil, handler)
	if err == nil || !strings.Contains(err.Error(), "repeated its cursor") {
		t.Fatalf("FetchStream() error = %v", err)
	}
}

func TestConnectorFetchStreamPropagatesHandlerErrors(t *testing.T) {
	fake := &fakeAPI{searchFn: func(searchRequest) (searchPage, error) {
		return searchPage{Tweets: []tweet{{ID: "1"}}}, nil
	}}
	connector := connectorWith(fake, time.Now().UTC())
	emitErr := errors.New("ingest failed")
	handler := &recordingHandler{emitErr: emitErr}

	_, err := connector.FetchStream(context.Background(), testConfig("query"), nil, handler)
	if !errors.Is(err, emitErr) {
		t.Fatalf("FetchStream() error = %v", err)
	}
}

func TestConnectorRejectsInvalidPostID(t *testing.T) {
	fake := &fakeAPI{searchFn: func(searchRequest) (searchPage, error) {
		return searchPage{Tweets: []tweet{{ID: "../outside"}}}, nil
	}}
	connector := connectorWith(fake, time.Now().UTC())

	_, err := connector.FetchAll(context.Background(), testConfig("query"), nil)
	if err == nil || !strings.Contains(err.Error(), "invalid id") {
		t.Fatalf("FetchAll() error = %v", err)
	}
}

func TestPostItemUsesSafePublicURLAndSingleLineAuthor(t *testing.T) {
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	connector := connectorWith(&fakeAPI{}, now)
	item := connector.postItem(tweet{
		ID: "123", Text: "Post", Author: author{Name: "Ada\nLovelace", Username: "../unsafe"},
	}, "query", now)

	if item.URL != "https://x.com/i/web/status/123" {
		t.Fatalf("URL = %q", item.URL)
	}
	if !strings.HasPrefix(item.Title, "Ada Lovelace") {
		t.Fatalf("Title = %q", item.Title)
	}
	if strings.Contains(item.Title, "\n") || strings.Contains(string(item.Content), "Ada\n") {
		t.Fatalf("author was not normalized: title=%q content=%q", item.Title, item.Content)
	}
}
