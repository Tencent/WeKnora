package notion

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/internal/types"
)

const attachmentPageEditTime = "2026-10-03T10:00:00Z"

const attachmentPageJSON = `{"id":"page","object":"page","url":"https://notion.so/page",` +
	`"last_edited_time":"` + attachmentPageEditTime + `","parent":{"type":"workspace"},` +
	`"properties":{"title":{"type":"title","title":[{"plain_text":"Attachment page"}]}}}`

// attachmentRetryFixture serves a single page whose whole content is one pdf
// block. The attachment URL keeps answering 500 until failing is cleared.
type attachmentRetryFixture struct {
	baseURL       string
	attachmentURL string
	failing       atomic.Bool
	blockFetches  atomic.Int32
	downloads     atomic.Int32
}

func (f *attachmentRetryFixture) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/v1/search":
		_, _ = fmt.Fprint(w, `{"results":[`+attachmentPageJSON+`],"has_more":false}`)
	case "/v1/pages/page":
		_, _ = fmt.Fprint(w, attachmentPageJSON)
	case "/v1/blocks/page/children":
		f.blockFetches.Add(1)
		_, _ = fmt.Fprintf(w, `{"results":[{"id":"pdf-block","type":"pdf","pdf":{"type":"file",`+
			`"name":"reference.pdf","file":{"url":%q,"expiry_time":"2030-01-01T00:00:00Z"}}}],`+
			`"has_more":false}`, f.attachmentURL)
	case "/att.pdf":
		f.downloads.Add(1)
		if f.failing.Load() {
			http.Error(w, "storage unavailable", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte("%PDF-1.4 recovered attachment"))
	default:
		http.NotFound(w, r)
	}
}

func newAttachmentRetryFixture(t *testing.T) (*attachmentRetryFixture, *types.DataSourceConfig) {
	t.Helper()
	allowNotionTestServer(t)
	fixture := &attachmentRetryFixture{}
	fixture.failing.Store(true)
	server := httptest.NewServer(http.HandlerFunc(fixture.serve))
	t.Cleanup(server.Close)
	fixture.baseURL = server.URL
	fixture.attachmentURL = server.URL + "/att.pdf"
	return fixture, makeNotionConfig(&Config{APIKey: "test-token"}, server.URL, []string{"page"})
}

// fileUploadRetryFixture serves a page whose only file block is a file_upload
// that the source refuses to resolve.
type fileUploadRetryFixture struct {
	blockFetches atomic.Int32
}

func (f *fileUploadRetryFixture) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/v1/search":
		_, _ = fmt.Fprint(w, `{"results":[`+attachmentPageJSON+`],"has_more":false}`)
	case "/v1/pages/page":
		_, _ = fmt.Fprint(w, attachmentPageJSON)
	case "/v1/blocks/page/children":
		f.blockFetches.Add(1)
		_, _ = fmt.Fprint(w, `{"results":[{"id":"upload-block","type":"file","file":`+
			`{"type":"file_upload","name":"uploaded.pdf","file_upload":{"id":"upload-1"}}}],`+
			`"has_more":false}`)
	case "/v1/blocks/upload-block":
		http.Error(w, "block unavailable", http.StatusNotFound)
	default:
		http.NotFound(w, r)
	}
}

func newFileUploadRetryFixture(t *testing.T) (*fileUploadRetryFixture, *types.DataSourceConfig) {
	t.Helper()
	allowNotionTestServer(t)
	fixture := &fileUploadRetryFixture{}
	server := httptest.NewServer(http.HandlerFunc(fixture.serve))
	t.Cleanup(server.Close)
	return fixture, makeNotionConfig(&Config{APIKey: "test-token"}, server.URL, []string{"page"})
}

