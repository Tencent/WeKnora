package doc

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Tencent/WeKnora/cli/internal/cmdutil"
	"github.com/Tencent/WeKnora/cli/internal/iostreams"
	sdk "github.com/Tencent/WeKnora/client"
)

// fakeFetchSvc captures call arguments and returns canned responses.
type fakeFetchSvc struct {
	resp *sdk.Knowledge
	err  error
	got  struct {
		kbID string
		req  sdk.CreateKnowledgeFromURLRequest
	}
}

func (f *fakeFetchSvc) CreateKnowledgeFromURL(
	_ context.Context,
	kbID string,
	req sdk.CreateKnowledgeFromURLRequest,
) (*sdk.Knowledge, error) {
	f.got.kbID = kbID
	f.got.req = req
	return f.resp, f.err
}

func TestFetch_Success_Human(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeFetchSvc{resp: &sdk.Knowledge{ID: "doc_url_1", FileName: "whitepaper.pdf"}}
	opts := &FetchOptions{URL: "https://example.com/whitepaper.pdf"}
	require.NoError(t, runFetch(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "kb_xxx"))

	assert.Equal(t, "kb_xxx", svc.got.kbID)
	assert.Equal(t, "https://example.com/whitepaper.pdf", svc.got.req.URL)
	assert.Equal(t, "api", svc.got.req.Channel)
	assert.Contains(t, out.String(), "Ingested")
	assert.Contains(t, out.String(), "doc_url_1")
}

func TestFetch_WithName_PassesAsFileName(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeFetchSvc{resp: &sdk.Knowledge{ID: "doc_url_2"}}
	opts := &FetchOptions{URL: "https://example.com/article.html", Name: "Q3 Article"}
	require.NoError(t, runFetch(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "kb_xxx"))
	assert.Equal(t, "Q3 Article", svc.got.req.FileName,
		"--name must be forwarded as FileName (server uses it for file-vs-crawl mode hint)")
}

func TestFetch_Title(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeFetchSvc{resp: &sdk.Knowledge{ID: "doc_u"}}
	opts := &FetchOptions{URL: "https://example.com/a.pdf", Title: "My Title"}
	require.NoError(t, runFetch(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "kb_xxx"))
	assert.Equal(t, "My Title", svc.got.req.Title)
}

func TestFetch_FileType(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeFetchSvc{resp: &sdk.Knowledge{ID: "doc_u"}}
	opts := &FetchOptions{URL: "https://example.com/no-ext", FileType: "pdf"}
	require.NoError(t, runFetch(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "kb_xxx"))
	assert.Equal(t, "pdf", svc.got.req.FileType)
}

func TestFetch_TagID(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeFetchSvc{resp: &sdk.Knowledge{ID: "doc_u"}}
	opts := &FetchOptions{URL: "https://example.com/a.pdf", TagID: "tag_99"}
	require.NoError(t, runFetch(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "kb_xxx"))
	assert.Equal(t, "tag_99", svc.got.req.TagID)
}

func TestFetch_EnableMultimodel_Forwarded(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeFetchSvc{resp: &sdk.Knowledge{ID: "doc_u"}}
	mm := true
	opts := &FetchOptions{URL: "https://example.com/a.pdf", EnableMultimodel: &mm}
	require.NoError(t, runFetch(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "kb_xxx"))
	require.NotNil(t, svc.got.req.EnableMultimodel)
	assert.True(t, *svc.got.req.EnableMultimodel)
}

func TestFetch_Channel_Override(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeFetchSvc{resp: &sdk.Knowledge{ID: "doc_u"}}
	opts := &FetchOptions{URL: "https://example.com/a.pdf", Channel: "web"}
	require.NoError(t, runFetch(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "kb_xxx"))
	assert.Equal(t, "web", svc.got.req.Channel)
}

func TestFetch_Channel_DefaultIsAPI(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeFetchSvc{resp: &sdk.Knowledge{ID: "doc_u"}}
	opts := &FetchOptions{URL: "https://example.com/a.pdf"}
	require.NoError(t, runFetch(context.Background(), opts, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "kb_xxx"))
	assert.Equal(t, uploadChannel, svc.got.req.Channel)
}

func TestFetch_JSON_Envelope(t *testing.T) {
	out, _ := iostreams.SetForTest(t)
	svc := &fakeFetchSvc{resp: &sdk.Knowledge{ID: "doc_url_3", FileName: "ok.pdf"}}
	fopts := &cmdutil.FormatOptions{Mode: cmdutil.FormatJSON}
	require.NoError(t, runFetch(context.Background(),
		&FetchOptions{URL: "https://example.com/ok.pdf"}, fopts, svc, "kb_xxx"))
	got := out.String()
	var env struct {
		OK   bool          `json:"ok"`
		Data sdk.Knowledge `json:"data"`
	}
	require.NoError(t, json.Unmarshal([]byte(got), &env), "expected valid JSON envelope, got %q", got)
	assert.True(t, env.OK, "envelope.ok must be true")
	assert.Equal(t, "doc_url_3", env.Data.ID, "envelope.data.id must be doc_url_3")
	assert.NotContains(t, got, `"risk":`)
}

func TestFetch_DuplicateURL_Maps_resource_already_exists(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeFetchSvc{
		resp: &sdk.Knowledge{ID: "doc_existing"},
		err:  sdk.ErrDuplicateURL,
	}
	err := runFetch(context.Background(),
		&FetchOptions{URL: "https://example.com/dup.pdf"}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "kb_xxx")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeResourceAlreadyExists, typed.Code)
}

func TestFetch_ServerError_Wraps(t *testing.T) {
	_, _ = iostreams.SetForTest(t)
	svc := &fakeFetchSvc{err: errors.New("HTTP error 500: internal server error")}
	err := runFetch(context.Background(),
		&FetchOptions{URL: "https://example.com/doc.pdf"}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "kb_xxx")
	require.Error(t, err)
	var typed *cmdutil.Error
	require.ErrorAs(t, err, &typed)
	assert.Equal(t, cmdutil.CodeServerError, typed.Code)
}

func TestFetch_KBResolutionFailure_Propagates(t *testing.T) {
	// runFetch itself receives a pre-resolved kbID; KB resolution failure
	// happens in RunE before runFetch is called. This test verifies that
	// runFetch does NOT swallow an error returned from the service when
	// kbID is empty (which a broken resolution chain might produce).
	_, _ = iostreams.SetForTest(t)
	svc := &fakeFetchSvc{err: errors.New("HTTP error 404: knowledge base not found")}
	err := runFetch(context.Background(),
		&FetchOptions{URL: "https://example.com/doc.pdf"}, &cmdutil.FormatOptions{Mode: cmdutil.FormatHuman}, svc, "")
	require.Error(t, err)
}
