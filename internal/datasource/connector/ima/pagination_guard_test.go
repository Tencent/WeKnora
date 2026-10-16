package ima

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/logger"
)

// paginationHopCap mirrors maxPaginationHops in connector.go. It is spelled as
// a literal so this file also compiles — and then fails on behaviour — against
// a tree that has no guard at all.
const paginationHopCap = 10000

// fakeLoopBudget bounds an unguarded run a little above the guard's own cap:
// the fake answers 400 once the budget is gone, so a regression fails with the
// wrong error instead of spinning forever.
const fakeLoopBudget = paginationHopCap + 5

func imaTestClient(t *testing.T, fake *fakeIMA) *client {
	t.Helper()
	cfg, err := parseIMAConfig(fake.config("kb-1"))
	require.NoError(t, err)
	return newClient(cfg)
}

// A vendor that keeps answering is_end=false with the same next_cursor used to
// make all three loops request the same page until the task deadline. Each
// loop must now stop as soon as a cursor repeats.
func TestListResourcesRejectsRepeatedCursor(t *testing.T) {
	fake := newFakeIMA(t)
	fake.setKB("kb-1", nil)
	fake.paging["get_addable_knowledge_base_list"] = fakePaging{
		nextCursor:  "same-cursor",
		maxRequests: fakeLoopBudget,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := NewConnector().ListResources(ctx, fake.config("kb-1"), "")
	require.Error(t, err)
	require.Equal(t, 2, fake.callCount("get_addable_knowledge_base_list"),
		"the guard must reject the repeated cursor on the second page; err=%v", err)
	require.Contains(t, err.Error(), `repeated next_cursor "same-cursor"`)
}

func TestListResourcesSearchFallbackRejectsRepeatedCursor(t *testing.T) {
	fake := newFakeIMA(t)
	// No addable knowledge bases: ListResources falls back to search.
	fake.paging["search_knowledge_base"] = fakePaging{
		nextCursor:  "same-cursor",
		maxRequests: fakeLoopBudget,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := NewConnector().ListResources(ctx, fake.config(), "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "search_knowledge_base fallback")
	require.Equal(t, 2, fake.callCount("search_knowledge_base"),
		"the guard must reject the repeated cursor on the second page; err=%v", err)
	require.Contains(t, err.Error(), `repeated next_cursor "same-cursor"`)
}

func TestListAllKBFilesRejectsRepeatedCursor(t *testing.T) {
	fake := newFakeIMA(t)
	fake.paging["get_knowledge_list"] = fakePaging{
		nextCursor:  "same-cursor",
		maxRequests: fakeLoopBudget,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, _, err := listAllKBFiles(ctx, imaTestClient(t, fake), "kb-1")
	require.Error(t, err)
	require.Equal(t, 2, fake.callCount("get_knowledge_list"),
		"the guard must reject the repeated cursor on the second page; err=%v", err)
	require.Contains(t, err.Error(), `repeated next_cursor "same-cursor"`)
}

// A vendor that hands back a brand new cursor on every page never repeats one,
// so the repeat guard cannot stop it: the hop cap must, and it must say so in
// the log instead of vanishing into a task deadline.
func TestListResourcesSearchFallbackStopsAtPaginationCap(t *testing.T) {
	fake := newFakeIMA(t)
	fake.paging["search_knowledge_base"] = fakePaging{
		nextCursor:    "cursor",
		advanceCursor: true,
		maxRequests:   fakeLoopBudget,
	}

	var logs bytes.Buffer
	logger.SetOutput(&logs)
	defer logger.SetOutput(os.Stdout)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	_, err := NewConnector().ListResources(ctx, fake.config(), "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "pagination exceeded 10000 pages")
	require.Equal(t, paginationHopCap, fake.callCount("search_knowledge_base"))
	require.Contains(t, logs.String(), "pagination exceeded 10000 pages",
		"hitting the hop cap must be visible in the log")
}

// The guard must not truncate a listing that genuinely spans several pages.
func TestListAllKBFilesMergesRealPages(t *testing.T) {
	fake := newFakeIMA(t)
	fake.paging["get_knowledge_list"] = fakePaging{
		nextCursor:    "page",
		advanceCursor: true,
		stopAfter:     2, // page 2 ends the listing
		filesByCall: map[int][]fakeFile{
			1: {{MediaID: "media-1", Title: "a.txt", MediaType: mediaTypeMarkdown}},
			2: {{MediaID: "media-2", Title: "b.txt", MediaType: mediaTypeMarkdown}},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	files, _, err := listAllKBFiles(ctx, imaTestClient(t, fake), "kb-1")
	require.NoError(t, err)
	require.Len(t, files, 2)
	require.Equal(t, 2, fake.callCount("get_knowledge_list"))
	require.True(t, strings.HasSuffix(files[0].Title, "a.txt") || strings.HasSuffix(files[1].Title, "a.txt"))
}