// A page that cannot store one of its attachments must not be reported as a
// complete fetch: the caller would otherwise persist the page edit time and the
// attachment would stay missing until a full re-sync.
func TestNotionAttachmentDownloadFailureFailsPageFetch(t *testing.T) {
	fixture, _ := newAttachmentRetryFixture(t)
	client := mustTestClient(t, "test-token", fixture.baseURL)
	page, err := client.GetPage(context.Background(), "page")
	require.NoError(t, err)
	items, truncated, err := NewConnector().fetchPage(
		context.Background(), client, page, map[string]bool{})
	if err == nil && !truncated {
		t.Fatalf("fetchPage reported a complete fetch (truncated=false, err=nil) after the attachment download failed")
	}
	require.Error(t, err, "a failed attachment download must abort the page fetch")
	require.Nil(t, items)
	require.Positive(t, fixture.downloads.Load())
}

// The sync loop persists the cursor of a round that reported success, so an
// attachment failure must leave the page edit time unacknowledged and the next
// round must fetch the unchanged page again.
func TestNotionAttachmentFailurePreservesRetry(t *testing.T) {
	fixture, config := newAttachmentRetryFixture(t)
	connector := NewConnector()
	previous := buildCursor(map[string]time.Time{"page": time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)})

	items, next, err := connector.FetchIncremental(context.Background(), config, previous)
	require.Error(t, err, "a failed attachment download must fail the round")
	require.Nil(t, items, "a failed download must not emit a partial page")
	require.Nil(t, next, "a failed download must not acknowledge the page edit time")

	cursor := previous
	if next != nil {
		cursor = next
	}
	items, next, err = connector.FetchIncremental(context.Background(), config, cursor)
	require.Error(t, err, "the second round must retry the page whose attachment is still missing")
	require.Nil(t, items)
	require.Nil(t, next)
	require.GreaterOrEqual(t, fixture.blockFetches.Load(), int32(2),
		"the unchanged page must be fetched again until its attachment is stored")
}

// Once the source serves the attachment, the retried page must be ingested with
// its attachment and only then acknowledged.
func TestNotionAttachmentStoredAfterDownloadRecovers(t *testing.T) {
	fixture, config := newAttachmentRetryFixture(t)
	connector := NewConnector()
	previous := buildCursor(map[string]time.Time{"page": time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)})

	_, next, err := connector.FetchIncremental(context.Background(), config, previous)
	require.Error(t, err)
	require.Nil(t, next)

	fixture.failing.Store(false)
	items, next, err := connector.FetchIncremental(context.Background(), config, previous)
	require.NoError(t, err)
	require.NotNil(t, next)

	var foundPage, foundAttachment bool
	for _, item := range items {
		if item.ExternalID == "page" && !item.IsDeleted {
			foundPage = true
		}
		if item.Metadata["object_type"] == objectTypeAttachment {
			foundAttachment = true
			require.Equal(t, "reference.pdf", item.FileName)
			require.Contains(t, string(item.Content), "recovered attachment")
		}
	}
	require.True(t, foundPage, "the page markdown must be emitted once the round succeeds")
	require.True(t, foundAttachment, "the recovered attachment must be emitted")

	items, _, err = connector.FetchIncremental(context.Background(), config, next)
	require.NoError(t, err)
	require.Empty(t, items, "an acknowledged page must not be fetched again")
	require.Equal(t, int32(2), fixture.blockFetches.Load())
}

// An unresolved file_upload leaves the file URL empty, which silently drops the
// attachment the same way a failed download does.
func TestNotionFileUploadResolutionFailurePreservesRetry(t *testing.T) {
	fixture, config := newFileUploadRetryFixture(t)
	connector := NewConnector()
	previous := buildCursor(map[string]time.Time{"page": time.Date(2026, 10, 2, 10, 0, 0, 0, time.UTC)})

	items, next, err := connector.FetchIncremental(context.Background(), config, previous)
	require.Error(t, err, "an unresolved file_upload must fail the round")
	require.Nil(t, items)
	require.Nil(t, next)

	items, next, err = connector.FetchIncremental(context.Background(), config, previous)
	require.Error(t, err, "the second round must retry the page whose file_upload is unresolved")
	require.Nil(t, items)
	require.Nil(t, next)
	require.GreaterOrEqual(t, fixture.blockFetches.Load(), int32(2))
}
